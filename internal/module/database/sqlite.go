package database

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
	_ "github.com/glebarez/go-sqlite"
	"gorm.io/gorm"
)

const sqliteSettingKey = "database.sqlite.files"

type sqliteEntry struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Path    string `json:"path"`
	AddedAt string `json:"addedAt"`
	Removed bool   `json:"removed,omitempty"`
}

type sqliteFileView struct {
	sqliteEntry
	Size        int64  `json:"size"`
	Exists      bool   `json:"exists"`
	UpdatedAt   string `json:"updatedAt,omitempty"`
	BackupCount int    `json:"backupCount"`
}

type sqliteBackupItem struct {
	FileName  string `json:"fileName"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
	Path      string `json:"path"`
}

func (m *Module) sqliteRoutes(r *gin.RouterGroup) {
	sq := r.Group("/sqlite")
	sq.GET("/files", m.sqliteListFiles)
	sq.POST("/files", m.sqliteAddFile)
	sq.DELETE("/files/:id", m.sqliteRemoveFile)
	sq.GET("/files/:id/tables", m.sqliteTables)
	sq.GET("/files/:id/tables/:table/rows", m.sqliteTableRows)
	sq.POST("/files/:id/tables/:table/rows", m.sqliteInsertRow)
	sq.PUT("/files/:id/tables/:table/rows", m.sqliteUpdateRow)
	sq.DELETE("/files/:id/tables/:table/rows", m.sqliteDeleteRow)
	sq.POST("/files/:id/query", m.sqliteQuery)
	sq.GET("/files/:id/backups", m.sqliteListBackups)
	sq.POST("/files/:id/backups", m.sqliteCreateBackup)
	sq.GET("/files/:id/backups/:filename/download", m.sqliteDownloadBackup)
	sq.DELETE("/files/:id/backups/:filename", m.sqliteDeleteBackup)
}

func sqliteQuote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func (m *Module) readSQLiteEntries() ([]sqliteEntry, error) {
	var row store.Setting
	err := m.db.First(&row, "key = ?", sqliteSettingKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return []sqliteEntry{}, nil
	}
	if err != nil {
		return nil, err
	}
	var list []sqliteEntry
	if err := json.Unmarshal([]byte(row.Value), &list); err != nil {
		return nil, err
	}
	return list, nil
}

func (m *Module) saveSQLiteEntries(list []sqliteEntry) error {
	raw, err := json.Marshal(list)
	if err != nil {
		return err
	}
	return m.db.Save(&store.Setting{Key: sqliteSettingKey, Value: string(raw)}).Error
}

func (m *Module) findSQLiteEntry(id string) (sqliteEntry, error) {
	list, err := m.readSQLiteEntries()
	if err != nil {
		return sqliteEntry{}, err
	}
	for _, item := range list {
		if item.ID == id && !item.Removed {
			return item, nil
		}
	}
	return sqliteEntry{}, errors.New("SQLite 文件记录不存在")
}

func sqliteDSN(real string, busyTimeoutMs int) string {
	return (&url.URL{
		Scheme:   "file",
		Path:     real,
		RawQuery: fmt.Sprintf("_pragma=busy_timeout(%d)", busyTimeoutMs),
	}).String()
}

func (m *Module) getSQLitePolicy() (*pathsafe.Policy, error) {
	m.sqlitePolicyMu.Lock()
	defer m.sqlitePolicyMu.Unlock()
	if m.sqlitePolicy != nil {
		return m.sqlitePolicy, nil
	}
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return nil, err
	}
	m.sqlitePolicy = policy
	return policy, nil
}

func (m *Module) openSQLiteFile(path string) (*sql.DB, error) {
	policy, err := m.getSQLitePolicy()
	if err != nil {
		return nil, err
	}
	real, err := policy.Check(path, false)
	if err != nil {
		return nil, errors.New("该路径受保护或无效")
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		return nil, errors.New("本地 SQLite 文件不存在或不是普通文件")
	}
	db, err := sql.Open("sqlite", sqliteDSN(real, 5000))
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	return db, nil
}

func verifySQLiteFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	var header [16]byte
	n, err := io.ReadFull(f, header[:])
	_ = f.Close()
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return err
	}
	if n > 0 && string(header[:]) != "SQLite format 3\x00" {
		return errors.New("所选文件不是有效的 SQLite 3 数据库文件")
	}
	return nil
}

func (m *Module) sqliteListFiles(c *gin.Context) {
	list, err := m.readSQLiteEntries()
	if err != nil {
		response.Fail(c, response.System, "读取 SQLite 列表失败")
		return
	}
	out := make([]sqliteFileView, 0, len(list))
	for _, item := range list {
		if item.Removed {
			continue
		}
		v := sqliteFileView{sqliteEntry: item}
		if info, err := os.Stat(item.Path); err == nil && info.Mode().IsRegular() {
			v.Exists = true
			v.Size = info.Size()
			v.UpdatedAt = info.ModTime().UTC().Format(time.RFC3339)
		}
		if backups, err := m.readSQLiteBackups(item.ID); err == nil {
			v.BackupCount = len(backups)
		}
		out = append(out, v)
	}
	response.OK(c, gin.H{"list": out})
}

func (m *Module) sqliteAddFile(c *gin.Context) {
	var in struct {
		Name string `json:"name"`
		Path string `json:"path"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Path = strings.TrimSpace(in.Path)
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	real, err := policy.Check(in.Path, false)
	if err != nil {
		response.Fail(c, response.Invalid, "文件路径无效或属于受保护的系统目录")
		return
	}
	info, err := os.Stat(real)
	if err != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.Invalid, "服务器本地文件不存在或不是普通文件")
		return
	}
	if err := verifySQLiteFile(real); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	db, err := sql.Open("sqlite", sqliteDSN(real, 3000))
	if err != nil {
		response.Fail(c, response.Invalid, "无法打开 SQLite 文件")
		return
	}
	var ver int
	err = db.QueryRowContext(c.Request.Context(), "PRAGMA schema_version").Scan(&ver)
	_ = db.Close()
	if err != nil {
		response.Fail(c, response.Invalid, "文件无法作为 SQLite 数据库读取："+response.Summary(err.Error()))
		return
	}

	m.sqliteMu.Lock()
	defer m.sqliteMu.Unlock()
	list, err := m.readSQLiteEntries()
	if err != nil {
		response.Fail(c, response.System, "读取列表失败")
		return
	}
	name := strings.TrimSpace(in.Name)
	if name == "" {
		name = filepath.Base(real)
	}
	entry := sqliteEntry{Name: name, Path: real}
	for i, existing := range list {
		if existing.Path != real {
			continue
		}
		if !existing.Removed {
			response.Fail(c, response.Conflict, "该 SQLite 文件已导入")
			return
		}
		entry.ID = existing.ID
		entry.AddedAt = existing.AddedAt
		list[i] = entry
		break
	}
	if entry.ID == "" {
		entry.ID = store.ID()
		entry.AddedAt = time.Now().UTC().Format(time.RFC3339)
		list = append(list, entry)
	}
	if err := m.saveSQLiteEntries(list); err != nil {
		response.Fail(c, response.System, "保存 SQLite 记录失败")
		return
	}
	response.OK(c, entry)
}

