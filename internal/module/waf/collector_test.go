package waf

import (
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/config"
)

func TestCollectorReadsCurrentLogAfterRotatedLogError(t *testing.T) {
	cfg := config.Config{InstallDir: t.TempDir()}
	m, err := New(nil, cfg, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(m.Close)
	path := m.wafEventLog()
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("moved.jsonl", path+".1"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("{\"ip\":\"192.0.2.1\",\"action\":\"observe\"}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.collectOnce(t.Context()); err == nil {
		t.Fatal("expected the unreadable rotated log to be reported")
	}
	var count int64
	if err := m.wafDB.Model(&Event{}).Count(&count).Error; err != nil || count != 1 {
		t.Fatalf("collected %d current events, error %v", count, err)
	}
}
