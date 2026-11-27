package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestPluginSiteCapabilityScope(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sql.Close() })
	for _, id := range []string{"allowed-site", "other-site"} {
		if err := db.Create(&store.Website{ID: id, Name: id, Domains: `["test.example.com"]`}).Error; err != nil {
			t.Fatal(err)
		}
		for n := 1; n <= 2; n++ {
			label := id + strconv.Itoa(n)
			row := store.SiteBackup{ID: label, WebsiteID: &id, SiteName: id, FilePath: "/backup/" + label, CreatedAt: time.Unix(int64(n), 0)}
			if err := db.Create(&row).Error; err != nil {
				t.Fatal(err)
			}
		}
	}
	manifest := Manifest{Permissions: []string{"sites.read", "backups.read", "backups.create", "events.subscribe"}, Events: []string{"site.backup.finished"}, SiteAction: &SiteAction{Title: "巡检"}}
	data, _ := json.Marshal(manifest)
	if err := db.Create(&store.Plugin{ID: "test-audit", Type: "process", Manifest: string(data), Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	grant := store.Setting{Key: grantPrefix + "test-audit", Value: `{"siteIds":["allowed-site"],"events":["site.backup.finished"]}`}
	if err := db.Create(&grant).Error; err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	delivered := make(chan Event, 2)
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/_panel/event" || r.Header.Get("X-Panel-Proxy-Token") != "test-token" || r.Header.Get("X-Panel-User") != "system" {
			t.Error("invalid panel callback")
		}
		var event Event
		if err := json.NewDecoder(r.Body).Decode(&event); err != nil {
			t.Error(err)
		}
		delivered <- event
		w.WriteHeader(http.StatusNoContent)
	}))
	defer backend.Close()
	port, err := strconv.Atoi(backend.URL[strings.LastIndexByte(backend.URL, ':')+1:])
	if err != nil {
		t.Fatal(err)
	}
	run := &running{ctx: ctx, port: port, proxy: "test-token", log: &pluginLog{}}
	m := &Module{db: db, runs: map[string]*running{"test-audit": run}, statuses: map[string]string{"test-audit": "running"}}
	r := gin.New()
	m.Routes(r.Group(""))
	r.Use(func(c *gin.Context) { c.Set("plugin.identity", hostIdentity{"test-audit", manifest, run}) })
	r.GET("/resources/backups", func(c *gin.Context) { m.listResources(c, "backups") })
	r.POST("/sites/:site/backup", m.siteBackup)
	for _, tc := range []struct {
		method, path, has, lacks string
		status                   int
	}{
		{"GET", "/resources/backups", "allowed-site2", "allowed-site1", 200},
		{"GET", "/resources/backups", "allowed-site2", "other-site", 200},
		{"GET", "/api/plugins/site-actions/allowed-site", "test-audit", "", 200},
		{"GET", "/api/plugins/site-actions/other-site", `"data":[]`, "test-audit", 200},
		{"POST", "/sites/other-site/backup", "未获此网站授权", "", 403},
		{"POST", "/plugin/test-audit/api/_panel/event", "内部调用", "", 403},
	} {
		rec := httptest.NewRecorder()
		r.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
		body := rec.Body.String()
		if rec.Code != tc.status || !strings.Contains(body, tc.has) || tc.lacks != "" && strings.Contains(body, tc.lacks) {
			t.Fatalf("%s %s: %d %s", tc.method, tc.path, rec.Code, body)
		}
	}
	for _, id := range []string{"other-site", "allowed-site"} {
		if err := m.enqueueEvent(Event{Name: "site.backup.finished", SiteID: id}); err != nil {
			t.Fatal(err)
		}
	}
	for _, row := range m.pendingEvents(nil, 4) {
		m.attemptDelivery(row)
	}
	if len(delivered) != 1 || (<-delivered).SiteID != "allowed-site" {
		t.Fatal("only the authorized site event should be delivered")
	}
	c, _ := gin.CreateTestContext(httptest.NewRecorder())
	c.Request = httptest.NewRequest(http.MethodPut, "/api/plugins/test-audit/capabilities", nil)
	if err := m.validateGrant(c, manifest, &capabilityGrant{AllResources: []string{"containers"}}); err == nil {
		t.Fatal("an undeclared category was granted")
	}
	whole := capabilityGrant{AllResources: []string{"sites"}, SiteIDs: []string{"allowed-site"}, Events: manifest.Events}
	if err := m.validateGrant(c, manifest, &whole); err != nil || len(whole.SiteIDs) != 0 {
		t.Fatalf("whole category did not replace selected sites: %+v, %v", whole, err)
	}
	if err := m.saveGrants("test-audit", manifest, whole); err != nil {
		t.Fatal(err)
	}
	if err := db.Create(&store.Website{ID: "new-site", Name: "New site"}).Error; err != nil {
		t.Fatal(err)
	}
	resources, err := m.resourceSummaries(t.Context(), "test-audit", "sites")
	if err != nil || len(resources) != 3 {
		t.Fatalf("whole category did not include the newly added site: %+v, %v", resources, err)
	}
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/api/plugins/site-actions/new-site", nil))
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "test-audit") {
		t.Fatalf("new site did not expose the authorized menu: %d %s", rec.Code, rec.Body.String())
	}
	if err := m.enqueueEvent(Event{Name: "site.backup.finished", SiteID: "new-site"}); err != nil {
		t.Fatal(err)
	}
	if pending := m.pendingEvents(nil, 4); len(pending) != 1 {
		t.Fatalf("whole category did not include the new site's event: %+v", pending)
	} else {
		// 模拟事件排队后撤销全部授权，投递前应按当前范围重新检查。
		if err := m.saveGrants("test-audit", manifest, capabilityGrant{SiteIDs: []string{"allowed-site"}, Events: manifest.Events}); err != nil {
			t.Fatal(err)
		}
		m.attemptDelivery(pending[0])
		if len(delivered) != 0 {
			t.Fatal("an event outside the new grant was delivered")
		}
	}
}

