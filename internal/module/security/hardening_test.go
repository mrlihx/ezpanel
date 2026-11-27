package security

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"ezpanel/internal/config"
	"ezpanel/internal/hardening"
	"github.com/gin-gonic/gin"
)

func TestHardeningAPIControlAndMonitoring(t *testing.T) {
	gin.SetMode(gin.TestMode)
	root := t.TempDir()
	m := New(config.Config{InstallDir: root}, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	if err := m.ConfigureHardening(ctx); err != nil {
		t.Fatal(err)
	}
	router := gin.New()
	m.Routes(router.Group(""))
	request := func(method, path string, input any, status int) hardening.State {
		t.Helper()
		data, _ := json.Marshal(input)
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(method, "/api/security/hardening"+path, bytes.NewReader(data)))
		if recorder.Code != status {
			t.Fatalf("%s %s: %d %s", method, path, recorder.Code, recorder.Body.String())
		}
		var envelope struct {
			Data hardening.State `json:"data"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	if state := request("GET", "", nil, 200); state.Enabled {
		t.Fatal("系统加固应默认关闭")
	}
	request("PUT", "", map[string]any{}, 400)
	target := filepath.Join(root, "sample.conf")
	if err := os.WriteFile(target, []byte("before"), 0600); err != nil {
		t.Fatal(err)
	}
	state := request("POST", "/rules", hardening.RuleInput{Path: target, Category: "critical", Mode: "monitor", Enabled: true}, 200)
	var id string
	for _, rule := range state.Rules {
		if rule.Path == target {
			id = rule.ID
		}
	}
	if id == "" {
		t.Fatal("规则未保存")
	}
	request("POST", "/scan", nil, 200)
	if err := os.WriteFile(target, []byte("after"), 0600); err != nil {
		t.Fatal(err)
	}
	state = request("POST", "/scan", nil, 200)
	for _, rule := range state.Rules {
		if rule.ID == id && rule.Status != "changed" {
			t.Fatalf("未发现内容变化：%+v", rule)
		}
	}
	if state.Enabled {
		t.Fatal("手动扫描不应开启总开关")
	}
	request("POST", "/rules/"+id+"/action", map[string]any{"action": "baseline"}, 200)
	state = request("PUT", "", map[string]any{"enabled": true}, 200)
	if !state.Enabled {
		t.Fatal("总开关未开启")
	}
	state = request("PUT", "", map[string]any{"enabled": false}, 200)
	if state.Enabled {
		t.Fatal("总开关未关闭")
	}
	request("DELETE", "/rules/"+id, nil, 200)
	if _, err := os.Stat(target); err != nil {
		t.Fatal("删除规则不应删除文件", err)
	}
	request("POST", "/rules/missing/action", map[string]any{"action": "baseline"}, 400)
}
