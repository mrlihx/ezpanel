package server

import (
	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/settings"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
	"golang.org/x/crypto/bcrypt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func visitEntry(t *testing.T, router http.Handler, origin, entry string) *http.Cookie {
	t.Helper()
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodGet, origin+"/"+entry, nil))
	cookies := w.Result().Cookies()
	if w.Code != http.StatusFound || len(cookies) != 1 || cookies[0].Name != "panel_entry" {
		t.Fatalf("entry did not issue a cookie: %d", w.Code)
	}
	return cookies[0]
}

func TestStaticShellAndFallback(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.Dev = true
	db, _ := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	a, _ := auth.New(db, cfg)
	router, err := New(Options{Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	origin := cfg.PanelOrigin("").String()
	entry := visitEntry(t, router, origin, cfg.Entry)
	for _, path := range []string{"/", "/websites"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, origin+path, nil)
		req.AddCookie(entry)
		router.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
}

func TestFallbackDoesNotMaskMissingResources(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.Dev = true
	db, _ := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	a, _ := auth.New(db, cfg)
	router, err := New(Options{Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	origin := cfg.PanelOrigin("").String()
	entry := visitEntry(t, router, origin, cfg.Entry)
	for _, path := range []string{"/api", "/api/missing", "/ws", "/ws/missing", "/plugin", "/plugin/example/ui/1/index.js", "/assets/missing.js", "/missing.css", "/../go.mod"} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, origin+path, nil)
		req.AddCookie(entry)
		router.ServeHTTP(rec, req)
		if (rec.Code != http.StatusNotFound && rec.Code != http.StatusUnauthorized) || strings.Contains(rec.Body.String(), "<html") {
			t.Fatalf("%s: %d %s", path, rec.Code, rec.Body.String())
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, origin+"/websites", nil)
	req.AddCookie(entry)
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("POST fallback: %d", rec.Code)
	}
}

func TestOpaquePluginOriginCanOnlyLoadFrameResources(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	a, err := auth.New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	router, err := New(Options{Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	router.GET(auth.PluginFramePath, func(c *gin.Context) { c.Status(http.StatusNoContent) })
	origin := cfg.PanelOrigin("").String()
	for _, tc := range []struct {
		path string
		want int
	}{
		{"/plugin-frame/test/1.0.0/123/example/assets/entry.js", http.StatusNoContent},
		{"/api/auth/branding", http.StatusNotFound},
		{"/plugin/test/api/status", http.StatusNotFound},
		{"/plugin-frame.html", http.StatusNotFound},
	} {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, origin+tc.path, nil)
		req.Header.Set("Origin", "null")
		router.ServeHTTP(w, req)
		if w.Code != tc.want {
			t.Errorf("opaque origin for %s: got %d, want %d", tc.path, w.Code, tc.want)
		}
	}
}

type writeModule struct{ calls *int }

func (writeModule) Name() string { return "test" }
func (m writeModule) Routes(g *gin.RouterGroup) {
	g.POST("/api/test-write", func(c *gin.Context) { *m.calls++; c.Status(200) })
}
func TestSourceRejectedBeforeSideEffects(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.Dev = true
	db, _ := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	user := store.User{Username: "admin", PasswordHash: "unused", AuthVersion: 1}
	db.Create(&user)
	a, _ := auth.New(db, cfg)
	token, _ := a.Issue(user)
	r, _ := New(Options{Auth: a})
	calls := 0
	r.RegisterModule(writeModule{&calls})
	for _, tt := range []struct {
		origin, header string
		want           int
	}{{"http://evil.example", "1", 403}, {"", "", 403}, {"", "1", 200}} {
		q := httptest.NewRequest("POST", "http://127.0.0.1:"+strconv.Itoa(cfg.Port)+"/api/test-write", nil)
		q.AddCookie(&http.Cookie{Name: "panel_token", Value: token})
		q.Header.Set("Origin", tt.origin)
		q.Header.Set("X-Panel-Request", tt.header)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		if w.Code != tt.want {
			t.Fatal(w.Code)
		}
		if tt.want == 403 && calls != 0 {
			t.Fatal("side effect before source validation")
		}
	}
	if calls != 1 {
		t.Fatal(calls)
	}
}

func TestEntryAuthPasswordAndLogout(t *testing.T) {
	cfg, _ := config.New(t.TempDir())
	cfg.Dev = true
	cfg = config.Shared(cfg)
	db, _ := store.Open(filepath.Join(t.TempDir(), "panel.db"))
	h, _ := bcrypt.GenerateFromPassword([]byte("password123"), bcrypt.MinCost)
	db.Create(&store.User{Username: "admin", PasswordHash: string(h), AuthVersion: 1})
	a, _ := auth.New(db, cfg)
	r, _ := New(Options{Auth: a})
	settingsModule, err := settings.New(db, cfg, a, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r.RegisterModule(settingsModule)
	origin := "http://192.0.2.10:" + strconv.Itoa(cfg.Port)
	do := func(method, path, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		q := httptest.NewRequest(method, origin+path, strings.NewReader(body))
		q.Header.Set("X-Panel-Request", "1")
		q.Header.Set("Origin", origin)
		q.Header.Set("Content-Type", "application/json")
		for _, cookie := range cookies {
			q.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		r.ServeHTTP(w, q)
		return w
	}
	for _, p := range []string{"/", "/login", "/api/auth/me", "/ws/test", "/assets/missing.js", "/api/auth/login/"} {
		w := do("GET", p, "")
		if w.Code != 404 || w.Body.Len() != 0 {
			t.Fatalf("entry %s: %d %s", p, w.Code, w.Body.String())
		}
	}
	if w := do("POST", "/api/auth/login", `{"username":"admin","password":"password123"}`); w.Code != 404 || len(w.Result().Cookies()) != 0 {
		t.Fatalf("login without entry: %d", w.Code)
	}
	entry := visitEntry(t, r, origin, cfg.Entry)
	for _, p := range []string{"/api/tasks", "/api/missing", "/ws/test"} {
		if w := do("GET", p, "", entry); w.Code != 401 {
			t.Fatalf("auth %s: %d", p, w.Code)
		}
	}
	login := func(password string) *httptest.ResponseRecorder {
		return do("POST", "/api/auth/login", `{"username":"admin","password":"`+password+`"}`, entry)
	}
	w := login("password123")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	cookies := w.Result().Cookies()
	if len(cookies) != 2 || cookies[1].Name != "panel_entry" || cookies[1].MaxAge != -1 {
		t.Fatal("login should clear the entry cookie")
	}
	token := cookies[0]
	if token.Secure || !token.HttpOnly || token.SameSite != http.SameSiteStrictMode {
		t.Fatal(token)
	}
	for _, path := range []string{"/", "/websites", "/api/auth/me"} {
		if w := do("GET", path, "", token); w.Code != http.StatusOK {
			t.Fatalf("authenticated access without entry %s: %d", path, w.Code)
		}
	}
	w = do("PUT", "/api/settings/network", `{"entry":"12345"}`, token)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"loginRequired":false`) || !strings.Contains(w.Body.String(), origin+"/12345") || cfg.Current().Entry != "12345" || cfg.Current().PublicURL != "" {
		t.Fatalf("change entry: %d %s", w.Code, w.Body.String())
	}
	if do("GET", "/api/auth/me", "", token).Code != 200 {
		t.Fatal("changing the entry revoked a valid session")
	}
	if do("GET", "/login", "", entry).Code != 404 {
		t.Fatal("old entry cookie is still accepted")
	}
	entry = visitEntry(t, r, origin, cfg.Current().Entry)
	w = do("POST", "/api/auth/password", `{"oldPassword":"password123","newPassword":"newPassword123"}`, entry, token)
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if do("GET", "/api/auth/me", "", entry, token).Code != 401 {
		t.Fatal("password did not revoke")
	}
	w = login("newPassword123")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	token = w.Result().Cookies()[0]
	if do("POST", "/api/auth/logout", "", entry, token).Code != 200 {
		t.Fatal("logout failed")
	}
	if do("GET", "/api/auth/me", "", entry, token).Code != 401 {
		t.Fatal("logout did not revoke")
	}
	if do("GET", "/login", "", token).Code != 404 {
		t.Fatal("revoked token opened the login page")
	}
	logs, _ := store.QueryOpLogs(db, store.OpLogFilter{})
	if len(logs) < 4 {
		t.Fatal("missing audit")
	}
	for _, log := range logs {
		if strings.Contains(log.Target, "password") || strings.Contains(log.ErrorMsg, "password123") {
			t.Fatal("secret audit")
		}
	}
}
