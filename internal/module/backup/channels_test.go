package backup

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path"
	"strings"
	"sync"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

func TestWebDAVConnectionChecksDirectory(t *testing.T) {
	for _, tc := range []struct {
		name, resource string
		wantError      bool
	}{
		{"目录", "<D:collection/>", false},
		{"文件", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				username, password, _ := r.BasicAuth()
				if r.Method != "PROPFIND" || r.URL.Path != "/backup/" || r.Header.Get("Depth") != "0" || username != "account" || password != "test-secret" {
					w.WriteHeader(http.StatusBadRequest)
					return
				}
				w.WriteHeader(http.StatusMultiStatus)
				_, _ = w.Write([]byte(`<D:multistatus xmlns:D="DAV:"><D:response><D:propstat><D:prop><D:resourcetype>` + tc.resource + `</D:resourcetype></D:prop><D:status>HTTP/1.1 200 OK</D:status></D:propstat></D:response></D:multistatus>`))
			}))
			defer server.Close()
			address, err := url.Parse(server.URL + "/backup")
			if err != nil {
				t.Fatal(err)
			}
			target := channelTarget{backupChannel: backupChannel{Protocol: "webdav", Username: "account"}, url: address, password: "test-secret"}
			if err := target.test(context.Background()); (err != nil) != tc.wantError {
				t.Fatalf("目录探测结果错误：%v", err)
			}
		})
	}
}

func TestBackupChannelEditAndDelete(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	router := gin.New()
	New(db, config.Config{AESKey: key}, nil, nil, nil).Routes(router.Group(""))
	request := func(method, path, body string, status int) {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		router.ServeHTTP(w, req)
		if w.Code != status || strings.Contains(w.Body.String(), "test-secret") {
			t.Fatalf("%s %s 返回 %d，预期 %d", method, path, w.Code, status)
		}
	}
	const list = "/api/backups/channels"
	request(http.MethodPost, list, `{"name":"原渠道","protocol":"webdav","endpoint":"https://backup.example/dir","username":"tester","password":"test-secret","enabled":true}`, http.StatusOK)
	var channel backupChannel
	if err := db.First(&channel, "name = ?", "原渠道").Error; err != nil {
		t.Fatal(err)
	}
	path := list + "/" + channel.ID
	request(http.MethodPut, path, `{"name":"新渠道","protocol":"webdav","endpoint":"https://backup.example/dir","username":"tester","enabled":false}`, http.StatusOK)
	if err := db.First(&channel, "id = ?", channel.ID).Error; err != nil {
		t.Fatal(err)
	}
	password, err := store.Decrypt(key, channel.Secret)
	if err != nil || password != "test-secret" || channel.Name != "新渠道" || channel.Enabled {
		t.Fatal("修改时未保留凭据或未更新渠道状态")
	}
	request(http.MethodPut, path, `{"name":"新渠道","protocol":"webdav","endpoint":"https://other.example/dir","username":"tester"}`, http.StatusBadRequest)
	job := store.CronJob{ID: store.ID(), Name: "备份任务", Type: "backup_site", Schedule: "0 0 * * *", Config: `{"backupStorageId":"` + channel.ID + `"}`}
	if err := db.Create(&job).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodDelete, path, "", http.StatusConflict)
	if err := db.Delete(&job).Error; err != nil {
		t.Fatal(err)
	}
	request(http.MethodDelete, path, "", http.StatusOK)
}

func TestWebDAVUploadsAndDeletesWithinTargetDirectories(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()

	// 同一渠道允许重复使用已有目录，删除只命中登记的对象。
	folders := map[string]bool{"/backup/": true}
	files := map[string]bool{}
	var objectsMu sync.Mutex
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		objectsMu.Lock()
		defer objectsMu.Unlock()
		object := r.URL.Path
		parent := path.Dir(strings.TrimSuffix(object, "/")) + "/"
		switch r.Method {
		case "MKCOL":
			if folders[object] {
				w.WriteHeader(http.StatusMethodNotAllowed)
			} else if !folders[parent] {
				w.WriteHeader(http.StatusConflict)
			} else {
				folders[object] = true
				w.WriteHeader(http.StatusCreated)
			}
		case http.MethodPut:
			if !folders[parent] || r.Header.Get("If-None-Match") != "*" {
				w.WriteHeader(http.StatusConflict)
				return
			}
			files[object] = true
			w.WriteHeader(http.StatusCreated)
		case http.MethodDelete:
			delete(files, object)
			w.WriteHeader(http.StatusNoContent)
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}))
	defer server.Close()

	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	secret, err := store.Encrypt(key, "")
	if err != nil {
		t.Fatal(err)
	}
	row := backupChannel{ID: store.ID(), Name: "WebDAV", Protocol: "webdav", Endpoint: server.URL + "/backup", Enabled: true, Secret: secret}
	if err := db.Create(&row).Error; err != nil {
		t.Fatal(err)
	}
	storage := &channelStorage{db: db, key: key}
	ctx := context.Background()
	const dbKey = "database/mysql/demo/demo_mysql_data_20261120_100000.sql.gz"
	const olderDB = "database/mysql/demo/demo_mysql_data_20261119_100000.sql.gz"
	const siteKey = "site/网站/网站_20261120_100000.tar.gz"
	const directoryKey = "directory/网站/网站_20261120_100000.tar.gz"
	for _, object := range []string{dbKey, olderDB, siteKey, directoryKey} {
		backedUp, err := storage.Upload(ctx, row.ID, object, strings.NewReader("test"))
		if err != nil || backedUp != object {
			t.Fatalf("远程归档路径 %q，错误 %v", backedUp, err)
		}
	}
	if err := storage.Delete(ctx, row.ID, olderDB); err != nil {
		t.Fatal(err)
	}
	objectsMu.Lock()
	defer objectsMu.Unlock()
	if len(files) != 3 || !files["/backup/"+dbKey] || !files["/backup/"+siteKey] || !files["/backup/"+directoryKey] {
		t.Fatalf("远程对象清理范围错误: %v", files)
	}
}
