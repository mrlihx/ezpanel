package plugin

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
)

func TestRestoreKeepsOperationUntilCommitted(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m, err := New(db, config.Config{InstallDir: t.TempDir()}, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	const id = "test-plugin"
	const file = "state"
	put := func(dir, content string) {
		t.Helper()
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, file), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	check := func(dir, want string) {
		t.Helper()
		b, err := os.ReadFile(filepath.Join(dir, file))
		if err != nil || string(b) != want {
			t.Fatalf("%s: got %q, want %q: %v", dir, b, want, err)
		}
	}
	pkg, data := filepath.Join(m.packages(), id), filepath.Join(m.private(), id)
	put(pkg, "old package")
	put(data, "old data")
	p := store.Plugin{ID: id, Version: "1.0.0", Manifest: "{}", Config: "{}"}
	if err := db.Create(&p).Error; err != nil {
		t.Fatal(err)
	}
	history := filepath.Join(m.packages(), ".backup", id)
	if err := m.snapshot(p, history); err != nil {
		t.Fatal(err)
	}
	put(pkg, "current package")
	put(data, "current data")
	p.Version = "2.0.0"
	if err := db.Save(&p).Error; err != nil {
		t.Fatal(err)
	}
	rescue := filepath.Join(m.packages(), ".backup", ".rollback", id)
	if err := m.snapshot(p, rescue); err != nil {
		t.Fatal(err)
	}
	if err := m.record(&p, operation{Kind: "upgrade", Phase: "replacing", Snapshot: rescue}); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(filepath.Join(history, "package"), filepath.Join(history, "missing")); err != nil {
		t.Fatal(err)
	}
	if err := m.restore(id, history, p.Operation); err == nil {
		t.Fatal("incomplete snapshot was accepted")
	}
	check(pkg, "current package")
	check(data, "current data")
	if err := os.Rename(filepath.Join(history, "missing"), filepath.Join(history, "package")); err != nil {
		t.Fatal(err)
	}
	if err := m.restore(id, history, p.Operation); err != nil {
		t.Fatal(err)
	}
	var row store.Plugin
	if err := db.Take(&row, "id = ?", id).Error; err != nil || row.Version != "1.0.0" || row.Operation == nil {
		t.Fatalf("the rollback record ended too early: version=%s, err=%v", row.Version, err)
	}
	if err := m.recoverOperation(row); err != nil {
		t.Fatal(err)
	}
	check(pkg, "current package")
	check(data, "current data")
	row = store.Plugin{}
	if err := db.Take(&row, "id = ?", id).Error; err != nil || row.Version != "2.0.0" || row.Operation != nil {
		t.Fatalf("interrupted rollback was not recovered: version=%s, err=%v", row.Version, err)
	}
}

func TestCleanUploadsSkipsOngoingWork(t *testing.T) {
	m := &Module{cfg: config.Config{InstallDir: t.TempDir()}}
	active := filepath.Join(m.activeUploads(), "active-upload")
	expired := filepath.Join(m.staging(), "expired-upload")
	old := time.Now().Add(-time.Hour)
	for _, dir := range []string{active, m.activeUploads(), expired} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chtimes(dir, old, old); err != nil {
			t.Fatal(err)
		}
	}
	if err := m.cleanUploads(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(active); err != nil {
		t.Fatalf("active upload was removed: %v", err)
	}
	if _, err := os.Stat(expired); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expired upload was retained: %v", err)
	}
}

func TestStorageBodyCloseReleasesOneSlot(t *testing.T) {
	releases := 0
	body := &storageBody{ReadCloser: io.NopCloser(strings.NewReader("")), finish: func() { releases++ }}
	_ = body.Close()
	_ = body.Close()
	if releases != 1 {
		t.Fatalf("released %d slots, want one", releases)
	}
}
