package database

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"database/sql"
	"errors"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"github.com/go-sql-driver/mysql"
	"gorm.io/gorm"
)

type Module struct {
	localMu  sync.Mutex
	sqliteMu sync.Mutex
	pgMetaMu sync.Mutex
	db       *gorm.DB
	cfg      config.Config
	tasks    *task.Manager
	packages *pkgmgr.Manager
	auth     *auth.Service
}

func New(db *gorm.DB, cfg config.Config, tasks *task.Manager, packages *pkgmgr.Manager, a *auth.Service) *Module {
	return &Module{db: db, cfg: cfg, tasks: tasks, packages: packages, auth: a}
}
func (m *Module) Name() string  { return "database" }
func LockKey(sid string) string { return "database:" + sid }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/db")
	r.GET("/env", m.environment)
	r.GET("/instances/:engine/:instance/diagnostics/:view", m.databaseDiagnostics)
	r.DELETE("/instances/:engine/:instance/diagnostics/:view", m.databaseDiagnostics)
	r.GET("/columns", m.columns)
	r.PUT("/columns", m.columns)
	r.GET("/trash", m.trashList)
	r.POST("/trash/empty", m.emptyTrash)
	r.DELETE("/trash/:id", m.purgeTrash)
	r.POST("/trash/:id/restore", m.restoreTrash)
	m.noSQLRoutes(r)
	m.postgresRoutes(r)
	m.sqliteRoutes(r)
	r.GET("/servers", m.servers)
	r.POST("/servers", m.save)
	r.POST("/servers/test", m.test)
	r.PUT("/servers/:sid/credentials", m.localCredentials)
	r.POST("/servers/:sid/root-password/reveal", m.rootPassword)
	r.PUT("/servers/:sid/root-password", m.setRootPassword)
	r.POST("/servers/:sid/root-password/reset", m.resetRootPassword)
	r.PUT("/servers/:sid", m.save)
	r.DELETE("/servers/:sid", m.deleteServer)
	r.GET("/servers/:sid/charsets", m.charsets)
	r.GET("/servers/:sid/databases", m.databases)
	r.POST("/servers/:sid/databases", m.createDatabase)
	r.POST("/servers/:sid/databases/batch", m.batchDatabases)
	r.PUT("/servers/:sid/databases/:name/metadata", m.databaseMetadata)
	r.POST("/servers/:sid/databases/:name/password/reveal", m.revealPassword)
	r.PUT("/servers/:sid/databases/:name/access", m.databaseAccess)
	r.PUT("/servers/:sid/databases/:name/password", m.databasePassword)
	r.DELETE("/servers/:sid/databases/:name", m.dropDatabase)
	m.importRoutes(r.Group("/servers/:sid/databases/:name/import-files"))
	r.GET("/servers/:sid/users", m.users)
	r.POST("/servers/:sid/users", m.createUser)
	r.PUT("/servers/:sid/users/password", m.password)
	r.DELETE("/servers/:sid/users", m.dropUser)
	r.GET("/backups", m.backups)
	r.GET("/servers/:sid/backups", m.backups)
	r.POST("/servers/:sid/backups", m.backupHandler)
	r.POST("/servers/:sid/backups/batch", m.batchBackup)
	r.POST("/backups/:id/restore", m.restoreHandler)
	r.GET("/backups/:id/download", m.download)
	r.DELETE("/backups/:id", m.deleteBackup)
}

var identifier = regexp.MustCompile(`^[a-zA-Z0-9_]{1,64}$`)
var username = regexp.MustCompile(`^[a-zA-Z0-9_]{1,32}$`)
var hostname = regexp.MustCompile(`^[a-zA-Z0-9](?:[a-zA-Z0-9.-]{0,251}[a-zA-Z0-9])?$`)