func (m *Module) sqliteRemoveFile(c *gin.Context) {
	m.sqliteMu.Lock()
	defer m.sqliteMu.Unlock()
	list, err := m.readSQLiteEntries()
	if err != nil {
		response.Fail(c, response.System, "读取列表失败")
		return
	}
	for i := range list {
		if list[i].ID != c.Param("id") || list[i].Removed {
			continue
		}
		// 保留登记 ID，让重新添加同一路径时仍能找到原备份。
		list[i].Removed = true
		if err := m.saveSQLiteEntries(list); err != nil {
			response.Fail(c, response.System, "移除记录失败")
			return
		}
		response.OK(c, gin.H{"deleted": true})
		return
	}
	response.Fail(c, response.NotFound, "记录不存在")
}

func (m *Module) sqliteTables(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	rows, err := db.QueryContext(c.Request.Context(),
		`SELECT name, type, COALESCE(sql, '') FROM sqlite_master WHERE type IN ('table', 'view') AND name NOT LIKE 'sqlite_%' ORDER BY type, name`)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	defer rows.Close()
	type tableInfo struct {
		Name     string `json:"name"`
		Type     string `json:"type"`
		RowCount int64  `json:"rowCount"`
		SQL      string `json:"sql"`
	}
	list := []tableInfo{}
	for rows.Next() {
		var t tableInfo
		if err := rows.Scan(&t.Name, &t.Type, &t.SQL); err != nil {
			continue
		}
		list = append(list, t)
	}
	for i := range list {
		var cnt int64
		if err := db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM "+sqliteQuote(list[i].Name)).Scan(&cnt); err == nil {
			list[i].RowCount = cnt
		}
	}
	response.OK(c, gin.H{"list": list})
}

