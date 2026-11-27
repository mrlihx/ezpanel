package main

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mrlihx/ezpanel/sdk/go/panelsdk"
)

func parsePage(r *http.Request) (int, int) {
	page, _ := strconv.Atoi(r.URL.Query().Get("page"))
	if page < 1 {
		page = 1
	}
	size, _ := strconv.Atoi(r.URL.Query().Get("size"))
	if size < 1 || size > 100 {
		size = 10
	}
	return page, size
}

// registerResourceRoutes 注册面板六大资源（网站、容器、数据库、证书、Compose、系统应用）代理接口。
// 所有资源列表与操作均由面板 Host API 按「应用商店 → 插件设置 → 授权」范围实时校验，
// 写操作与备份/恢复/部署会返回 taskId，前端可通过 sdk.showTask(taskId) 打开面板任务日志抽屉。
func registerResourceRoutes(p *panelsdk.Plugin) error {
	// 网站资源与运维
	if err := p.HandleFunc("/api/resources/sites", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		sites, err := p.Host.Sites(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		// 备份列表读取失败时降级为空映射，避免阻断站点列表本身展示。
		backups, _ := p.Host.Backups(r.Context())
		latest := make(map[string]panelsdk.ResourceSummary, len(backups))
		for _, b := range backups {
			latest[b.SiteID] = b
		}
		rows := make([]map[string]any, 0, len(sites))
		for _, s := range sites {
			item := map[string]any{
				"id":      s.ID,
				"name":    s.Name,
				"kind":    s.Kind,
				"status":  s.Status,
				"domains": s.Domains,
			}
			if b, found := latest[s.ID]; found {
				item["latestBackup"] = b
			}
			rows = append(rows, item)
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-detail", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少站点 ID"))
			return
		}
		detail, err := p.Host.SiteDetail(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, detail)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-logs", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少站点 ID"))
			return
		}
		opts := url.Values{}
		if t := r.URL.Query().Get("type"); t != "" {
			opts.Set("type", t)
		}
		if lines := r.URL.Query().Get("lines"); lines != "" {
			opts.Set("lines", lines)
		}
		if kw := r.URL.Query().Get("keyword"); kw != "" {
			opts.Set("keyword", kw)
		}
		tail, err := p.Host.SiteLogs(r.Context(), id, opts)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, tail)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-control", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.ControlSite(r.Context(), in.ID, in.Action)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-restart", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.RestartSite(r.Context(), in.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-backups", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少站点 ID"))
			return
		}
		page, size := parsePage(r)
		res, err := p.Host.SiteBackupHistory(r.Context(), id, page, size)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, res)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-backup", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.CreateSiteBackup(r.Context(), in.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-backup-verify", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			SiteID   string `json:"siteId"`
			BackupID string `json:"backupId"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.VerifySiteBackup(r.Context(), in.SiteID, in.BackupID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/site-backup-restore", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			SiteID      string `json:"siteId"`
			BackupID    string `json:"backupId"`
			ConfirmName string `json:"confirmName"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.RestoreSiteBackup(r.Context(), in.SiteID, in.BackupID, in.ConfirmName)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	// 容器资源与运维
	if err := p.HandleFunc("/api/resources/containers", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		rows, err := p.Host.Containers(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/container-logs", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少容器 ID"))
			return
		}
		opts := url.Values{}
		if lines := r.URL.Query().Get("lines"); lines != "" {
			opts.Set("lines", lines)
		}
		tail, err := p.Host.ContainerLogs(r.Context(), id, opts)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, tail)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/container-stats", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少容器 ID"))
			return
		}
		var stats map[string]any
		if err := p.Host.ContainerStats(r.Context(), id, &stats); err != nil {
			fail(w, err)
			return
		}
		ok(w, stats)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/container-control", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.ControlContainer(r.Context(), in.ID, in.Action)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	// 数据库资源与运维
	if err := p.HandleFunc("/api/resources/databases", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		rows, err := p.Host.Databases(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/database-names", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少数据库实例 ID"))
			return
		}
		names, err := p.Host.DatabaseNames(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, names)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/database-backup", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ServerID string `json:"serverId"`
			Name     string `json:"name"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.CreateDatabaseBackup(r.Context(), in.ServerID, in.Name)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/database-backups", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := r.URL.Query().Get("id")
		if id == "" {
			fail(w, errors.New("缺少数据库实例 ID"))
			return
		}
		page, size := parsePage(r)
		res, err := p.Host.DatabaseBackups(r.Context(), id, page, size)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, res)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/database-restore", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ServerID        string `json:"serverId"`
			BackupID        string `json:"backupId"`
			ConfirmDatabase string `json:"confirmDatabase"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.RestoreDatabaseBackup(r.Context(), in.ServerID, in.BackupID, in.ConfirmDatabase)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	// 证书
	if err := p.HandleFunc("/api/resources/certificates", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		rows, err := p.Host.Certificates(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/certificate-bind", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			SiteID        string `json:"siteId"`
			CertificateID string `json:"certificateId"`
			ForceHTTPS    bool   `json:"forceHttps"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.BindSiteCertificate(r.Context(), in.SiteID, in.CertificateID, in.ForceHTTPS)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/certificate-renew", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID string `json:"id"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.RenewCertificate(r.Context(), in.ID)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	// Compose
	if err := p.HandleFunc("/api/resources/compose", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		rows, err := p.Host.ComposeProjects(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/compose-deploy", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			Name string `json:"name"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.DeployCompose(r.Context(), in.Name)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	// 应用管理
	if err := p.HandleFunc("/api/resources/apps", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		rows, err := p.Host.Applications(r.Context())
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, rows)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/app-status", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodGet) {
			return
		}
		id := strings.TrimSpace(r.URL.Query().Get("id"))
		if id == "" {
			fail(w, errors.New("缺少应用 ID"))
			return
		}
		st, err := p.Host.AppStatus(r.Context(), id)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, st)
	}); err != nil {
		return err
	}

	if err := p.HandleFunc("/api/resources/app-control", func(w http.ResponseWriter, r *http.Request) {
		if !requireMethod(w, r, http.MethodPost) {
			return
		}
		var in struct {
			ID     string `json:"id"`
			Action string `json:"action"`
		}
		if !decodeJSON(w, r, 4096, &in) {
			return
		}
		taskID, err := p.Host.ControlApp(r.Context(), in.ID, in.Action)
		if err != nil {
			fail(w, err)
			return
		}
		ok(w, map[string]string{"taskId": taskID})
	}); err != nil {
		return err
	}

	return nil
}
