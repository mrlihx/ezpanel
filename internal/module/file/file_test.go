package file

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"ezpanel/internal/config"
	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"fmt"
	"github.com/gin-gonic/gin"
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
