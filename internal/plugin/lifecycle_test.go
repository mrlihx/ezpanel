package plugin

import (
	"archive/zip"
	"bytes"
	"encoding/base64"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"ezpanel/internal/auth"
	"ezpanel/internal/config"
	"ezpanel/internal/module/alerts"
	"ezpanel/internal/module/backup"
	"ezpanel/internal/module/certificate"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"golang.org/x/sys/unix"
)

func TestOfflinePluginLifecycle(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cfg := config.Config{InstallDir: t.TempDir(), Dev: true, JWTSecret: key, AESKey: key, TaskLogMaxBytes: 1 << 20}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	tasks, err := task.New(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	a, err := auth.New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(db, cfg, tasks, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Start(Services{Backups: backup.New(db, cfg, tasks, a, nil), Alerts: alerts.New(db, cfg, nil, nil), Certificates: certificate.New(db, cfg, tasks, a, nil)}); err != nil {
		t.Fatal(err)
	}
	g := gin.New()
	g.Use(func(c *gin.Context) {
		c.Set("panel.session", auth.Session{User: store.User{ID: 1, Username: "test-admin"}})
	})
	m.Routes(g.Group(""))
	m.FrameRoutes(g)
	call := func(method, path, mime string, body io.Reader, want int) string {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, body)
		req.Header.Set("Content-Type", mime)
		g.ServeHTTP(w, req)
		if w.Code != want {
			t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body.String())
		}
		return w.Body.String()
	}
	decode := func(body string, out any) {
		t.Helper()
		var env struct{ Data json.RawMessage }
		if err := json.Unmarshal([]byte(body), &env); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(env.Data, out); err != nil {
			t.Fatal(err)
		}
	}
	inspect := func(version, js string) (string, string) {
		t.Helper()
		var archive bytes.Buffer
		archiveWriter := zip.NewWriter(&archive)
		for name, data := range map[string]string{
			"plugin.yaml":  "id: sample-plugin\nname: 示例插件\nversion: " + version + "\ntype: static\nminPanelVersion: 0.0.0\nsdkVersion: 1\nui: web/entry.js\nmenu: {title: 示例插件, children: [{title: 文件, path: files}]}\n",
			"web/entry.js": js,
		} {
			entry, err := archiveWriter.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := io.WriteString(entry, data); err != nil {
				t.Fatal(err)
			}
		}
		if err := archiveWriter.Close(); err != nil {
			t.Fatal(err)
		}
		var body bytes.Buffer
		multipartWriter := multipart.NewWriter(&body)
		file, err := multipartWriter.CreateFormFile("file", "sample.zip")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := io.Copy(file, &archive); err != nil {
			t.Fatal(err)
		}
		if err := multipartWriter.Close(); err != nil {
			t.Fatal(err)
		}
		var inspected struct {
			UploadID         string
			InstalledVersion string
			AccessPreview    permissionPreview
		}
		decode(call(http.MethodPost, "/api/plugins/inspect", multipartWriter.FormDataContentType(), &body, 200), &inspected)
		if preview := inspected.AccessPreview; len(preview.Permissions) != 0 || len(preview.RuntimeRisks) != 0 || len(preview.Capabilities) != 1 || preview.Capabilities[0].Label != "侧边菜单" {
			t.Fatalf("unexpected preview for an isolated static plugin: %+v", preview)
		}
		return inspected.UploadID, inspected.InstalledVersion
	}
	waitTask := func(body, want string) {
		t.Helper()
		var data struct{ TaskID string }
		decode(body, &data)
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			var row store.Task
			if err := db.Take(&row, "id = ?", data.TaskID).Error; err == nil && row.Status != "running" {
				if row.Status != want {
					t.Fatalf("task ended with %s: %s", row.Status, row.ErrorMsg)
				}
				return
			}
		}
		t.Fatal("plugin task did not finish")
	}
	const app = "/api/plugins/sample-plugin"
	const asset = "/plugin/sample-plugin/ui/1.0.0/entry.js"
	const jsonType = "application/json"
	uploadID, installedVersion := inspect("1.0.0", "export default function Sample() { return null }")
	if installedVersion != "" {
		t.Fatalf("expected empty installedVersion before install, got %q", installedVersion)
	}
	waitTask(call(http.MethodPost, "/api/plugins/install", jsonType, strings.NewReader(`{"uploadId":"`+uploadID+`"}`), 200), "success")
	call(http.MethodGet, asset, "", nil, http.StatusServiceUnavailable)
	call(http.MethodPost, app+"/enable", "", nil, 200)
	if body := call(http.MethodGet, "/api/plugins", "", nil, 200); !strings.Contains(body, `"path":"files"`) {
		t.Fatal("plugin menu was not registered")
	}
	call(http.MethodGet, asset, "", nil, http.StatusNotFound)
	call(http.MethodPost, app+"/ui-isolation", jsonType, strings.NewReader(`{"isolated":false}`), 200)
	call(http.MethodPost, app+"/ui-session", "", nil, http.StatusConflict)
	if body := call(http.MethodGet, asset, "", nil, 200); !strings.Contains(body, "Sample") {
		t.Fatal("native UI asset was not served when isolation is off")
	}
	call(http.MethodPost, app+"/ui-isolation", jsonType, strings.NewReader(`{"isolated":true}`), 200)
	call(http.MethodGet, asset, "", nil, http.StatusNotFound)
	frameURL := func() string {
		var data struct{ URL string }
		decode(call(http.MethodPost, app+"/ui-session", "", nil, 200), &data)
		return data.URL
	}
	firstFrame := frameURL()
	if body := call(http.MethodGet, firstFrame, "", nil, 200); !strings.Contains(body, `data-entry="entry.js"`) {
		t.Fatal("isolated frame does not contain the plugin entry")
	}
	frameAsset := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, firstFrame+"assets/entry.js", nil)
	req.Header.Set("Origin", "null")
	g.ServeHTTP(frameAsset, req)
	if frameAsset.Code != 200 || frameAsset.Header().Get("Access-Control-Allow-Origin") != "null" || frameAsset.Header().Get("Content-Security-Policy") != isolatedDocument {
		t.Fatalf("unexpected isolated asset response: %d %v", frameAsset.Code, frameAsset.Header())
	}
	call(http.MethodGet, strings.Replace(firstFrame, "1.0.0", "1.0.1", 1), "", nil, http.StatusNotFound)
	newVersion, prevVersion := inspect("1.1.0", "export default function Sample() { return 'updated' }")
	if prevVersion != "1.0.0" {
		t.Fatalf("expected installedVersion 1.0.0 on upgrade inspect, got %q", prevVersion)
	}
	waitTask(call(http.MethodPost, app+"/upgrade", jsonType, strings.NewReader(`{"uploadId":"`+newVersion+`"}`), 200), "success")
	call(http.MethodGet, firstFrame, "", nil, http.StatusNotFound)
	if body := call(http.MethodGet, frameURL()+"assets/entry.js", "", nil, 200); !strings.Contains(body, "updated") {
		t.Fatal("new package was not loaded")
	}
	call(http.MethodPost, app+"/upgrade", jsonType, strings.NewReader(`{"uploadId":"`+newVersion+`"}`), http.StatusBadRequest)
	private := filepath.Join(m.private(), "sample-plugin")
	if err := os.MkdirAll(private, 0700); err != nil {
		t.Fatal(err)
	}
	pipe := filepath.Join(private, "transient.pipe")
	if err := unix.Mkfifo(pipe, 0600); err != nil {
		t.Fatal(err)
	}
	badVersion, _ := inspect("1.2.0", "export default function Sample() { return null }")
	waitTask(call(http.MethodPost, app+"/upgrade", jsonType, strings.NewReader(`{"uploadId":"`+badVersion+`"}`), 200), "failed")
	if err := os.Remove(pipe); err != nil {
		t.Fatal(err)
	}
	if body := call(http.MethodGet, app+"/rollback", "", nil, 200); !strings.Contains(body, `"version":"1.0.0"`) {
		t.Fatal("failed upgrade discarded the last successful rollback point")
	}
	waitTask(call(http.MethodPost, app+"/rollback", jsonType, strings.NewReader(`{"version":"1.0.0"}`), 200), "success")
	call(http.MethodGet, frameURL()+"assets/entry.js", "", nil, 200)
	entries, err := os.ReadDir(m.historyDir("sample-plugin"))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected one retained history snapshot, got %d: %v", len(entries), err)
	}
	if err := os.WriteFile(m.logPath("sample-plugin"), []byte("first\nlast\n"), 0600); err != nil {
		t.Fatal(err)
	}
	var tail struct {
		Content   string
		Truncated bool
	}
	decode(call(http.MethodGet, app+"/logs?lines=1", "", nil, 200), &tail)
	if tail.Content != "last\n" || !tail.Truncated {
		t.Fatalf("unexpected log tail: %+v", tail)
	}
	call(http.MethodPost, app+"/disable", "", nil, 200)
	waitTask(call(http.MethodPost, app+"/uninstall", jsonType, strings.NewReader(`{"purgeData":true}`), 200), "success")
	call(http.MethodGet, app, "", nil, 404)
}

