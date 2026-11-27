package website

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
)

func TestRestoreSiteFilesPreservesOriginal(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	install, sites := filepath.Join(base, "panel"), filepath.Join(base, "sites")
	root := filepath.Join(sites, "sample")
	for _, dir := range []string{install, root} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "index.html"), []byte("old"), 0644); err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m, err := New(db, config.Config{InstallDir: install, SiteRoot: sites}, nil, nil, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	row := store.Website{ID: store.ID(), Name: "test site", Root: root, Type: "static", RootCreatedByPanel: true}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	read := func(dir, want string) {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(dir, "index.html"))
		if err != nil || string(data) != want {
			t.Fatalf("index.html = %q, want %q (%v)", data, want, err)
		}
	}
	restore := func(extract func(context.Context, string, string, string) error) (string, error) {
		return m.RestoreSiteFiles(t.Context(), row.ID, row.Name, strings.NewReader("archive bytes"), 13, extract)
	}
	if _, err := restore(func(context.Context, string, string, string) error { return errors.New("invalid archive") }); err == nil {
		t.Fatal("invalid archive accepted")
	}
	read(root, "old")
	old, err := restore(func(_ context.Context, archive, dir, _ string) error {
		data, err := os.ReadFile(archive)
		if err != nil || string(data) != "archive bytes" {
			t.Fatalf("archive = %q, %v", data, err)
		}
		return os.WriteFile(filepath.Join(dir, "index.html"), []byte("new"), 0644)
	})
	if err != nil {
		t.Fatal(err)
	}
	read(root, "new")
	read(old, "old")
}
