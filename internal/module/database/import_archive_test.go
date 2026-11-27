package database

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"io"
	"strings"
	"testing"
)

type bz2TestWriter struct {
	dst  io.Writer
	data []byte
}

func (w *bz2TestWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *bz2TestWriter) Close() error {
	_, err := w.dst.Write(w.data)
	return err
}

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
	bzip2SQLZip := func() []byte {
		var archive bytes.Buffer
		w := zip.NewWriter(&archive)
		// bzip2 compressed bytes of "CREATE TABLE example (id INT);\n"
		bzip2Payload := []byte{
			0x42, 0x5a, 0x68, 0x39, 0x31, 0x41, 0x59, 0x26, 0x53, 0x59, 0xca, 0x1e, 0xfe, 0xe5, 0x00, 0x00,
			0x05, 0xdf, 0x80, 0x00, 0x10, 0x40, 0x60, 0x00, 0x08, 0x3a, 0x25, 0x14, 0x00, 0x26, 0x26, 0x40,
			0x40, 0x20, 0x00, 0x22, 0x83, 0x43, 0x20, 0xc4, 0x32, 0x14, 0x68, 0xc8, 0x1a, 0x34, 0xc8, 0xd0,
			0x15, 0x71, 0x0a, 0xa0, 0xca, 0x84, 0x34, 0xda, 0x5f, 0x46, 0x68, 0x04, 0xfd, 0xf0, 0x75, 0xd9,
			0xdb, 0xe2, 0xee, 0x48, 0xa7, 0x0a, 0x12, 0x19, 0x43, 0xdf, 0xdc, 0xa0,
		}
		w.RegisterCompressor(12, func(out io.Writer) (io.WriteCloser, error) {
			return &bz2TestWriter{dst: out, data: bzip2Payload}, nil
		})
		f, err := w.CreateHeader(&zip.FileHeader{Name: "example.sql", Method: 12})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := f.Write([]byte(sql)); err != nil {
			t.Fatal(err)
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
		{"backup-bzip2.zip", bzip2SQLZip(), 4096, sql},
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
