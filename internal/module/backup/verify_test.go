package backup

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
)

func TestVerifySiteBackupChecksScopeAndArchiveChecksum(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	site := store.Website{ID: store.ID(), Name: "test site"}
	if err := db.Create(&site).Error; err != nil {
		t.Fatal(err)
	}
	var data bytes.Buffer
	gz := gzip.NewWriter(&data)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "index.html", Mode: 0644, Size: 4}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("site")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(t.TempDir(), "backup.tar.gz")
	if err := os.WriteFile(name, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	row := store.SiteBackup{ID: store.ID(), WebsiteID: &site.ID, SiteName: site.Name, FilePath: name, Size: int64(data.Len())}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	m := New(db, config.Config{}, nil, nil, nil)
	if _, err := m.VerifySiteBackup(t.Context(), "other-site", row.ID); err == nil {
		t.Fatal("unrelated site must be rejected")
	}
	digest, err := m.VerifySiteBackup(t.Context(), site.ID, row.ID)
	if err != nil || digest != fmt.Sprintf("%x", sha256.Sum256(data.Bytes())) {
		t.Fatalf("invalid digest %s: %v", digest, err)
	}
	data.Bytes()[data.Len()-8] ^= 1
	if err := os.WriteFile(name, data.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := m.VerifySiteBackup(t.Context(), site.ID, row.ID); err == nil {
		t.Fatal("corrupted gzip checksum was accepted")
	}
}
