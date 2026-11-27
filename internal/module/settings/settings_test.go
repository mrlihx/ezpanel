package settings

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"testing"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestHiddenMenusSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	cfg, err := config.New(filepath.Join(root, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()

	m, err := New(db, cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("panel.session", auth.Session{User: store.User{ID: 1, Username: "admin"}})
	})
	m.Routes(r.Group(""))

	doReq := func(method, path string, body any) (int, map[string]any) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}

	code, resp := doReq(http.MethodGet, "/api/settings", nil)
	if code != 200 {
		t.Fatalf("GET /api/settings status = %d: %+v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if hidden, ok := data["hiddenMenus"].([]any); !ok || len(hidden) != 0 {
		t.Fatalf("default hiddenMenus = %#v", data["hiddenMenus"])
	}

	code, resp = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
		"hiddenMenus": []string{"/websites", "/tools", "/p/demo-app", "/websites"},
	})
	if code != 200 {
		t.Fatalf("PUT /api/settings/menus status = %d: %+v", code, resp)
	}
	got := m.readHiddenMenus()
	if !slices.Equal(got, []string{"/websites", "/tools", "/p/demo-app"}) {
		t.Fatalf("saved hiddenMenus = %v", got)
	}

	for _, locked := range []string{"/", "/apps", "/settings", "logout"} {
		code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{"hiddenMenus": []string{locked}})
		if code != 400 {
			t.Fatalf("expected 400 for locked menu %q, got %d", locked, code)
		}
	}

	code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{"hiddenMenus": []string{"/unknown"}})
	if code != 400 {
		t.Fatalf("expected 400 for invalid menu, got %d", code)
	}

	if order := m.readMenuOrder(m.readMenuGroups()); len(order) != 0 {
		t.Fatalf("default menuOrder = %v, want empty", order)
	}
	code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
		"menuOrder": []string{"/files", "/websites", "/p/demo-app", "/files"},
	})
	if code != 200 {
		t.Fatalf("expected 200 saving menuOrder, got %d", code)
	}
	if order := m.readMenuOrder(m.readMenuGroups()); !slices.Equal(order, []string{"/files", "/websites", "/p/demo-app"}) {
		t.Fatalf("saved menuOrder = %v", order)
	}
	// 更新顺序不应覆盖已保存的隐藏菜单配置
	if got := m.readHiddenMenus(); !slices.Equal(got, []string{"/websites", "/tools", "/p/demo-app"}) {
		t.Fatalf("hiddenMenus changed unexpectedly: %v", got)
	}
	for _, bad := range []string{"/", "logout", "/unknown"} {
		code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{"menuOrder": []string{bad}})
		if code != 400 {
			t.Fatalf("expected 400 for invalid order key %q, got %d", bad, code)
		}
	}

	// 旧数据若在 menu_order 中包含 "/" 或 "logout"，读取时应自动过滤
	if err := db.Save(&store.Setting{Key: menuOrderSettingKey, Value: `["/","/files","logout"]`}).Error; err != nil {
		t.Fatal(err)
	}
	if order := m.readMenuOrder(m.readMenuGroups()); !slices.Equal(order, []string{"/files"}) {
		t.Fatalf("legacy filtered menuOrder = %v, want [/files]", order)
	}
}

