package database

import (
	"strings"
	"testing"
)

func TestSlowLogFileSamplesCompleteStatements(t *testing.T) {
	const log = `text from before the tail
# Time: 2026-11-23T10:00:01.234Z
# User@Host: root[root] @  [127.0.0.1]
# Query_time: 1.500000  Lock_time: 0.000001 Rows_sent: 1  Rows_examined: 17
use demo;
SET timestamp=1795428001;
SELECT *
FROM users WHERE email = 'alice@example.test' AND id = 42;
# Time: 2026-11-23T10:00:02.234Z
# User@Host: root[root] @  [127.0.0.1]
# Query_time: 2.750000  Lock_time: 0.000001 Rows_sent: 1  Rows_examined: 23
SET timestamp=1795428002;
select * from users where email='bob@example.test' and id=67;
# Time: 2026-11-23T10:00:03.234Z
# Query_time: 2.000000  Lock_time: 0.000001 Rows_sent: 1  Rows_examined: 13
SET timestamp=1795428003;
SELECT * FROM users where email = 'incomplete`
	var statements []string
	var total float64
	var rows uint64
	truncated := parseSlowLogFile(log, func(statement string, seconds float64, examined uint64) {
		statements = append(statements, normalizeSlowSQL(statement))
		total += seconds
		rows += examined
	})
	if !truncated || len(statements) != 2 || total != 4.25 || rows != 40 {
		t.Fatalf("truncated=%v statements=%v total=%v rows=%v", truncated, statements, total, rows)
	}
	if statements[0] != statements[1] || strings.Contains(statements[0], "@example.test") || strings.Contains(statements[0], "use demo") {
		t.Fatalf("unexpected patterns: %q", statements)
	}
}

func TestSlowQueryFingerprintMasksLiteralsWithoutMergingTables(t *testing.T) {
	pattern := normalizeSlowSQL("SELECT /* remark */ * FROM `User` WHERE id = 0xFF AND amount=.5 AND secret='a''b'")
	want := normalizeSlowSQL("select * from `User` where id=123 and amount = 1e4 and secret='another'")
	if pattern != want || pattern == normalizeSlowSQL("select * from `Other` where id=123 and amount=1e4 and secret='another'") {
		t.Fatalf("unexpected normalization %q / %q", pattern, want)
	}
}
