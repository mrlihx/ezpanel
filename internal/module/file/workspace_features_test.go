package file

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func fileFeatureRequest(router http.Handler, method, path, body string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	w := httptest.NewRecorder()
	router.ServeHTTP(w, r)
	return w
}

func TestResumableUploadContinuesAfterModuleRestart(t *testing.T) {
	m, root := fixture(t)
	r := gin.New()
	m.Routes(r.Group(""))
	root = filepath.Join(root, "destination")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	input, _ := json.Marshal(map[string]any{"dir": root, "path": "nested/large.bin", "size": 10, "fingerprint": "file-sample"})
	const endpoint = "/api/files/uploads"
	start := fileFeatureRequest(r, "POST", endpoint, string(input))
	var status struct {
		Data struct {
			ID     string
			Offset int64
		}
	}
	if e := json.Unmarshal(start.Body.Bytes(), &status); e != nil || status.Data.ID == "" {
		t.Fatalf("start: %d %s", start.Code, start.Body)
	}
	upload := endpoint + "/" + status.Data.ID
	if w := fileFeatureRequest(r, "PATCH", upload+"?offset=0", "first"); w.Code != 200 {
		t.Fatalf("first chunk: %d %s", w.Code, w.Body)
	}
	target := filepath.Join(root, "nested", "large.bin")
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("partial data became visible at %s", target)
	}
	restarted, err := New(m.db, m.cfg, m.tasks, nil)
	if err != nil {
		t.Fatal(err)
	}
	r = gin.New()
	restarted.Routes(r.Group(""))
	w := fileFeatureRequest(r, "POST", endpoint, string(input))
	if e := json.Unmarshal(w.Body.Bytes(), &status); e != nil || status.Data.Offset != 5 {
		t.Fatalf("resume: %d %s", w.Code, w.Body)
	}
	if w := fileFeatureRequest(r, "PATCH", upload+"?offset=0", "first"); w.Code != 409 {
		t.Fatalf("duplicate chunk was accepted: %d %s", w.Code, w.Body)
	}
	if w := fileFeatureRequest(r, "PATCH", upload+"?offset=5", "last!"); w.Code != 200 {
		t.Fatalf("last chunk: %d %s", w.Code, w.Body)
	}
	if err := os.WriteFile(target, []byte("existing"), 0640); err != nil {
		t.Fatal(err)
	}
	if w := fileFeatureRequest(r, "POST", upload+"/finish", ""); w.Code != 409 {
		t.Fatalf("finish overwrote a new target: %d %s", w.Code, w.Body)
	}
	if err := os.Remove(target); err != nil {
		t.Fatal(err)
	}
	if w := fileFeatureRequest(r, "POST", upload+"/finish", ""); w.Code != 200 {
		t.Fatalf("finish: %d %s", w.Code, w.Body)
	}
	b, err := os.ReadFile(target)
	if err != nil || string(b) != "firstlast!" {
		t.Fatalf("published data: %q %v", b, err)
	}
	info, err := os.Stat(target)
	if err != nil || info.Mode().Perm() != 0755 {
		t.Fatalf("published mode: %v %v", info, err)
	}
}

func TestPreviewStreamsRangeAndRejectsActiveContent(t *testing.T) {
	m, root := fixture(t)
	r := gin.New()
	m.Routes(r.Group(""))
	for name, body := range map[string]string{"document.pdf": "%PDF-1.7 example", "script.pdf": "<html><script></script></html>"} {
		path := filepath.Join(root, name)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		req := httptest.NewRequest("GET", "/api/files/preview?path="+url.QueryEscape(path), nil)
		req.Header.Set("Range", "bytes=0-4")
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		if name == "document.pdf" {
			if w.Code != 206 || w.Body.String() != "%PDF-" || w.Header().Get("Content-Type") != "application/pdf" {
				t.Fatalf("preview range: %d %s", w.Code, w.Body)
			}
		} else if w.Code != 403 {
			t.Fatalf("active content was allowed: %d %s", w.Code, w.Body)
		}
	}
}

func TestFileContentSearchDoesNotFollowSymlinks(t *testing.T) {
	m, root := fixture(t)
	r := gin.New()
	m.Routes(r.Group(""))
	dir := filepath.Join(root, "search")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	for path, data := range map[string]string{
		filepath.Join(dir, "match.txt"):   "first\nfind . this\n",
		filepath.Join(dir, "other.txt"):   "find x this\n",
		filepath.Join(dir, "binary"):      "\x00find . this\n",
		filepath.Join(root, "secret.txt"): "find . this\n",
	} {
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Symlink(filepath.Join(root, "secret.txt"), filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}
	w := fileFeatureRequest(r, "GET", "/api/files/content-search?path="+url.QueryEscape(dir)+"&keyword="+url.QueryEscape("find . this"), "")
	var result struct {
		Data struct {
			List []struct {
				Name  string
				Match struct {
					Line int
					Text string
				}
			}
		}
	}
	if e := json.Unmarshal(w.Body.Bytes(), &result); e != nil || len(result.Data.List) != 1 {
		t.Fatalf("search: %d %s", w.Code, w.Body)
	}
	match := result.Data.List[0]
	if match.Name != "match.txt" || match.Match.Line != 2 || match.Match.Text != "find . this" {
		t.Fatalf("result: %+v", match)
	}
}

func TestDirectorySizeReturnsTaskResult(t *testing.T) {
	m, root := fixture(t)
	r := gin.New()
	m.Routes(r.Group(""))
	m.tasks.Routes(r.Group(""), m.auth)
	dir := filepath.Join(root, "stats")
	if err := os.Mkdir(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "file"), []byte("sample"), 0600); err != nil {
		t.Fatal(err)
	}
	w := fileFeatureRequest(r, "POST", "/api/files/size?path="+url.QueryEscape(dir), "")
	var start struct {
		Data struct{ TaskID string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &start); err != nil || start.Data.TaskID == "" {
		t.Fatalf("size start: %d %s", w.Code, w.Body)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		w = fileFeatureRequest(r, "GET", "/api/tasks/"+start.Data.TaskID, "")
		if strings.Contains(w.Body.String(), `"result":{"size":6}`) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("size result: %d %s", w.Code, w.Body)
}
