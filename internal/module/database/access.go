package database

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func (m *Module) databaseAccess(c *gin.Context) {
	var in struct {
		Host string `json:"host"`
	}
	if !response.Bind(c, &in) {
		return
	}
	normalized, err := normalizeAccessHost(in.Host)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		if err := m.setDatabaseAccess(ctx, db, c.Param("sid"), c.Param("name"), normalized); err != nil {
			return err
		}
		response.OK(c, nil)
		return nil
	})
}

func (m *Module) setDatabaseAccess(ctx context.Context, db *sql.DB, sid, name, host string) error {
	row, err := m.boundAccount(ctx, db, sid, name)
	if err != nil {
		return err
	}
	server, err := m.server(row.ServerID)
	if err != nil {
		return err
	}
	targetHost, companionHost, localSubnet, isContainer, err := m.resolveAccessTargets(ctx, server, host)
	if err != nil {
		return err
	}
	grantName, err := dbGrantPattern(ctx, db, row.Name)
	if err != nil {
		return errors.New("无法确认库级授权匹配规则，未修改访问权限")
	}
	plainPassword := ""
	if row.PasswordEnc != "" {
		if plain, decErr := store.Decrypt(m.cfg.AESKey, row.PasswordEnc); decErr == nil {
			plainPassword = plain
		}
	}
	if targetHost == row.Host {
		if companionHost != "" {
			return ensureCompanionUser(ctx, db, row.Username, companionHost, plainPassword, grantName)
		}
		return nil
	}
	var shared int64
	if err = m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND username = ? AND host = ? AND name <> ?", row.ServerID, row.Username, row.Host, row.Name).Count(&shared).Error; err != nil {
		return errors.New("账号关联数据库读取失败，未修改访问权限")
	}
	if shared != 0 {
		return errors.New("该账号关联多个数据库，修改访问权限会影响其他库，请先拆分专属账号")
	}
	rows, err := db.QueryContext(ctx, "SHOW GRANTS FOR ?@?", row.Username, row.Host)
	if err != nil {
		return errors.New("账号授权读取失败，未修改访问权限")
	}
	// RENAME USER保留密码及授权；只允许移动当前库的专属账号。
	for rows.Next() {
		var grant string
		if err = rows.Scan(&grant); err != nil {
			break
		}
		upper := strings.ToUpper(grant)
		if strings.HasPrefix(upper, "GRANT USAGE ON *.* TO ") {
			continue
		}
		start, end := strings.Index(upper, " ON "), strings.LastIndex(upper, " TO ")
		if start < 0 || end <= start+4 {
			err = errors.New("该账号含全局权限或角色授权，请先拆分专属账号")
			break
		}
		scope := grant[start+4 : end]
		scope = strings.TrimPrefix(strings.TrimPrefix(scope, "PROCEDURE "), "FUNCTION ")
		if scope != "`"+grantName+"`.*" && !strings.HasPrefix(scope, "`"+row.Name+"`.`") {
			err = errors.New("该账号还持有其他库或全局权限，请先拆分专属账号")
			break
		}
	}
	if err == nil {
		err = rows.Err()
	}
	rows.Close()
	if err != nil {
		return err
	}
	var definers int
	if err = db.QueryRowContext(ctx, `SELECT
		(SELECT COUNT(*) FROM information_schema.VIEWS WHERE DEFINER = ?) +
		(SELECT COUNT(*) FROM information_schema.ROUTINES WHERE DEFINER = ?) +
		(SELECT COUNT(*) FROM information_schema.TRIGGERS WHERE DEFINER = ?) +
		(SELECT COUNT(*) FROM information_schema.EVENTS WHERE DEFINER = ?)`, row.Username+"@"+row.Host, row.Username+"@"+row.Host, row.Username+"@"+row.Host, row.Username+"@"+row.Host).Scan(&definers); err != nil {
		return errors.New("无法检查账号的视图、存储过程、触发器及事件依赖，未修改访问权限")
	}
	if definers != 0 {
		return errors.New("该账号是视图、存储过程、触发器或事件的定义者，请先迁移定义者后修改访问权限")
	}

	// 容器化 MySQL 的权限切换：确保同机容器与宿主机始终通过 localSubnet 保持可连。
	if isContainer && targetHost == localSubnet {
		if exists, err := mysqlUserExists(ctx, db, row.Username, localSubnet); err == nil && exists {
			if err := ensureCompanionUser(ctx, db, row.Username, localSubnet, plainPassword, grantName); err != nil {
				return err
			}
			if _, err = db.ExecContext(ctx, "DROP USER ?@?", row.Username, row.Host); err != nil {
				return errors.New("原外部访问账号清理失败，请检查数据库权限")
			}
			if err = m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND name = ?", row.ServerID, row.Name).Update("host", targetHost).Error; err != nil {
				return errors.New("账号访问权限已修改，但面板记录保存失败，请刷新确认")
			}
			return nil
		}
	}

	if isContainer && row.Host == localSubnet && companionHost == localSubnet {
		// 从「本地服务器」切换到「指定IP」：保留原容器内网账号不动，新增指定 IP 账号并授予同库权限。
		if plainPassword == "" {
			return errors.New("面板未保存该账号密码，请先重设数据库密码后再修改为指定IP")
		}
		if _, err = db.ExecContext(ctx, "CREATE USER ?@? IDENTIFIED BY ?", row.Username, targetHost, plainPassword); err != nil {
			return errors.New("创建指定IP访问账号失败，请检查目标账号是否已存在及密码策略")
		}
		if _, err = db.ExecContext(ctx, "GRANT ALL PRIVILEGES ON `"+grantName+"`.* TO ?@?", row.Username, targetHost); err != nil {
			_, _ = db.ExecContext(ctx, "DROP USER IF EXISTS ?@?", row.Username, targetHost)
			return errors.New("指定IP账号授权失败，未修改访问权限")
		}
		if err = m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND name = ?", row.ServerID, row.Name).Update("host", targetHost).Error; err != nil {
			_, _ = db.ExecContext(ctx, "DROP USER IF EXISTS ?@?", row.Username, targetHost)
			return errors.New("面板账号记录保存失败，数据库账号访问权限已回退")
		}
		return nil
	}

	if _, err = db.ExecContext(ctx, "RENAME USER ?@? TO ?@?", row.Username, row.Host, row.Username, targetHost); err != nil {
		return errors.New("访问权限修改失败，请检查目标账号是否已存在及数据库权限")
	}
	if companionHost != "" {
		if err = ensureCompanionUser(ctx, db, row.Username, companionHost, plainPassword, grantName); err != nil {
			_, _ = db.ExecContext(ctx, "RENAME USER ?@? TO ?@?", row.Username, targetHost, row.Username, row.Host)
			return err
		}
	} else if isContainer && targetHost == "%" {
		m.dropCompanionIfExclusive(ctx, db, row.ServerID, row.Name, row.Username, localSubnet, grantName)
	}
	if err = m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND name = ?", row.ServerID, row.Name).Update("host", targetHost).Error; err != nil {
		if _, rollbackErr := db.ExecContext(ctx, "RENAME USER ?@? TO ?@?", row.Username, targetHost, row.Username, row.Host); rollbackErr != nil {
			return errors.New("账号访问权限已修改，但面板记录保存及账号回退失败，请手工核对账号host")
		}
		return errors.New("面板账号记录保存失败，数据库账号访问权限已回退")
	}
	return nil
}

