package auth

import (
	"crypto/tls"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func fixture(t *testing.T) (*Service, config.Config) {
	t.Helper()
	cfg, e := config.New(t.TempDir())
	if e != nil {
		t.Fatal(e)
	}
	cfg.PublicURL = "https://panel.example"
	db, e := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	if e != nil {
		t.Fatal(e)
	}
	h, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	db.Create(&store.User{Username: "admin", PasswordHash: string(h), AuthVersion: 1})
	s, e := New(db, cfg)
	if e != nil {
		t.Fatal(e)
	}
	return s, cfg
}
func TestPasswordBoundary(t *testing.T) {
	for _, p := range []string{"12345678", "abcdefgh", "a1短", "a1" + strings.Repeat("中", 24)} {
		if ValidatePassword(p) == nil {
			t.Errorf("accepted %q", p)
		}
	}
	for _, p := range []string{"password123", "a1" + strings.Repeat("中", 23)} {
		if e := ValidatePassword(p); e != nil {
			t.Fatal(e)
		}
	}
}
func TestSessionClaimsAndRevocation(t *testing.T) {
	s, _ := fixture(t)
	var u store.User
	s.db.First(&u)
	token, e := s.Issue(u)
	if e != nil {
		t.Fatal(e)
	}
	session, e := s.CheckSession(token)
	if e != nil {
		t.Fatal(e)
	}
	if session.User.Username != "admin" {
		t.Fatal(session)
	}
	for _, mutate := range []func(*Claims){func(c *Claims) { c.ExpiresAt = nil }, func(c *Claims) { c.Issuer = "other" }, func(c *Claims) { c.Audience = []string{"entry"} }, func(c *Claims) { c.Purpose = "entry" }, func(c *Claims) { c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-time.Minute)) }} {
		c := s.claims(u)
		mutate(&c)
		bad, _ := jwt.NewWithClaims(jwt.SigningMethodHS256, c).SignedString(s.key)
		if _, e := s.CheckSession(bad); e == nil {
			t.Fatal("invalid claims accepted")
		}
	}
	bad, _ := jwt.NewWithClaims(jwt.SigningMethodHS384, s.claims(u)).SignedString(s.key)
	if _, e := s.CheckSession(bad); e == nil {
		t.Fatal("algorithm accepted")
	}
	s.db.Model(&u).Update("auth_version", 2)
	if _, e := s.CheckSession(token); e == nil {
		t.Fatal("old session accepted")
	}
}
func TestEntryCookie(t *testing.T) {
	s, cfg := fixture(t)
	r := gin.New()
	r.Use(s.EntryGuard())
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	r.GET("/login", func(c *gin.Context) { c.Status(200) })
	request := func(path, cookie string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		q := httptest.NewRequest("GET", path, nil)
		if cookie != "" {
			q.Header.Set("Cookie", cookie)
		}
		r.ServeHTTP(w, q)
		return w
	}
	if w := request("/", ""); w.Code != 404 || w.Body.Len() != 0 {
		t.Fatal(w)
	}
	w := request("/"+cfg.Entry, "")
	if w.Code != 302 {
		t.Fatal(w)
	}
	ck := w.Result().Cookies()[0]
	if !ck.HttpOnly || !ck.Secure || ck.SameSite != 3 || ck.Path != "/" {
		t.Fatal(ck)
	}
	if request("/", ck.String()).Code != 200 {
		t.Fatal("entry denied")
	}
	if request("/", "panel_entry="+ck.Value+"x").Code != 404 {
		t.Fatal("tamper accepted")
	}
	s.cfg.Entry = "newentry"
	if request("/", ck.String()).Code != 404 {
		t.Fatal("old entry accepted")
	}
	s.cfg.Entry = "login"
	w = request("/login", "")
	if w.Code != 302 || request("/login", w.Result().Cookies()[0].String()).Code != 200 {
		t.Fatal("login as configured entry did not open the login page")
	}
}
func TestOriginAndProxy(t *testing.T) {
	s, _ := fixture(t)
	for _, tt := range []struct {
		host, origin, remote, forward string
		want                          int
	}{{"panel.example", "https://panel.example", "127.0.0.1:4", "", 200}, {"evil.example", "https://evil.example", "127.0.0.1:4", "", 403}, {"panel.example", "https://evil.example", "127.0.0.1:4", "", 403}, {"internal", "https://panel.example", "127.0.0.1:4", "panel.example", 403}} {
		r := gin.New()
		r.Use(s.SourceGuard())
		r.POST("/", func(c *gin.Context) { c.Status(200) })
		w := httptest.NewRecorder()
		q := httptest.NewRequest("POST", "/", nil)
		q.TLS = &tls.ConnectionState{}
		q.Host = tt.host
		q.RemoteAddr = tt.remote
		q.Header.Set("Origin", tt.origin)
		q.Header.Set("X-Panel-Request", "1")
		q.Header.Set("X-Forwarded-Host", tt.forward)
		r.ServeHTTP(w, q)
		if w.Code != tt.want {
			t.Fatalf("%+v: %d", tt, w.Code)
		}
	}
}

