package main

import (
	"errors"
	"net/http"
	"strings"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

// registerExtensionRoutes 注册通知发送、自定义探针、Compose 部署钩子策略、外部凭据指纹、安全状态、审计与任务查询路由。
func registerExtensionRoutes(p *panelsdk.Plugin, state *StateStore, storage *DemoBackupStorage) error {
	if err := p.HandleFunc("/api/extensions/state", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		snap := state.Snapshot()
		objects, _ := storage.ListObjects()
		ok(w, map[string]any{
			"state":          snap,
			"storageObjects": objects,
		})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/notify", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Title   string `json:"title"`
			Message string `json:"message"`
		}
		if !decodeJSON(w, r, 16<<10, &in) {
			return
		}
		if strings.TrimSpace(in.Title) == "" || strings.TrimSpace(in.Message) == "" {
			fail(w, errors.New("通知标题和正文不能为空"))
			return
		}
		if err := p.Host.Notify(r.Context(), in.Title, in.Message); err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]bool{"sent": true})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/probe", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Healthy bool   `json:"healthy"`
			Message string `json:"message"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		state.SetProbe(in.Healthy, strings.TrimSpace(in.Message))
		healthy, msg := state.Probe()
		ok(w, map[string]any{"healthy": healthy, "message": msg})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/deploy-hook-policy", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			BlockNextBefore bool `json:"blockNextBefore"`
		}
		if !decodeJSON(w, r, 1024, &in) {
			return
		}
		state.SetBlockNextBeforeHook(in.BlockNextBefore)
		ok(w, map[string]bool{"blockNextBeforeHook": in.BlockNextBefore})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/inbox/clear", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		state.ClearNotices()
		ok(w, map[string]bool{"cleared": true})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/credential", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		slot := strings.TrimSpace(r.URL.Query().Get("slot"))
		if slot == "" {
			slot = "demo-api-key"
		}
		val, err := p.Host.Credential(r.Context(), slot)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]any{
			"slot":    slot,
			"summary": SummarizeSecret(val),
		})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/security", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		status, err := p.Host.SecurityStatus(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, status)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/security/scan", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		taskID, err := p.Host.ScanHardening(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/audit", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		page, size := parsePage(r)
		res, err := p.Host.AuditHistory(r.Context(), page, size)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, res)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/task", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			fail(w, errors.New("请输入任务 ID"))
			return
		}
		summary, err := p.Host.Task(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, summary)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/extensions/service-selftest", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Name    string `json:"name"`
			Payload string `json:"payload"`
		}
		if !decodeJSON(w, r, 16<<10, &in) {
			return
		}
		resp, err := executeService(p.ID, in.Name, []byte(in.Payload), state)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, resp)
	}); err != nil {
		return err
	}

	return nil
}
