package file

import (
	"bytes"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/pathsafe"
	"github.com/gin-gonic/gin"
)

func TestUploadMixedRootsAndEmptyDirectory(t *testing.T) {
	base := t.TempDir()
	root := filepath.Join(base, "upload")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	policy, err := pathsafe.New(filepath.Join(base, "panel"))
	if err != nil {
		t.Fatal(err)
	}
	module := &Module{policy: policy}
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	if err := writer.WriteField("dir", root); err != nil {
		t.Fatal(err)
	}
	if err := writer.WriteField("dirs[]", "first/empty"); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"first/deep/a.txt", "second/b.txt", "top.txt"} {
		part, err := writer.CreateFormFile("files[]", name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := part.Write([]byte(name)); err != nil {
			t.Fatal(err)
		}
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)
	c.Request = httptest.NewRequest("POST", "/upload", &body)
	c.Request.Header.Set("Content-Type", writer.FormDataContentType())
	module.upload(c)
	if recorder.Code != 200 || !bytes.Contains(recorder.Body.Bytes(), []byte(`"successCount":4`)) {
		t.Fatal(recorder.Body.String())
	}
	if info, err := os.Stat(filepath.Join(root, "first/empty")); err != nil || !info.IsDir() {
		t.Fatalf("空目录未创建：%v", err)
	}
	for _, name := range []string{"first/deep/a.txt", "second/b.txt", "top.txt"} {
		path := filepath.Join(root, name)
		content, err := os.ReadFile(path)
		if err != nil || string(content) != name {
			t.Fatalf("%s: %v", name, err)
		}
		info, err := os.Stat(path)
		if err != nil {
			t.Fatal(err)
		}
		if mode := info.Mode().Perm(); mode != 0755 {
			t.Fatalf("%s 权限为 %04o，预期 0755", name, mode)
		}
	}
}
