package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

type pluginConfig struct {
	Greeting      string        `json:"greeting"`
	HistoryLimit  int           `json:"historyLimit"`
	AutoNotify    bool          `json:"autoNotify"`
	InspectMode   string        `json:"inspectMode"`
	SampleFile    string        `json:"sampleFile"`
	WorkDir       string        `json:"workDir"`
	WebhookSecret SecretSummary `json:"webhookSecret"`
	DemoToken     SecretSummary `json:"demoToken"`
}

// ok 返回面板约定信封格式 {"code": 0, "data": ...}，供前端 sdk.api 自动解包 data。
func ok(w http.ResponseWriter, data any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": 0,
		"data": data,
	})
}

// fail 透传 *panelsdk.APIError 中的业务 Code/Status/Message，便于前端区分 403 未授权与普通参数错误。
func fail(w http.ResponseWriter, err error) {
	status := http.StatusBadRequest
	code := 1001
	msg := err.Error()
	var apiErr *panelsdk.APIError
	if errors.As(err, &apiErr) {
		if apiErr.Status >= 400 && apiErr.Status < 600 {
			status = apiErr.Status
		}
		if apiErr.Code != 0 {
			code = apiErr.Code
		}
		msg = apiErr.Message
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{
		"code": code,
		"msg":  msg,
	})
}

func requireMethod(w http.ResponseWriter, r *http.Request, method string) bool {
	if r.Method != method {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return false
	}
	return true
}

func decodeJSON(w http.ResponseWriter, r *http.Request, limit int64, dst any) bool {
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, limit)).Decode(dst); err != nil {
		fail(w, errors.New("请求体 JSON 无效"))
		return false
	}
	return true
}

// registerCoreRoutes 注册概览卡片、插件元信息、KV 存储（1000 项 / 单值 64KiB）、日志与监控路由。
// 注意：panelsdk.HandleFunc 要求路径严格以 "/api/" 开头（不支持 Go 1.22 方法前缀）。
func registerCoreRoutes(p *panelsdk.Plugin, cfg pluginConfig, state *StateStore) error {
	// 面板概览卡片每 60 秒轮询 GET /api/dashboard，须返回 {value, description}。
	if err := p.HandleFunc("/api/dashboard", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		snap := state.Snapshot()
		probe := "正常"
		if !snap.ProbeHealthy {
			probe = "异常"
		}
		ok(w, map[string]string{
			"value":       fmt.Sprintf("%d 任务 / %d 事件", len(snap.JobRuns), len(snap.Events)),
			"description": fmt.Sprintf("探针%s · 通知收件 %d · DNS 记录 %d", probe, len(snap.Notices), len(snap.DNSRecords)),
		})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/overview", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		info, err := p.Host.Info(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		snap := state.Snapshot()
		ok(w, map[string]any{
			"user":     r.Header.Get("X-Panel-User"),
			"info":     info,
			"config":   cfg,
			"hostname": hostnameOrEmpty(),
			"counts": map[string]int{
				"jobRuns":      len(snap.JobRuns),
				"events":       len(snap.Events),
				"notices":      len(snap.Notices),
				"dnsKeys":      len(snap.DNSRecords),
				"deployHooks":  len(snap.DeployHooks),
				"storageOps":   len(snap.StorageOps),
				"serviceCalls": len(snap.ServiceCalls),
			},
			"probeHealthy": snap.ProbeHealthy,
			"probeMessage": snap.ProbeMessage,
		})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/kv", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			key := r.URL.Query().Get("key")
			if key != "" {
				val, err := p.Host.KVGet(r.Context(), key)
				if err != nil {
					fail(w, err)
					return
				}
				ok(w, map[string]string{"key": key, "value": val})
				return
			}
			prefix := r.URL.Query().Get("prefix")
			items, err := p.Host.KVList(r.Context(), prefix)
			if err != nil {
				fail(w, err)
				return
			}
			if items == nil {
				items = []panelsdk.KVItem{}
			}
			ok(w, items)
		case http.MethodPut:
			var in struct {
				Key   string `json:"key"`
				Value string `json:"value"`
			}
			if !decodeJSON(w, r, 128<<10, &in) {
				return
			}
			if strings.TrimSpace(in.Key) == "" {
				fail(w, errors.New("KV 键名不能为空"))
				return
			}
			if err := p.Host.KVSet(r.Context(), in.Key, in.Value); err != nil {
				fail(w, err)
				return
			}
			ok(w, map[string]string{"key": in.Key})
		case http.MethodDelete:
			key := r.URL.Query().Get("key")
			if strings.TrimSpace(key) == "" {
				fail(w, errors.New("请指定要删除的键名"))
				return
			}
			if err := p.Host.KVDelete(r.Context(), key); err != nil {
				fail(w, err)
				return
			}
			ok(w, map[string]string{"deleted": key})
		default:
			w.WriteHeader(http.StatusMethodNotAllowed)
		}
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/log", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Level string `json:"level"`
			Msg   string `json:"msg"`
		}
		if !decodeJSON(w, r, 8192, &in) {
			return
		}
		if err := p.Host.Log(r.Context(), in.Level, in.Msg); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]bool{"written": true})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/sysinfo", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		overview, err := p.Host.Sysinfo(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, overview)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/monitoring", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		now := time.Now()
		points, err := p.Host.MonitoringHistory(r.Context(), now.Add(-1*time.Hour), now)
		if err != nil {
			fail(w, err)
			return
		}
		if points == nil {
			points = []panelsdk.MetricPoint{}
		}
		ok(w, points)
	}); err != nil {
		return err
	}

	return nil
}

func hostnameOrEmpty() string {
	name, err := os.Hostname()
	if err != nil {
		return ""
	}
	return name
}
