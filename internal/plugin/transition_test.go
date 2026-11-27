package plugin

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
)

func TestStartupWaitsForDependencyAndResumes(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	cfg := config.Config{InstallDir: t.TempDir()}
	tasks, err := task.New(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	m, err := New(db, cfg, tasks, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	const dependency = "base-plugin"
	const plugin = "linked-plugin"
	for _, row := range []store.Plugin{
		{ID: dependency, Enabled: false, Version: "1.0.0", Type: "static", Manifest: `{"id":"base-plugin"}`, Config: `{}`},
		{ID: plugin, Enabled: true, Version: "1.0.0", Type: "static", Manifest: `{"id":"linked-plugin","dependencies":{"base-plugin":"1.0.0"}}`, Config: `{}`},
	} {
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := m.Start(Services{}); err != nil {
		t.Fatal(err)
	}
	waitFor := func(status string) {
		t.Helper()
		for deadline := time.Now().Add(2 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			if m.statusByID(plugin) == status {
				return
			}
		}
		t.Fatalf("plugin status %s, want %s", m.statusByID(plugin), status)
	}
	waitFor("blocked")
	if !strings.Contains(m.startupError(plugin), dependency) {
		t.Fatal("blocked dependency was not identified")
	}
	router := gin.New()
	m.Routes(router.Group(""))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/api/plugins/"+dependency+"/enable", nil))
	if w.Code != http.StatusOK {
		t.Fatalf("could not start dependency: %s", w.Body.String())
	}
	waitFor("running")
	if m.startupError(plugin) != "" {
		t.Fatal("old dependency error is still visible")
	}
}

func TestUpgradeGrantReconciliation(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	const id = "test-plugin"
	if err := db.Create(&store.Plugin{ID: id}).Error; err != nil {
		t.Fatal(err)
	}
	before := Manifest{Permissions: []string{"sites.read", "apps.control"}, Events: []string{"site.project.finished"}, Jobs: []Job{{ID: "task"}}}
	after := Manifest{Permissions: []string{"sites.read", "sites.control", "apps.control"}, Jobs: []Job{{ID: "task", Steps: []string{"run"}}}}
	grant := capabilityGrant{AllResources: []string{"sites"}, AppIDs: []string{"mysql"}, Schedules: map[string]string{"task": "0 8 * * *"}, Events: before.Events}
	data, _ := json.Marshal(grant)
	if err := db.Create(&store.Setting{Key: grantPrefix + id, Value: string(data)}).Error; err != nil {
		t.Fatal(err)
	}
	event := eventDelivery{ID: "pending", PluginID: id, EventName: "site.project.finished", Payload: `{"name":"site.project.finished","siteId":"site-one"}`, Status: "pending"}
	if err := db.Create(&event).Error; err != nil {
		t.Fatal(err)
	}
	m := &Module{db: db}
	if changes, err := m.grantPreview(id, before, after); err != nil || len(changes) != 3 {
		t.Fatalf("preview: %v (%v)", changes, err)
	}
	if err := m.reconcileGrants(db, id, before, after); err != nil {
		t.Fatal(err)
	}
	if err := m.reconcileGrants(db, id, after, before); err != nil {
		t.Fatal(err)
	}
	current, err := m.grant(id)
	if err != nil || len(current.AllResources)+len(current.Events)+len(current.Schedules) != 0 || !slices.Equal(current.AppIDs, grant.AppIDs) {
		t.Fatalf("unexpected grants after upgrade and rollback: %+v (%v)", current, err)
	}
	if err := db.Take(&event, "id = ?", event.ID).Error; err != nil || event.Status != "skipped" {
		t.Fatalf("revoked delivery status = %q (%v)", event.Status, err)
	}
}

func TestPendingEventsSelectOnePerAvailablePlugin(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	for _, id := range []string{"slow-plugin", "fast-plugin", "boot-plugin"} {
		if err := db.Create(&store.Plugin{ID: id}).Error; err != nil {
			t.Fatal(err)
		}
	}
	now := time.Now().UTC()
	rows := []eventDelivery{
		{ID: "slow-1", PluginID: "slow-plugin", Status: "pending", NextAt: now.Add(-2 * time.Minute)},
		{ID: "slow-2", PluginID: "slow-plugin", Status: "pending", NextAt: now.Add(-time.Minute)},
		{ID: "fast-1", PluginID: "fast-plugin", Status: "pending", NextAt: now},
		{ID: "boot-1", PluginID: "boot-plugin", Status: "pending", NextAt: now},
	}
	if err := db.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	m := &Module{db: db, statuses: map[string]string{"boot-plugin": "waiting"}}
	got := m.pendingEvents(nil, 4)
	if len(got) != 2 || got[0].ID != "slow-1" || got[1].ID != "fast-1" {
		t.Fatalf("unexpected initial queue selection: %+v", got)
	}
	got = m.pendingEvents(map[string]bool{"slow-plugin": true}, 4)
	if len(got) != 1 || got[0].ID != "fast-1" {
		t.Fatalf("an in-flight plugin delayed another plugin: %+v", got)
	}
}
