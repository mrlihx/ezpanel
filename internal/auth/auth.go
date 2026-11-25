package auth

import (
	"crypto/hmac"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	SessionRecheckInterval = 60 * time.Second
	passwordAttemptLimit   = 5
	passwordLockDuration   = time.Hour
)

var ErrCredentials = errors.New("用户名或密码错误")
var ErrLocked = errors.New("尝试次数过多，已锁定 1 小时")
var usernamePattern = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_.-]{2,31}$`)

type Claims struct {
	jwt.RegisteredClaims
	AuthVersion uint   `json:"authVersion"`
	Purpose     string `json:"purpose"`
}
type Session struct {
	User      store.User
	Token     string
	ExpiresAt time.Time
}
type Service struct {
	db      *gorm.DB
	cfg     config.Config
	key     []byte
	proxies []*net.IPNet
	now     func() time.Time
}

func New(db *gorm.DB, cfg config.Config) (*Service, error) {
	key, e := base64.StdEncoding.DecodeString(cfg.JWTSecret)
	if e != nil {
		return nil, e
	}
	s := &Service{db: db, cfg: cfg, key: key, now: time.Now}
	for _, p := range cfg.TrustedProxies {
		if ip := net.ParseIP(p); ip != nil {
			bits := 128
			if ip.To4() != nil {
				bits = 32
			}
			s.proxies = append(s.proxies, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		} else {
			_, n, e := net.ParseCIDR(p)
			if e != nil {
				return nil, e
			}
			s.proxies = append(s.proxies, n)
		}
	}
	return s, nil
}
func ValidateUsername(username string) error {
	if !usernamePattern.MatchString(username) {
		return errors.New("账号须为3至32位字母、数字或_.-")
	}
	return nil
}
func ValidatePassword(p string) error {
	var letter, digit bool
	for _, r := range p {
		letter = letter || unicode.IsLetter(r)
		digit = digit || unicode.IsDigit(r)
	}
	if !utf8.ValidString(p) || utf8.RuneCountInString(p) < 8 || len(p) > 72 || !letter || !digit {
		return errors.New("密码至少8个字符，包含字母和数字，最多72个UTF-8字节")
	}
	return nil
}
func (s *Service) claims(u store.User) Claims {
	now := s.now()
	return Claims{RegisteredClaims: jwt.RegisteredClaims{ID: store.ID(), Subject: strconv.FormatUint(uint64(u.ID), 10), Issuer: "ezpanel", Audience: jwt.ClaimStrings{"panel-session"}, IssuedAt: jwt.NewNumericDate(now), ExpiresAt: jwt.NewNumericDate(now.Add(12 * time.Hour))}, AuthVersion: u.AuthVersion, Purpose: "session"}
}
func (s *Service) Issue(u store.User) (string, error) {
	claims := s.claims(u)
	raw, e := jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
	if e != nil {
		return "", e
	}
	now := s.now()
	if e = s.db.Where("expires_at <= ?", now).Delete(&store.AuthSession{}).Error; e != nil {
		return "", e
	}
	e = s.db.Create(&store.AuthSession{TokenHash: tokenHash(raw), UserID: u.ID, LastActive: now, ExpiresAt: claims.ExpiresAt.Time}).Error
	return raw, e
}

// CheckSession 每次读取账户版本；WS 拥有者每60秒调用，失败后关闭连接并清理资源。
func (s *Service) CheckSession(raw string) (Session, error) {
	var c Claims
	t, e := jwt.ParseWithClaims(raw, &c, func(t *jwt.Token) (any, error) { return s.key, nil }, jwt.WithValidMethods([]string{"HS256"}), jwt.WithIssuer("ezpanel"), jwt.WithAudience("panel-session"), jwt.WithExpirationRequired(), jwt.WithIssuedAt(), jwt.WithTimeFunc(s.now))
	if e != nil || !t.Valid || c.Purpose != "session" || c.IssuedAt == nil {
		return Session{}, ErrCredentials
	}
	id, e := strconv.ParseUint(c.Subject, 10, 64)
	if e != nil {
		return Session{}, ErrCredentials
	}
	var u store.User
	if s.db.First(&u, id).Error != nil || u.AuthVersion != c.AuthVersion {
		return Session{}, ErrCredentials
	}
	var active store.AuthSession
	if s.db.Where("token_hash = ? AND user_id = ?", tokenHash(raw), u.ID).First(&active).Error != nil || !active.LastActive.Add(time.Duration(s.cfg.Current().IdleMinutes)*time.Minute).After(s.now()) {
		return Session{}, ErrCredentials
	}
	return Session{User: u, Token: raw, ExpiresAt: c.ExpiresAt.Time}, nil
}
func Current(c *gin.Context) Session { v, _ := c.Get("panel.session"); s, _ := v.(Session); return s }

func (s *Service) requestSession(c *gin.Context) Session {
	if _, checked := c.Get("panel.session"); checked {
		return Current(c)
	}
	var session Session
	if raw, err := c.Cookie("panel_token"); err == nil {
		session, _ = s.CheckSession(raw)
	}
	c.Set("panel.session", session)
	return session
}

func (s *Service) Auth() gin.HandlerFunc {
	return func(c *gin.Context) {
		if s.requestSession(c).Token == "" {
			s.audit(c, "unauthenticated", false, 1002)
			response.Fail(c, 1002, "未登录或会话过期")
			return
		}
		c.Next()
	}
}
func (s *Service) cookie(c *gin.Context, name, value string, age int) {
	c.SetSameSite(http.SameSiteStrictMode)
	c.SetCookie(name, value, age, "/", "", s.RequestOrigin(c).Scheme == "https", true)
}
func (s *Service) entryMAC(value string) string {
	h := hmac.New(sha256.New, s.key)
	h.Write([]byte("panel-entry:" + value))
	return base64.RawURLEncoding.EncodeToString(h.Sum(nil))
}
func (s *Service) EntryGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Referrer-Policy", "no-referrer")
		if s.requestSession(c).Token != "" {
			c.Next()
			return
		}
		cfg := s.cfg.Current()
		v, _ := c.Cookie("panel_entry")
		parts := strings.Split(v, ".")
		if len(parts) == 3 && parts[0] == cfg.Entry {
			expiry, e := strconv.ParseInt(parts[1], 10, 64)
			expected := s.entryMAC(parts[0] + "." + parts[1])
			if e == nil && expiry > s.now().Unix() && hmac.Equal([]byte(expected), []byte(parts[2])) {
				c.Next()
				return
			}
		}
		c.Header("Cache-Control", "no-store")
		if c.Request.Method == http.MethodGet && c.Request.URL.Path == "/"+cfg.Entry {
			if cfg.DirectIPAccess() && directRequestOrigin(cfg, c.Request) == nil {
				c.AbortWithStatus(http.StatusNotFound)
				return
			}
			v := cfg.Entry + "." + strconv.FormatInt(s.now().Add(12*time.Hour).Unix(), 10)
			s.cookie(c, "panel_entry", v+"."+s.entryMAC(v), 43200)
			c.Redirect(http.StatusFound, "/")
			c.Abort()
			return
		}
		c.AbortWithStatus(http.StatusNotFound)
	}
}
func (s *Service) trusted(remote string) bool {
	host, _, e := net.SplitHostPort(remote)
	if e != nil {
		return false
	}
	ip := net.ParseIP(host)
	for _, n := range s.proxies {
		if n.Contains(ip) {
			return true
		}
	}
	return false
}
func (s *Service) ClientIP(c *gin.Context) string {
	host, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
	if s.trusted(c.Request.RemoteAddr) {
		hops := strings.Split(c.GetHeader("X-Forwarded-For"), ",")
		for i := len(hops) - 1; i >= 0; i-- {
			ip := net.ParseIP(strings.TrimSpace(hops[i]))
			if ip == nil {
				return host
			}
			host = ip.String()
			trusted := false
			for _, n := range s.proxies {
				trusted = trusted || n.Contains(ip)
			}
			if !trusted {
				break
			}
		}
	}
	return host
}

// 直连仅允许 IP 和实际监听端口，公网 IP 可由 NAT 转发，无需属于本机网卡。
func directRequestOrigin(cfg config.Config, request *http.Request) *url.URL {
	scheme, defaultPort := "http", 80
	if request.TLS != nil {
		scheme, defaultPort = "https", 443
	}
	parsed, err := url.Parse(scheme + "://" + request.Host)
	if err != nil || parsed.Host != request.Host || parsed.User != nil || strings.HasSuffix(request.Host, ":") {
		return nil
	}
	ip := net.ParseIP(parsed.Hostname())
	if !ip.IsGlobalUnicast() && !ip.IsLoopback() {
		return nil
	}
	port := defaultPort
	if parsed.Port() != "" {
		port, err = strconv.Atoi(parsed.Port())
		if err != nil {
			return nil
		}
	}
	origin := cfg.PanelOrigin(parsed.Hostname())
	if port != cfg.Port || scheme != origin.Scheme {
		return nil
	}
	return origin
}

func (s *Service) SourceGuard() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := s.cfg.Current()
		host := c.Request.Host
		scheme := "http"
		if c.Request.TLS != nil {
			scheme = "https"
		}
		if s.trusted(c.Request.RemoteAddr) {
			if h := c.GetHeader("X-Forwarded-Host"); h != "" {
				host = h
			}
			if p := c.GetHeader("X-Forwarded-Proto"); p != "" {
				scheme = p
			}
		}
		currentOrigin := cfg.PanelOrigin("")
		hostOK := host == currentOrigin.Host && (scheme == currentOrigin.Scheme || cfg.Dev)
		if cfg.DirectIPAccess() {
			currentOrigin = directRequestOrigin(cfg, c.Request)
			hostOK = currentOrigin != nil
		}
		if !hostOK {
			response.Fail(c, 1003, "请求来源不被允许")
			return
		}
		allowed := currentOrigin.String()
		origin := c.GetHeader("Origin")
		devOrigin := cfg.Dev && (origin == "http://127.0.0.1:5173" || origin == "http://localhost:5173")
		ws := c.Request.URL.Path == "/ws" || strings.HasPrefix(c.Request.URL.Path, "/ws/") || strings.EqualFold(c.GetHeader("Upgrade"), "websocket")
		unsafe := c.Request.Method != "GET" && c.Request.Method != "HEAD" && c.Request.Method != "OPTIONS"
		// phpMyAdmin使用标准HTML表单；仍要求已认证且浏览器明确来自同源。
		pmaForm := strings.HasPrefix(c.Request.URL.Path, "/api/phpmyadmin/view/") && origin == allowed
		if (origin != "" && origin != allowed && !devOrigin) || (ws && origin == "") || (unsafe && c.GetHeader("X-Panel-Request") != "1" && !pmaForm) {
			response.Fail(c, 1003, "请求来源不被允许")
			return
		}
		c.Set("panel.origin", currentOrigin)
		c.Next()
	}
}
func (s *Service) ConfirmPassword(ip string, u store.User, password string) error {
	var attempt store.LoginAttempt
	if err := s.db.Take(&attempt, "ip = ?", ip).Error; err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if attempt.Count >= passwordAttemptLimit && attempt.ExpiresAt > s.now().Unix() {
		return ErrLocked
	}
	invalid := len(password) > 72 || bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(password)) != nil
	now := s.now()
	if invalid {
		if err := s.db.Where("expires_at <= ?", now.Unix()).Delete(&store.LoginAttempt{}).Error; err != nil {
			return err
		}
		// 到达上限后固定解锁时间，并发失败请求也不会延长锁定。
		var count int
		err := s.db.Raw(`INSERT INTO login_attempts (ip, count, expires_at) VALUES (?, 1, ?)
			ON CONFLICT(ip) DO UPDATE SET count = count + 1, expires_at = excluded.expires_at
			WHERE count < ? RETURNING count`, ip, now.Add(passwordLockDuration).Unix(), passwordAttemptLimit).Row().Scan(&count)
		if errors.Is(err, sql.ErrNoRows) || count >= passwordAttemptLimit {
			return ErrLocked
		}
		if err != nil {
			return err
		}
		return ErrCredentials
	}
	result := s.db.Where("ip = ? AND (count < ? OR expires_at <= ?)", ip, passwordAttemptLimit, now.Unix()).Delete(&store.LoginAttempt{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		if err := s.db.Take(&attempt, "ip = ? AND count >= ? AND expires_at > ?", ip, passwordAttemptLimit, now.Unix()).Error; err == nil {
			return ErrLocked
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
	}
	return nil
}
func (s *Service) audit(c *gin.Context, action string, success bool, code int) {
	var username *string
	if v := Current(c); v.User.ID != 0 {
		username = &v.User.Username
	}
	_ = s.db.Create(&store.OpLog{Username: username, IP: s.ClientIP(c), UserAgent: response.Summary(c.Request.UserAgent()), Module: "auth", Action: action, Phase: "completed", Success: success, ErrorCode: code, CreatedAt: s.now().UTC()}).Error
}
func (s *Service) Routes(g *gin.RouterGroup) {
	g.POST("/api/auth/login", s.login)
	g.GET("/api/auth/branding", func(c *gin.Context) { response.OK(c, gin.H{"alias": s.cfg.Current().Alias}) })
	secured := g.Group("")
	secured.GET("/api/auth/me", func(c *gin.Context) {
		response.OK(c, s.userInfo(Current(c).User))
	})
	secured.POST("/api/auth/logout", s.logout)
	secured.POST("/api/auth/activity", s.activity)
	secured.POST("/api/auth/password", s.password)
}
func (s *Service) login(c *gin.Context) {
	var p struct {
		Username string `json:"username"`
		Password string `json:"password"`
	}
	if !response.Bind(c, &p) {
		code := 1001
		if v, ok := c.Get("panel.errorCode"); ok {
			code = v.(int)
		}
		s.audit(c, "login", false, code)
		return
	}
	var u store.User
	if e := s.db.Where("username = ?", p.Username).First(&u).Error; e != nil && !errors.Is(e, gorm.ErrRecordNotFound) {
		s.audit(c, "login", false, 5000)
		response.Fail(c, 5000, "")
		return
	}
	e := s.ConfirmPassword(s.ClientIP(c), u, p.Password)
	if e != nil {
		code, message := response.Internal, ""
		switch {
		case errors.Is(e, ErrLocked):
			code = 1003
			message = e.Error()
		case errors.Is(e, ErrCredentials):
			code = 1002
			message = e.Error()
		}
		s.audit(c, "login", false, code)
		response.Fail(c, code, message)
		return
	}
	raw, e := s.Issue(u)
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	c.Set("panel.session", Session{User: u})
	s.cookie(c, "panel_token", raw, 43200)
	s.cookie(c, "panel_entry", "", -1)
	s.audit(c, "login", true, 0)
	response.OK(c, s.userInfo(u))
}
func (s *Service) logout(c *gin.Context) {
	if e := s.db.Where("token_hash = ?", tokenHash(Current(c).Token)).Delete(&store.AuthSession{}).Error; e != nil {
		response.Fail(c, 5000, "")
		return
	}
	s.cookie(c, "panel_token", "", -1)
	s.cookie(c, "panel_entry", "", -1)
	s.audit(c, "logout", true, 0)
	response.OK(c, nil)
}
func (s *Service) password(c *gin.Context) {
	var p struct {
		OldPassword string `json:"oldPassword"`
		NewPassword string `json:"newPassword"`
	}
	if !response.Bind(c, &p) {
		return
	}
	if e := ValidatePassword(p.NewPassword); e != nil {
		response.Fail(c, 1001, e.Error())
		return
	}
	u := Current(c).User
	if e := s.ConfirmPassword(s.ClientIP(c), u, p.OldPassword); e != nil {
		s.audit(c, "password", false, 1003)
		response.Fail(c, 1003, e.Error())
		return
	}
	h, e := bcrypt.GenerateFromPassword([]byte(p.NewPassword), bcrypt.DefaultCost)
	if e == nil {
		e = s.db.Model(&u).Updates(map[string]any{"password_hash": string(h), "auth_version": gorm.Expr("auth_version + 1")}).Error
	}
	if e != nil {
		response.Fail(c, 5000, "")
		return
	}
	s.cookie(c, "panel_token", "", -1)
	s.cookie(c, "panel_entry", "", -1)
	s.audit(c, "password", true, 0)
	response.OK(c, nil)
}

// Audit 记录已注册模块的写操作；记录路由模板及处理器确认的资源标识，不采集查询串、正文和实际文件路径。
func (s *Service) Audit(module string) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Next()
		method := c.Request.Method
		if c.GetBool("panel.taskSubmitted") || c.GetBool("panel.auditRecorded") {
			return
		}
		if (method == "GET" && !strings.HasSuffix(c.FullPath(), "/download")) || method == "HEAD" || method == "OPTIONS" {
			return
		}
		session := Current(c)
		var username *string
		if session.User.ID != 0 {
			username = &session.User.Username
		}
		code := 0
		if v, ok := c.Get("panel.errorCode"); ok {
			code = v.(int)
		}
		action := c.GetString("panel.auditAction")
		if action == "" {
			action = method
		}
		auditModule, target, skip := classifyAudit(c, module, method, action, c.GetString("panel.auditName"))
		if skip {
			return
		}
		target = response.Summary(target)
		if detail := c.GetString("panel.auditDetail"); detail != "" {
			target += "\n" + strings.ToValidUTF8(detail, "�")
		}
		if value := c.GetString("panel.auditModule"); value != "" {
			auditModule = value
		}
		_ = s.db.Create(&store.OpLog{Username: username, IP: s.ClientIP(c), Module: auditModule, Action: action, Target: target, ResourceID: c.GetString("panel.auditResource"), Phase: "completed", Success: c.Writer.Status() < 400, ErrorCode: code, CreatedAt: s.now().UTC()}).Error
		c.Set("panel.auditRecorded", true)
	}
}