func TestMenuGroupsSetting(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	cfg, err := config.New(filepath.Join(root, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()

	m, err := New(db, cfg, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("panel.session", auth.Session{User: store.User{ID: 1, Username: "admin"}})
	})
	m.Routes(r.Group(""))

	doReq := func(method, path string, body any) (int, map[string]any) {
		var raw []byte
		if body != nil {
			raw, _ = json.Marshal(body)
		}
		req := httptest.NewRequest(method, path, bytes.NewReader(raw))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		var resp map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return w.Code, resp
	}

	code, resp := doReq(http.MethodGet, "/api/settings", nil)
	if code != 200 {
		t.Fatalf("GET /api/settings status = %d: %+v", code, resp)
	}
	data := resp["data"].(map[string]any)
	if groups, ok := data["menuGroups"].([]any); !ok || len(groups) != 0 {
		t.Fatalf("default menuGroups = %#v", data["menuGroups"])
	}

	code, resp = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
		"menuGroups": []map[string]any{
			{
				"id":    "ops",
				"title": "运维工具",
				"icon":  "wrench",
				"items": []string{"/terminal", "/oplogs", "/terminal"},
			},
			{
				"id":    "extensions",
				"title": "扩展应用",
				"icon":  "invalid-icon",
				"items": []string{"/p/demo-app"},
			},
		},
		"menuOrder": []string{"/websites", "group:ops", "/apps", "group:extensions"},
	})
	if code != 200 {
		t.Fatalf("PUT /api/settings/menus with groups status = %d: %+v", code, resp)
	}

	groups := m.readMenuGroups()
	if len(groups) != 2 {
		t.Fatalf("saved menuGroups len = %d, want 2: %+v", len(groups), groups)
	}
	if groups[0].ID != "ops" || groups[0].Icon != "wrench" || !slices.Equal(groups[0].Items, []string{"/terminal", "/oplogs"}) {
		t.Fatalf("unexpected group[0]: %+v", groups[0])
	}
	if groups[1].ID != "extensions" || groups[1].Icon != "blocks" || !slices.Equal(groups[1].Items, []string{"/p/demo-app"}) {
		t.Fatalf("unexpected group[1]: %+v", groups[1])
	}
	if order := m.readMenuOrder(m.readMenuGroups()); !slices.Equal(order, []string{"/websites", "group:ops", "/apps", "group:extensions"}) {
		t.Fatalf("saved menuOrder = %v", order)
	}

	// 拒绝 "/" 和 "logout" 入组
	for _, pinned := range []string{"/", "logout"} {
		code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
			"menuGroups": []map[string]any{{"id": "g1", "title": "分组", "icon": "folder", "items": []string{pinned}}},
		})
		if code != 400 {
			t.Fatalf("expected 400 for pinned item %q in group, got %d", pinned, code)
		}
	}

	// 拒绝同一菜单同时属于多个分组
	code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
		"menuGroups": []map[string]any{
			{"id": "g1", "title": "分组一", "icon": "folder", "items": []string{"/websites"}},
			{"id": "g2", "title": "分组二", "icon": "folder", "items": []string{"/websites"}},
		},
	})
	if code != 400 {
		t.Fatalf("expected 400 for duplicate item across groups, got %d", code)
	}

	// 拒绝空标题、超长标题、非法分组 ID
	for _, badGroup := range []map[string]any{
		{"id": "g1", "title": "   ", "icon": "folder", "items": []string{}},
		{"id": "g1", "title": "一二三四五六七八九十一二三四五六七八九十一", "icon": "folder", "items": []string{}},
		{"id": "Bad_ID", "title": "分组", "icon": "folder", "items": []string{}},
	} {
		code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
			"menuGroups": []map[string]any{badGroup},
		})
		if code != 400 {
			t.Fatalf("expected 400 for invalid group %+v, got %d", badGroup, code)
		}
	}

	// 拒绝不存在的 group:nonexistent
	code, _ = doReq(http.MethodPut, "/api/settings/menus", map[string]any{
		"menuOrder": []string{"group:nonexistent"},
	})
	if code != 400 {
		t.Fatalf("expected 400 for nonexistent group in menuOrder, got %d", code)
	}
}

func TestNetworkPolicyPartialUpdate(t *testing.T) {
	cfg, err := config.New(filepath.Join(t.TempDir(), "panel"))
	if err != nil {
		t.Fatal(err)
	}
	cfg.BoundDomain = "panel.example.test"
	cfg.PublicURL = "http://" + cfg.BoundDomain + ":18080"
	cfg.Port = 18080
	cfg = config.Shared(cfg)
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
	m, err := New(db, cfg, a, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	r := gin.New()
	m.Routes(r.Group(""))
	put := func(body string) int {
		req := httptest.NewRequest(http.MethodPut, cfg.PublicURL+"/api/settings/network", bytes.NewBufferString(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	if status := put(`{"unauthStatus":403}`); status != http.StatusOK {
		t.Fatalf("policy save returned %d", status)
	}
	if cfg.Current().BoundDomain != cfg.BoundDomain || a.SecurityPolicy().UnauthStatus != 403 {
		t.Fatal("policy-only save changed network settings or did not apply")
	}
	if status := put(`{"unauthStatus":404,"sslEnabled":true,"certificate":"invalid"}`); status != http.StatusBadRequest {
		t.Fatalf("invalid network save returned %d", status)
	}
	if a.SecurityPolicy().UnauthStatus != 403 {
		t.Fatal("failed network save persisted policy")
	}
}