func TestUpgradeWithoutConfigValuesAndDisabledInstall(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	cfg := config.Config{InstallDir: t.TempDir(), Dev: true, JWTSecret: key, AESKey: key, TaskLogMaxBytes: 1 << 20}
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	tasks, err := task.New(t.Context(), db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer tasks.Close()
	a, err := auth.New(db, cfg)
	if err != nil {
		t.Fatal(err)
	}
	m, err := New(db, cfg, tasks, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer m.Close()
	if err := m.Start(Services{}); err != nil {
		t.Fatal(err)
	}
	g := gin.New()
	g.Use(func(c *gin.Context) {
		c.Set("panel.session", auth.Session{User: store.User{ID: 1, Username: "test-admin"}})
	})
	m.Routes(g.Group(""))

	archKey := runtime.GOOS + "-" + runtime.GOARCH
	entryBin := "bin/" + archKey + "/agent"
	upload := func(manifestYAML string) string {
		t.Helper()
		var archive bytes.Buffer
		zw := zip.NewWriter(&archive)
		for name, content := range map[string]string{
			"plugin.yaml":  manifestYAML,
			"web/entry.js": "export default function App() { return null }",
			entryBin:       "dummy",
		} {
			f, err := zw.Create(name)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = io.WriteString(f, content)
		}
		_ = zw.Close()
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, _ := mw.CreateFormFile("file", "proc.zip")
		_, _ = io.Copy(part, &archive)
		_ = mw.Close()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/api/plugins/inspect", &body)
		req.Header.Set("Content-Type", mw.FormDataContentType())
		g.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("inspect failed %d: %s", w.Code, w.Body.String())
		}
		var resp struct {
			Data struct {
				UploadID string `json:"uploadId"`
			}
		}
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		return resp.Data.UploadID
	}
	waitTask := func(method, path, payload string) {
		t.Helper()
		w := httptest.NewRecorder()
		req := httptest.NewRequest(method, path, strings.NewReader(payload))
		req.Header.Set("Content-Type", "application/json")
		g.ServeHTTP(w, req)
		if w.Code != 200 {
			t.Fatalf("%s %s returned %d: %s", method, path, w.Code, w.Body.String())
		}
		var resp struct{ Data struct{ TaskID string } }
		_ = json.Unmarshal(w.Body.Bytes(), &resp)
		for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
			var row store.Task
			if err := db.Take(&row, "id = ?", resp.Data.TaskID).Error; err == nil && row.Status != "running" {
				if row.Status != "success" {
					t.Fatalf("task %s ended %s: %s", path, row.Status, row.ErrorMsg)
				}
				return
			}
		}
		t.Fatal("task timeout")
	}

	v1 := upload("id: proc-plugin\nname: 进程插件\nversion: 1.0.0\ntype: process\nminPanelVersion: 0.0.0\nsdkVersion: 1\nentry: {" + archKey + ": " + entryBin + "}\nui: web/entry.js\nenv:\n  - {key: KEEP_KEY, label: Keep, default: ok, required: true, secret: false}\n  - {key: FLIP_KEY, label: Flip, default: plain, required: false, secret: false}\n")
	waitTask(http.MethodPost, "/api/plugins/install", `{"uploadId":"`+v1+`"}`)

	var installed store.Plugin
	if err := db.First(&installed, "id = ?", "proc-plugin").Error; err != nil {
		t.Fatal(err)
	}
	if installed.Enabled {
		t.Fatal("newly installed plugin must remain disabled")
	}

	v2 := upload("id: proc-plugin\nname: 进程插件\nversion: 1.1.0\ntype: process\nminPanelVersion: 0.0.0\nsdkVersion: 1\nentry: {" + archKey + ": " + entryBin + "}\nui: web/entry.js\nenv:\n  - {key: KEEP_KEY, label: Keep, default: ok, required: true, secret: false}\n  - {key: FLIP_KEY, label: Flip, default: '', required: false, secret: true}\n  - {key: API_KEY, label: API Key, default: '', required: true, secret: true}\n")
	waitTask(http.MethodPost, "/api/plugins/proc-plugin/upgrade", `{"uploadId":"`+v2+`"}`)

	var upgraded store.Plugin
	if err := db.First(&upgraded, "id = ?", "proc-plugin").Error; err != nil {
		t.Fatal(err)
	}
	if upgraded.Version != "1.1.0" || upgraded.Enabled {
		t.Fatalf("unexpected upgraded state: version=%s enabled=%v", upgraded.Version, upgraded.Enabled)
	}
	var savedCfg map[string]string
	_ = json.Unmarshal([]byte(upgraded.Config), &savedCfg)
	if savedCfg["KEEP_KEY"] != "ok" || savedCfg["FLIP_KEY"] != "" || savedCfg["API_KEY"] != "" {
		t.Fatalf("unexpected config after upgrade: %+v", savedCfg)
	}
}
