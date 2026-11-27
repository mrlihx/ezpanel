package database

import (
	"compress/gzip"
	"context"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/logview"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

const pgMetaSettingKey = "database.postgresql.meta"

type pgDBMeta struct {
	Remark string `json:"remark,omitempty"`
}

var pgIdent = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_$-]{0,62}$`)
var pgSelectInto = regexp.MustCompile(`(?i)\bINTO\b`)

func (m *Module) postgresRoutes(r *gin.RouterGroup) {
	pg := r.Group("/postgresql")
	pg.GET("/instances", m.pgInstances)
	pg.GET("/:instance/databases", m.pgDatabases)
	pg.POST("/:instance/databases", m.pgCreateDatabase)
	pg.DELETE("/:instance/databases/:name", m.pgDropDatabase)
	pg.POST("/:instance/databases/:name/password/reveal", m.pgRevealPassword)
	pg.PUT("/:instance/databases/:name/metadata", m.pgDatabaseMetadata)
	pg.GET("/:instance/users", m.pgUsers)
	pg.POST("/:instance/users", m.pgCreateUser)
	pg.PUT("/:instance/users/:name/password", m.pgUserPassword)
	pg.DELETE("/:instance/users/:name", m.pgDropUser)
	pg.GET("/:instance/databases/:name/tables", m.pgTables)
	pg.GET("/:instance/databases/:name/tables/:schema/:table/rows", m.pgTableRows)
	pg.POST("/:instance/databases/:name/query", m.pgQuery)
	pg.GET("/:instance/databases/:name/backups", m.pgListBackups)
	pg.POST("/:instance/databases/:name/backups", m.pgCreateBackup)
	pg.GET("/:instance/databases/:name/backups/:filename/download", m.pgDownloadBackup)
	pg.DELETE("/:instance/databases/:name/backups/:filename", m.pgDeleteBackup)
	pg.GET("/:instance/extensions", m.pgExtensions)
	pg.POST("/:instance/extensions", m.pgInstallExtension)
	pg.DELETE("/:instance/extensions/:name", m.pgUninstallExtension)
}

func pgMetaKey(instance, dbName string) string {
	return instance + "/" + dbName
}

func (m *Module) readPGMeta() (map[string]pgDBMeta, error) {
	var row store.Setting
	err := m.db.First(&row, "key = ?", pgMetaSettingKey).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return map[string]pgDBMeta{}, nil
	}
	if err != nil {
		return nil, err
	}
	var data map[string]pgDBMeta
	if err := json.Unmarshal([]byte(row.Value), &data); err != nil || data == nil {
		return map[string]pgDBMeta{}, nil
	}
	return data, nil
}

func (m *Module) savePGMeta(data map[string]pgDBMeta) error {
	raw, err := json.Marshal(data)
	if err != nil {
		return err
	}
	return m.db.Save(&store.Setting{Key: pgMetaSettingKey, Value: string(raw)}).Error
}

func pgRolePasswordPrefix(instance string) string {
	return "database.postgresql.role-password." + instance + "."
}

func (m *Module) savePGRolePassword(ctx context.Context, instance, role, password string) error {
	encrypted, err := store.Encrypt(m.cfg.AESKey, password)
	if err != nil {
		return err
	}
	return m.db.WithContext(ctx).Save(&store.Setting{
		Key: pgRolePasswordPrefix(instance) + role, Value: encrypted,
	}).Error
}

func pgQuoteIdent(s string) string {
	return `"` + strings.ReplaceAll(s, `"`, `""`) + `"`
}