func TestDirectIPSourceGuard(t *testing.T) {
	s, cfg := fixture(t)
	cfg.PublicURL = ""
	cfg.Port = 18080
	const direct = "http://192.0.2.10:18080"
	for _, test := range []struct {
		listen, host, origin string
		want                 int
	}{
		{"0.0.0.0", direct, direct, 200},
		{"0.0.0.0", direct, "http://192.0.2.11:18080", 403},
		{"0.0.0.0", "http://192.0.2.10:18081", "http://192.0.2.10:18081", 403},
		{"0.0.0.0", "http://unbound.example:18080", "http://unbound.example:18080", 403},
		{"127.0.0.1", direct, direct, 403},
		{"127.0.0.1", "http://127.0.0.1:18080", "http://127.0.0.1:18080", 200},
	} {
		cfg.ListenAddr = test.listen
		s.cfg = cfg
		r := gin.New()
		r.Use(s.SourceGuard())
		r.POST("/", func(c *gin.Context) { c.Status(200) })
		q := httptest.NewRequest("POST", test.host+"/", nil)
		q.Header.Set("Origin", test.origin)
		q.Header.Set("X-Panel-Request", "1")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != test.want {
			t.Fatalf("%+v: %d", test, w.Code)
		}
	}
}
func TestSharedPasswordLock(t *testing.T) {
	s, cfg := fixture(t)
	var u store.User
	s.db.First(&u)
	const ip = "127.0.0.1"
	now := time.Unix(1_800_000_000, 0)
	s.now = func() time.Time { return now }
	for range 4 {
		if err := s.ConfirmPassword(ip, u, "wrong"); !errors.Is(err, ErrCredentials) {
			t.Fatalf("expected invalid credentials, got %v", err)
		}
	}
	if err := s.ConfirmPassword(ip, u, "wrong"); !errors.Is(err, ErrLocked) {
		t.Fatalf("fifth failure did not lock: %v", err)
	}
	restarted, err := New(s.db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	restarted.now = s.now
	now = now.Add(59 * time.Minute)
	if err := restarted.ConfirmPassword(ip, u, "password123"); !errors.Is(err, ErrLocked) {
		t.Fatalf("correct password bypassed the persisted lock: %v", err)
	}
	if err := restarted.ConfirmPassword("127.0.0.2", u, "password123"); err != nil {
		t.Fatalf("lock leaked to another IP: %v", err)
	}
	now = now.Add(time.Minute)
	if err := restarted.ConfirmPassword(ip, u, "password123"); err != nil {
		t.Fatalf("still locked after an hour: %v", err)
	}
}
func TestTrustedProxyAndWebSocket(t *testing.T) {
	s, cfg := fixture(t)
	cfg.TrustedProxies = []string{"127.0.0.1"}
	s, _ = New(s.db, cfg)
	r := gin.New()
	r.Use(s.SourceGuard())
	r.GET("/ws/test", func(c *gin.Context) { c.String(200, s.ClientIP(c)) })
	for _, tt := range []struct {
		remote, origin, forwardIP string
		want                      int
		ip                        string
	}{{"127.0.0.1:4", "https://panel.example", "198.51.100.8", 200, "198.51.100.8"}, {"127.0.0.1:4", "", "198.51.100.8", 403, ""}, {"192.0.2.1:4", "https://panel.example", "198.51.100.8", 403, ""}} {
		q := httptest.NewRequest("GET", "http://internal/ws/test", nil)
		q.RemoteAddr = tt.remote
		q.Header.Set("X-Forwarded-Host", "panel.example")
		q.Header.Set("X-Forwarded-Proto", "https")
		q.Header.Set("X-Forwarded-For", tt.forwardIP)
		q.Header.Set("Origin", tt.origin)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != tt.want || (tt.ip != "" && w.Body.String() != tt.ip) {
			t.Fatal(tt, w.Code, w.Body.String())
		}
	}
}
func TestExpiredEntryAndTokenPurpose(t *testing.T) {
	s, _ := fixture(t)
	s.now = func() time.Time { return time.Now().Add(-13 * time.Hour) }
	r := gin.New()
	r.Use(s.EntryGuard())
	r.GET("/", func(c *gin.Context) { c.Status(200) })
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("GET", "/"+s.cfg.Entry, nil))
	cookie := w.Result().Cookies()[0]
	s.now = time.Now
	q := httptest.NewRequest("GET", "/", nil)
	q.AddCookie(cookie)
	w = httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != 404 {
		t.Fatal("expired entry")
	}
	if _, e := s.CheckSession(cookie.Value); e == nil {
		t.Fatal("entry accepted as jwt")
	}
	var u store.User
	s.db.First(&u)
	token, _ := s.Issue(u)
	q = httptest.NewRequest("GET", "/", nil)
	q.AddCookie(&http.Cookie{Name: "panel_entry", Value: token})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != 404 {
		t.Fatal("jwt accepted as entry")
	}
}