type sqliteColInfo struct {
	Name         string `json:"name"`
	Type         string `json:"type"`
	NotNull      bool   `json:"notNull"`
	PrimaryKey   bool   `json:"primaryKey"`
	DefaultValue string `json:"defaultValue"`
}

func sqliteTableColumns(ctx context.Context, db *sql.DB, table string) ([]sqliteColInfo, bool, error) {
	var exists int
	if err := db.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name = ?`, table).Scan(&exists); err != nil || exists == 0 {
		return nil, false, errors.New("数据表不存在")
	}
	rows, err := db.QueryContext(ctx, "PRAGMA table_info("+sqliteQuote(table)+")")
	if err != nil {
		return nil, false, err
	}
	defer rows.Close()
	cols := []sqliteColInfo{}
	hasPK := false
	for rows.Next() {
		var cid int
		var name, colType string
		var notNull, pk int
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &colType, &notNull, &dflt, &pk); err != nil {
			return nil, false, err
		}
		if pk > 0 {
			hasPK = true
		}
		cols = append(cols, sqliteColInfo{
			Name:         name,
			Type:         colType,
			NotNull:      notNull != 0,
			PrimaryKey:   pk > 0,
			DefaultValue: dflt.String,
		})
	}
	return cols, hasPK, nil
}

func normalizeSQLiteValue(v any) any {
	if b, ok := v.([]byte); ok {
		return string(b)
	}
	return v
}

func (m *Module) sqliteTableRows(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	table := c.Param("table")
	cols, hasPK, err := sqliteTableColumns(c.Request.Context(), db, table)
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	page, _ := strconv.Atoi(c.DefaultQuery("page", "1"))
	rawSize := c.Query("page_size")
	if rawSize == "" {
		rawSize = c.DefaultQuery("pageSize", "50")
	}
	pageSize, _ := strconv.Atoi(rawSize)
	if page < 1 {
		page = 1
	}
	if pageSize < 1 || pageSize > 200 {
		pageSize = 50
	}
	offset := (page - 1) * pageSize

	var total int64
	_ = db.QueryRowContext(c.Request.Context(), "SELECT COUNT(*) FROM "+sqliteQuote(table)).Scan(&total)

	keyColumns, err := sqliteKeyColumns(c.Request.Context(), db, table, cols)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	prefix, order := "", ""
	if len(keyColumns) > 0 {
		keys := strings.Join(keyColumns, ", ")
		prefix = keys + ", "
		order = " ORDER BY " + keys
	}
	query := fmt.Sprintf("SELECT %s* FROM %s%s LIMIT ? OFFSET ?", prefix, sqliteQuote(table), order)
	rows, err := db.QueryContext(c.Request.Context(), query, pageSize, offset)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	defer rows.Close()
	colNames, _ := rows.Columns()
	result := []gin.H{}
	for rows.Next() {
		raw := make([]any, len(colNames))
		ptrs := make([]any, len(colNames))
		for i := range raw {
			ptrs[i] = &raw[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			response.Fail(c, response.System, response.Summary(err.Error()))
			return
		}
		values := make(map[string]any, len(cols))
		for i := len(keyColumns); i < len(colNames); i++ {
			values[colNames[i]] = normalizeSQLiteValue(raw[i])
		}
		result = append(result, gin.H{"key": encodeSQLiteKey(raw[:len(keyColumns)]), "values": values})
	}
	if err := rows.Err(); err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"columns": cols, "hasPK": hasPK, "rows": result, "total": total, "page": page, "pageSize": pageSize})
}

func (m *Module) sqliteInsertRow(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	table := c.Param("table")
	cols, _, err := sqliteTableColumns(c.Request.Context(), db, table)
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	validCols := map[string]bool{}
	for _, col := range cols {
		validCols[col.Name] = true
	}
	var in struct {
		Values map[string]any `json:"values"`
	}
	if !response.Bind(c, &in) || len(in.Values) == 0 {
		return
	}
	names := make([]string, 0, len(in.Values))
	placeholders := make([]string, 0, len(in.Values))
	args := make([]any, 0, len(in.Values))
	for k, v := range in.Values {
		if !validCols[k] {
			continue
		}
		names = append(names, sqliteQuote(k))
		placeholders = append(placeholders, "?")
		args = append(args, v)
	}
	if len(names) == 0 {
		response.Fail(c, response.Invalid, "没有有效的字段值")
		return
	}
	query := fmt.Sprintf("INSERT INTO %s (%s) VALUES (%s)", sqliteQuote(table), strings.Join(names, ", "), strings.Join(placeholders, ", "))
	if _, err := db.ExecContext(c.Request.Context(), query, args...); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"inserted": true})
}

// 普通表优先用 SQLite 的内部行号，避开被同名列遮蔽的别名。
func sqliteKeyColumns(ctx context.Context, db *sql.DB, table string, columns []sqliteColInfo) ([]string, error) {
	var kind string
	var withoutRowID bool
	err := db.QueryRowContext(ctx, "SELECT type, wr FROM pragma_table_list WHERE schema = 'main' AND name = ?", table).Scan(&kind, &withoutRowID)
	if err != nil || kind != "table" {
		return nil, err
	}
	if !withoutRowID {
		for _, alias := range []string{"rowid", "_rowid_", "oid"} {
			used := false
			for _, column := range columns {
				used = used || strings.EqualFold(column.Name, alias)
			}
			if !used {
				return []string{alias}, nil
			}
		}
	}
	var keys []string
	for _, column := range columns {
		if column.PrimaryKey {
			keys = append(keys, sqliteQuote(column.Name))
		}
	}
	return keys, nil
}

type sqliteKeyPart struct {
	Kind  string
	Value string
}

func encodeSQLiteKey(values []any) string {
	if len(values) == 0 {
		return ""
	}
	parts := make([]sqliteKeyPart, len(values))
	for i, value := range values {
		switch value := value.(type) {
		case int64:
			parts[i] = sqliteKeyPart{"integer", strconv.FormatInt(value, 10)}
		case float64:
			parts[i] = sqliteKeyPart{"real", strconv.FormatFloat(value, 'g', -1, 64)}
		case string:
			parts[i] = sqliteKeyPart{"text", value}
		case []byte:
			parts[i] = sqliteKeyPart{"blob", base64.StdEncoding.EncodeToString(value)}
		default:
			return ""
		}
	}
	raw, _ := json.Marshal(parts)
	return string(raw)
}

func buildSQLiteWhere(ctx context.Context, db *sql.DB, table string, cols []sqliteColInfo, key string) (string, []any, error) {
	columns, err := sqliteKeyColumns(ctx, db, table, cols)
	if err != nil {
		return "", nil, err
	}
	var parts []sqliteKeyPart
	if len(columns) == 0 || json.Unmarshal([]byte(key), &parts) != nil || len(parts) != len(columns) {
		return "", nil, errors.New("当前记录没有有效的唯一行键")
	}
	clauses, args := make([]string, len(parts)), make([]any, len(parts))
	for i, part := range parts {
		var err error
		switch part.Kind {
		case "integer":
			args[i], err = strconv.ParseInt(part.Value, 10, 64)
		case "real":
			args[i], err = strconv.ParseFloat(part.Value, 64)
		case "blob":
			args[i], err = base64.StdEncoding.DecodeString(part.Value)
		case "text":
			args[i] = part.Value
		default:
			return "", nil, errors.New("无效的唯一行键")
		}
		if err != nil {
			return "", nil, errors.New("无效的唯一行键")
		}
		clauses[i] = columns[i] + " = ?"
	}
	return strings.Join(clauses, " AND "), args, nil
}

func sqliteChangeRow(ctx context.Context, db *sql.DB, query string, args ...any) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	count, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return errors.New("记录已变化或定位结果不唯一，请刷新后重试")
	}
	return tx.Commit()
}

func (m *Module) sqliteUpdateRow(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	table := c.Param("table")
	cols, _, err := sqliteTableColumns(c.Request.Context(), db, table)
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	validCols := map[string]bool{}
	for _, col := range cols {
		validCols[col.Name] = true
	}
	var in struct {
		Key    string         `json:"key"`
		Values map[string]any `json:"values"`
	}
	if !response.Bind(c, &in) {
		return
	}
	whereClause, whereArgs, err := buildSQLiteWhere(c.Request.Context(), db, table, cols, in.Key)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	sets := []string{}
	args := []any{}
	for k, v := range in.Values {
		if !validCols[k] {
			continue
		}
		sets = append(sets, sqliteQuote(k)+" = ?")
		args = append(args, v)
	}
	if len(sets) == 0 {
		response.Fail(c, response.Invalid, "没有需要更新的字段")
		return
	}
	args = append(args, whereArgs...)
	query := fmt.Sprintf("UPDATE %s SET %s WHERE %s", sqliteQuote(table), strings.Join(sets, ", "), whereClause)
	if err := sqliteChangeRow(c.Request.Context(), db, query, args...); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"affected": 1})
}

func (m *Module) sqliteDeleteRow(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	table := c.Param("table")
	cols, _, err := sqliteTableColumns(c.Request.Context(), db, table)
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	var in struct {
		Key string `json:"key"`
	}
	if !response.Bind(c, &in) {
		return
	}
	whereClause, whereArgs, err := buildSQLiteWhere(c.Request.Context(), db, table, cols, in.Key)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	query := fmt.Sprintf("DELETE FROM %s WHERE %s", sqliteQuote(table), whereClause)
	if err := sqliteChangeRow(c.Request.Context(), db, query, whereArgs...); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"affected": 1})
}

func (m *Module) sqliteQuery(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	var in struct {
		SQL string `json:"sql"`
	}
	if !response.Bind(c, &in) {
		return
	}
	sqlText := strings.TrimSpace(in.SQL)
	if sqlText == "" {
		response.Fail(c, response.Invalid, "请输入 SQL 语句")
		return
	}
	upper := strings.ToUpper(strings.TrimLeft(sqlText, " \t\r\n("))
	isQuery := strings.HasPrefix(upper, "SELECT") || strings.HasPrefix(upper, "PRAGMA") || strings.HasPrefix(upper, "EXPLAIN") || strings.HasPrefix(upper, "WITH")
	if isQuery {
		rows, err := db.QueryContext(c.Request.Context(), sqlText)
		if err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
		defer rows.Close()
		cols, _ := rows.Columns()
		result := []map[string]any{}
		for rows.Next() && len(result) < 500 {
			raw := make([]any, len(cols))
			ptrs := make([]any, len(cols))
			for i := range raw {
				ptrs[i] = &raw[i]
			}
			if err := rows.Scan(ptrs...); err != nil {
				continue
			}
			item := make(map[string]any, len(cols))
			for i, name := range cols {
				item[name] = normalizeSQLiteValue(raw[i])
			}
			result = append(result, item)
		}
		response.OK(c, gin.H{"columns": cols, "rows": result, "message": fmt.Sprintf("查询返回 %d 行", len(result))})
		return
	}
	res, err := db.ExecContext(c.Request.Context(), sqlText)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	affected, _ := res.RowsAffected()
	response.OK(c, gin.H{"columns": []string{}, "rows": []map[string]any{}, "affected": affected, "message": fmt.Sprintf("执行成功，影响 %d 行", affected)})
}

func (m *Module) sqliteBackupDir(id string) (string, error) {
	safeID := pathsafe.BackupName(id)
	if safeID == "" {
		return "", errors.New("无效的数据库标识")
	}
	dir := filepath.Join(m.cfg.Current().BackupRoot, "database", "sqlite", safeID)
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", err
	}
	if _, err := policy.Check(dir, true); err != nil {
		return "", errors.New("数据库备份目录不可用或指向受保护目录")
	}
	return dir, nil
}

func sqliteSafeBackupFilename(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return strings.HasSuffix(name, ".db")
}

func (m *Module) readSQLiteBackups(id string) ([]sqliteBackupItem, error) {
	dir, err := m.sqliteBackupDir(id)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []sqliteBackupItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]sqliteBackupItem, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".db") {
			continue
		}
		info, err := ent.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, sqliteBackupItem{
			FileName:  ent.Name(),
			Size:      info.Size(),
			CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
			Path:      filepath.Join(dir, ent.Name()),
		})
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt > out[j].CreatedAt
	})
	return out, nil
}

func (m *Module) sqliteListBackups(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	list, err := m.readSQLiteBackups(entry.ID)
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) sqliteCreateBackup(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	db, err := m.openSQLiteFile(entry.Path)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	defer db.Close()

	dir, err := m.sqliteBackupDir(entry.ID)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		response.Fail(c, response.System, "备份目录不可写")
		return
	}
	loc, err := time.LoadLocation(m.cfg.Current().Timezone)
	if err != nil {
		loc = time.UTC
	}
	safeName := pathsafe.BackupName(entry.Name)
	if safeName == "" {
		safeName = "sqlite"
	}
	filename := fmt.Sprintf("%s_sqlite_%s.db", safeName, time.Now().In(loc).Format("20060102_150405"))
	finalPath := filepath.Join(dir, filename)
	partPath := finalPath + ".part"
	_ = os.Remove(partPath)

	ctx, cancel := context.WithTimeout(c.Request.Context(), 5*time.Minute)
	defer cancel()
	if _, err := db.ExecContext(ctx, "VACUUM INTO ?", partPath); err != nil {
		_ = os.Remove(partPath)
		response.Fail(c, response.System, "备份失败: "+response.Summary(err.Error()))
		return
	}
	if err := os.Chmod(partPath, 0600); err != nil {
		_ = os.Remove(partPath)
		response.Fail(c, response.System, "设置备份文件权限失败")
		return
	}
	if err := os.Rename(partPath, finalPath); err != nil {
		_ = os.Remove(partPath)
		response.Fail(c, response.System, "保存备份文件失败")
		return
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		response.Fail(c, response.System, "读取备份文件信息失败")
		return
	}
	response.OK(c, sqliteBackupItem{
		FileName:  filename,
		Size:      info.Size(),
		CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		Path:      finalPath,
	})
}

func (m *Module) sqliteDownloadBackup(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	filename := c.Param("filename")
	if !sqliteSafeBackupFilename(filename) {
		response.Fail(c, response.Invalid, "无效的备份文件名")
		return
	}
	dir, err := m.sqliteBackupDir(entry.ID)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	full := filepath.Join(dir, filename)
	info, err := os.Stat(full)
	if err != nil || !info.Mode().IsRegular() {
		response.Fail(c, response.NotFound, "备份文件不存在")
		return
	}
	c.FileAttachment(full, filename)
}

func (m *Module) sqliteDeleteBackup(c *gin.Context) {
	entry, err := m.findSQLiteEntry(c.Param("id"))
	if err != nil {
		response.Fail(c, response.NotFound, err.Error())
		return
	}
	filename := c.Param("filename")
	if !sqliteSafeBackupFilename(filename) {
		response.Fail(c, response.Invalid, "无效的备份文件名")
		return
	}
	dir, err := m.sqliteBackupDir(entry.ID)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	full := filepath.Join(dir, filename)
	if err := os.Remove(full); err != nil && !errors.Is(err, os.ErrNotExist) {
		response.Fail(c, response.System, "删除备份文件失败")
		return
	}
	response.OK(c, gin.H{"deleted": filename})
}
