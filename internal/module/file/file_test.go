package file

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"fmt"
	"github.com/gin-gonic/gin"
	"github.com/klauspost/compress/zstd"
	"github.com/ulikunitz/xz"
	securezip "github.com/yeka/zip"
	"gorm.io/gorm"
	"io"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestAtomicAndPathBoundary(t *testing.T) {
	root := t.TempDir()
	p, _ := pathsafe.New(filepath.Join(root, "panel"))
	m := &Module{policy: p}
	target := filepath.Join(root, "a")
	os.WriteFile(target, []byte("old"), 0640)
	if e := m.atomic(context.Background(), target, strings.NewReader("new"), true, 2, 0644); e == nil {
		t.Fatal("limit accepted")
	}
	b, _ := os.ReadFile(target)
	if string(b) != "old" {
		t.Fatal("old changed")
	}
	os.Symlink(target, filepath.Join(root, "link"))
	if e := m.atomic(context.Background(), filepath.Join(root, "link"), strings.NewReader("bad"), true, 10, 0644); e == nil {
		t.Fatal("link accepted")
	}
	if _, e := p.Check(filepath.Join(root, "panel", "secret"), true); e == nil {
		t.Fatal("panel accepted")
	}
}
func TestBatchAncestors(t *testing.T) {
	root := t.TempDir()
	p, _ := pathsafe.New(filepath.Join(root, "panel"))
	m := &Module{policy: p}
	os.Mkdir(filepath.Join(root, "a"), 0755)
	os.WriteFile(filepath.Join(root, "a", "b"), nil, 0644)
	if _, e := m.sources([]string{filepath.Join(root, "a"), filepath.Join(root, "a", "b")}, true); e == nil {
		t.Fatal("ancestor accepted")
	}
}
func TestArchiveEscapeAndCleanup(t *testing.T) {
	root := t.TempDir()
	p, _ := pathsafe.New(filepath.Join(root, "panel"))
	m := &Module{policy: p, cfg: config.Config{ExtractMaxBytes: 3, ExtractMaxEntries: 10}}
	dest := filepath.Join(root, "out")
	os.Mkdir(dest, 0755)
	os.WriteFile(filepath.Join(dest, "existing"), []byte("keep"), 0644)
	for _, tc := range []struct {
		name    string
		files   []string
		content string
	}{{"escape", []string{"safe", "../escape"}, "a"}, {"limit", []string{"safe", "large"}, "xx"}, {"existing", []string{"safe", "existing"}, "a"}} {
		t.Run(tc.name, func(t *testing.T) {
			archive := filepath.Join(root, tc.name+".zip")
			f, _ := os.Create(archive)
			z := zip.NewWriter(f)
			for _, n := range tc.files {
				w, _ := z.Create(n)
				io.WriteString(w, tc.content)
			}
			z.Close()
			f.Close()
			if e := m.extractArchive(context.Background(), archive, dest); e == nil {
				t.Fatal("accepted")
			}
			if _, e := os.Lstat(filepath.Join(dest, "safe")); !os.IsNotExist(e) {
				t.Fatal("failed cleanup")
			}
			b, _ := os.ReadFile(filepath.Join(dest, "existing"))
			if string(b) != "keep" {
				t.Fatal("existing changed")
			}
		})
	}
	outside := filepath.Join(root, "outside")
	os.Mkdir(outside, 0755)
	os.Symlink(outside, filepath.Join(dest, "link"))
	f, _ := os.Create(filepath.Join(root, "link.zip"))
	z := zip.NewWriter(f)
	w, _ := z.Create("link/escaped")
	io.WriteString(w, "x")
	z.Close()
	f.Close()
	if e := m.extractArchive(context.Background(), filepath.Join(root, "link.zip"), dest); e == nil {
		t.Fatal("existing symlink accepted")
	}
	if _, e := os.Stat(filepath.Join(outside, "escaped")); !os.IsNotExist(e) {
		t.Fatal("escaped")
	}
}