func TestSevenDayIdleSession(t *testing.T) {
	s, _ := fixture(t)
	s.cfg.IdleMinutes = 168 * 60
	now := time.Now().UTC().Truncate(time.Second)
	s.now = func() time.Time { return now }
	var user store.User
	s.db.First(&user)
	token, err := s.Issue(user)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(167 * time.Hour)
	if _, err = s.CheckSession(token); err != nil {
		t.Fatal("168小时以内不应因令牌提前过期", err)
	}
	if !s.Touch(token) {
		t.Fatal("会话活动未延长空闲时间")
	}
	now = now.Add(167 * time.Hour)
	if _, err = s.CheckSession(token); err != nil {
		t.Fatal("有效活动后仍应保持会话", err)
	}
	now = now.Add(time.Hour)
	if _, err = s.CheckSession(token); err == nil {
		t.Fatal("达到168小时空闲应退出")
	}
}

func TestSourceGuardAllowsPluginWebhook(t *testing.T) {
	s, cfg := fixture(t)
	r := gin.New()
	r.Use(s.EntryGuard())
	r.Use(s.SourceGuard())
	r.POST("/plugin-webhooks/:id/:job", func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/api/settings", func(c *gin.Context) { c.Status(http.StatusOK) })

	q := httptest.NewRequest(http.MethodPost, "/plugin-webhooks/demo/sync", nil)
	q.Host = "panel.example"
	q.TLS = &tls.ConnectionState{}
	w := httptest.NewRecorder()
	r.ServeHTTP(w, q)
	if w.Code != http.StatusOK {
		t.Fatalf("expected plugin webhook to bypass SourceGuard X-Panel-Request check, got %d (%s)", w.Code, w.Body.String())
	}

	r.GET("/"+cfg.Entry, s.EntryGuard(), func(c *gin.Context) { c.Status(http.StatusOK) })
	entryReq := httptest.NewRequest(http.MethodGet, "/"+cfg.Entry, nil)
	entryReq.Host = "panel.example"
	entryReq.TLS = &tls.ConnectionState{}
	entryW := httptest.NewRecorder()
	r.ServeHTTP(entryW, entryReq)
	entryCookie := entryW.Result().Cookies()[0]

	q2 := httptest.NewRequest(http.MethodPost, "/api/settings", nil)
	q2.Host = "panel.example"
	q2.TLS = &tls.ConnectionState{}
	q2.AddCookie(entryCookie)
	w2 := httptest.NewRecorder()
	r.ServeHTTP(w2, q2)
	if w2.Code != http.StatusForbidden {
		t.Fatalf("expected /api/settings without X-Panel-Request to be forbidden, got %d", w2.Code)
	}
}