func account(user, host string) bool {
	return username.MatchString(user) && (host == "%" || host == "localhost" || net.ParseIP(host) != nil || isMySQLNetmaskHost(host) || hostname.MatchString(host))
}
func protectedDB(s string) bool {
	switch strings.ToLower(s) {
	case "mysql", "information_schema", "performance_schema", "sys":
		return true
	}
	return false
}
func protectedUser(s string) bool {
	switch strings.ToLower(s) {
	case "root", "mysql.sys", "mysql.session", "mysql.infoschema", "mariadb.sys", "mysql":
		return true
	}
	return false
}
func failure(c *gin.Context, e error) {
	switch {
	case errors.Is(e, task.ErrBusy):
		response.Fail(c, response.Conflict, "数据库操作正在运行，请稍后重试")
	case errors.Is(e, gorm.ErrRecordNotFound):
		response.Fail(c, response.NotFound, "记录不存在")
	default:
		response.Fail(c, response.System, response.Summary(e.Error()))
	}
}
func (m *Module) server(sid string) (store.DBServer, error) {
	var s store.DBServer
	e := m.db.First(&s, "id = ?", sid).Error
	return s, e
}
func (m *Module) row(c *gin.Context) (store.DBServer, bool) {
	s, e := m.server(c.Param("sid"))
	if e != nil {
		failure(c, e)
	}
	return s, e == nil
}

type input struct {
	ServerID   string  `json:"serverId"`
	Name       string  `json:"name"`
	Type       string  `json:"type"`
	Network    string  `json:"network"`
	Host       string  `json:"host"`
	Port       int     `json:"port"`
	SocketPath string  `json:"socketPath"`
	Username   string  `json:"username"`
	Password   *string `json:"password"`
	TLS        struct {
		Mode       string  `json:"mode"`
		ServerName string  `json:"serverName"`
		CACert     *string `json:"caCert"`
	} `json:"tls"`
}

