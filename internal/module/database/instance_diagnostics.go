package database

import (
	"bufio"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/logview"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func (m *Module) databaseDiagnostics(c *gin.Context) {
	ctx, cancel := context.WithTimeout(c.Request.Context(), 15*time.Second)
	defer cancel()
	engine, id, view := c.Param("engine"), c.Param("instance"), c.Param("view")
	if c.Request.Method == "DELETE" && (engine != "mysql" || view != "binlog") {
		response.Fail(c, response.Invalid, "仅支持清理 MySQL 二进制日志")
		return
	}
	row, err := m.packages.DatabaseInstance(ctx, engine, id)
	if err != nil {
		failure(c, err)
		return
	}
	if row.ContainerID == "" {
		response.Fail(c, response.NotFound, "数据库实例不存在")
		return
	}
	if view == "tuning" && (engine == "mysql" || engine == "redis") {
		out, err := recommendedDatabaseTuning(ctx, engine, row.MemoryLimit)
		if err != nil {
			failure(c, err)
			return
		}
		response.OK(c, out)
		return
	}
	if engine == "redis" && view == "log" && !row.Running {
		out, err := m.packages.DatabaseInstanceLog(ctx, engine, row, "")
		if err != nil {
			failure(c, err)
			return
		}
		response.OK(c, out)
		return
	}
	if !row.Running {
		response.Fail(c, response.Invalid, "请先启动实例")
		return
	}
	switch engine {
	case "mysql":
		err = m.mysqlDiagnostics(ctx, c, row, view)
	case "postgresql":
		err = m.postgresDiagnostics(ctx, c, row, view)
	case "redis":
		err = m.redisDiagnostics(ctx, c, row, view)
	case "memcached":
		if view != "status" && view != "performance" {
			err = errors.New("无效的 Memcached 诊断类型")
			break
		}
		var settings, metrics map[string]string
		settings, metrics, err = memcachedStats(ctx, row.Port)
		if err == nil {
			response.OK(c, gin.H{"variables": settings, "metrics": metrics})
		}
	default:
		err = errors.New("无效的数据库诊断类型")
	}
	if err != nil {
		failure(c, err)
	}
}

func (m *Module) mysqlDiagnostics(ctx context.Context, c *gin.Context, row pkgmgr.DatabaseInstance, view string) error {
	id := row.ID
	if id == "default" {
		id = ""
	}
	var entry store.DBServer
	err := m.db.WithContext(ctx).First(&entry, "is_local = ? AND type = ? AND instance_id = ?", true, "mysql", id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		servers, _, syncErr := m.mysqlServers(ctx)
		if syncErr != nil {
			return syncErr
		}
		for _, server := range servers {
			if server.IsLocal && server.Type == "mysql" && server.InstanceID == id {
				entry = server
				break
			}
		}
	} else if err != nil {
		return err
	}
	if entry.ID == "" {
		return errors.New("该实例的本机连接尚未登记")
	}
	if id != "" {
		entry.Host, entry.Port = "127.0.0.1", row.Port
	}
	db, err := m.connect(ctx, entry, false)
	if err != nil {
		return err
	}
	defer db.Close()
	switch view {
	case "status", "performance":
		var out = gin.H{}
		if view == "status" {
			metrics, err := mysqlPairs(ctx, db, `SHOW GLOBAL STATUS WHERE Variable_name IN (
				'Uptime','Questions','Connections','Threads_connected','Threads_running','Threads_created','Max_used_connections',
				'Slow_queries','Bytes_sent','Bytes_received','Com_commit','Com_rollback','Key_read_requests','Key_reads',
				'Innodb_buffer_pool_read_requests','Innodb_buffer_pool_reads','Innodb_buffer_pool_bytes_data',
				'Created_tmp_disk_tables','Created_tmp_tables','Open_tables','Select_scan','Select_full_join','Sort_merge_passes')`)
			if err != nil {
				return err
			}
			out["sampledAt"] = time.Now().UnixMilli()
			var file, pos string
			for _, query := range []string{"SHOW BINARY LOG STATUS", "SHOW MASTER STATUS"} {
				fields, err := mysqlFirstRow(ctx, db, query)
				if err == nil {
					if len(fields) >= 2 {
						file, pos = fields[0], fields[1]
					}
					break
				}
			}
			out["metrics"], out["binlogFile"], out["binlogPosition"] = metrics, file, pos
		} else {
			variables, err := mysqlPairs(ctx, db, "SHOW GLOBAL VARIABLES WHERE Variable_name IN ('table_open_cache','thread_cache_size','key_buffer_size','tmp_table_size','innodb_buffer_pool_size','innodb_log_buffer_size','max_connections','sort_buffer_size','read_buffer_size','read_rnd_buffer_size','join_buffer_size','thread_stack','binlog_cache_size')")
			if err != nil {
				return err
			}
			out["variables"] = variables
		}
		response.OK(c, out)
	case "slow", "slow-analysis":
		var enabled int
		var seconds, path, output string
		if err := db.QueryRowContext(ctx, "SELECT @@GLOBAL.slow_query_log, @@GLOBAL.long_query_time, @@GLOBAL.slow_query_log_file, @@GLOBAL.log_output").Scan(&enabled, &seconds, &path, &output); err != nil {
			return err
		}
		if view == "slow-analysis" {
			analysis, err := m.mysqlSlowAnalysis(ctx, db, row, path, output)
			if err != nil {
				return err
			}
			response.OK(c, analysis)
			return nil
		}
		var log logview.Text
		switch {
		case strings.Contains(output, "FILE"):
			log, err = m.packages.DatabaseInstanceLog(ctx, "mysql", row, path)
		case strings.Contains(output, "TABLE"):
			log, err = mysqlSlowTable(ctx, db)
		default:
			log.Path = output
		}
		if err != nil {
			return err
		}
		response.OK(c, gin.H{"enabled": enabled == 1, "seconds": seconds, "log": log})
	case "binlog":
		var enabled int
		var base string
		if err := db.QueryRowContext(ctx, "SELECT @@GLOBAL.log_bin, COALESCE(@@GLOBAL.log_bin_basename, '')").Scan(&enabled, &base); err != nil {
			return err
		}
		files := []binaryLogFile{}
		if enabled == 1 {
			files, err = mysqlBinaryLogs(ctx, db, row.DataDir, base)
			if err != nil {
				return err
			}
		}
		if c.Request.Method == "DELETE" {
			var in struct {
				Name string `json:"name"`
			}
			if !response.Bind(c, &in) {
				return nil
			}
			for i, file := range files[:max(0, len(files)-1)] {
				next := files[i+1].Name
				if file.Name != in.Name || !binaryLogName.MatchString(next) {
					continue
				}
				if _, err := db.ExecContext(ctx, "PURGE BINARY LOGS TO '"+next+"'"); err != nil {
					return err
				}
				response.OK(c, nil)
				return nil
			}
			return errors.New("当前二进制日志不能清理，或日志已变更")
		}
		response.OK(c, gin.H{"enabled": enabled == 1, "files": files})
	default:
		return errors.New("无效的 MySQL 诊断类型")
	}
	return nil
}