func TestArchiveRollbackPreservesOverwrittenFile(t *testing.T) {
	root := t.TempDir()
	policy, err := pathsafe.New(filepath.Join(root, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{policy: policy, cfg: config.Config{ExtractMaxBytes: 10, ExtractMaxEntries: 2}}
	dest := t.TempDir()
	existing := filepath.Join(dest, "existing")
	if err := os.WriteFile(existing, []byte("old"), 0600); err != nil {
		t.Fatal(err)
	}
	var archive bytes.Buffer
	zw := zip.NewWriter(&archive)
	for _, entry := range []struct{ name, body string }{{"existing", "new"}, {"added", "new"}, {"last", "new"}} {
		w, err := zw.Create(entry.name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, entry.body); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backup.zip")
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	if err := m.extractWithOptions(t.Context(), path, dest, extraction{Overwrite: true, UID: -1, GID: -1}); !errors.Is(err, errLimit) {
		t.Fatalf("expected an entry limit error, got %v", err)
	}
	got, err := os.ReadFile(existing)
	if err != nil || string(got) != "old" {
		t.Fatalf("original file changed: %q, %v", got, err)
	}
	entries, err := os.ReadDir(dest)
	if err != nil || len(entries) != 1 || entries[0].Name() != "existing" {
		t.Fatalf("unexpected contents after rollback: %v, %v", entries, err)
	}
}

func TestSevenZipListRejectsOutOfRangeSize(t *testing.T) {
	const listing = "Path = backup.txt\nSize = 18446744073709551615\n"
	if err := parseSevenZipList(strings.NewReader(listing), 10, 1024); !errors.Is(err, errLimit) {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestSevenZipListReadsBeyondCommandLogLimit(t *testing.T) {
	listing := strings.Repeat("Path = a.txt\nSize = 1\nMethod = LZMA2\n\n", 30000)
	if err := parseSevenZipList(strings.NewReader(listing), 30000, 30000); err != nil {
		t.Fatalf("valid large listing rejected: %v", err)
	}
	if err := parseSevenZipList(strings.NewReader(listing), 30000, 29000); !errors.Is(err, errLimit) {
		t.Fatalf("expected size limit error, got %v", err)
	}
}

func TestSiteBackupRestoresArchivePermissions(t *testing.T) {
	root := t.TempDir()
	policy, err := pathsafe.New(filepath.Join(root, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	m := &Module{policy: policy, cfg: config.Config{ExtractMaxBytes: 1024, ExtractMaxEntries: 10}}
	var archive bytes.Buffer
	gz := gzip.NewWriter(&archive)
	tw := tar.NewWriter(gz)
	if err := tw.WriteHeader(&tar.Header{Name: "private/", Mode: 0700, Typeflag: tar.TypeDir}); err != nil {
		t.Fatal(err)
	}
	if err := tw.WriteHeader(&tar.Header{Name: "private/config.ini", Mode: 0600, Size: 3}); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte("a=b")); err != nil {
		t.Fatal(err)
	}
	for _, header := range []*tar.Header{
		{Name: "sym", Typeflag: tar.TypeSymlink, Linkname: "private/config.ini"},
		{Name: "hard", Typeflag: tar.TypeLink, Linkname: "private/config.ini"},
	} {
		if err := tw.WriteHeader(header); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(tw.Close(), gz.Close()); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "backup.tar.gz")
	if err := os.WriteFile(path, archive.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	dest := t.TempDir()
	if err := m.ExtractSiteBackup(t.Context(), path, dest, "/www/site"); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(dest, "sym")); err != nil || got != "private/config.ini" {
		t.Fatalf("symbolic link = %q: %v", got, err)
	}
	a, err := os.Stat(filepath.Join(dest, "hard"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.Stat(filepath.Join(dest, "private/config.ini"))
	if err != nil || !os.SameFile(a, b) {
		t.Fatalf("missing hardlink relationship: %v", err)
	}
	for name, want := range map[string]os.FileMode{"private": 0700, "private/config.ini": 0600} {
		info, err := os.Stat(filepath.Join(dest, name))
		if err != nil {
			t.Fatal(err)
		}
		if got := info.Mode().Perm(); got != want {
			t.Errorf("%s restored with mode %o, want %o", name, got, want)
		}
	}
}
func TestListingOver5000(t *testing.T) {
	root := t.TempDir()
	p, _ := pathsafe.New(filepath.Join(root, "panel"))
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	defer sql.Close()
	m := &Module{policy: p, db: db}
	dir := filepath.Join(root, "files")
	os.Mkdir(dir, 0755)
	for i := 0; i < 5002; i++ {
		os.WriteFile(filepath.Join(dir, fmt.Sprintf("%05d", i)), nil, 0644)
	}
	r := gin.New()
	r.GET("/list", m.list)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/list?path="+url.QueryEscape(dir)+"&page=11&page_size=500", nil)
	r.ServeHTTP(w, req)
	var body struct {
		Data struct {
			Total int
			List  []Entry
		}
	}
	json.Unmarshal(w.Body.Bytes(), &body)
	if body.Data.Total != 5002 || len(body.Data.List) != 2 {
		t.Fatalf("%s", w.Body.String())
	}
}
func TestInvalidPathAndResidualErrors(t *testing.T) {
	p, _ := pathsafe.New(filepath.Join(t.TempDir(), "panel"))
	m := &Module{policy: p}
	r := gin.New()
	r.POST("/copy", m.transfer)
	w := httptest.NewRecorder()
	req := httptest.NewRequest("POST", "/copy", strings.NewReader(`{"paths":["relative"],"dest":"/tmp"}`))
	req.Header.Set("Content-Type", "application/json")
	r.ServeHTTP(w, req)
	if w.Code != 400 {
		t.Fatalf("invalid format status %d: %s", w.Code, w.Body.String())
	}
	code, msg := errorResult(errors.New("元数据写入失败，数据保留在回收站暂存区，需人工处理"))
	if code != 2001 || !strings.Contains(msg, "保留") {
		t.Fatal("residual explanation hidden")
	}
}
func fixture(t *testing.T) (*Module, string) {
	t.Helper()
	root := t.TempDir()
	cfg, e := config.New(filepath.Join(root, "panel"))
	if e != nil {
		t.Fatal(e)
	}
	os.MkdirAll(cfg.DataDir(), 0700)
	db, e := store.Open(filepath.Join(cfg.DataDir(), "test.db"))
	if e != nil {
		t.Fatal(e)
	}
	sql, _ := db.DB()
	t.Cleanup(func() { sql.Close() })
	manager, e := task.New(context.Background(), db, cfg)
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(manager.Close)
	m, e := New(db, cfg, manager, nil)
	if e != nil {
		t.Fatal(e)
	}
	return m, root
}
func TestTrashCrossDeviceAndMetadataFailure(t *testing.T) {
	t.Run("copy-failure-keeps-source", func(t *testing.T) {
		m, root := fixture(t)
		src := filepath.Join(root, "fifo")
		if e := syscall.Mkfifo(src, 0600); e != nil {
			t.Fatal(e)
		}
		m.rename = func(string, string) error { return syscall.EXDEV }
		if e := m.trashPut(context.Background(), src); e == nil {
			t.Fatal("special accepted")
		}
		if _, e := os.Lstat(src); e != nil {
			t.Fatal("source removed")
		}
		entries, _ := os.ReadDir(filepath.Join(m.cfg.Layout().TrashDir(), "files"))
		if len(entries) != 0 {
			t.Fatal("partial copy retained")
		}
	})
	t.Run("preserves-copy", func(t *testing.T) {
		m, root := fixture(t)
		src := filepath.Join(root, "a")
		os.Mkdir(src, 0750)
		os.WriteFile(filepath.Join(src, "file"), []byte("ok"), 0640)
		os.Symlink("file", filepath.Join(src, "link"))
		stamp := time.Unix(1600000000, 0)
		os.Chtimes(filepath.Join(src, "file"), stamp, stamp)
		m.rename = func(string, string) error { return syscall.EXDEV }
		if e := m.trashPut(context.Background(), src); e != nil {
			t.Fatal(e)
		}
		var row store.TrashItem
		m.db.First(&row)
		info, _ := os.Stat(filepath.Join(row.StoragePath, "file"))
		link, _ := os.Readlink(filepath.Join(row.StoragePath, "link"))
		if info.Mode().Perm() != 0640 || !info.ModTime().Equal(stamp) || link != "file" {
			t.Fatal("metadata/link lost")
		}
		if _, e := os.Lstat(src); !os.IsNotExist(e) {
			t.Fatal("source remains")
		}
	})
	t.Run("db-failure-retains-data", func(t *testing.T) {
		m, root := fixture(t)
		src := filepath.Join(root, "a")
		os.WriteFile(src, []byte("keep"), 0644)
		m.db.Callback().Create().Before("gorm:create").Register("fail-trash", func(tx *gorm.DB) {
			if tx.Statement.Table == "trash_items" {
				tx.AddError(errors.New("injected"))
			}
		})
		if e := m.trashPut(context.Background(), src); e == nil || !strings.Contains(e.Error(), "保留") {
			t.Fatal("missing residual explanation")
		}
		entries, _ := os.ReadDir(filepath.Join(m.cfg.Layout().TrashDir(), "files"))
		if len(entries) != 1 {
			t.Fatal("copy lost")
		}
		b, _ := os.ReadFile(filepath.Join(m.cfg.Layout().TrashDir(), "files", entries[0].Name()))
		if string(b) != "keep" {
			t.Fatal("data lost")
		}
	})
}
func TestBatchPartialAndProtection(t *testing.T) {
	m, root := fixture(t)
	src := filepath.Join(root, "src")
	dst := filepath.Join(root, "dest")
	os.Mkdir(src, 0755)
	os.Mkdir(dst, 0755)
	a, b := filepath.Join(src, "a"), filepath.Join(src, "b")
	os.WriteFile(a, []byte("a"), 0644)
	os.WriteFile(b, []byte("b"), 0644)
	os.WriteFile(filepath.Join(dst, "a"), []byte("old"), 0644)
	r := gin.New()
	r.POST("/copy", m.transfer)
	body, _ := json.Marshal(map[string]any{"paths": []string{a, b}, "dest": dst})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/copy", bytes.NewReader(body)))
	var accepted struct {
		Data struct {
			TaskID string `json:"taskId"`
		}
	}
	json.Unmarshal(w.Body.Bytes(), &accepted)
	if accepted.Data.TaskID == "" {
		t.Fatal(w.Body.String())
	}
	deadline := time.Now().Add(3 * time.Second)
	var row store.Task
	for time.Now().Before(deadline) {
		m.db.First(&row, "id = ?", accepted.Data.TaskID)
		if row.Status != "running" {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if row.Status != "failed" || row.Result == nil {
		t.Fatalf("%+v", row)
	}
	var result task.BatchResult
	json.Unmarshal([]byte(*row.Result), &result)
	if result.SuccessCount != 1 || result.FailedCount != 1 {
		t.Fatalf("%+v", result)
	}
	data, _ := os.ReadFile(filepath.Join(dst, "a"))
	if string(data) != "old" {
		t.Fatal("conflict overwritten")
	}
	body, _ = json.Marshal(map[string]any{"paths": []string{filepath.Dir(m.cfg.InstallDir)}, "dest": dst})
	w = httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest("POST", "/copy", bytes.NewReader(body)))
	if w.Code != 403 {
		t.Fatal(w.Body.String())
	}
}

type fixedCompressorWriter struct {
	dst  io.Writer
	data []byte
}

func (w *fixedCompressorWriter) Write(p []byte) (int, error) { return len(p), nil }
func (w *fixedCompressorWriter) Close() error {
	_, err := w.dst.Write(w.data)
	return err
}

func TestArchiveFormatsCompressionPasswordAndErrors(t *testing.T) {
	const sampleText = "hello bzip2 zip\n"
	bzip2Payload := []byte{
		0x42, 0x5a, 0x68, 0x39, 0x31, 0x41, 0x59, 0x26, 0x53, 0x59, 0x14, 0x21, 0x70, 0x16, 0x00, 0x00,
		0x03, 0x59, 0x80, 0x00, 0x10, 0x40, 0x00, 0x10, 0x00, 0x12, 0x64, 0xc0, 0x10, 0x20, 0x00, 0x21,
		0xa0, 0x06, 0x42, 0x01, 0xa6, 0x9a, 0x09, 0xa8, 0xad, 0xb4, 0x39, 0x04, 0x31, 0x78, 0xbb, 0x92,
		0x29, 0xc2, 0x84, 0x80, 0xa1, 0x0b, 0x80, 0xb0,
	}

	root := t.TempDir()
	p, _ := pathsafe.New(filepath.Join(root, "panel"))
	m := &Module{policy: p, cfg: config.Config{ExtractMaxBytes: 1 << 20, ExtractMaxEntries: 100}}

	writeAndVerifyExtract := func(t *testing.T, archivePath string, raw []byte, expectRel string, expectBody string) {
		t.Helper()
		if err := os.WriteFile(archivePath, raw, 0644); err != nil {
			t.Fatal(err)
		}
		dest := t.TempDir()
		if err := m.extractArchive(context.Background(), archivePath, dest); err != nil {
			t.Fatalf("extract %s failed: %v", archivePath, err)
		}
		got, err := os.ReadFile(filepath.Join(dest, expectRel))
		if err != nil {
			t.Fatalf("read extracted %s failed: %v", expectRel, err)
		}
		if string(got) != expectBody {
			t.Fatalf("extracted %s content = %q, want %q", expectRel, string(got), expectBody)
		}
	}

	t.Run("zip-bzip2", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zw.RegisterCompressor(zipMethodBZip2, func(w io.Writer) (io.WriteCloser, error) {
			return &fixedCompressorWriter{dst: w, data: bzip2Payload}, nil
		})
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "SDK/hello.txt", Method: zipMethodBZip2})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, sampleText); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		writeAndVerifyExtract(t, filepath.Join(root, "bzip2.zip"), buf.Bytes(), "SDK/hello.txt", sampleText)
	})

	t.Run("zip-zstd", func(t *testing.T) {
		var buf bytes.Buffer
		zw := zip.NewWriter(&buf)
		zw.RegisterCompressor(zipMethodZstd, func(w io.Writer) (io.WriteCloser, error) {
			return zstd.NewWriter(w)
		})
		w, err := zw.CreateHeader(&zip.FileHeader{Name: "zstd/hello.txt", Method: zipMethodZstd})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(w, "hello zstd zip\n"); err != nil {
			t.Fatal(err)
		}
		if err := zw.Close(); err != nil {
			t.Fatal(err)
		}
		writeAndVerifyExtract(t, filepath.Join(root, "zstd.zip"), buf.Bytes(), "zstd/hello.txt", "hello zstd zip\n")
	})

	makeTar := func(name, content string) []byte {
		var tb bytes.Buffer
		tw := tar.NewWriter(&tb)
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0644, Size: int64(len(content))}); err != nil {
			t.Fatal(err)
		}
		if _, err := io.WriteString(tw, content); err != nil {
			t.Fatal(err)
		}
		if err := tw.Close(); err != nil {
			t.Fatal(err)
		}
		return tb.Bytes()
	}

	wrapGzip := func(raw []byte) []byte {
		var b bytes.Buffer
		gz := gzip.NewWriter(&b)
		_, _ = gz.Write(raw)
		_ = gz.Close()
		return b.Bytes()
	}
	wrapXZ := func(raw []byte) []byte {
		var b bytes.Buffer
		xw, err := xz.NewWriter(&b)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = xw.Write(raw)
		_ = xw.Close()
		return b.Bytes()
	}
	wrapZstd := func(raw []byte) []byte {
		var b bytes.Buffer
		zw, err := zstd.NewWriter(&b)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = zw.Write(raw)
		_ = zw.Close()
		return b.Bytes()
	}

	tarRaw := makeTar("dir/file.txt", sampleText)
	t.Run("tar", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "a.tar"), tarRaw, "dir/file.txt", sampleText)
	})
	t.Run("tgz", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "a.tgz"), wrapGzip(tarRaw), "dir/file.txt", sampleText)
	})
	t.Run("tgz-checksum", func(t *testing.T) {
		// GNU tar pads the output after the archive end marker.
		padded := append(bytes.Clone(tarRaw), make([]byte, 8192)...)
		broken := wrapGzip(padded)
		broken[len(broken)-8] ^= 1
		archive := filepath.Join(root, "bad-checksum.tar.gz")
		if err := os.WriteFile(archive, broken, 0600); err != nil {
			t.Fatal(err)
		}
		if err := m.ExtractSiteBackup(t.Context(), archive, t.TempDir(), "/www/site"); !errors.Is(err, errArchiveChecksum) {
			t.Fatalf("expected an archive checksum error, got %v", err)
		}
	})
	t.Run("tar.xz", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "a.tar.xz"), wrapXZ(tarRaw), "dir/file.txt", sampleText)
	})
	t.Run("tar.zst", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "a.tar.zst"), wrapZstd(tarRaw), "dir/file.txt", sampleText)
	})
	t.Run("single-gz", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "dump.sql.gz"), wrapGzip([]byte(sampleText)), "dump.sql", sampleText)
	})
	t.Run("single-bz2", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "note.txt.bz2"), bzip2Payload, "note.txt", sampleText)
	})
	t.Run("single-xz", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "note.txt.xz"), wrapXZ([]byte(sampleText)), "note.txt", sampleText)
	})
	t.Run("single-zst", func(t *testing.T) {
		writeAndVerifyExtract(t, filepath.Join(root, "note.txt.zst"), wrapZstd([]byte(sampleText)), "note.txt", sampleText)
	})

	t.Run("zip-compress-extract-password", func(t *testing.T) {
		srcDir := filepath.Join(root, "secret_src")
		if err := os.MkdirAll(srcDir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "secret.txt"), []byte("top-secret"), 0644); err != nil {
			t.Fatal(err)
		}
		encZip := filepath.Join(root, "encrypted.zip")
		f, err := os.Create(encZip)
		if err != nil {
			t.Fatal(err)
		}
		if err := archiveWrite(context.Background(), f, []string{srcDir}, "zip", "secret123"); err != nil {
			f.Close()
			t.Fatal(err)
		}
		f.Close()

		if err := m.extractWithOptions(context.Background(), encZip, t.TempDir(), extraction{Encoding: "UTF-8", DirMode: 0755, UID: -1, GID: -1}); !errors.Is(err, errArchivePassword) {
			t.Fatalf("empty password err = %v, want %v", err, errArchivePassword)
		}
		if err := m.extractWithOptions(context.Background(), encZip, t.TempDir(), extraction{Password: "wrong", Encoding: "UTF-8", DirMode: 0755, UID: -1, GID: -1}); !errors.Is(err, errArchivePassword) {
			t.Fatalf("wrong password err = %v, want %v", err, errArchivePassword)
		}
		dest := t.TempDir()
		if err := m.extractWithOptions(context.Background(), encZip, dest, extraction{Password: "secret123", Encoding: "UTF-8", DirMode: 0755, UID: -1, GID: -1}); err != nil {
			t.Fatalf("valid password extract failed: %v", err)
		}
		got, err := os.ReadFile(filepath.Join(dest, "secret_src", "secret.txt"))
		if err != nil || string(got) != "top-secret" {
			t.Fatalf("got = %q, err = %v", string(got), err)
		}
	})

	t.Run("compress-targz-rejects-password", func(t *testing.T) {
		r := gin.New()
		r.POST("/compress", m.compress)
		body, _ := json.Marshal(map[string]any{
			"paths":    []string{root},
			"dest":     filepath.Join(root, "out.tar.gz"),
			"format":   "tar.gz",
			"password": "123",
		})
		w := httptest.NewRecorder()
		req := httptest.NewRequest("POST", "/compress", bytes.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		r.ServeHTTP(w, req)
		if w.Code != 400 {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
	})

	t.Run("error-mapping", func(t *testing.T) {
		for _, tc := range []struct {
			err  error
			want string
		}{
			{mapArchiveError(securezip.ErrAlgorithm), "压缩算法"},
			{mapArchiveError(securezip.ErrFormat), "格式无效"},
			{mapArchiveError(securezip.ErrChecksum), "校验失败"},
			{errArchiveEncoding, "编码无效"},
			{errArchiveEscape, "越界"},
			{errArchiveSpecial, "特殊文件"},
		} {
			code, msg := errorResult(tc.err)
			if code != 1001 || !strings.Contains(msg, tc.want) {
				t.Fatalf("errorResult(%v) = (%d, %q), want substring %q", tc.err, code, msg, tc.want)
			}
		}
	})
}