func pgQuoteLiteral(s string) string {
	return `E'` + strings.NewReplacer(`\`, `\\`, `'`, `''`).Replace(s) + `'`
}

func (m *Module) pgContainer(ctx context.Context, instance string) (pkgmgr.DatabaseInstance, error) {
	row, err := m.packages.DatabaseInstance(ctx, "postgresql", instance)
	if err != nil {
		return row, err
	}
	if row.ContainerID == "" {
		return row, errors.New("PostgreSQL 实例不存在")
	}
	if !row.Running {
		return row, errors.New("PostgreSQL 实例未运行，请先启动实例")
	}
	return row, nil
}

func (m *Module) pgExec(ctx context.Context, container, dbName, sqlText string, output ...string) (string, error) {
	return m.pgRun(ctx, container, dbName, sqlText, false, output)
}

func (m *Module) pgRun(ctx context.Context, container, dbName, sqlText string, private bool, output []string) (string, error) {
	docker, err := executor.LookPath("docker")
	if err != nil {
		return "", errors.New("找不到 Docker 命令")
	}
	if dbName == "" {
		dbName = "postgres"
	}
	args := []string{"exec", "-i", container, "psql", "-U", "postgres", "-d", dbName, "-v", "ON_ERROR_STOP=1", "-X", "-q"}
	if len(output) == 0 {
		args = append(args, "-At")
	} else {
		args = append(args, output...)
	}
	var stdin io.Reader
	if private {
		// 角色密码通过标准输入传给 psql，避免出现在进程参数中。
		stdin = strings.NewReader(sqlText + "\n")
		args = append(args, "-f", "-")
	} else {
		args = append(args, "-c", sqlText)
	}
	res, err := executor.Pipe(ctx, 30*time.Second, nil, stdin, nil, docker, args...)
	if err != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		if msg == "" {
			msg = err.Error()
		}
		return "", errors.New(msg)
	}
	if res.Truncated {
		return "", errors.New("PostgreSQL 查询输出超过 1 MiB，请缩小查询范围")
	}
	if len(output) > 0 {
		return res.Stdout, nil
	}
	return strings.TrimSpace(res.Stdout), nil
}

func (m *Module) pgInstances(c *gin.Context) {
	rows, err := m.packages.DatabaseInstances(c.Request.Context(), "postgresql")
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"list": rows})
}

func (m *Module) pgDatabases(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	query := `SELECT coalesce(json_agg(t), '[]'::json) FROM (
		SELECT d.datname AS name,
		       pg_catalog.pg_get_userbyid(d.datdba) AS owner,
		       pg_catalog.pg_encoding_to_char(d.encoding) AS encoding,
		       pg_catalog.pg_database_size(d.datname) AS "sizeBytes"
		FROM pg_catalog.pg_database d
		WHERE d.datistemplate = false AND d.datname <> 'postgres'
		ORDER BY d.datname
	) t;`
	out, err := m.pgExec(c.Request.Context(), row.ContainerID, "postgres", query)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
		return
	}
	metaMap, _ := m.readPGMeta()
	var credentials []store.Setting
	prefix := pgRolePasswordPrefix(row.ID)
	if err := m.db.WithContext(c.Request.Context()).Where("key LIKE ?", prefix+"%").Find(&credentials).Error; err != nil {
		response.Fail(c, response.System, "读取角色密码记录失败")
		return
	}
	roles := make(map[string]bool, len(credentials))
	for _, entry := range credentials {
		roles[strings.TrimPrefix(entry.Key, prefix)] = entry.Value != ""
	}
	for _, item := range list {
		if dbName, ok := item["name"].(string); ok && dbName != "" {
			if backups, err := m.readPGBackups(row.ID, dbName); err == nil {
				item["backupCount"] = len(backups)
			} else {
				item["backupCount"] = 0
			}
			meta := metaMap[pgMetaKey(row.ID, dbName)]
			username, _ := item["owner"].(string)
			item["username"] = username
			item["hasPassword"] = roles[username]
			item["remark"] = meta.Remark
		}
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) pgCreateDatabase(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	var in struct {
		Name      string `json:"name"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Encoding  string `json:"encoding"`
		ReuseRole bool   `json:"reuseRole"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !pgIdent.MatchString(in.Name) || in.Name == "postgres" || in.Name == "template0" || in.Name == "template1" {
		response.Fail(c, response.Invalid, "数据库名称无效或为系统保留名")
		return
	}
	encoding := strings.TrimSpace(in.Encoding)
	if encoding == "" {
		encoding = "UTF8"
	}
	if encoding != "UTF8" && encoding != "SQL_ASCII" && encoding != "LATIN1" {
		response.Fail(c, response.Invalid, "不支持的字符编码")
		return
	}
	owner := strings.TrimSpace(in.Username)
	if owner == "" {
		owner, in.ReuseRole = "postgres", true
	}
	if !pgIdent.MatchString(owner) {
		response.Fail(c, response.Invalid, "用户名格式无效")
		return
	}
	ctx := c.Request.Context()
	if !in.ReuseRole {
		if len(in.Password) < 6 {
			response.Fail(c, response.Invalid, "新建角色密码至少需要 6 位")
			return
		}
		createRole := fmt.Sprintf("CREATE ROLE %s WITH LOGIN PASSWORD %s;", pgQuoteIdent(owner), pgQuoteLiteral(in.Password))
		if _, err := m.pgRun(ctx, row.ContainerID, "postgres", createRole, true, nil); err != nil {
			response.Fail(c, response.Invalid, response.Summary(err.Error()))
			return
		}
	}
	sqlText := fmt.Sprintf("CREATE DATABASE %s OWNER %s ENCODING %s;", pgQuoteIdent(in.Name), pgQuoteIdent(owner), pgQuoteLiteral(encoding))
	if _, err := m.pgExec(ctx, row.ContainerID, "postgres", sqlText); err != nil {
		if !in.ReuseRole {
			_, cleanupErr := m.pgExec(context.WithoutCancel(ctx), row.ContainerID, "postgres", "DROP ROLE "+pgQuoteIdent(owner)+";")
			err = errors.Join(err, cleanupErr)
		}
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	if !in.ReuseRole {
		if err := m.savePGRolePassword(ctx, row.ID, owner, in.Password); err != nil {
			response.Fail(c, response.System, "数据库已创建，但面板保存角色密码失败")
			return
		}
	}
	response.OK(c, gin.H{"name": in.Name, "owner": owner})
}

func (m *Module) pgDropDatabase(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(name) || name == "postgres" || name == "template0" || name == "template1" {
		response.Fail(c, response.Invalid, "不能删除系统默认数据库")
		return
	}
	_, _ = m.pgExec(c.Request.Context(), row.ContainerID, "postgres",
		fmt.Sprintf("SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = %s AND pid <> pg_backend_pid();", pgQuoteLiteral(name)))
	if _, err := m.pgExec(c.Request.Context(), row.ContainerID, "postgres",
		fmt.Sprintf("DROP DATABASE IF EXISTS %s;", pgQuoteIdent(name))); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	m.pgMetaMu.Lock()
	if metaMap, err := m.readPGMeta(); err == nil {
		delete(metaMap, pgMetaKey(row.ID, name))
		_ = m.savePGMeta(metaMap)
	}
	m.pgMetaMu.Unlock()
	response.OK(c, gin.H{"deleted": true})
}

func (m *Module) pgRevealPassword(c *gin.Context) {
	ctx := c.Request.Context()
	row, err := m.pgContainer(ctx, c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(name) {
		response.Fail(c, response.Invalid, "数据库名格式无效")
		return
	}
	owner, err := m.pgExec(ctx, row.ContainerID, "postgres",
		"SELECT pg_catalog.pg_get_userbyid(datdba) FROM pg_catalog.pg_database WHERE datname = "+pgQuoteLiteral(name)+";")
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	if owner == "" {
		response.Fail(c, response.NotFound, "数据库不存在")
		return
	}
	var record store.Setting
	err = m.db.WithContext(ctx).First(&record, "key = ?", pgRolePasswordPrefix(row.ID)+owner).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		response.Fail(c, response.NotFound, "未记录该角色密码")
		return
	}
	if err != nil {
		response.Fail(c, response.System, "读取密码记录失败")
		return
	}
	plain, err := store.Decrypt(m.cfg.AESKey, record.Value)
	if err != nil {
		response.Fail(c, response.System, "解密密码失败")
		return
	}
	c.Header("Cache-Control", "no-store")
	response.OK(c, gin.H{"password": plain})
}

func (m *Module) pgDatabaseMetadata(c *gin.Context) {
	instance := c.Param("instance")
	name := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(name) {
		response.Fail(c, response.Invalid, "数据库名格式无效")
		return
	}
	var in struct {
		Remark string `json:"remark"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if len(in.Remark) > 512 {
		response.Fail(c, response.Invalid, "备注长度不能超过 512 字符")
		return
	}
	m.pgMetaMu.Lock()
	defer m.pgMetaMu.Unlock()
	metaMap, err := m.readPGMeta()
	if err != nil {
		response.Fail(c, response.System, "读取元数据失败")
		return
	}
	meta := metaMap[pgMetaKey(instance, name)]
	meta.Remark = strings.TrimSpace(in.Remark)
	metaMap[pgMetaKey(instance, name)] = meta
	if err := m.savePGMeta(metaMap); err != nil {
		response.Fail(c, response.System, "保存备注失败")
		return
	}
	response.OK(c, gin.H{"remark": meta.Remark})
}

func (m *Module) pgUsers(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	query := `SELECT coalesce(json_agg(t), '[]'::json) FROM (
		SELECT rolname AS name, rolsuper AS superuser, rolcreatedb AS createdb, rolcanlogin AS "canLogin"
		FROM pg_catalog.pg_roles
		WHERE rolcanlogin = true AND left(rolname, 3) <> 'pg_'
		ORDER BY rolname
	) t;`
	out, err := m.pgExec(c.Request.Context(), row.ContainerID, "postgres", query)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
		return
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) pgCreateUser(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	var in struct {
		Name      string `json:"name"`
		Password  string `json:"password"`
		Superuser bool   `json:"superuser"`
		CreateDB  bool   `json:"createdb"`
	}
	if !response.Bind(c, &in) {
		return
	}
	in.Name = strings.TrimSpace(in.Name)
	if !pgIdent.MatchString(in.Name) || strings.HasPrefix(in.Name, "pg_") {
		response.Fail(c, response.Invalid, "角色名无效")
		return
	}
	if len(in.Password) < 6 {
		response.Fail(c, response.Invalid, "密码至少需要 6 位")
		return
	}
	opts := []string{"LOGIN", "PASSWORD " + pgQuoteLiteral(in.Password)}
	if in.Superuser {
		opts = append(opts, "SUPERUSER")
	}
	if in.CreateDB {
		opts = append(opts, "CREATEDB")
	}
	if _, err := m.pgRun(c.Request.Context(), row.ContainerID, "postgres",
		fmt.Sprintf("CREATE ROLE %s WITH %s;", pgQuoteIdent(in.Name), strings.Join(opts, " ")), true, nil); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	if err := m.savePGRolePassword(c.Request.Context(), row.ID, in.Name, in.Password); err != nil {
		response.Fail(c, response.System, "角色已创建，但面板保存密码失败")
		return
	}
	response.OK(c, gin.H{"name": in.Name})
}

func (m *Module) pgUserPassword(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	var in struct {
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if !pgIdent.MatchString(name) || len(in.Password) < 6 {
		response.Fail(c, response.Invalid, "角色名无效或密码少于 6 位")
		return
	}
	if _, err := m.pgRun(c.Request.Context(), row.ContainerID, "postgres",
		fmt.Sprintf("ALTER ROLE %s WITH PASSWORD %s;", pgQuoteIdent(name), pgQuoteLiteral(in.Password)), true, nil); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	if err := m.savePGRolePassword(c.Request.Context(), row.ID, name, in.Password); err != nil {
		response.Fail(c, response.System, "角色密码已更新，但面板保存密码失败")
		return
	}
	response.OK(c, gin.H{"updated": true})
}

func (m *Module) pgDropUser(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	name := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(name) || name == "postgres" {
		response.Fail(c, response.Invalid, "不能删除 postgres 超级管理员")
		return
	}
	if _, err := m.pgExec(c.Request.Context(), row.ContainerID, "postgres",
		fmt.Sprintf("DROP ROLE IF EXISTS %s;", pgQuoteIdent(name))); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	if err := m.db.Delete(&store.Setting{}, "key = ?", pgRolePasswordPrefix(row.ID)+name).Error; err != nil {
		response.Fail(c, response.System, "角色已删除，但面板清理密码记录失败")
		return
	}
	response.OK(c, gin.H{"deleted": true})
}

func (m *Module) pgTables(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(dbName) {
		response.Fail(c, response.Invalid, "数据库名无效")
		return
	}
	query := `SELECT coalesce(json_agg(t), '[]'::json) FROM (
		SELECT n.nspname AS schema,
		       c.relname AS name,
		       CASE c.relkind WHEN 'v' THEN 'view' ELSE 'table' END AS type,
		       GREATEST(c.reltuples::bigint, 0) AS "rowEstimate",
		       pg_total_relation_size(c.oid) AS "sizeBytes"
		FROM pg_catalog.pg_class c
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		WHERE c.relkind IN ('r', 'p', 'v')
		  AND n.nspname NOT IN ('pg_catalog', 'information_schema')
		  AND n.nspname NOT LIKE 'pg_toast%'
		ORDER BY n.nspname, c.relname
	) t;`
	out, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, query)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
		return
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) pgTableRows(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Param("name"))
	schema := strings.TrimSpace(c.Param("schema"))
	table := strings.TrimSpace(c.Param("table"))
	if !pgIdent.MatchString(dbName) || !pgIdent.MatchString(schema) || !pgIdent.MatchString(table) {
		response.Fail(c, response.Invalid, "参数无效")
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

	colSQL := fmt.Sprintf(`SELECT coalesce(json_agg(t), '[]'::json) FROM (
		SELECT a.attname AS name,
		       pg_catalog.format_type(a.atttypid, a.atttypmod) AS type,
		       NOT a.attnotnull AS nullable,
		       COALESCE(i.indisprimary, false) AS "primaryKey"
		FROM pg_catalog.pg_attribute a
		JOIN pg_catalog.pg_class c ON c.oid = a.attrelid
		JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
		LEFT JOIN pg_catalog.pg_index i ON i.indrelid = c.oid AND i.indisprimary AND a.attnum = ANY(i.indkey)
		WHERE n.nspname = %s AND c.relname = %s AND a.attnum > 0 AND NOT a.attisdropped
		ORDER BY a.attnum
	) t;`, pgQuoteLiteral(schema), pgQuoteLiteral(table))
	colOut, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, colSQL)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var columns []map[string]any
	if err := json.Unmarshal([]byte(colOut), &columns); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 表结构")
		return
	}

	qualified := pgQuoteIdent(schema) + "." + pgQuoteIdent(table)
	countOut, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, "SELECT count(*) FROM "+qualified+";")
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	total, _ := strconv.ParseInt(strings.TrimSpace(countOut), 10, 64)

	rowsSQL := fmt.Sprintf("SELECT coalesce(json_agg(row_to_json(t)), '[]'::json) FROM (SELECT * FROM %s LIMIT %d OFFSET %d) t;", qualified, pageSize, offset)
	rowsOut, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, rowsSQL)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var rows []map[string]any
	if err := json.Unmarshal([]byte(rowsOut), &rows); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 表数据")
		return
	}
	response.OK(c, gin.H{"columns": columns, "rows": rows, "total": total, "page": page, "pageSize": pageSize})
}