func mysqlPairs(ctx context.Context, db *sql.DB, query string) (map[string]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return nil, err
		}
		result[strings.ToLower(key)] = value
	}
	return result, rows.Err()
}

func mysqlFirstRow(ctx context.Context, db *sql.DB, query string) ([]string, error) {
	rows, err := db.QueryContext(ctx, query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	return mysqlRow(rows)
}

func mysqlRow(rows *sql.Rows) ([]string, error) {
	fields, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	values := make([]string, len(fields))
	pointers := make([]any, len(fields))
	for i := range pointers {
		pointers[i] = &values[i]
	}
	return values, rows.Scan(pointers...)
}

type binaryLogFile struct {
	Name      string `json:"name"`
	Size      int64  `json:"size"`
	UpdatedAt string `json:"updatedAt,omitempty"`
}

var binaryLogName = regexp.MustCompile(`^[A-Za-z0-9_.-]+\.[0-9]+$`)

func mysqlBinaryLogs(ctx context.Context, db *sql.DB, dataDir, base string) ([]binaryLogFile, error) {
	rows, err := db.QueryContext(ctx, "SHOW BINARY LOGS")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	files := []binaryLogFile{}
	for rows.Next() {
		values, err := mysqlRow(rows)
		if err != nil {
			return nil, err
		}
		size, _ := strconv.ParseInt(values[1], 10, 64)
		file := binaryLogFile{Name: values[0], Size: size}
		if dir, ok := strings.CutPrefix(filepath.Dir(base)+"/", "/var/lib/mysql/"); ok && binaryLogName.MatchString(file.Name) {
			if info, err := os.Stat(filepath.Join(dataDir, dir, file.Name)); err == nil {
				file.UpdatedAt = info.ModTime().Format(time.RFC3339)
			}
		}
		files = append(files, file)
	}
	return files, rows.Err()
}

func mysqlSlowTable(ctx context.Context, db *sql.DB) (logview.Text, error) {
	out := logview.Text{Path: "mysql.slow_log"}
	rows, err := db.QueryContext(ctx, "SELECT CAST(start_time AS CHAR), CAST(query_time AS CHAR), LEFT(sql_text, 2000) FROM mysql.slow_log ORDER BY start_time DESC LIMIT 100")
	if err != nil {
		return out, err
	}
	defer rows.Close()
	var lines []string
	for rows.Next() {
		var start, elapsed, statement string
		if err := rows.Scan(&start, &elapsed, &statement); err != nil {
			return out, err
		}
		lines = append(lines, fmt.Sprintf("[%s] %s\n%s\n", start, elapsed, statement))
	}
	var body strings.Builder
	for i := len(lines) - 1; i >= 0; i-- {
		body.WriteString(lines[i])
	}
	out.Content = strings.ToValidUTF8(body.String(), "�")
	out.Size = int64(len(out.Content))
	return out, rows.Err()
}

func (m *Module) redisDiagnostics(ctx context.Context, c *gin.Context, row pkgmgr.DatabaseInstance, view string) error {
	key := "database.redis.local"
	if row.ID != "default" {
		key = "database.redis.redis-" + row.ID
	}
	var setting store.Setting
	db := m.db.WithContext(ctx)
	if err := db.First(&setting, "key = ?", key).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		if row.ID == "default" {
			_, err = m.ensureNoSQLLocal(ctx, "redis")
		} else {
			err = m.syncNoSQLInstances(ctx, "redis", map[string]pkgmgr.DatabaseInstance{row.ID: row})
		}
		if err != nil {
			return err
		}
		if err := db.First(&setting, "key = ?", key).Error; err != nil {
			return err
		}
	} else if err != nil {
		return err
	}
	var entry noSQLConnection
	if err := json.Unmarshal([]byte(setting.Value), &entry); err != nil {
		return err
	}
	var password string
	if entry.PasswordEnc != "" {
		var err error
		password, err = store.Decrypt(m.cfg.AESKey, entry.PasswordEnc)
		if err != nil {
			return err
		}
	}
	entry.Host, entry.Port = "127.0.0.1", row.Port
	err := withRedisConnection(ctx, entry, password, "0", func(conn *redisConnection) error {
		switch view {
		case "status":
			reply, err := conn.cmd("INFO")
			if err != nil {
				return err
			}
			response.OK(c, gin.H{"metrics": redisInfoMetrics(reply), "sampledAt": time.Now().UnixMilli()})
		case "performance", "log":
			keys := []string{"timeout", "maxclients", "databases", "maxmemory", "maxmemory-policy", "tcp-keepalive"}
			if view == "log" {
				keys = []string{"logfile"}
			}
			commands := make([][]string, len(keys))
			for i, key := range keys {
				commands[i] = []string{"CONFIG", "GET", key}
			}
			replies, err := conn.pipeline(commands)
			if err != nil {
				return err
			}
			values := map[string]string{}
			for _, reply := range replies {
				if pair, ok := reply.([]any); ok && len(pair) == 2 {
					values[fmt.Sprint(pair[0])] = fmt.Sprint(pair[1])
				}
			}
			if view == "performance" {
				reply, err := conn.cmd("INFO", "memory")
				if err != nil {
					return err
				}
				response.OK(c, gin.H{"variables": values, "metrics": redisInfoMetrics(reply)})
			} else {
				log, err := m.packages.DatabaseInstanceLog(ctx, "redis", row, values["logfile"])
				if err != nil {
					return err
				}
				response.OK(c, log)
			}
		default:
			return errors.New("无效的 Redis 诊断类型")
		}
		return nil
	})
	if err != nil && view == "log" {
		out, fallback := m.packages.DatabaseInstanceLog(ctx, "redis", row, "")
		if fallback == nil {
			response.OK(c, out)
			return nil
		}
	}
	return err
}

