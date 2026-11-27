package plugin

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/pathsafe"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestPluginFileStreamStaysInGrantedRoot(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	panel, allowed, other := filepath.Join(base, "panel"), filepath.Join(base, "allowed"), filepath.Join(base, "other")
	for _, dir := range []string{panel, allowed, other} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := pathsafe.New(panel)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m := &Module{db: db, policy: policy}
	g := gin.New()
	g.Use(func(c *gin.Context) {
		c.Set("plugin.identity", hostIdentity{id: "file-test", run: &running{log: &pluginLog{}}, manifest: Manifest{FSRoots: []string{allowed}}})
	})
	g.GET("/stream", m.fsOpen)
	g.PUT("/stream", m.fsUpload)
	call := func(method, name string, body []byte, want int) []byte {
		t.Helper()
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(method, "/stream?path="+url.QueryEscape(name), bytes.NewReader(body)))
		if w.Code != want {
			t.Fatalf("%s returned %d: %s", method, w.Code, w.Body.String())
		}
		return w.Body.Bytes()
	}
	data := []byte{0, 255, 17, 13}
	inside := filepath.Join(allowed, "binary")
	call(http.MethodPut, inside, data, 200)
	if got := call(http.MethodGet, inside, nil, 200); !bytes.Equal(got, data) {
		t.Fatalf("binary transfer returned %v", got)
	}
	outside := filepath.Join(other, "private")
	if err := os.WriteFile(outside, []byte("test file"), 0600); err != nil {
		t.Fatal(err)
	}
	boundary := filepath.Join(allowed, "shortcut")
	if err := os.Symlink(outside, boundary); err != nil {
		t.Fatal(err)
	}
	call(http.MethodGet, boundary, nil, 403)
	call(http.MethodPut, boundary, data, 403)
}

func TestPluginFileStreamOffsetAndLimit(t *testing.T) {
	base, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	panel, allowed := filepath.Join(base, "panel"), filepath.Join(base, "allowed")
	for _, dir := range []string{panel, allowed} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	policy, err := pathsafe.New(panel)
	if err != nil {
		t.Fatal(err)
	}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	m := &Module{db: db, policy: policy}
	g := gin.New()
	g.Use(func(c *gin.Context) {
		c.Set("plugin.identity", hostIdentity{id: "file-test", run: &running{log: &pluginLog{}}, manifest: Manifest{FSRoots: []string{allowed}}})
	})
	g.GET("/stream", m.fsOpen)

	logPath := filepath.Join(allowed, "access.log")
	if err := os.WriteFile(logPath, []byte("line1\nline2\nline3\n"), 0600); err != nil {
		t.Fatal(err)
	}

	req := func(query string) *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		g.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/stream?path="+url.QueryEscape(logPath)+query, nil))
		return w
	}

	// 1. 分段增量读取：offset=0, limit=6
	w := req("&offset=0&limit=6")
	if w.Code != 200 || w.Body.String() != "line1\n" || w.Header().Get("X-Next-Offset") != "6" || w.Header().Get("X-File-Size") != "18" || w.Header().Get("X-File-Inode") == "" {
		t.Fatalf("first chunk failed: code=%d body=%q headers=%v", w.Code, w.Body.String(), w.Header())
	}

	// 2. 续读剩余部分：offset=6
	w = req("&offset=6")
	if w.Code != 200 || w.Body.String() != "line2\nline3\n" || w.Header().Get("X-Next-Offset") != "18" {
		t.Fatalf("second chunk failed: code=%d body=%q next=%s", w.Code, w.Body.String(), w.Header().Get("X-Next-Offset"))
	}

	// 3. offset >= size（如文件末尾或日志轮转截断后）返回空体与当前文件大小
	w = req("&offset=100")
	if w.Code != 200 || w.Body.Len() != 0 || w.Header().Get("X-Next-Offset") != "18" || w.Header().Get("X-File-Size") != "18" {
		t.Fatalf("eof/truncated chunk failed: code=%d len=%d next=%s", w.Code, w.Body.Len(), w.Header().Get("X-Next-Offset"))
	}

	// 4. 非法 offset / limit 返回 400
	if w := req("&offset=-1"); w.Code != 400 {
		t.Fatalf("expected 400 for negative offset, got %d", w.Code)
	}
	if w := req("&limit=abc"); w.Code != 400 {
		t.Fatalf("expected 400 for invalid limit, got %d", w.Code)
	}
}