func (m *Module) pgQuery(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(dbName) {
		response.Fail(c, response.Invalid, "数据库名无效")
		return
	}
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
	statement := strings.TrimRight(sqlText, "; \t\r\n")
	keyword := strings.ToUpper(strings.Fields(sqlText)[0])
	major, _ := strconv.Atoi(strings.SplitN(row.Version, ".", 2)[0])
	csvResult := major >= 12 && !strings.Contains(statement, ";") &&
		(keyword == "SELECT" || keyword == "WITH" || keyword == "SHOW" || keyword == "EXPLAIN" || keyword == "TABLE" || keyword == "VALUES")
	var options []string
	jsonResult := major < 12 && keyword == "SELECT" && !strings.Contains(statement, ";") && !pgSelectInto.MatchString(statement)
	nullValue := ""
	command := sqlText
	if csvResult {
		// psql 会为点分隔的 CSV 字段加引号，单列空字符串仍占一整行。
		nullValue = "ezpanel-null-" + store.ID()
		options = []string{"--csv", "-P", "csv_fieldsep=.", "-P", "null=" + nullValue}
	} else if jsonResult {
		// 旧版 psql 不支持 CSV。普通 SELECT 在一条语句内生成有界结果。
		command = "SELECT coalesce(json_agg(row_to_json(t)), '[]'::json) FROM (SELECT * FROM (" + statement + ") q LIMIT 501) t;"
	}
	out, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, command, options...)
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	columns := []string{}
	rows := []map[string]any{}
	message := out
	if csvResult && out != "" {
		reader := csv.NewReader(strings.NewReader(out))
		reader.Comma = '.'
		columns, err = reader.Read()
		if err != nil {
			response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
			return
		}
		for len(rows) <= 500 {
			fields, err := reader.Read()
			if errors.Is(err, io.EOF) {
				break
			}
			if err != nil {
				response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
				return
			}
			item := make(map[string]any, len(columns))
			for i, name := range columns {
				if fields[i] == nullValue {
					item[name] = nil
				} else {
					item[name] = fields[i]
				}
			}
			rows = append(rows, item)
		}
	} else if jsonResult {
		if err := json.Unmarshal([]byte(out), &rows); err != nil {
			response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
			return
		}
		if len(rows) != 0 {
			for name := range rows[0] {
				columns = append(columns, name)
			}
			sort.Strings(columns)
		}
	}
	if csvResult && out != "" || jsonResult {
		if len(rows) > 500 {
			rows = rows[:500]
			message = "仅显示前 500 行，请缩小查询范围"
		} else {
			message = fmt.Sprintf("查询返回 %d 行", len(rows))
		}
	}
	if message == "" {
		message = "执行成功"
	}
	response.OK(c, gin.H{"columns": columns, "rows": rows, "message": message})
}

