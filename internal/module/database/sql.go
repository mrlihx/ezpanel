package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm/clause"
)

func (m *Module) withDB(c *gin.Context, write bool, fn func(context.Context, *sql.DB) error) {
	m.withDBOptions(c, write, write, fn)
}

func (m *Module) withDBOptions(c *gin.Context, lock, interpolate bool, fn func(context.Context, *sql.DB) error) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	if lock {
		release, e := m.tasks.Acquire(LockKey(s.ID))
		if e != nil {
			failure(c, e)
			return
		}
		defer release()
	}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	db, e := m.connect(ctx, s, interpolate)
	if e != nil {
		failure(c, e)
		return
	}
	defer db.Close()
	if e = fn(ctx, db); e != nil {
		failure(c, e)
	}
}

type charset struct {
	Charset    string   `json:"charset"`
	Collations []string `json:"collations"`
}

func charsets(ctx context.Context, db *sql.DB) ([]charset, error) {
	rows, e := db.QueryContext(ctx, "SELECT CHARACTER_SET_NAME,COLLATION_NAME FROM information_schema.COLLATIONS ORDER BY CHARACTER_SET_NAME,COLLATION_NAME")
	if e != nil {
		return nil, errors.New("字符集查询失败，请检查数据库权限")
	}
	defer rows.Close()
	out := []charset{}
	for rows.Next() {
		var ch, co string
		if e := rows.Scan(&ch, &co); e != nil {
			return nil, errors.New("字符集读取失败")
		}
		if len(out) == 0 || out[len(out)-1].Charset != ch {
			out = append(out, charset{ch, []string{}})
		}
		out[len(out)-1].Collations = append(out[len(out)-1].Collations, co)
	}
	return out, rows.Err()
}
func (m *Module) charsets(c *gin.Context) {
	m.withDB(c, false, func(ctx context.Context, db *sql.DB) error {
		out, e := charsets(ctx, db)
		if e == nil {
			response.OK(c, gin.H{"list": out})
		}
		return e
	})
}
func (m *Module) databases(c *gin.Context) {
	m.withDB(c, false, func(ctx context.Context, db *sql.DB) error {
		rows, e := db.QueryContext(ctx, "SELECT SCHEMA_NAME,DEFAULT_CHARACTER_SET_NAME,DEFAULT_COLLATION_NAME FROM information_schema.SCHEMATA ORDER BY SCHEMA_NAME")
		if e != nil {
			return errors.New("数据库列表查询失败，请检查权限")
		}
		out := []gin.H{}
		for rows.Next() {
			var name, ch, co string
			if e = rows.Scan(&name, &ch, &co); e != nil {
				rows.Close()
				return errors.New("数据库列表读取失败")
			}
			out = append(out, gin.H{"name": name, "charset": ch, "collation": co, "system": protectedDB(name)})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return errors.New("数据库列表读取失败")
		}
		stats := map[string]struct{ size, count int64 }{}
		statRows, err := db.QueryContext(ctx, "SELECT TABLE_SCHEMA,COALESCE(SUM(DATA_LENGTH+INDEX_LENGTH),0),COUNT(*) FROM information_schema.TABLES GROUP BY TABLE_SCHEMA")
		if err != nil {
			return errors.New("数据库统计失败，请检查权限")
		}
		for statRows.Next() {
			var name string
			var size, count int64
			if err = statRows.Scan(&name, &size, &count); err != nil {
				break
			}
			stats[name] = struct{ size, count int64 }{size, count}
		}
		if err == nil {
			err = statRows.Err()
		}
		statRows.Close()
		if err != nil {
			return errors.New("数据库统计读取失败")
		}
		var metadata []store.DatabaseMetadata
		if m.db.Where("server_id = ?", c.Param("sid")).Find(&metadata).Error != nil {
			return errors.New("数据库备注及账号记录读取失败")
		}
		details := map[string]store.DatabaseMetadata{}
		for _, item := range metadata {
			details[item.Name] = item
		}
		var counts []struct {
			Database string
			Count    int64
		}
		if m.db.Model(&store.DBBackup{}).Select("\"database\",COUNT(*) AS count").Where("server_id = ? AND id NOT IN (SELECT backup_id FROM database_trash)", c.Param("sid")).Group("database").Scan(&counts).Error != nil {
			return errors.New("备份数量读取失败")
		}
		backups := map[string]int64{}
		for _, count := range counts {
			backups[count.Database] = count.Count
		}
		var records []databaseRecord
		if err := m.db.Where("engine = ? AND server_id = ?", "mysql", c.Param("sid")).Find(&records).Error; err != nil {
			return err
		}
		created := map[string]time.Time{}
		for _, record := range records {
			created[record.Name] = record.CreatedAt
		}
		localSubnet := "localhost"
		isContainer := false
		if server, err := m.server(c.Param("sid")); err == nil {
			if subnet, container, err := m.containerLocalSubnet(ctx, server); err == nil {
				localSubnet, isContainer = subnet, container
			}
		}
		for _, item := range out {
			name := item["name"].(string)
			detail := details[name]
			if stamp, ok := created[name]; ok {
				item["createdAt"] = stamp
			}
			item["size"] = stats[name].size
			item["tableCount"] = stats[name].count
			item["username"] = detail.Username
			item["userHost"] = detail.Host
			item["localAccess"] = detail.Host == "localhost" || (isContainer && detail.Host == localSubnet)
			item["hasPassword"] = detail.Username != ""
			item["remark"] = detail.Remark
			item["backupCount"] = backups[name]
		}
		response.OK(c, out)
		return nil
	})
}

