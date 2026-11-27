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

func TestFTPAndSFTPChannelAddressFields(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	storage := channelStorage{key: key}
	for _, tc := range []struct {
		protocol, address, want string
		port                    int
	}{
		{"ftp", "backup.example/archives", "ftp://backup.example:21/archives", 0},
		{"sftp", "192.0.2.5/backup", "sftp://192.0.2.5:2222/backup", 2222},
		{"sftp", "2001:db8::1", "sftp://[2001:db8::1]:22", 0},
	} {
		t.Run(tc.protocol+tc.address, func(t *testing.T) {
			target, err := storage.draft(channelInput{Name: "备份", Protocol: tc.protocol, Endpoint: tc.address, Port: tc.port, Username: "account", Password: "secret"}, backupChannel{})
			if err != nil || target.Endpoint != tc.want {
				t.Fatalf("地址规范化失败：%q, %v", target.Endpoint, err)
			}
			view := target.backupChannel.view()
			if strings.Contains(view.Endpoint, "://") || view.Port == 0 {
				t.Fatalf("界面地址未拆分：%+v", view)
			}
		})
	}
	secret, _ := store.Encrypt(key, "saved-password")
	old := backupChannel{ID: "existing", Name: "备份", Protocol: "sftp", Endpoint: "sftp://backup.example/backup", Username: "account", Secret: secret, Fingerprint: "SHA256:saved"}
	target, err := storage.draft(channelInput{Name: old.Name, Protocol: old.Protocol, Endpoint: "backup.example/backup", Port: 22, Username: old.Username}, old)
	if err != nil || target.password != "saved-password" || target.Fingerprint != old.Fingerprint {
		t.Fatalf("编辑未保留已有密码和主机公钥：%v", err)
	}
	target, err = storage.draft(channelInput{Name: old.Name, Protocol: old.Protocol, Endpoint: "backup.example/backup", Port: 22, Username: old.Username, Password: "new-password"}, old)
	if err != nil || target.Fingerprint != "" {
		t.Fatalf("重新填写凭据后未允许重新确认主机：%v", err)
	}
	if _, err := storage.draft(channelInput{Name: "备份", Protocol: "ftp", Endpoint: "backup.example", Port: 65536, Username: "account"}, backupChannel{}); err == nil {
		t.Fatal("未拒绝无效端口")
	}
}

func TestBackupChannelDirectoryFields(t *testing.T) {
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	storage := channelStorage{key: key}
	for _, protocol := range []string{"ftp", "sftp"} {
		t.Run(protocol, func(t *testing.T) {
			directory := "/备份/weekly archives"
			target, err := storage.draft(channelInput{Name: "备份", Protocol: protocol, Endpoint: "backup.example", Port: 2022, Directory: &directory, Username: "account", Password: "secret"}, backupChannel{})
			if err != nil {
				t.Fatal(err)
			}
			if target.remotePath("database/demo.sql.gz") != directory+"/database/demo.sql.gz" {
				t.Fatal("传输路径未使用独立备份目录")
			}
			view := target.backupChannel.view()
			if view.Endpoint != "backup.example" || view.Directory != directory || view.Port != 2022 {
				t.Fatalf("服务器与目录回显错误：%+v", view)
			}
			secret, err := store.Encrypt(key, target.password)
			if err != nil {
				t.Fatal(err)
			}
			row := target.backupChannel
			row.Secret = secret
			saved, err := storage.openTarget(row)
			if err != nil || saved.remotePath("") != directory {
				t.Fatalf("保存后的渠道目录错误：%v", err)
			}
			for _, invalid := range []string{"", "relative/backup", "/backup\nother"} {
				if _, err := storage.draft(channelInput{Name: "备份", Protocol: protocol, Endpoint: "backup.example", Directory: &invalid, Username: "account"}, backupChannel{}); err == nil {
					t.Fatalf("未拒绝无效目录 %q", invalid)
				}
			}
		})
	}
}
