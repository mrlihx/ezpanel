package phpmyadmin

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log/slog"
	"mime"
	"net/http"
	"net/textproto"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type entry struct {
	Session          [32]byte
	Server, Database string
	Expires          time.Time
	LastTouch        time.Time
}
type Module struct {
	db       *gorm.DB
	cfg      config.Config
	packages *pkgmgr.Manager
	auth     *auth.Service
	mu       sync.Mutex
	entries  map[string]entry
}

func New(db *gorm.DB, cfg config.Config, packages *pkgmgr.Manager, a *auth.Service) *Module {
	return &Module{db: db, cfg: cfg, packages: packages, auth: a, entries: map[string]entry{}}
}
func (m *Module) Name() string { return "phpmyadmin" }
func (m *Module) Routes(g *gin.RouterGroup) {
	r := g.Group("/api/phpmyadmin")
	r.GET("/status", m.status)
	r.GET("/settings", m.settings)
	r.PUT("/settings", m.settings)
	r.POST("/open", m.open)
	r.Any("/view/:entry/*file", m.serve)
}
func (m *Module) settings(c *gin.Context) {
	versions, err := m.packages.PHPMyAdminPHPVersions(c.Request.Context())
	if err != nil {
		response.Fail(c, response.System, "PHP版本读取失败")
		return
	}
	if c.Request.Method == http.MethodGet {
		response.OK(c, gin.H{"phpVersion": m.cfg.Current().PHPMyAdminPHPVersion, "versions": versions})
		return
	}
	var in struct {
		PHPVersion string `json:"phpVersion"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if in.PHPVersion != "" {
		available := false
		for _, version := range versions {
			if version == in.PHPVersion {
				available = true
				break
			}
		}
		if !available {
			response.Fail(c, response.Invalid, "请选择已安装且扩展完整的兼容PHP版本")
			return
		}
	}
	next, err := m.cfg.Apply(func(next *config.Config) error {
		next.PHPMyAdminPHPVersion = in.PHPVersion
		return nil
	})
	if err != nil {
		response.Fail(c, response.System, "PHP版本设置保存失败")
		return
	}
	response.OK(c, gin.H{"phpVersion": next.PHPMyAdminPHPVersion})
}
func (m *Module) status(c *gin.Context) {
	state, err := m.packages.Status(c.Request.Context(), "phpmyadmin")
	if err != nil {
		response.Fail(c, response.System, "phpMyAdmin安装状态读取失败")
		return
	}
	_, runtimeErr := m.packages.PHPMyAdminRuntime(c.Request.Context())
	reason := ""
	if runtimeErr != nil {
		reason = runtimeErr.Error()
	}
	response.OK(c, gin.H{"installed": state.Installed, "phpAvailable": runtimeErr == nil, "phpVersion": m.cfg.Current().PHPMyAdminPHPVersion, "reason": reason})
}
func (m *Module) open(c *gin.Context) {
	var in struct {
		ServerID string `json:"serverId"`
		Database string `json:"database"`
	}
	if !response.Bind(c, &in) {
		return
	}
	if _, err := os.Stat(filepath.Join(m.packages.PHPMyAdminRoot(), "index.php")); err != nil {
		response.Fail(c, response.NotFound, "请先在应用商店安装phpMyAdmin")
		return
	}
	if _, err := m.packages.PHPMyAdminRuntime(c.Request.Context()); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	server, account, err := m.credentials(in.ServerID, in.Database)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	useSocket := server.Network == "unix" && account.Host == "localhost"
	if err := m.packages.PreparePHPMyAdmin(useSocket); err != nil {
		response.Fail(c, response.System, response.Summary(err.Error()))
		return
	}
	session := auth.Current(c)
	id := store.ID()
	m.mu.Lock()
	for id, item := range m.entries {
		if time.Now().After(item.Expires) {
			delete(m.entries, id)
		}
	}
	m.entries[id] = entry{Session: sha256.Sum256([]byte(session.Token)), Server: in.ServerID, Database: in.Database, Expires: time.Now().Add(2 * time.Hour), LastTouch: time.Now()}
	m.mu.Unlock()
	response.OK(c, gin.H{"url": "/api/phpmyadmin/view/" + id + "/index.php?route=/database/structure&db=" + url.QueryEscape(in.Database)})
}
func (m *Module) credentials(serverID, database string) (store.DBServer, store.DatabaseMetadata, error) {
	var server store.DBServer
	var account store.DatabaseMetadata
	if err := m.db.First(&server, "id = ?", serverID).Error; err != nil {
		return server, account, errors.New("数据库服务器不存在")
	}
	if server.Network == "unix" && server.SocketPath != m.packages.MySQLSocket() {
		return server, account, errors.New("请使用面板安装的本机 MySQL 或远程 TCP 连接")
	}
	if err := m.db.First(&account, "server_id = ? AND name = ?", serverID, database).Error; err != nil || account.Username == "" || account.PasswordEnc == "" {
		return server, account, errors.New("未保存该数据库的专属账号密码，请先设置数据库账号及密码")
	}
	if server.Network != "unix" && server.TLSMode == "verify" && server.TLSServerName != "" && server.TLSServerName != server.Host {
		return server, account, errors.New("phpMyAdmin需要连接地址与TLS校验主机名一致，请先调整数据库连接地址")
	}
	return server, account, nil
}
func (m *Module) serve(c *gin.Context) {
	id := c.Param("entry")
	m.mu.Lock()
	item, ok := m.entries[id]
	m.mu.Unlock()
	if !ok || time.Now().After(item.Expires) || item.Session != sha256.Sum256([]byte(auth.Current(c).Token)) {
		response.Fail(c, response.Forbidden, "phpMyAdmin入口已失效，请从数据库页面重新进入")
		return
	}
	name := strings.TrimPrefix(c.Param("file"), "/")
	if name == "" {
		c.Redirect(http.StatusFound, "/api/phpmyadmin/view/"+id+"/index.php?route=/database/structure&db="+url.QueryEscape(item.Database))
		return
	}
	if path.Clean(name) != name || strings.ContainsAny(name, "\\\x00") || strings.HasPrefix(name, "../") {
		c.Status(http.StatusNotFound)
		return
	}
	root, err := filepath.EvalSymlinks(m.packages.PHPMyAdminRoot())
	if err != nil {
		response.Fail(c, response.NotFound, "phpMyAdmin未安装，请从数据库页面重新进入")
		return
	}
	file, err := filepath.EvalSymlinks(filepath.Join(root, filepath.FromSlash(name)))
	if err != nil || !pathsafe.Contains(root, file) {
		c.Status(http.StatusNotFound)
		return
	}
	info, err := os.Stat(file)
	if err != nil || !info.Mode().IsRegular() {
		c.Status(http.StatusNotFound)
		return
	}
	c.Header("X-Content-Type-Options", "nosniff")
	c.Header("Referrer-Policy", "same-origin")
	if !strings.HasSuffix(name, ".php") {
		if !(strings.HasPrefix(name, "js/") || strings.HasPrefix(name, "themes/") || name == "favicon.ico") || strings.HasSuffix(name, ".map") {
			c.Status(http.StatusNotFound)
			return
		}
		typeName := mime.TypeByExtension(filepath.Ext(name))
		if typeName == "" {
			c.Status(http.StatusNotFound)
			return
		}
		c.Header("Cache-Control", "private, max-age=86400")
		c.Header("Content-Type", typeName)
		c.File(file)
		return
	}
	c.Header("Cache-Control", "no-store")
	if name != "index.php" && name != "url.php" && name != "js/messages.php" {
		c.Status(http.StatusNotFound)
		return
	}
	if name == "index.php" && (c.Query("ajax_request") == "" || c.Request.Method == http.MethodPost) {
		now := time.Now()
		needTouch := false
		m.mu.Lock()
		if cur, exists := m.entries[id]; exists && now.Sub(cur.LastTouch) >= 45*time.Second {
			cur.LastTouch = now
			m.entries[id] = cur
			needTouch = true
		}
		m.mu.Unlock()
		if needTouch {
			m.auth.Touch(auth.Current(c).Token)
		}
	}
	server, account, err := m.credentials(item.Server, item.Database)
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	endpoint, err := m.packages.PHPMyAdminRuntime(c.Request.Context())
	if err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	password, err := store.Decrypt(m.cfg.AESKey, account.PasswordEnc)
	if err != nil {
		response.Fail(c, response.Internal, "")
		return
	}
	base := m.packages.PHPMyAdminData()
	origin := m.auth.RequestOrigin(c)
	prefix := "/api/phpmyadmin/view/" + id + "/"
	entryURL := strings.TrimRight(origin.String(), "/") + prefix
	host, socket, dbPort := server.Host, "", server.Port
	if server.InstanceID != "" {
		instance, err := m.packages.RunningDatabaseInstance(c.Request.Context(), server.Type, server.InstanceID, server.Port)
		if err != nil {
			response.Fail(c, response.Invalid, "选中的 MySQL 实例未运行或连接地址已变更")
			return
		}
		if account.Host == "localhost" {
			path, cleanup, err := m.packages.PHPMyAdminInstanceSocket(c.Request.Context(), instance)
			if err != nil {
				response.Fail(c, response.System, "无法接通所选 MySQL 实例的本地套接字")
				return
			}
			defer cleanup()
			host, socket, dbPort = "localhost", path, 0
		} else {
			if instance.ContainerIP == "" {
				response.Fail(c, response.Invalid, "所选 MySQL 实例无法通过 Docker 网络连接")
				return
			}
			host, dbPort = instance.ContainerIP, instance.ContainerPort
		}
	} else if server.Network == "unix" {
		if account.Host == "localhost" {
			host, socket, dbPort = "localhost", server.SocketPath, 0
		} else {
			// 默认 Docker MySQL 实例：账号 host 为容器内网网段或 % 时，经 ezpanel-network TCP 连接以匹配授权。
			instance, err := m.packages.DatabaseInstance(c.Request.Context(), "mysql", "default")
			if err != nil || instance.ContainerID == "" || !instance.Running {
				response.Fail(c, response.Invalid, "默认 MySQL 实例未运行，无法打开 phpMyAdmin")
				return
			}
			if instance.ContainerIP == "" {
				response.Fail(c, response.Invalid, "默认 MySQL 实例无法通过 Docker 网络连接")
				return
			}
			host, socket, dbPort = instance.ContainerIP, "", instance.ContainerPort
		}
	}
	verifyTLS := server.Network != "unix" && server.TLSMode == "verify"
	caPath := ""
	if verifyTLS && server.TLSCAPath != "" {
		data, readErr := os.ReadFile(server.TLSCAPath)
		if readErr != nil {
			response.Fail(c, response.System, "数据库 TLS 证书无法读取")
			return
		}
		caPath = filepath.Join(base, "tmp", store.ID()+".pem")
		if err := os.WriteFile(caPath, data, 0600); err != nil {
			response.Fail(c, response.System, "数据库 TLS 证书无法提供给 PHP")
			return
		}
		defer os.Remove(caPath)
		// 面板的 umask 会屏蔽读权限，PHP-FPM 需要读取公开的 CA 证书。
		if err := os.Chmod(caPath, 0644); err != nil {
			response.Fail(c, response.System, "数据库 TLS 证书无法提供给 PHP")
			return
		}
	}
	secret := sha256.Sum256([]byte(m.cfg.AESKey + id))
	values := map[string]any{"base": entryURL, "logout": strings.TrimRight(origin.String(), "/") + "/databases/mysql", "secret": hex.EncodeToString(secret[:])[:32], "user": account.Username, "password": password, "database": account.Name, "host": host, "port": dbPort, "socket": socket, "ssl": verifyTLS, "ca": caPath, "temp": base + "/tmp"}
	data, _ := json.Marshal(values)
	port := origin.Port()
	if port == "" {
		port = "80"
		if origin.Scheme == "https" {
			port = "443"
		}
	}
	env := []string{"REDIRECT_STATUS=200", "GATEWAY_INTERFACE=CGI/1.1", "SCRIPT_FILENAME=" + file, "SCRIPT_NAME=" + prefix + name, "REQUEST_METHOD=" + c.Request.Method, "REQUEST_URI=" + c.Request.URL.RequestURI(), "QUERY_STRING=" + c.Request.URL.RawQuery, "SERVER_PROTOCOL=HTTP/1.1", "SERVER_NAME=" + origin.Hostname(), "SERVER_PORT=" + port, "HTTP_HOST=" + origin.Host, "REMOTE_ADDR=127.0.0.1", "CONTENT_TYPE=" + c.GetHeader("Content-Type"), "EZPANEL_PMA_CONFIG=" + base64.StdEncoding.EncodeToString(data)}
	if origin.Scheme == "https" {
		env = append(env, "HTTPS=on")
	}
	if c.Request.ContentLength > 64<<20 {
		response.Fail(c, response.TooLarge, "phpMyAdmin单次上传不超过64MiB")
		return
	}
	if c.Request.ContentLength > 0 {
		env = append(env, "CONTENT_LENGTH="+strconv.FormatInt(c.Request.ContentLength, 10))
	}
	for _, header := range []string{"Accept", "Accept-Language", "User-Agent", "X-Requested-With", "Referer"} {
		env = append(env, "HTTP_"+strings.ToUpper(strings.ReplaceAll(header, "-", "_"))+"="+c.GetHeader(header))
	}
	var cookies []string
	for _, cookie := range c.Request.Cookies() {
		if cookie.Name == "phpMyAdmin" || cookie.Name == "__Secure-phpMyAdmin_https" || strings.HasPrefix(cookie.Name, "pma") || strings.HasPrefix(cookie.Name, "__Secure-pma") {
			cookies = append(cookies, cookie.Name+"="+cookie.Value)
		}
	}
	env = append(env, "HTTP_COOKIE="+strings.Join(cookies, "; "))
	output := &cgiResponse{ctx: c, entryURL: entryURL}
	ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Minute)
	defer cancel()
	err = pkgmgr.FastCGI(ctx, "unix", endpoint, env, http.MaxBytesReader(c.Writer, c.Request.Body, 64<<20), output)
	switch {
	case errors.Is(err, errSignonRedirect):
		c.String(http.StatusBadGateway, "phpMyAdmin 自动登录失败，请检查数据库服务和面板中保存的数据库账号及密码。")
	case c.Writer.Written():
		if err != nil {
			if c.Request.Context().Err() == nil {
				slog.Warn("phpMyAdmin 响应传输中断")
			}
			panic(http.ErrAbortHandler)
		}
	case err == nil || errors.Is(err, errCGIHeaders):
		response.Fail(c, response.System, "phpMyAdmin响应无效")
	default:
		var sizeErr *http.MaxBytesError
		if errors.As(err, &sizeErr) {
			response.Fail(c, response.TooLarge, "phpMyAdmin单次上传不超过64MiB")
			return
		}
		response.Fail(c, response.System, "phpMyAdmin执行失败，请检查PHP扩展及任务配置")
	}
}

var (
	errSignonRedirect = errors.New("phpMyAdmin signon redirected to entry")
	errCGIHeaders     = errors.New("invalid FastCGI response headers")
)

type cgiResponse struct {
	ctx      *gin.Context
	entryURL string
	headers  []byte
	started  bool
}

func (w *cgiResponse) Write(p []byte) (int, error) {
	if w.started {
		n, err := w.ctx.Writer.Write(p)
		if err == nil && n > 0 {
			w.ctx.Writer.Flush()
		}
		return n, err
	}

	// FastCGI 的响应头可能跨多个记录，只缓冲到空行。
	const headerLimit = 64 << 10
	n := len(p)
	w.headers = append(w.headers, p...)
	headEnd, separatorLen := bytes.Index(w.headers, []byte("\r\n\r\n")), 4
	if lfEnd := bytes.Index(w.headers, []byte("\n\n")); lfEnd >= 0 && (headEnd < 0 || lfEnd < headEnd) {
		headEnd, separatorLen = lfEnd, 2
	}
	if headEnd < 0 && len(w.headers) < headerLimit {
		return n, nil
	}
	if headEnd < 0 || headEnd+separatorLen > headerLimit {
		return 0, errCGIHeaders
	}
	bodyStart := headEnd + separatorLen
	reader := bufio.NewReader(bytes.NewReader(w.headers[:bodyStart]))
	headers, err := textproto.NewReader(reader).ReadMIMEHeader()
	if err != nil {
		return 0, errCGIHeaders
	}
	status := http.StatusOK
	if value := headers.Get("Status"); value != "" {
		fields := strings.Fields(value)
		if len(fields) == 0 {
			return 0, errCGIHeaders
		}
		status, err = strconv.Atoi(fields[0])
		if err != nil || status < 200 || status > 599 {
			return 0, errCGIHeaders
		}
	} else if headers.Get("Location") != "" {
		status = http.StatusFound
	}
	if status >= 300 && status < 400 && headers.Get("Location") == w.entryURL {
		return 0, errSignonRedirect
	}
	for _, line := range headers.Values("Connection") {
		for _, name := range strings.Split(line, ",") {
			headers.Del(strings.TrimSpace(name))
		}
	}
	for _, name := range []string{"Status", "Content-Length", "Connection", "Transfer-Encoding", "Keep-Alive", "Upgrade", "Trailer", "TE", "Proxy-Connection"} {
		headers.Del(name)
	}
	for key, values := range headers {
		for _, value := range values {
			w.ctx.Writer.Header().Add(key, value)
		}
	}
	w.ctx.Status(status)
	w.started = true
	body := w.headers[bodyStart:]
	w.headers = nil
	if len(body) == 0 {
		w.ctx.Writer.WriteHeaderNow()
	} else if _, err := w.Write(body); err != nil {
		return 0, err
	}
	return n, nil
}