func (m *Module) parse(c *gin.Context, old store.DBServer) (store.DBServer, *string, bool) {
	var p input
	if c.ShouldBindJSON(&p) != nil {
		response.Fail(c, response.Invalid, "连接配置格式无效")
		return old, nil, false
	}
	if p.Name == "" || len([]rune(p.Name)) > 64 || (p.Type != "mysql" && p.Type != "mariadb") || (p.Network != "tcp" && p.Network != "unix") || p.Username == "" || len(p.Username) > 128 || strings.ContainsRune(p.Username, 0) {
		response.Fail(c, response.Invalid, "服务器名称、类型、连接方式或账号无效")
		return old, nil, false
	}
	if p.Network == "tcp" {
		if p.Host == "" {
			p.Host = "127.0.0.1"
		}
		if p.Port == 0 {
			p.Port = 3306
		}
		if p.Port < 1 || p.Port > 65535 || (!hostname.MatchString(p.Host) && net.ParseIP(p.Host) == nil) {
			response.Fail(c, response.Invalid, "地址或端口无效")
			return old, nil, false
		}
		if p.TLS.Mode == "" {
			p.TLS.Mode = "verify"
		}
		if p.TLS.Mode != "verify" && p.TLS.Mode != "disabled" {
			response.Fail(c, response.Invalid, "TLS模式无效")
			return old, nil, false
		}
		if p.TLS.ServerName != "" && !hostname.MatchString(p.TLS.ServerName) && net.ParseIP(p.TLS.ServerName) == nil {
			response.Fail(c, response.Invalid, "TLS服务器名称无效")
			return old, nil, false
		}
	} else {
		if !filepath.IsAbs(p.SocketPath) {
			response.Fail(c, response.Invalid, "socket须为绝对路径")
			return old, nil, false
		}
		st, e := os.Stat(p.SocketPath)
		if e != nil || st.Mode()&os.ModeSocket == 0 {
			response.Fail(c, response.Invalid, "本机socket不存在")
			return old, nil, false
		}
		p.Host = ""
		p.Port = 0
		p.TLS.Mode = "disabled"
		p.TLS.ServerName = ""
	}
	if old.ID == "" && p.ServerID != "" && strings.HasSuffix(c.FullPath(), "/test") {
		var e error
		old, e = m.server(p.ServerID)
		if e != nil {
			failure(c, e)
			return old, nil, false
		}
	}
	old.Name = p.Name
	old.Type = p.Type
	old.Network = p.Network
	old.Host = p.Host
	old.Port = p.Port
	old.SocketPath = p.SocketPath
	old.Username = p.Username
	old.TLSMode = p.TLS.Mode
	old.TLSServerName = p.TLS.ServerName
	if p.Network == "tcp" {
		old.SocketPath = ""
	}
	if p.Password != nil {
		if len(*p.Password) > 1024 || strings.ContainsRune(*p.Password, 0) {
			response.Fail(c, response.Invalid, "密码长度或格式无效")
			return old, nil, false
		}
		old.PasswordEnc = ""
		if *p.Password != "" {
			var e error
			old.PasswordEnc, e = store.Encrypt(m.cfg.AESKey, *p.Password)
			if e != nil {
				failure(c, e)
				return old, nil, false
			}
		}
	}
	if p.TLS.CACert != nil && *p.TLS.CACert != "" {
		roots := x509.NewCertPool()
		if len(*p.TLS.CACert) > 1<<20 || !roots.AppendCertsFromPEM([]byte(*p.TLS.CACert)) {
			response.Fail(c, response.Invalid, "CA证书PEM无效")
			return old, nil, false
		}
	}
	return old, p.TLS.CACert, true
}
func (m *Module) connect(ctx context.Context, s store.DBServer, interpolate bool) (*sql.DB, error) {
	if s.InstanceID != "" {
		if _, err := m.packages.RunningDatabaseInstance(ctx, s.Type, s.InstanceID, s.Port); err != nil {
			return nil, err
		}
	}
	cfg := mysql.NewConfig()
	cfg.User = s.Username
	cfg.Net = s.Network
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 30 * time.Second
	cfg.WriteTimeout = 30 * time.Second
	cfg.InterpolateParams = interpolate
	var e error
	if s.PasswordEnc != "" {
		cfg.Passwd, e = store.Decrypt(m.cfg.AESKey, s.PasswordEnc)
		if e != nil {
			return nil, e
		}
	}
	if s.Network == "unix" {
		cfg.Addr = s.SocketPath
	} else {
		cfg.Addr = net.JoinHostPort(s.Host, strconv.Itoa(s.Port))
		if s.TLSMode == "verify" {
			name := s.TLSServerName
			if name == "" {
				name = s.Host
			}
			tc := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: name}
			if s.TLSCAPath != "" {
				b, e := os.ReadFile(s.TLSCAPath)
				if e != nil {
					return nil, errors.New("CA文件无法读取")
				}
				roots, e := x509.SystemCertPool()
				if e != nil {
					roots = x509.NewCertPool()
				}
				if !roots.AppendCertsFromPEM(b) {
					return nil, errors.New("CA文件无效")
				}
				tc.RootCAs = roots
			}
			cfg.TLS = tc
		}
	}
	connector, e := mysql.NewConnector(cfg)
	if e != nil {
		return nil, errors.New("数据库连接配置无效")
	}
	db := sql.OpenDB(connector)
	db.SetMaxOpenConns(1)
	if e = db.PingContext(ctx); e != nil {
		db.Close()
		return nil, errors.New("数据库连接失败，请检查地址、认证、权限与TLS配置")
	}
	return db, nil
}
func (m *Module) ca(s *store.DBServer, value *string) (func(), error) {
	if value == nil {
		return func() {}, nil
	}
	s.TLSCAPath = ""
	if *value == "" {
		return func() {}, nil
	}
	dir := filepath.Join(m.cfg.DataDir(), "certs", "database")
	if e := os.MkdirAll(dir, 0700); e != nil {
		return nil, e
	}
	path := filepath.Join(dir, store.ID()+".pem")
	if e := os.WriteFile(path, []byte(*value), 0600); e != nil {
		return nil, e
	}
	s.TLSCAPath = path
	return func() { os.Remove(path) }, nil
}
func serverView(s store.DBServer) gin.H {
	return gin.H{"local": s.IsLocal, "containerClient": usesContainerClient(s), "id": s.ID, "name": s.Name, "type": s.Type, "network": s.Network, "host": s.Host, "port": s.Port, "socketPath": s.SocketPath, "username": s.Username, "hasPassword": s.PasswordEnc != "", "caConfigured": s.TLSCAPath != "", "tls": gin.H{"mode": s.TLSMode, "serverName": s.TLSServerName}, "createdAt": s.CreatedAt, "updatedAt": s.UpdatedAt}
}
func (m *Module) servers(c *gin.Context) {
	rows, instances, err := m.mysqlServers(c.Request.Context())
	if err != nil {
		failure(c, err)
		return
	}
	out := make([]gin.H, 0, len(rows))
	for _, row := range rows {
		view := serverView(row)
		if instance, ok := instances[row.InstanceID]; ok {
			view["instanceId"], view["version"], view["running"] = instance.ID, instance.Version, instance.Running
		}
		out = append(out, view)
	}
	response.OK(c, out)
}
func (m *Module) save(c *gin.Context) {
	var old store.DBServer
	var release func()
	if c.Param("sid") != "" {
		var ok bool
		old, ok = m.row(c)
		if !ok {
			return
		}
		if old.IsLocal {
			response.Fail(c, response.Invalid, "本机连接自动管理，请使用本机凭据设置")
			return
		}
		var e error
		release, e = m.tasks.Acquire(LockKey(old.ID))
		if e != nil {
			failure(c, e)
			return
		}
		defer release()
	}
	s, ca, ok := m.parse(c, old)
	if !ok {
		return
	}
	if s.Network != "tcp" {
		response.Fail(c, response.Invalid, "本机实例自动识别，新增服务器仅用于远程TCP连接")
		return
	}
	cleanup, e := m.ca(&s, ca)
	if e != nil {
		failure(c, e)
		return
	}
	if s.ID == "" {
		s.ID = store.ID()
	}
	if e = m.db.Save(&s).Error; e != nil {
		cleanup()
		failure(c, errors.New("连接记录保存失败"))
		return
	}
	if ca != nil && old.TLSCAPath != "" {
		os.Remove(old.TLSCAPath)
	}
	response.OK(c, gin.H{"id": s.ID})
}
func (m *Module) test(c *gin.Context) {
	s, ca, ok := m.parse(c, store.DBServer{})
	if !ok {
		return
	}
	cleanup, e := m.ca(&s, ca)
	if e != nil {
		failure(c, e)
		return
	}
	defer cleanup()
	ctx, cancel := context.WithTimeout(c.Request.Context(), 30*time.Second)
	defer cancel()
	db, e := m.connect(ctx, s, false)
	if e != nil {
		failure(c, e)
		return
	}
	defer db.Close()
	var version string
	if e = db.QueryRowContext(ctx, "SELECT VERSION()").Scan(&version); e != nil {
		failure(c, errors.New("版本查询失败"))
		return
	}
	response.OK(c, gin.H{"version": version})
}
func (m *Module) deleteServer(c *gin.Context) {
	s, ok := m.row(c)
	if !ok {
		return
	}
	if s.IsLocal {
		response.Fail(c, response.Invalid, "本机数据库自动管理，不能删除连接记录")
		return
	}
	release, e := m.tasks.Acquire(LockKey(s.ID))
	if e != nil {
		failure(c, e)
		return
	}
	defer release()
	e = m.db.Transaction(func(tx *gorm.DB) error {
		if e := tx.Model(&store.DBBackup{}).Where("server_id = ?", s.ID).Update("server_id", nil).Error; e != nil {
			return e
		}
		if e := store.DisableTargetJobs(tx, "backup_db", s.ID, "", "数据库服务器已删除"); e != nil {
			return e
		}
		return tx.Delete(&s).Error
	})
	if e != nil {
		failure(c, errors.New("服务器记录删除失败"))
		return
	}
	if s.TLSCAPath != "" {
		os.Remove(s.TLSCAPath)
	}
	response.OK(c, nil)
}
func (m *Module) environment(c *gin.Context) {
	state, e := m.packages.Status(c.Request.Context(), "mysql")
	if e != nil {
		failure(c, e)
		return
	}
	if !state.Installed {
		state, e = m.packages.Status(c.Request.Context(), "mariadb")
		if e != nil {
			failure(c, e)
			return
		}
	}
	var socket *string
	for _, p := range []string{"/run/mysqld/mysqld.sock", "/var/run/mysqld/mysqld.sock", "/tmp/mysql.sock"} {
		if st, e := os.Stat(p); e == nil && st.Mode()&os.ModeSocket != 0 {
			socket = &p
			break
		}
	}
	_, dumpErr := clientPath("", true)
	_, importErr := clientPath("", false)
	var active store.Task
	e = m.db.Where("module = ? AND resource_id IN ? AND status = ?", "software", []string{"mysql", "mariadb", "mysql-client"}, "running").Order("started_at DESC").First(&active).Error
	if e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		failure(c, e)
		return
	}
	var activeID *string
	if active.ID != "" {
		activeID = &active.ID
	}
	var localType any
	if state.Installed {
		localType = state.ID
	}
	response.OK(c, gin.H{"application": state, "localType": localType, "localInstalled": state.Installed, "localRunning": state.Running, "clientInstalled": dumpErr == nil && importErr == nil, "backupClientInstalled": dumpErr == nil, "socketPath": socket, "activeTaskId": activeID})
}
