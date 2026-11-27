package auth

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
)

func newTestAuthService(t *testing.T) (*Service, store.User) {
	t.Helper()
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { _ = sql.Close() })

	hash, err := bcrypt.GenerateFromPassword([]byte("Pass12345"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	user := store.User{Username: "admin", PasswordHash: string(hash), AuthVersion: 1}
	if err := db.Create(&user).Error; err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		JWTSecret:   "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		AESKey:      "MDEyMzQ1Njc4OWFiY2RlZjAxMjM0NTY3ODlhYmNkZWY=",
		Entry:       "entry123",
		IdleMinutes: 60,
		Port:        8512,
	}
	svc, err := New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return svc, user
}

func TestTOTPVerifyAndLogin(t *testing.T) {
	svc, user := newTestAuthService(t)
	now := time.Unix(1700000000, 0).UTC()
	svc.now = func() time.Time { return now }

	secret, err := generateTOTPSecret()
	if err != nil || len(secret) < 16 {
		t.Fatalf("unexpected secret: %q %v", secret, err)
	}
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	validCode := totpCodeAt(key, uint64(now.Unix()/30))
	if !verifyTOTPCode(secret, validCode, now) {
		t.Fatal("expected valid TOTP code to pass")
	}
	if verifyTOTPCode(secret, "000000", now) && validCode != "000000" {
		t.Fatal("expected invalid TOTP code to fail")
	}

	if err := svc.saveTOTPSettings(t.Context(), totpConfig{Enabled: true, Secret: secret, UserID: user.ID}); err != nil {
		t.Fatal(err)
	}

	r := gin.New()
	svc.Routes(r.Group(""))

	// 无 TOTP 验证码时登录应失败
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(`{"username":"admin","password":"Pass12345"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), "动态口令") {
		t.Fatalf("expected totp failure, got %d %s", rec.Code, rec.Body.String())
	}

	// 带正确 TOTP 验证码时登录成功
	rec = httptest.NewRecorder()
	body, _ := json.Marshal(map[string]string{"username": "admin", "password": "Pass12345", "totpCode": validCode})
	req = httptest.NewRequest(http.MethodPost, "/api/auth/login", strings.NewReader(string(body)))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"username":"admin"`) {
		t.Fatalf("expected login success, got %d %s", rec.Code, rec.Body.String())
	}

	// 关闭后应保留原密钥，不重置时 setup 返回原有密钥，reset=true 时生成新密钥
	authed := gin.New()
	authed.Use(func(c *gin.Context) {
		c.Set("panel.session", Session{User: user})
	})
	svc.Routes(authed.Group(""))

	rec = httptest.NewRecorder()
	authed.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/totp/disable", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("disableTOTP failed: %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	authed.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/api/auth/totp/setup", nil))
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"existing":true`) || !strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("expected existing secret reused, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	resetReq := httptest.NewRequest(http.MethodPost, "/api/auth/totp/setup", strings.NewReader(`{"reset":true}`))
	resetReq.Header.Set("Content-Type", "application/json")
	authed.ServeHTTP(rec, resetReq)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"existing":false`) || strings.Contains(rec.Body.String(), secret) {
		t.Fatalf("expected new secret on reset, got %d %s", rec.Code, rec.Body.String())
	}
}

func TestSecurityPolicyUserAgentAndUnauthResponse(t *testing.T) {
	svc, _ := newTestAuthService(t)

	// 当前 UA 不匹配时拒绝保存以防自锁
	if err := svc.SaveSecurityPolicy(t.Context(), "Mozilla/5.0 Chrome/120", SecurityPolicy{
		UserAgentEnabled: true,
		UserAgentAllow:   "OnlyMyBrowser",
		UnauthStatus:     403,
	}); err == nil {
		t.Fatal("expected self-lockout protection error")
	}

	// 当前 UA 匹配时允许保存
	if err := svc.SaveSecurityPolicy(t.Context(), "Mozilla/5.0 MyPanelClient/1.0", SecurityPolicy{
		UserAgentEnabled: true,
		UserAgentAllow:   "MyPanelClient\nOtherAgent",
		UnauthStatus:     403,
		UnauthHTML:       "<h1>Access Denied</h1>",
	}); err != nil {
		t.Fatalf("SaveSecurityPolicy failed: %v", err)
	}

	r := gin.New()
	r.Use(svc.EntryGuard())
	r.GET("/entry123", func(c *gin.Context) { c.Status(http.StatusOK) })

	// UA 不匹配 -> 返回 403 + 自定义 HTML
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/entry123", nil)
	req.Header.Set("User-Agent", "curl/8.0")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden || rec.Body.String() != "<h1>Access Denied</h1>" {
		t.Fatalf("unexpected UA block response: %d %s", rec.Code, rec.Body.String())
	}

	// UA 匹配 -> 通过入口校验并重定向
	rec = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/entry123", nil)
	req.Header.Set("User-Agent", "Mozilla/5.0 MyPanelClient/1.0")
	r.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound {
		t.Fatalf("expected redirect for allowed UA, got %d", rec.Code)
	}
}

func TestConsumeTOTPTracksUsedStep(t *testing.T) {
	svc, user := newTestAuthService(t)
	now := time.Unix(1700000000, 0).UTC()
	svc.now = func() time.Time { return now }
	secret, err := generateTOTPSecret()
	if err != nil {
		t.Fatal(err)
	}
	key, err := totpEncoding.DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	cfg := totpConfig{Enabled: true, Secret: secret, UserID: user.ID}
	code := totpCodeAt(key, uint64(now.Unix()/30))
	if ok, err := svc.consumeTOTP(t.Context(), cfg, code); err != nil || !ok {
		t.Fatalf("valid step should be accepted: %v, %v", ok, err)
	}
	if ok, err := svc.consumeTOTP(t.Context(), cfg, code); err != nil || ok {
		t.Fatalf("used step must be rejected: %v, %v", ok, err)
	}
	now = now.Add(60 * time.Second)
	code = totpCodeAt(key, uint64(now.Unix()/30))
	if ok, err := svc.consumeTOTP(t.Context(), cfg, code); err != nil || !ok {
		t.Fatalf("next step should be accepted: %v, %v", ok, err)
	}
}
