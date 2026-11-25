package database

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestImportFilesIncludeOnlyExistingLocalBackupsForSelectedDatabase(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	root := t.TempDir()
	cfg := config.Config{InstallDir: filepath.Join(root, "panel"), BackupRoot: filepath.Join(root, "backups")}
	if err := os.Mkdir(cfg.BackupRoot, 0700); err != nil {
		t.Fatal(err)
	}
	server, other := "server", "other"
	for _, id := range []string{server, other} {
		if err := db.Create(&store.DBServer{ID: id, Type: "mysql"}).Error; err != nil {
			t.Fatal(err)
		}
	}
	for _, entry := range []struct {
		id, storage, database string
		server                *string
	}{
		{"local", "local", "demo", &server},
		{"remote", "remote", "demo", &server},
		{"missing", "local", "demo", &server},
		{"other-db", "local", "different", &server},
		{"other-server", "local", "demo", &other},
		{"trash", "local", "demo", &server},
	} {
		filename := filepath.Join(cfg.BackupRoot, entry.id+".sql.gz")
		if entry.id != "missing" {
			if err := os.WriteFile(filename, []byte("archive"), 0600); err != nil {
				t.Fatal(err)
			}
		}
		row := store.DBBackup{
			ID: entry.id, ServerID: entry.server, Database: entry.database,
			FilePath: filename, BackupFile: store.BackupFile{StorageType: entry.storage},
		}
		if err := db.Create(&row).Error; err != nil {
			t.Fatal(err)
		}
	}
	if err := db.Create(&store.DatabaseTrash{ID: "deleted", BackupID: "trash", State: "recycled"}).Error; err != nil {
		t.Fatal(err)
	}
	m := &Module{db: db, cfg: cfg}
	r := gin.New()
	m.Routes(r.Group(""))
	const endpoint = "/api/db/servers/server/databases/demo/import-files"
	type item struct{ Name, BackupID string }
	get := func(query string, total int, want item) {
		t.Helper()
		w := httptest.NewRecorder()
		r.ServeHTTP(w, httptest.NewRequest(http.MethodGet, endpoint+query, nil))
		var response struct {
			Data struct {
				Total int
				List  []item
			}
		}
		if w.Code != http.StatusOK || json.Unmarshal(w.Body.Bytes(), &response) != nil ||
			response.Data.Total != total || len(response.Data.List) != 1 || response.Data.List[0] != want {
			t.Fatalf("list %s = %d %s", query, w.Code, w.Body.String())
		}
	}
	local := item{Name: "local.sql.gz", BackupID: "local"}
	get("", 1, local)
	for _, id := range []string{"remote", "missing", "other-db", "other-server", "trash"} {
		f, _, err := m.openImportSource(t.Context(), server, "demo", "", id)
		if err == nil {
			f.Close()
			t.Fatalf("restricted backup %q opened for import", id)
		}
	}

	if err := os.Chtimes(filepath.Join(cfg.BackupRoot, local.Name), time.Unix(1, 0), time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := m.stageImportFile(t.Context(), server, "demo", local.Name, strings.NewReader("staged")); err != nil {
		t.Fatal(err)
	}
	get("?page=1&page_size=1", 2, item{Name: local.Name})
	get("?page=2&page_size=1&keyword=LOCAL", 2, local)
	f, name, err := m.openImportSource(t.Context(), server, "demo", "", local.BackupID)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	data, err := io.ReadAll(f)
	if err != nil || name != local.Name || string(data) != "archive" {
		t.Fatalf("registered backup source = %q, %q, %v", name, data, err)
	}
}
