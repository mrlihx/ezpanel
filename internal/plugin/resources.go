package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"ezpanel/internal/module/database"
	"ezpanel/internal/module/docker"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type resource struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	Kind      string     `json:"kind,omitempty"`
	Status    string     `json:"status,omitempty"`
	Version   string     `json:"version,omitempty"`
	Domains   []string   `json:"domains,omitempty"`
	SiteID    string     `json:"siteId,omitempty"`
	CreatedAt *time.Time `json:"createdAt,omitempty"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
}

func (m *Module) listResources(c *gin.Context, kind string) {
	rows, err := m.resourceSummaries(c.Request.Context(), identity(c).id, kind)
	if err != nil {
		response.Fail(c, response.System, "无法读取面板资源")
		return
	}
	response.OK(c, rows)
}

func (m *Module) resourceSummaries(ctx context.Context, id, kind string) ([]resource, error) {
	grant, err := m.grant(id)
	if err != nil {
		return nil, err
	}
	scope := kind
	if kind == "backups" {
		scope = "sites"
	}
	all := slices.Contains(grant.AllResources, scope)
	ids := *grant.resourceIDs(scope)
	if !all && len(ids) == 0 {
		return []resource{}, nil
	}
	if scope == "sites" {
		if all {
			ids = nil
		}
		return listSiteResources(m.db.WithContext(ctx), kind, ids)
	}
	rows, err := m.resourceCatalog(ctx, kind)
	if !all {
		rows = slices.DeleteFunc(rows, func(row resource) bool { return !slices.Contains(ids, row.ID) })
	}
	return rows, err
}

func (m *Module) resourceCatalog(ctx context.Context, kind string) ([]resource, error) {
	rows := []resource{}
	db := m.db.WithContext(ctx)
	var err error
	switch kind {
	case "sites":
		return listSiteResources(db, kind, nil)
	case "certificates":
		var certs []store.Certificate
		err = db.Select("id, domains, source, not_after").Find(&certs).Error
		for _, cert := range certs {
			var domains []string
			_ = json.Unmarshal([]byte(cert.Domains), &domains)
			rows = append(rows, resource{ID: cert.ID, Name: strings.Join(domains, ", "), Kind: cert.Source, Domains: domains, ExpiresAt: &cert.NotAfter})
		}
	case "containers":
		var containers []docker.PluginContainer
		containers, err = m.services.Docker.PluginContainers(ctx)
		for _, item := range containers {
			rows = append(rows, resource{ID: item.ID, Name: item.Name, Kind: item.Image, Status: item.Status})
		}
	case "databases":
		var databases []database.PluginResource
		databases, err = m.services.Database.PluginResources(ctx)
		for _, item := range databases {
			rows = append(rows, resource{ID: item.ID, Name: item.Name, Kind: item.Kind, Status: item.Status, Version: item.Version})
		}
	case "compose":
		var projects []store.DockerComposeProject
		err = db.Select("name, source").Find(&projects).Error
		for _, project := range projects {
			rows = append(rows, resource{ID: project.Name, Name: project.Name, Kind: project.Source})
		}
	case "apps":
		for id, name := range pkgmgr.ApplicationNames() {
			rows = append(rows, resource{ID: id, Name: name})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].ID < rows[j].ID })
	return rows, err
}

func (m *Module) resourceChoices(c *gin.Context) {
	plugin, ok := m.row(c)
	if !ok {
		return
	}
	var manifest Manifest
	if !plugin.Enabled || m.status(plugin) != "running" || json.Unmarshal([]byte(plugin.Manifest), &manifest) != nil {
		response.Fail(c, response.Conflict, "插件未运行")
		return
	}
	kind := c.Param("kind")
	permission := map[string]string{"sites": "sites.read", "containers": "containers.read", "databases": "databases.read", "certificates": "certificates.read", "channels": "notifications.send"}[kind]
	if permission == "" {
		response.Fail(c, response.Forbidden, "插件未获得该资源选择权限")
		return
	}
	if err := m.checkPermission(plugin.ID, manifest, permission); err != nil {
		response.Fail(c, response.Forbidden, err.Error())
		return
	}
	ctx := c.Request.Context()
	if kind == "channels" {
		grant, err := m.grant(plugin.ID)
		if err != nil {
			response.Fail(c, response.System, "无法读取渠道授权")
			return
		}
		rows := []resource{}
		if grant.ChannelID != "" {
			channels, err := m.services.Alerts.PluginChannels(ctx)
			if err != nil {
				response.Fail(c, response.System, "无法读取通知渠道")
				return
			}
			for _, channel := range channels {
				if channel.ID == grant.ChannelID {
					rows = append(rows, resource{ID: channel.ID, Name: channel.Name})
					break
				}
			}
		}
		response.OK(c, rows)
		return
	}
	rows, err := m.resourceSummaries(ctx, plugin.ID, kind)
	if err != nil {
		response.Fail(c, response.System, "无法读取面板资源")
		return
	}
	response.OK(c, rows)
}

func listSiteResources(db *gorm.DB, kind string, siteIDs []string) ([]resource, error) {
	rows := []resource{}
	if kind == "sites" {
		var sites []store.Website
		query := db.Select("id, name, domains, project_type, enabled")
		if len(siteIDs) > 0 {
			query = query.Where("id IN ?", siteIDs)
		}
		err := query.Order("name, id").Find(&sites).Error
		for _, site := range sites {
			status := "disabled"
			if site.Enabled {
				status = "enabled"
			}
			var domains []string
			_ = json.Unmarshal([]byte(site.Domains), &domains)
			rows = append(rows, resource{ID: site.ID, Name: site.Name, Kind: site.ProjectType, Status: status, Domains: domains})
		}
		return rows, err
	}
	// 巡检只需每个网站最近一次登记的备份，避免返回完整备份历史。
	var backups []store.SiteBackup
	query := db.Select("id, website_id, site_name, storage_type, created_at").Where("website_id IS NOT NULL")
	if len(siteIDs) > 0 {
		query = query.Where("website_id IN ?", siteIDs)
	}
	err := query.Where("id = (SELECT id FROM site_backups AS latest WHERE latest.website_id = site_backups.website_id ORDER BY latest.created_at DESC, latest.id DESC LIMIT 1)").Order("website_id").Find(&backups).Error
	for _, backup := range backups {
		rows = append(rows, resource{ID: backup.ID, Name: backup.SiteName, SiteID: *backup.WebsiteID, Kind: backup.StorageType, CreatedAt: &backup.CreatedAt})
	}
	return rows, err
}

func (m *Module) siteBackupHistory(c *gin.Context) {
	id := c.Param("site")
	if !m.authorizeResource(c, "sites", id) {
		return
	}
	page, size, ok := response.Page(c, 100, 20)
	if !ok {
		return
	}
	var total int64
	rows := []store.SiteBackup{}
	db := m.db.WithContext(c.Request.Context()).Model(&store.SiteBackup{}).Where("website_id = ?", id)
	if err := db.Session(&gorm.Session{}).Count(&total).Error; err != nil {
		response.Fail(c, response.System, "无法读取站点备份历史")
		return
	}
	if int64(page-1) <= total/int64(size) {
		if err := db.Session(&gorm.Session{}).Order("created_at DESC, id DESC").Offset((page - 1) * size).Limit(size).Find(&rows).Error; err != nil {
			response.Fail(c, response.System, "无法读取站点备份历史")
			return
		}
	}
	response.OK(c, gin.H{"list": rows, "total": total})
}

func (m *Module) verifySiteBackup(c *gin.Context) {
	siteID, id := c.Param("site"), c.Param("id")
	if !m.authorizeResource(c, "sites", siteID) {
		return
	}
	m.submitManagedAction(c, "site-backup-verify", id, siteID, "website:"+siteID, func(ctx context.Context, log *task.Log) (any, error) {
		ctx, cancel := context.WithTimeout(ctx, 30*time.Minute)
		defer cancel()
		digest, err := m.services.Backups.VerifySiteBackup(ctx, siteID, id)
		if err != nil {
			log.Line("归档校验失败")
			return nil, err
		}
		log.Line("SHA-256 " + digest)
		return gin.H{"backupId": id, "sha256": digest}, nil
	})
}

func (m *Module) notifyChannel(c *gin.Context) {
	var in struct{ Title, Message string }
	if !response.Bind(c, &in) {
		return
	}
	i := identity(c)
	if strings.TrimSpace(in.Title) == "" || strings.ContainsAny(in.Title, "\r\n\x00") || utf8.RuneCountInString(in.Title) > 120 || strings.TrimSpace(in.Message) == "" || len(in.Message) > 8192 {
		response.Fail(c, response.Invalid, "通知标题或正文无效")
		return
	}
	grant, err := m.grant(i.id)
	if err != nil || grant.ChannelID == "" {
		response.Fail(c, response.Forbidden, "请让管理员为此插件选择通知渠道")
		return
	}
	m.mu.Lock()
	if time.Since(i.run.lastNotice) < time.Minute {
		m.mu.Unlock()
		response.Fail(c, response.RateLimited, "同一插件每分钟最多发送一次通知")
		return
	}
	i.run.lastNotice = time.Now()
	m.mu.Unlock()
	secrets := m.hostSecrets(i)
	err = m.services.Alerts.SendPluginNotice(c.Request.Context(), i.id, grant.ChannelID, redact(in.Title, secrets), redact(in.Message, secrets))
	if err != nil {
		response.Fail(c, response.System, "通知发送失败，请查看通知渠道与发送记录")
	}
	m.hostAudit(c, "notifications.send", grant.ChannelID, err == nil)
	if err == nil {
		response.OK(c, nil)
	}
}

func (m *Module) siteBackup(c *gin.Context) {
	i := identity(c)
	siteID := c.Param("site")
	grant, err := m.grant(i.id)
	if err != nil || !grant.allowsResource("sites", siteID) {
		response.Fail(c, response.Forbidden, "插件未获此网站授权")
		m.hostAudit(c, "backups.create", siteID, false)
		return
	}
	var site store.Website
	if err := m.db.Select("id, name").First(&site, "id = ? AND root <> ''", siteID).Error; err != nil {
		response.Fail(c, response.NotFound, "网站不存在或没有可备份的目录")
		return
	}
	id, err := m.services.Backups.SubmitSiteBackup(site.ID, site.Name, "plugin:"+i.id, "plugin:"+i.id, "127.0.0.1")
	if err != nil {
		code := response.System
		if errors.Is(err, task.ErrBusy) {
			code = response.Conflict
		}
		response.Fail(c, code, "无法开始网站备份")
		return
	}
	response.OK(c, gin.H{"taskId": id})
}

func (m *Module) hostTask(c *gin.Context) {
	i := identity(c)
	var row store.Task
	if m.db.First(&row, "id = ? AND module = ?", c.Param("task"), "plugin:"+i.id).Error != nil {
		response.Fail(c, response.NotFound, "插件任务不存在")
		return
	}
	view := task.Summary(row)
	if row.Result != nil {
		view["result"] = json.RawMessage(*row.Result)
	}
	response.OK(c, view)
}
