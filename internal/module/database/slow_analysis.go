package database

import (
	"cmp"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"ezpanel/internal/pkgmgr"
)

const slowTableSample = 500

type slowQueryGroup struct {
	ID           string  `json:"id"`
	Statement    string  `json:"statement"`
	Count        int     `json:"count"`
	TotalSeconds float64 `json:"totalSeconds"`
	MaxSeconds   float64 `json:"maxSeconds"`
	RowsExamined uint64  `json:"rowsExamined"`
}

type slowQueryAnalysis struct {
	Source       string           `json:"source"`
	Truncated    bool             `json:"truncated"`
	Samples      int              `json:"samples"`
	TotalSeconds float64          `json:"totalSeconds"`
	Groups       []slowQueryGroup `json:"groups"`
}

var slowLogStats = regexp.MustCompile(`^# Query_time:\s+([0-9.]+).*\bRows_examined:\s+([0-9]+)`)
var sqlFingerprintTokens = regexp.MustCompile(`(?s)/\*.*?\*/|(?:#|--[ \t])[^\r\n]*|'(?:\\.|''|[^'\\])*'|"(?:\\.|""|[^"\\])*"|` + "`(?:``|[^`])*`" + `|[\pL_$][\pL\pN_$]*|(?:0[xX][0-9a-fA-F]+|0[bB][01]+|(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?)|[^\s]`)

func normalizeSlowSQL(statement string) string {
	var parts []string
	for _, token := range sqlFingerprintTokens.FindAllString(statement, -1) {
		if strings.HasPrefix(token, "/*") || strings.HasPrefix(token, "--") || strings.HasPrefix(token, "#") || token == ";" {
			continue
		}
		switch {
		case token[0] == '\'' || token[0] == '"' || (token[0] >= '0' && token[0] <= '9') || len(token) > 1 && token[0] == '.' && token[1] >= '0' && token[1] <= '9':
			parts = append(parts, "?")
		case token[0] == '`':
			parts = append(parts, token)
		default:
			parts = append(parts, strings.ToLower(token))
		}
	}
	return strings.Join(parts, " ")
}

func (m *Module) mysqlSlowAnalysis(ctx context.Context, db *sql.DB, row pkgmgr.DatabaseInstance, path, source string) (slowQueryAnalysis, error) {
	out := slowQueryAnalysis{Source: "NONE", Groups: []slowQueryGroup{}}
	groups := map[string]*slowQueryGroup{}
	add := func(statement string, seconds float64, examined uint64) {
		normalized := normalizeSlowSQL(strings.ToValidUTF8(statement, "�"))
		if normalized == "" {
			return
		}
		digest := sha256.Sum256([]byte(normalized))
		id := hex.EncodeToString(digest[:])
		group := groups[id]
		if group == nil {
			display := []rune(normalized)
			if len(display) > 320 {
				display = append(display[:320], '…')
			}
			group = &slowQueryGroup{ID: id, Statement: string(display)}
			groups[id] = group
		}
		group.Count++
		group.TotalSeconds += seconds
		group.MaxSeconds = max(group.MaxSeconds, seconds)
		group.RowsExamined += examined
		out.Samples++
		out.TotalSeconds += seconds
	}
	switch {
	case strings.Contains(source, "FILE"):
		out.Source = "FILE"
		log, err := m.packages.DatabaseInstanceLog(ctx, "mysql", row, path)
		if err != nil {
			return out, err
		}
		clipped := parseSlowLogFile(log.Content, add)
		out.Truncated = log.Truncated || clipped
	case strings.Contains(source, "TABLE"):
		out.Source = "TABLE"
		rows, err := db.QueryContext(ctx, `SELECT TIME_TO_SEC(query_time), rows_examined, LEFT(sql_text, 16384),
			OCTET_LENGTH(sql_text) > 16384 FROM mysql.slow_log ORDER BY start_time DESC LIMIT ?`, slowTableSample+1)
		if err != nil {
			return out, err
		}
		defer rows.Close()
		for i := 0; rows.Next(); i++ {
			if i == slowTableSample {
				out.Truncated = true
				break
			}
			var statement string
			var seconds float64
			var examined uint64
			var clipped bool
			if err := rows.Scan(&seconds, &examined, &statement, &clipped); err != nil {
				return out, err
			}
			out.Truncated = out.Truncated || clipped
			if !clipped {
				add(statement, seconds, examined)
			}
		}
		if err := rows.Err(); err != nil {
			return out, err
		}
	}
	for _, group := range groups {
		out.Groups = append(out.Groups, *group)
	}
	slices.SortFunc(out.Groups, func(a, b slowQueryGroup) int {
		if duration := cmp.Compare(b.TotalSeconds, a.TotalSeconds); duration != 0 {
			return duration
		}
		return cmp.Compare(a.ID, b.ID)
	})
	return out, nil
}

func parseSlowLogFile(log string, add func(statement string, seconds float64, examined uint64)) bool {
	const maxStatement = 16384
	var sqlText strings.Builder
	var seconds float64
	var examined uint64
	var active, headerTimestamp, clipped, truncated bool
	flush := func() {
		if active && !clipped {
			statement := strings.TrimSpace(sqlText.String())
			if strings.HasSuffix(statement, ";") {
				add(statement, seconds, examined)
			} else if statement != "" {
				truncated = true
			}
		}
		sqlText.Reset()
		active, headerTimestamp, clipped = false, false, false
	}
	for line := range strings.SplitSeq(log, "\n") {
		line = strings.TrimSuffix(line, "\r")
		switch {
		case strings.HasPrefix(line, "# Time:") || strings.HasPrefix(line, "# User@Host:"):
			flush()
		case strings.HasPrefix(line, "# Query_time:"):
			flush()
			if fields := slowLogStats.FindStringSubmatch(line); fields != nil {
				elapsed, err := strconv.ParseFloat(fields[1], 64)
				if err != nil {
					continue
				}
				seconds = elapsed
				examined, _ = strconv.ParseUint(fields[2], 10, 64)
				active = true
			}
		case active && !clipped:
			if sqlText.Len() == 0 {
				if strings.HasPrefix(line, "#") || strings.TrimSpace(line) == "" {
					continue
				}
				if !headerTimestamp && strings.HasPrefix(line, "use ") && strings.HasSuffix(line, ";") {
					continue
				}
				if !headerTimestamp && strings.HasPrefix(line, "SET timestamp=") {
					headerTimestamp = true
					continue
				}
			}
			if sqlText.Len()+len(line)+1 > maxStatement {
				clipped, truncated = true, true
				continue
			}
			sqlText.WriteString(line)
			sqlText.WriteByte('\n')
		}
	}
	if strings.HasSuffix(log, "\n") {
		flush()
	} else if active {
		truncated = true
	}
	return truncated
}