type pgBackupItem struct {
	FileName  string `json:"fileName"`
	Size      int64  `json:"size"`
	CreatedAt string `json:"createdAt"`
	Path      string `json:"path"`
}

func (m *Module) pgBackupDir(instance, dbName string) (string, error) {
	safeInst := pathsafe.BackupName(instance)
	safeDB := pathsafe.BackupName(dbName)
	if safeInst == "" || safeDB == "" {
		return "", errors.New("无效的数据库标识")
	}
	dir := filepath.Join(m.cfg.Current().BackupRoot, "database", "postgresql", safeInst, safeDB)
	policy, err := pathsafe.New(m.cfg.InstallDir)
	if err != nil {
		return "", err
	}
	if _, err := policy.Check(dir, true); err != nil {
		return "", errors.New("数据库备份目录不可用或指向受保护目录")
	}
	return dir, nil
}

func pgSafeBackupFilename(name string) bool {
	if name == "" || strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return false
	}
	return strings.HasSuffix(name, ".sql.gz")
}

func (m *Module) readPGBackups(instance, dbName string) ([]pgBackupItem, error) {
	dir, err := m.pgBackupDir(instance, dbName)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir(dir)
	if errors.Is(err, os.ErrNotExist) {
		return []pgBackupItem{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := make([]pgBackupItem, 0, len(entries))
	for _, ent := range entries {
		if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".sql.gz") {
			continue
		}
		info, err := ent.Info()
		if err != nil || !info.Mode().IsRegular() {
			continue
		}
		out = append(out, pgBackupItem{
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

func (m *Module) pgListBackups(c *gin.Context) {
	dbName := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(dbName) {
		response.Fail(c, response.Invalid, "数据库名格式无效")
		return
	}
	list, err := m.readPGBackups(c.Param("instance"), dbName)
	if err != nil {
		response.Fail(c, response.System, err.Error())
		return
	}
	response.OK(c, gin.H{"list": list})
}

func (m *Module) pgCreateBackup(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(dbName) {
		response.Fail(c, response.Invalid, "数据库名格式无效")
		return
	}
	docker, err := executor.LookPath("docker")
	if err != nil {
		response.Fail(c, response.System, "找不到 Docker 命令")
		return
	}
	dir, err := m.pgBackupDir(row.ID, dbName)
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
	safeName := pathsafe.BackupName(dbName)
	filename := safeName + "_postgresql_data_" + time.Now().In(loc).Format("20060102") + "_" + strconv.Itoa(rand.IntN(900000)+100000) + ".sql.gz"
	finalPath := filepath.Join(dir, filename)
	partPath := finalPath + ".part"
	f, err := os.OpenFile(partPath, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		response.Fail(c, response.System, "无法创建备份文件")
		return
	}
	defer func() {
		_ = f.Close()
		_ = os.Remove(partPath)
	}()

	gz := gzip.NewWriter(f)
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Minute)
	defer cancel()
	res, err := executor.Pipe(ctx, 30*time.Minute, nil, nil, gz, docker,
		"exec", row.ContainerID, "pg_dump", "-U", "postgres", "-d", dbName)
	closeErr := gz.Close()
	if err != nil {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = err.Error()
		}
		response.Fail(c, response.System, "备份失败: "+response.Summary(msg))
		return
	}
	if closeErr != nil {
		response.Fail(c, response.System, "写入压缩备份失败")
		return
	}
	if err := f.Sync(); err != nil {
		response.Fail(c, response.System, "保存备份文件失败")
		return
	}
	_ = f.Close()
	if err := os.Rename(partPath, finalPath); err != nil {
		response.Fail(c, response.System, "保存备份文件失败")
		return
	}
	info, err := os.Stat(finalPath)
	if err != nil {
		response.Fail(c, response.System, "读取备份文件信息失败")
		return
	}
	response.OK(c, pgBackupItem{
		FileName:  filename,
		Size:      info.Size(),
		CreatedAt: info.ModTime().UTC().Format(time.RFC3339),
		Path:      finalPath,
	})
}

func (m *Module) pgDownloadBackup(c *gin.Context) {
	dbName := strings.TrimSpace(c.Param("name"))
	filename := c.Param("filename")
	if !pgIdent.MatchString(dbName) || !pgSafeBackupFilename(filename) {
		response.Fail(c, response.Invalid, "无效的备份文件名")
		return
	}
	dir, err := m.pgBackupDir(c.Param("instance"), dbName)
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

func (m *Module) pgDeleteBackup(c *gin.Context) {
	dbName := strings.TrimSpace(c.Param("name"))
	filename := c.Param("filename")
	if !pgIdent.MatchString(dbName) || !pgSafeBackupFilename(filename) {
		response.Fail(c, response.Invalid, "无效的备份文件名")
		return
	}
	dir, err := m.pgBackupDir(c.Param("instance"), dbName)
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

func (m *Module) postgresDiagnostics(ctx context.Context, c *gin.Context, row pkgmgr.DatabaseInstance, view string) error {
	switch view {
	case "status":
		metrics := map[string]string{}
		sqlQuery := `SELECT json_build_object(
			'connections', (SELECT count(*)::text FROM pg_stat_activity),
			'sizeKB', (SELECT (coalesce(sum(pg_database_size(datname)), 0) / 1024)::text || ' kB' FROM pg_database),
			'startTime', to_char(pg_postmaster_start_time() AT TIME ZONE 'UTC', 'YYYY-MM-DD"T"HH24:MI:SS"Z"')
		)::text;`
		if out, err := m.pgExec(ctx, row.ContainerID, "postgres", sqlQuery); err == nil {
			var res map[string]string
			if json.Unmarshal([]byte(out), &res) == nil {
				for k, v := range res {
					metrics[k] = v
				}
			}
		}
		docker, err := executor.LookPath("docker")
		if err == nil {
			// Inspect host PID of container and /proc/<pid>/status + process count inside container
			pidRes, err := executor.Run(ctx, 5*time.Second, docker, "inspect", "--format", "{{.State.Pid}}", row.ContainerID)
			hostPid := strings.TrimSpace(pidRes.Stdout)
			if err == nil && hostPid != "" && hostPid != "0" {
				metrics["pid"] = hostPid
				if raw, err := os.ReadFile("/proc/" + hostPid + "/status"); err == nil {
					for line := range strings.SplitSeq(string(raw), "\n") {
						k, v, ok := strings.Cut(line, ":")
						if !ok {
							continue
						}
						v = strings.TrimSpace(v)
						switch k {
						case "VmRSS", "VmLck", "VmHWM", "VmData", "VmStk", "VmExe", "VmLib", "VmSwap", "VmPTE", "SigQ":
							metrics[k] = v
						}
					}
				}
			}
			// Count postgres processes inside the container
			cntRes, err := executor.Run(ctx, 5*time.Second, docker, "exec", row.ContainerID, "sh", "-c", "ps -eo comm 2>/dev/null | grep -c postgres || echo 1")
			if err == nil {
				if cnt := strings.TrimSpace(cntRes.Stdout); cnt != "" {
					metrics["processCount"] = cnt
				}
			}
		}
		response.OK(c, gin.H{"metrics": metrics, "variables": map[string]string{}})
		return nil

	case "slow":
		durOut, _ := m.pgExec(ctx, row.ContainerID, "postgres", "SHOW log_min_duration_statement;")
		dur := strings.TrimSpace(durOut)
		enabled := dur != "" && dur != "-1" && dur != "-1ms"
		sources, err := m.packages.DatabaseLogSources(ctx, "postgresql", row)
		var out logview.Text
		if err == nil && len(sources) > 0 && sources[0].Path != "" {
			if full, rerr := logview.Read(sources[0].Path); rerr == nil {
				out = full
				var slowLines []string
				for line := range strings.SplitSeq(full.Content, "\n") {
					if strings.Contains(line, "duration:") {
						slowLines = append(slowLines, line)
					}
				}
				out.Content = strings.Join(slowLines, "\n")
				out.Clearable = false
			}
		}
		response.OK(c, gin.H{"enabled": enabled, "seconds": dur, "log": out})
		return nil
	default:
		return errors.New("无效的 PostgreSQL 诊断类型")
	}
}

func (m *Module) pgExtensions(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Query("db"))
	if dbName == "" {
		dbName = "postgres"
	}
	if !pgIdent.MatchString(dbName) {
		response.Fail(c, response.Invalid, "数据库名格式无效")
		return
	}
	query := `SELECT coalesce(json_agg(t), '[]'::json) FROM (
		SELECT name,
		       coalesce(default_version, '') AS "defaultVersion",
		       coalesce(installed_version, '') AS "installedVersion",
		       coalesce(comment, '') AS comment
		FROM pg_available_extensions
		ORDER BY (installed_version IS NULL), name
	) t;`
	out, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName, query)
	if err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	var list []map[string]any
	if err := json.Unmarshal([]byte(out), &list); err != nil {
		response.Fail(c, response.System, "无法解析 PostgreSQL 查询结果")
		return
	}
	dbsOut, _ := m.pgExec(c.Request.Context(), row.ContainerID, "postgres",
		`SELECT coalesce(json_agg(datname ORDER BY datname), '[]'::json) FROM pg_database WHERE datistemplate = false;`)
	var dbs []string
	if json.Unmarshal([]byte(dbsOut), &dbs) != nil {
		dbs = []string{"postgres"}
	}
	response.OK(c, gin.H{"list": list, "databases": dbs, "database": dbName})
}

func (m *Module) pgInstallExtension(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	var in struct {
		DB   string `json:"db"`
		Name string `json:"name"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.DB == "" {
		in.DB = "postgres"
	}
	if !pgIdent.MatchString(in.DB) || !pgIdent.MatchString(in.Name) {
		response.Fail(c, response.Invalid, "扩展名或数据库名格式无效")
		return
	}
	if _, err := m.pgExec(c.Request.Context(), row.ContainerID, in.DB,
		fmt.Sprintf("CREATE EXTENSION IF NOT EXISTS %s;", pgQuoteIdent(in.Name))); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"installed": in.Name})
}

func (m *Module) pgUninstallExtension(c *gin.Context) {
	row, err := m.pgContainer(c.Request.Context(), c.Param("instance"))
	if err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	dbName := strings.TrimSpace(c.Query("db"))
	if dbName == "" {
		dbName = "postgres"
	}
	name := strings.TrimSpace(c.Param("name"))
	if !pgIdent.MatchString(dbName) || !pgIdent.MatchString(name) {
		response.Fail(c, response.Invalid, "扩展名或数据库名格式无效")
		return
	}
	if _, err := m.pgExec(c.Request.Context(), row.ContainerID, dbName,
		fmt.Sprintf("DROP EXTENSION IF EXISTS %s;", pgQuoteIdent(name))); err != nil {
		response.Fail(c, response.Invalid, response.Summary(err.Error()))
		return
	}
	response.OK(c, gin.H{"uninstalled": name})
}
