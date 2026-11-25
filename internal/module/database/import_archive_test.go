package database

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"strings"
	"testing"
)

func TestReadImportSQL(t *testing.T) {
	const sql = "CREATE TABLE example (id INT);\n"
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	var tarball bytes.Buffer
	gz = gzip.NewWriter(&tarball)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "dump/database.sql", Size: int64(len(sql)), Mode: 0600}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(sql)); err != nil {
		t.Fatal(err)
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	makeZip := func(names ...string) []byte {
		t.Helper()
		var archive bytes.Buffer
		w := zip.NewWriter(&archive)
		for _, name := range names {
			f, err := w.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.Write([]byte(sql)); err != nil {
				t.Fatal(err)
			}
		}
		if err := w.Close(); err != nil {
			t.Fatal(err)
		}
		return archive.Bytes()
	}
	for _, tc := range []struct {
		name  string
		data  []byte
		limit int64
		want  string
	}{
		{"backup.sql", []byte(sql), 4096, sql},
		{"backup.SQL.GZ", compressed.Bytes(), 4096, sql},
		{"backup.gz", compressed.Bytes(), 4096, sql},
		{"backup.tar.gz", tarball.Bytes(), 4096, sql},
		{"backup.tgz", tarball.Bytes(), 4096, sql},
		{"backup.zip", makeZip("data/example.sql", "README.txt"), 4096, sql},
		{"corrupt.sql.gz", compressed.Bytes()[:compressed.Len()-5], 4096, ""},
		{"large.sql.gz", compressed.Bytes(), 10, ""},
		{"large.tar.gz", tarball.Bytes(), 128, ""},
		{"ambiguous.zip", makeZip("a.sql", "b.sql"), 4096, ""},
		{"missing.zip", makeZip("README.txt"), 4096, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var dst strings.Builder
			err := readImportSQL(context.Background(), &dst, bytes.NewReader(tc.data), int64(len(tc.data)), tc.name, tc.limit, 10)
			if tc.want == "" {
				if err == nil {
					t.Fatal("expected archive validation to fail")
				}
				return
			}
			if err != nil || dst.String() != tc.want {
				t.Fatalf("SQL = %q, error = %v", dst.String(), err)
			}
		})
	}
}
