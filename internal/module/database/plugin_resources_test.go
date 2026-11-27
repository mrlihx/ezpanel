package database

import (
	"context"
	"errors"
	"testing"
	"time"

	"ezpanel/internal/executor"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/store"
)

func TestPluginDatabaseResourcesKeepsRemoteWhenDockerUnavailable(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = sqlDB.Close() })
	if err := db.Create(&store.DBServer{ID: "remote", Name: "Remote MySQL", Type: "mysql"}).Error; err != nil {
		t.Fatal(err)
	}
	packages := pkgmgr.New()
	packages.LookPath = func(string) (string, error) { return "/usr/bin/docker", nil }
	packages.Run = func(context.Context, time.Duration, []string, string, ...string) (executor.Result, error) {
		return executor.Result{}, errors.New("docker unavailable")
	}
	m := &Module{db: db, packages: packages}
	rows, err := m.PluginResources(t.Context())
	if err != nil || len(rows) != 1 || rows[0].ID != "remote" {
		t.Fatalf("remote database unavailable in plugin resources: %v, %v", rows, err)
	}
}
