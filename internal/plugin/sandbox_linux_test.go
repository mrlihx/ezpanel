package plugin

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/executor"
	"ezpanel/internal/store"
)

func TestSandboxSystemd(t *testing.T) {
	if os.Getenv("EZPANEL_TEST_SYSTEMD") != "1" || os.Geteuid() != 0 {
		t.Skip("需要显式启用的 Linux systemd 测试主机")
	}
	root, err := os.MkdirTemp("/var/lib", "ezpanel-plugin-test-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(root)
	if err := os.Chmod(root, 0755); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m := Module{db: db, cfg: config.Config{InstallDir: root}}
	if err := os.MkdirAll(m.staging(), 0700); err != nil {
		t.Fatal(err)
	}
	id := "fixture-" + store.ID()[:8]
	uid, err := m.sandboxUID(id)
	if err != nil {
		t.Fatal(err)
	}
	sameUID, err := m.sandboxUID(id)
	if err != nil || sameUID != uid {
		t.Fatalf("sandbox identity was not persisted: %d, %v", sameUID, err)
	}
	data := filepath.Join(root, "data")
	if err := preparePluginData(data, uid, uid); err != nil {
		t.Fatal(err)
	}
	entry := filepath.Join(root, "fixture")
	other := filepath.Join(root, "outside")
	if err := os.Mkdir(other, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(other, 0777); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(entry, []byte("#!/bin/sh\n[ \"$SAMPLE\" = \"test value\" ] || exit 2\nif ( printf test > ../outside/probe ) 2>/dev/null; then exit 3; fi\nprintf ready > ready\nexec /usr/bin/sleep 25\n"), 0755); err != nil {
		t.Fatal(err)
	}
	var logs bytes.Buffer
	options, cleanup, err := m.sandboxOptions(id, 28123, Sandbox{CPUPercent: 50, MemoryMiB: 64, MaxProcesses: 20}, executor.ProcessOptions{
		Path: entry, Dir: data, Env: []string{"SAMPLE=test value"}, UID: uint32(uid), GID: uint32(uid), Output: &logs,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	p, err := executor.StartProcess(options)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Stop()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		if data, err := os.ReadFile(filepath.Join(data, "ready")); err == nil && string(data) == "ready" {
			break
		}
		select {
		case <-p.Done():
			t.Fatal("sandbox service exited before the fixture was ready:", logs.String())
		case <-ctx.Done():
			t.Fatal("sandbox service startup timed out")
		case <-time.After(50 * time.Millisecond):
		}
	}
	info, err := os.Stat(filepath.Join(data, "ready"))
	if err != nil || info.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
		t.Fatalf("fixture did not run as the plugin UID: %v", err)
	}
	result, err := executor.Run(ctx, 5*time.Second, "systemctl", "show", options.Service,
		"-p", "MemoryMax", "-p", "TasksMax", "-p", "CPUQuotaPerSecUSec", "-p", "NoNewPrivileges")
	if err != nil {
		t.Fatal(err, result.Stderr)
	}
	for _, want := range []string{"MemoryMax=67108864", "TasksMax=20", "CPUQuotaPerSecUSec=500ms", "NoNewPrivileges=yes"} {
		if !strings.Contains(result.Stdout, want) {
			t.Errorf("missing %s in %s", want, result.Stdout)
		}
	}
}
