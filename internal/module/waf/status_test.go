package waf

import (
	"context"
	"errors"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
)

func TestPluginStatusRequiresRunningWAFEngine(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Create(&wafConfigRow{ID: 1, Data: `{"enabled":true}`}).Error; err != nil {
		t.Fatal(err)
	}
	packages := pkgmgr.New()
	packages.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	packages.Run = func(context.Context, time.Duration, []string, string, ...string) (executor.Result, error) {
		return executor.Result{}, errors.New("docker unavailable")
	}
	m := &Module{db: db, cfg: config.Config{InstallDir: t.TempDir()}, packages: packages}
	if active, err := m.PluginStatus(t.Context()); err != nil || active {
		t.Fatalf("inactive engine reported as enabled: %v, %v", active, err)
	}
}