func TestPluginAuditHistoryPagination(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sql.Close() })
	for i := 0; i < 3; i++ {
		if err := db.Create(&store.OpLog{
			Module:    "plugin:test-audit",
			Action:    "sites.read",
			Target:    "/host/v1/resources/sites",
			IP:        "127.0.0.1",
			Phase:     "completed",
			Success:   true,
			CreatedAt: time.Now().UTC(),
		}).Error; err != nil {
			t.Fatal(err)
		}
	}
	m := &Module{db: db}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("plugin.identity", hostIdentity{id: "test-audit"})
	})
	r.GET("/audit", m.auditHistory)
	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/audit?page=1&page_size=10", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("auditHistory failed: %d %s", rec.Code, rec.Body.String())
	}
	var res struct {
		Code int `json:"code"`
		Data struct {
			Total int64         `json:"total"`
			List  []store.OpLog `json:"list"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.Code != 0 || res.Data.Total != 3 || len(res.Data.List) != 3 {
		t.Fatalf("unexpected auditHistory result: %+v", res)
	}
}

func TestPermissionToggleAndGrantValidation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sql.Close() })

	mf := Manifest{
		ID:          "perm-test",
		Permissions: []string{"sites.read", "fs.write", "credentials.read", "notifications.send", "events.subscribe", "jobs.run"},
		Events:      []string{"site.backup.finished"},
		Jobs:        []Job{{ID: "nightly", Title: "Nightly"}},
		CredentialSlots: []CredentialSlot{
			{ID: "api-key", Title: "API Key"},
		},
	}
	defaults := defaultDisabledPermissions(mf)
	if len(defaults) != 2 || defaults[0] != "credentials.read" || defaults[1] != "fs.write" {
		t.Fatalf("unexpected defaultDisabledPermissions: %+v", defaults)
	}

	rawMF, _ := json.Marshal(mf)
	if err := db.Create(&store.Plugin{ID: "perm-test", Type: "process", Manifest: string(rawMF), Enabled: true}).Error; err != nil {
		t.Fatal(err)
	}
	grant := capabilityGrant{DisabledPermissions: []string{"fs.write", "sites.read", "credentials.read", "notifications.send", "events.subscribe", "jobs.run"}}
	rawGrant, _ := json.Marshal(grant)
	if err := db.Save(&store.Setting{Key: grantPrefix + "perm-test", Value: string(rawGrant)}).Error; err != nil {
		t.Fatal(err)
	}

	m := &Module{db: db}
	r := gin.New()
	r.Use(func(c *gin.Context) {
		c.Set("plugin.identity", hostIdentity{id: "perm-test", manifest: mf, run: &running{log: &pluginLog{}}})
	})
	r.POST("/fs/write", m.permission("fs.write"), func(c *gin.Context) { c.Status(http.StatusOK) })
	r.POST("/exec", m.permission("exec.run"), func(c *gin.Context) { c.Status(http.StatusOK) })

	rec := httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/fs/write", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "写入和删除文件") || !strings.Contains(rec.Body.String(), "fs.write") {
		t.Fatalf("expected explicit disabled message, got %d %s", rec.Code, rec.Body.String())
	}

	rec = httptest.NewRecorder()
	r.ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/exec", nil))
	if rec.Code != http.StatusForbidden || !strings.Contains(rec.Body.String(), "执行特权命令") || !strings.Contains(rec.Body.String(), "exec.run") {
		t.Fatalf("expected explicit undeclared message, got %d %s", rec.Code, rec.Body.String())
	}

	// 验证禁用权限下禁止新增授权
	if err := validateGrantPermissions(mf, grant, capabilityGrant{AllResources: []string{"sites"}}); err == nil {
		t.Fatal("expected error when sites.read is disabled")
	}
	if err := validateGrantPermissions(mf, grant, capabilityGrant{Credentials: map[string]string{"api-key": "cred-1"}}); err == nil {
		t.Fatal("expected error when credentials.read is disabled")
	}
	if err := validateGrantPermissions(mf, grant, capabilityGrant{ChannelID: "ch-1"}); err == nil {
		t.Fatal("expected error when notifications.send is disabled")
	}
	if err := validateGrantPermissions(mf, grant, capabilityGrant{Events: []string{"site.backup.finished"}}); err == nil {
		t.Fatal("expected error when events.subscribe is disabled")
	}
	if err := validateGrantPermissions(mf, grant, capabilityGrant{Schedules: map[string]string{"nightly": "0 8 * * *"}}); err == nil {
		t.Fatal("expected error when jobs.run is disabled")
	}

	// 验证升级时保留已有开关，并自动关闭新增高风险权限
	nextMF := Manifest{
		ID:          "perm-test",
		Permissions: []string{"sites.read", "backups.restore"},
	}
	if err := m.reconcileGrants(db, "perm-test", mf, nextMF); err != nil {
		t.Fatal(err)
	}
	updated, err := m.grant("perm-test")
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.DisabledPermissions) != 2 || updated.DisabledPermissions[0] != "backups.restore" || updated.DisabledPermissions[1] != "sites.read" {
		t.Fatalf("unexpected DisabledPermissions after upgrade: %+v", updated.DisabledPermissions)
	}
}