var ErrDatabaseMissing = errors.New("目标数据库不存在或无权访问")

func databaseExists(ctx context.Context, db *sql.DB, name string) error {
	var n int
	if e := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE SCHEMA_NAME = ?", name).Scan(&n); e != nil {
		return errors.New("目标数据库状态查询失败")
	}
	if n != 1 {
		return ErrDatabaseMissing
	}
	return nil
}
func (m *Module) createDatabase(c *gin.Context) {
	var p struct {
		Name      string `json:"name"`
		Charset   string `json:"charset"`
		Collation string `json:"collation"`
		Username  string `json:"username"`
		Password  string `json:"password"`
		Host      string `json:"host"`
	}
	if !response.Bind(c, &p) {
		return
	}
	if !identifier.MatchString(p.Name) || protectedDB(p.Name) {
		response.Fail(c, response.Invalid, "数据库名称无效或属于系统库")
		return
	}
	if p.Charset == "" {
		p.Charset = "utf8mb4"
	}
	normalizedHost, hostErr := normalizeAccessHost(p.Host)
	if p.Username != "" && ((!username.MatchString(p.Username) || hostErr != nil) || protectedUser(p.Username) || len(p.Password) == 0 || len(p.Password) > 1024 || strings.ContainsRune(p.Password, 0)) {
		msg := "用户名、密码或访问权限无效"
		if hostErr != nil {
			msg = hostErr.Error()
		}
		response.Fail(c, response.Invalid, msg)
		return
	}
	p.Host = normalizedHost
	encrypted := ""
	if p.Username != "" {
		var err error
		encrypted, err = store.Encrypt(m.cfg.AESKey, p.Password)
		if err != nil {
			failure(c, err)
			return
		}
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		if p.Collation == "" {
			if err := db.QueryRowContext(ctx, "SELECT DEFAULT_COLLATE_NAME FROM information_schema.CHARACTER_SETS WHERE CHARACTER_SET_NAME = ?", p.Charset).Scan(&p.Collation); err != nil {
				return errors.New("所选字符集不可用")
			}
		}
		var valid int
		if err := db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.COLLATIONS WHERE CHARACTER_SET_NAME = ? AND COLLATION_NAME = ?", p.Charset, p.Collation).Scan(&valid); err != nil {
			return errors.New("字符集查询失败")
		}
		if valid != 1 || !identifier.MatchString(p.Charset) || !identifier.MatchString(p.Collation) {
			response.Fail(c, response.Invalid, "字符集与排序规则组合无效")
			return nil
		}
		grantName := p.Name
		primaryHost, companionHost := p.Host, ""
		if p.Username != "" {
			server, err := m.server(c.Param("sid"))
			if err != nil {
				return err
			}
			primaryHost, companionHost, _, _, err = m.resolveAccessTargets(ctx, server, p.Host)
			if err != nil {
				return err
			}
			grantName, err = dbGrantPattern(ctx, db, p.Name)
			if err != nil {
				return errors.New("无法确认授权匹配规则，未创建数据库")
			}
		}
		if _, err := db.ExecContext(ctx, "CREATE DATABASE `"+p.Name+"` CHARACTER SET "+p.Charset+" COLLATE "+p.Collation); err != nil {
			return errors.New("创建数据库失败，请检查重名、权限与字符集支持")
		}
		createdPrimary := false
		createdCompanion := false
		cleanup := func(reason string) error {
			// MySQL DDL会自动提交，只清理本次已成功创建的空库和账号。
			var userErr, compErr error
			if createdCompanion {
				_, compErr = db.ExecContext(ctx, "DROP USER ?@?", p.Username, companionHost)
			}
			if createdPrimary {
				_, userErr = db.ExecContext(ctx, "DROP USER ?@?", p.Username, primaryHost)
			}
			_, dbErr := db.ExecContext(ctx, "DROP DATABASE `"+p.Name+"`")
			if userErr != nil || compErr != nil || dbErr != nil {
				return errors.New(reason + "；新建资源清理不完整，请检查数据库与账号")
			}
			return errors.New(reason + "；本次创建的库及账号已清理")
		}
		if p.Username != "" {
			if _, err := db.ExecContext(ctx, "CREATE USER ?@? IDENTIFIED BY ?", p.Username, primaryHost, p.Password); err != nil {
				return cleanup("创建账号失败，请检查重名、权限与密码策略")
			}
			createdPrimary = true
			if _, err := db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+grantName+"`.* TO ?@?", p.Username, primaryHost); err != nil {
				return cleanup("数据库授权失败")
			}
			if companionHost != "" {
				if _, err := db.ExecContext(ctx, "CREATE USER ?@? IDENTIFIED BY ?", p.Username, companionHost, p.Password); err != nil {
					return cleanup("创建容器内网伴生账号失败，请检查重名、权限与密码策略")
				}
				createdCompanion = true
				if _, err := db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+grantName+"`.* TO ?@?", p.Username, companionHost); err != nil {
					return cleanup("容器内网伴生账号授权失败")
				}
			}
			row := store.DatabaseMetadata{ServerID: c.Param("sid"), Name: p.Name, Username: p.Username, Host: primaryHost, PasswordEnc: encrypted}
			if err := m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "server_id"}, {Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"username", "host", "password_enc"})}).Create(&row).Error; err != nil {
				return cleanup("面板账号信息保存失败")
			}
		}
		record := databaseRecord{Engine: "mysql", ServerID: c.Param("sid"), Name: p.Name, CreatedAt: time.Now().UTC()}
		if err := m.db.Clauses(clause.OnConflict{UpdateAll: true}).Create(&record).Error; err != nil {
			return errors.New("数据库已创建，但创建时间保存失败，请刷新列表")
		}
		response.OK(c, gin.H{"name": p.Name})
		return nil
	})
}
func (m *Module) users(c *gin.Context) {
	m.withDBOptions(c, false, true, func(ctx context.Context, db *sql.DB) error {
		server, e := m.server(c.Param("sid"))
		if e != nil {
			return e
		}
		rows, e := db.QueryContext(ctx, "SELECT User,Host FROM mysql.user ORDER BY User,Host")
		if e != nil {
			return errors.New("账号列表查询失败，需要读取mysql.user的权限")
		}
		out := []gin.H{}
		for rows.Next() {
			var user, host string
			if e = rows.Scan(&user, &host); e != nil {
				rows.Close()
				return errors.New("账号读取失败")
			}
			out = append(out, gin.H{"username": user, "host": host, "editable": account(user, host) && !protectedUser(user) && user != server.Username})
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return errors.New("账号读取失败")
		}
		for _, item := range out {
			rows, e := db.QueryContext(ctx, "SHOW GRANTS FOR ?@?", item["username"], item["host"])
			if e != nil {
				return errors.New("读取账号授权失败，请检查权限")
			}
			grants := []string{}
			for rows.Next() {
				var grant string
				if e = rows.Scan(&grant); e != nil {
					rows.Close()
					return errors.New("授权读取失败")
				}
				upper := strings.ToUpper(grant)
				if at := strings.Index(upper, " IDENTIFIED "); at >= 0 {
					grant = grant[:at]
				}
				grants = append(grants, grant)
			}
			e = rows.Err()
			rows.Close()
			if e != nil {
				return errors.New("授权读取失败")
			}
			item["grants"] = grants
		}
		response.OK(c, out)
		return nil
	})
}

