package software

import (
	"context"
	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestFRPServiceRoutesDoNotShadowCatalog(t *testing.T) {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(func(c *gin.Context) {
		c.String(200, "%s|%s|%s", c.FullPath(), c.Param("id"), c.Param("role"))
		c.Abort()
	})
	m := &Module{}
	m.Routes(router.Group(""))
	for _, route := range []struct{ method, path, pattern, id, role string }{
		{"GET", "/api/software/frp/sources", "/api/software/:id/sources", "frp", ""},
		{"GET", "/api/software/frp/versions", "/api/software/:id/versions", "frp", ""},
		{"GET", "/api/software/frp/settings", "/api/software/:id/settings", "frp", ""},
		{"PUT", "/api/software/frp/settings", "/api/software/:id/settings", "frp", ""},
		{"GET", "/api/software/frp/logs", "/api/software/:id/logs", "frp", ""},
		{"GET", "/api/software/frp/guard", "/api/software/:id/guard", "frp", ""},
		{"GET", "/api/software/frp/services", "/api/software/frp/services", "", ""},
		{"GET", "/api/software/frp/services/frps/config", "/api/software/frp/services/:role/config", "", "frps"},
		{"PUT", "/api/software/frp/services/frpc/config", "/api/software/frp/services/:role/config", "", "frpc"},
		{"POST", "/api/software/frp/services/frps/action", "/api/software/frp/services/:role/action", "", "frps"},
		{"GET", "/api/software/frp/services/frpc/log", "/api/software/frp/services/:role/log", "", "frpc"},
		{"DELETE", "/api/software/frp/services/frpc/log", "/api/software/frp/services/:role/log", "", "frpc"},
	} {
		t.Run(route.method+" "+route.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			router.ServeHTTP(recorder, httptest.NewRequest(route.method, route.path, nil))
			expected := route.pattern + "|" + route.id + "|" + route.role
			if recorder.Code != 200 || recorder.Body.String() != expected {
				t.Fatalf("route = %d %q, want %q", recorder.Code, recorder.Body.String(), expected)
			}
		})
	}
}

func TestFRPServiceControlDoesNotCreateTask(t *testing.T) {
	cfg := config.Config{InstallDir: t.TempDir()}
	db, err := store.Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	tasks, err := task.New(context.Background(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	packages := pkgmgr.New()
	packages.Configure(cfg)
	root := cfg.Layout().AppDir("frp")
	if err = os.MkdirAll(root, 0700); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(root, ".frps.json"), []byte(`{"version":"0.72.0"}`), 0600); err != nil {
		t.Fatal(err)
	}
	controlled := false
	packages.Run = func(_ context.Context, _ time.Duration, _ []string, _ string, args ...string) (executor.Result, error) {
		if strings.Join(args, " ") == "disable --now ezpanel-frps.service" {
			controlled = true
		}
		return executor.Result{}, nil
	}
	m := &Module{packages: packages, tasks: tasks}
	router := gin.New()
	router.Use(gin.Recovery())
	router.POST("/frp/:role/action", m.frpAction)
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest("POST", "/frp/frps/action", strings.NewReader(`{"action":"stop"}`)))
	if recorder.Code != 200 || !controlled || strings.Contains(recorder.Body.String(), "taskId") {
		t.Fatalf("control=%v response=%d %s", controlled, recorder.Code, recorder.Body.String())
	}
	var count int64
	if err = db.Model(&store.Task{}).Count(&count).Error; err != nil || count != 0 {
		t.Fatalf("tasks=%d err=%v", count, err)
	}
}