func redisInfoMetrics(reply any) map[string]string {
	info := map[string]string{}
	if raw, ok := reply.(string); ok {
		for line := range strings.SplitSeq(raw, "\n") {
			if key, value, found := strings.Cut(strings.TrimSpace(line), ":"); found {
				info[key] = value
			}
		}
	}
	return info
}

func memcachedStats(ctx context.Context, port int) (map[string]string, map[string]string, error) {
	conn, err := (&net.Dialer{Timeout: 3 * time.Second}).DialContext(ctx, "tcp4", "127.0.0.1:"+strconv.Itoa(port))
	if err != nil {
		return nil, nil, errors.New("无法连接该 Memcached 实例")
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("stats settings\r\nstats\r\n")); err != nil {
		return nil, nil, err
	}
	scanner := bufio.NewScanner(conn)
	values := []map[string]string{{}, {}}
	for _, value := range values {
		done := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if line == "END" {
				done = true
				break
			}
			fields := strings.SplitN(line, " ", 3)
			if len(fields) != 3 || fields[0] != "STAT" {
				return nil, nil, errors.New("Memcached 状态响应无效")
			}
			value[fields[1]] = fields[2]
		}
		if !done {
			return nil, nil, errors.New("Memcached 状态响应中断")
		}
	}
	values[1]["host_port"] = strconv.Itoa(port)
	return values[0], values[1], nil
}