type userInput struct {
	Username  string   `json:"username"`
	Host      string   `json:"host"`
	Password  string   `json:"password"`
	Databases []string `json:"databases"`
	Privilege string   `json:"privilege"`
}

func readAccount(c *gin.Context) (userInput, bool) {
	var p userInput
	if c.ShouldBindJSON(&p) != nil || !account(p.Username, p.Host) || protectedUser(p.Username) || len(p.Password) > 1024 || strings.ContainsRune(p.Password, 0) {
		response.Fail(c, response.Invalid, "账号或主机无效，内置账号禁止修改")
		return p, false
	}
	return p, true
}
func (m *Module) createUser(c *gin.Context) {
	p, ok := readAccount(c)
	if !ok {
		return
	}
	if p.Privilege != "all" && p.Privilege != "readonly" {
		response.Fail(c, response.Invalid, "授权类型无效")
		return
	}
	if len(p.Databases) == 0 {
		response.Fail(c, response.Invalid, "至少选择一个授权数据库")
		return
	}
	for _, name := range p.Databases {
		if !identifier.MatchString(name) || protectedDB(name) {
			response.Fail(c, response.Invalid, "不允许授权该数据库")
			return
		}
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		for _, name := range p.Databases {
			if e := databaseExists(ctx, db, name); e != nil {
				return e
			}
		}
		// 库级GRANT默认将下划线视为通配符；partial_revokes开启时使用字面库名。
		var variable, partial string
		e := db.QueryRowContext(ctx, "SHOW VARIABLES LIKE 'partial_revokes'").Scan(&variable, &partial)
		if e != nil && !errors.Is(e, sql.ErrNoRows) {
			return errors.New("无法确认库级授权匹配规则，未创建账号")
		}
		literal := strings.EqualFold(partial, "ON") || partial == "1"
		if _, e := db.ExecContext(ctx, "CREATE USER ?@? IDENTIFIED BY ?", p.Username, p.Host, p.Password); e != nil {
			return errors.New("创建账号失败，请检查重名、权限与密码策略")
		}
		priv := "ALL PRIVILEGES"
		if p.Privilege == "readonly" {
			priv = "SELECT, SHOW VIEW"
		}
		for _, name := range p.Databases {
			grantName := name
			if !literal {
				grantName = strings.ReplaceAll(name, "_", "\\_")
			}
			if _, e := db.ExecContext(ctx, "GRANT "+priv+" ON `"+grantName+"`.* TO ?@?", p.Username, p.Host); e != nil {
				_, cleanupErr := db.ExecContext(ctx, "DROP USER ?@?", p.Username, p.Host)
				if cleanupErr != nil {
					return errors.New("授权失败且新账号清理失败，请手工检查账号")
				}
				return errors.New("数据库授权失败，新账号已清理")
			}
		}
		encrypted := ""
		if p.Password != "" {
			var err error
			encrypted, err = store.Encrypt(m.cfg.AESKey, p.Password)
			if err != nil {
				return errors.New("账号已创建，但面板密码记录保存失败")
			}
		}
		for _, name := range p.Databases {
			row := store.DatabaseMetadata{ServerID: c.Param("sid"), Name: name, Username: p.Username, Host: p.Host, PasswordEnc: encrypted}
			if m.db.Clauses(clause.OnConflict{Columns: []clause.Column{{Name: "server_id"}, {Name: "name"}}, DoUpdates: clause.AssignmentColumns([]string{"username", "host", "password_enc"})}).Create(&row).Error != nil {
				return errors.New("账号及授权已创建，但面板账号记录保存失败，请勿重复创建")
			}
		}
		response.OK(c, gin.H{"username": p.Username, "host": p.Host})
		return nil
	})
}
func (m *Module) password(c *gin.Context) {
	p, ok := readAccount(c)
	if !ok {
		return
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		server, err := m.server(c.Param("sid"))
		if err != nil {
			return err
		}
		if p.Username == server.Username {
			return errors.New("服务器管理账号请通过管理密码入口修改")
		}
		encrypted := ""
		if p.Password != "" {
			encrypted, err = store.Encrypt(m.cfg.AESKey, p.Password)
			if err != nil {
				return errors.New("密码加密失败，数据库账号未修改")
			}
		}
		if _, e := db.ExecContext(ctx, "ALTER USER ?@? IDENTIFIED BY ?", p.Username, p.Host, p.Password); e != nil {
			return errors.New("改密失败，请检查账号、权限与服务器兼容性")
		}
		if m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND username = ? AND host = ?", c.Param("sid"), p.Username, p.Host).Update("password_enc", encrypted).Error != nil {
			return errors.New("密码已修改，但面板记录更新失败")
		}
		response.OK(c, nil)
		return nil
	})
}
func (m *Module) dropUser(c *gin.Context) {
	p, ok := readAccount(c)
	if !ok {
		return
	}
	server, ok := m.row(c)
	if !ok {
		return
	}
	if p.Username == server.Username {
		response.Fail(c, response.Invalid, "不能删除当前服务器的管理账号")
		return
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		if _, e := db.ExecContext(ctx, "DROP USER ?@?", p.Username, p.Host); e != nil {
			return errors.New("删除账号失败，请检查权限及账号是否存在")
		}
		if m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND username = ? AND host = ?", c.Param("sid"), p.Username, p.Host).Updates(map[string]any{"username": "", "host": "", "password_enc": ""}).Error != nil {
			return errors.New("账号已删除，但面板记录清理失败")
		}
		response.OK(c, nil)
		return nil
	})
}

// CountDatabases 汇总登记的业务库，跳过已知停止的额外实例。
func (m *Module) CountDatabases(ctx context.Context) (int64, error) {
	servers, instances, err := m.mysqlServers(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	for _, server := range servers {
		if server.InstanceID != "" && !instances[server.InstanceID].Running {
			continue
		}
		db, e := m.connect(ctx, server, false)
		if e != nil {
			return 0, errors.New("部分数据库服务器不可访问，无法统计业务库总数")
		}
		var count int64
		e = db.QueryRowContext(ctx, "SELECT COUNT(*) FROM information_schema.SCHEMATA WHERE LOWER(SCHEMA_NAME) NOT IN ('mysql','information_schema','performance_schema','sys')").Scan(&count)
		db.Close()
		if e != nil {
			return 0, errors.New("部分服务器的业务库数量读取失败，请检查权限")
		}
		total += count
	}
	return total, nil
}
