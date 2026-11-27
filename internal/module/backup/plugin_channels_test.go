package backup

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/store"
	"github.com/gin-gonic/gin"
)

type testPluginStorage struct {
	objects map[string]string
}

func (s *testPluginStorage) BackupProviders(context.Context) ([]PluginProvider, error) {
	return []PluginProvider{{Protocol: "plugin:demo-storage", Name: "网盘", Fields: []ChannelField{
		{Key: "folder", Label: "目录", Type: "text", Required: true},
		{Key: "token", Label: "令牌", Type: "password", Required: true},
	}}}, nil
}

func (s *testPluginStorage) CallBackupProvider(_ context.Context, id, action string, req PluginChannelRequest, body io.Reader) (io.ReadCloser, error) {
	if id != "demo-storage" || req.Values["token"] != "private-token" || req.Values["folder"] != "archives" {
		return nil, fmt.Errorf("unexpected provider or credentials")
	}
	key := req.ChannelID + "/" + req.Key
	switch action {
	case "test":
	case "upload":
		bytes, err := io.ReadAll(body)
		if err != nil {
			return nil, err
		}
		s.objects[key] = string(bytes)
	case "open":
		return io.NopCloser(strings.NewReader(s.objects[key])), nil
	case "delete":
		delete(s.objects, key)
	default:
		return nil, fmt.Errorf("unexpected action %s", action)
	}
	return io.NopCloser(strings.NewReader("")), nil
}

func TestPluginBackupChannel(t *testing.T) {
	db, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	sql, _ := db.DB()
	defer sql.Close()
	key := base64.StdEncoding.EncodeToString(make([]byte, 32))
	m := New(db, config.Config{AESKey: key}, nil, nil, nil)
	provider := &testPluginStorage{objects: map[string]string{}}
	m.SetPluginStorage(provider)
	router := gin.New()
	m.Routes(router.Group(""))
	call := func(method, path, body string) string {
		t.Helper()
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		res := httptest.NewRecorder()
		router.ServeHTTP(res, req)
		data := res.Body.String()
		if res.Code != http.StatusOK || strings.Contains(data, "private-token") {
			t.Fatalf("%s %s returned %d: %s", method, path, res.Code, data)
		}
		return data
	}
	const base = "/api/backups/channels"
	const input = `{"name":"网盘归档","protocol":"plugin:demo-storage","enabled":true,"fields":{"folder":"archives","token":"private-token"}}`
	call(http.MethodPost, base+"/test", `{"config":`+input+`}`)
	call(http.MethodPost, base, input)
	var row backupChannel
	if err := db.First(&row, "protocol = ?", "plugin:demo-storage").Error; err != nil {
		t.Fatal(err)
	}
	if row.Secret == "" || strings.Contains(row.Secret, "private-token") {
		t.Fatal("plugin channel credentials must be encrypted")
	}
	if data := call(http.MethodGet, base, ""); !strings.Contains(data, `"folder":"archives"`) || strings.Contains(data, `"token"`) {
		t.Fatalf("unexpected channel list: %s", data)
	}
	call(http.MethodPut, base+"/"+row.ID, strings.Replace(input, `,"token":"private-token"`, "", 1))
	ctx := t.Context()
	const object = "site/example/site.tar.gz"
	if saved, err := m.channels.Upload(ctx, row.ID, object, strings.NewReader("archive")); err != nil || saved != object {
		t.Fatalf("upload returned %q: %v", saved, err)
	}
	file, err := m.channels.Open(ctx, row.ID, object)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := io.ReadAll(file)
	file.Close()
	if err != nil || string(payload) != "archive" {
		t.Fatalf("unexpected archive %q: %v", payload, err)
	}
	if err := m.channels.Delete(ctx, row.ID, object); err != nil || len(provider.objects) != 0 {
		t.Fatalf("archive not deleted: %v", err)
	}
}
