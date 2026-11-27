package main

import (
	"net"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/server"
	"ezpanel/internal/store"
)

func TestUninitializedDoesNotCreateConfig(t *testing.T) {
	dir := t.TempDir()
	if run([]string{"server", "--dev", "--install-dir", dir}) == nil {
		t.Fatal("uninitialized started")
	}
	if _, e := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(e) {
		t.Fatal("created data without initialization")
	}
}
func TestInitRequiresInteractiveTerminal(t *testing.T) {
	if terminal() {
		t.Skip("test requires redirected stdin")
	}
	dir := t.TempDir()
	if initialize(dir, true) == nil {
		t.Fatal("noninteractive init")
	}
	if _, e := os.Stat(filepath.Join(dir, "data")); !os.IsNotExist(e) {
		t.Fatal("init wrote config")
	}
}
func TestCLIRejectsUnknownCommand(t *testing.T) {
	if run([]string{"delete-system", "--dev"}) == nil {
		t.Fatal("unknown command")
	}
}

func TestReadyCheckWithUserAgentRestriction(t *testing.T) {
	cfg, err := config.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(cfg.DataDir(), 0700); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(filepath.Join(cfg.DataDir(), "panel.db"))
	if err != nil {
		t.Fatal(err)
	}
	sqlDB, _ := db.DB()
	defer sqlDB.Close()
	probe := httptest.NewUnstartedServer(nil)
	defer probe.Close()
	_, port, err := net.SplitHostPort(probe.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	cfg.Port, _ = strconv.Atoi(port)
	a, err := auth.New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	policy := auth.SecurityPolicy{UserAgentEnabled: true, UserAgentAllow: "PanelBrowser", UnauthStatus: 404}
	if err := a.SaveSecurityPolicy(t.Context(), "Example PanelBrowser/1.0", policy); err != nil {
		t.Fatal(err)
	}
	router, err := server.New(server.Options{Auth: a})
	if err != nil {
		t.Fatal(err)
	}
	probe.Config.Handler = router
	probe.Start()
	if err := readyCheck(cfg); err != nil {
		t.Fatal(err)
	}
}