func (m *Module) databasePassword(c *gin.Context) {
	var in struct {
		Password string `json:"password"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.Password == "" || len(in.Password) > 1024 || strings.ContainsRune(in.Password, 0) {
		response.Fail(c, response.Invalid, "请输入1至1024字节的新密码")
		return
	}
	m.withDB(c, true, func(ctx context.Context, db *sql.DB) error {
		if err := m.setDatabasePassword(ctx, db, c.Param("sid"), c.Param("name"), in.Password); err != nil {
			return err
		}
		response.OK(c, nil)
		return nil
	})
}

func (m *Module) setDatabasePassword(ctx context.Context, db *sql.DB, sid, name, password string) error {
	encrypted, err := store.Encrypt(m.cfg.AESKey, password)
	if err != nil {
		return err
	}
	row, err := m.boundAccount(ctx, db, sid, name)
	if err != nil {
		return err
	}
	if _, err = db.ExecContext(ctx, "ALTER USER ?@? IDENTIFIED BY ?", row.Username, row.Host, password); err != nil {
		return errors.New("改密失败，请检查关联账号、数据库权限及密码策略")
	}
	if server, err := m.server(row.ServerID); err == nil {
		if localSubnet, isContainer, err := m.containerLocalSubnet(ctx, server); err == nil && isContainer && row.Host != "%" && row.Host != localSubnet {
			if exists, err := mysqlUserExists(ctx, db, row.Username, localSubnet); err == nil && exists {
				if _, err = db.ExecContext(ctx, "ALTER USER ?@? IDENTIFIED BY ?", row.Username, localSubnet, password); err != nil {
					return errors.New("主账号改密成功，但容器内网伴生账号密码同步失败")
				}
			}
		}
	}
	if err = m.db.Model(&store.DatabaseMetadata{}).Where("server_id = ? AND username = ? AND host = ?", row.ServerID, row.Username, row.Host).Update("password_enc", encrypted).Error; err != nil {
		return errors.New("数据库账号密码已修改，但面板密码记录更新失败，请重新设置密码同步记录")
	}
	return nil
}

// 数据库操作只使用面板记录的关联账号，不接收前端传入的用户名。
func (m *Module) boundAccount(ctx context.Context, db *sql.DB, sid, name string) (store.DatabaseMetadata, error) {
	var row store.DatabaseMetadata
	if !identifier.MatchString(name) || protectedDB(name) {
		return row, errors.New("不允许修改该数据库账号")
	}
	if err := databaseExists(ctx, db, name); err != nil {
		return row, err
	}
	if err := m.db.First(&row, "server_id = ? AND name = ?", sid, name).Error; err != nil {
		return row, errors.New("未记录此库的关联账号，请先配置专属账号")
	}
	server, err := m.server(row.ServerID)
	if err != nil {
		return row, err
	}
	if !account(row.Username, row.Host) || protectedUser(row.Username) || row.Username == server.Username {
		return row, errors.New("未记录有效的专属账号，或关联账号属于服务器管理账号，禁止修改")
	}
	return row, nil
}
