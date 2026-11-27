package plugin

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"

	"ezpanel/internal/auth"
	"ezpanel/internal/module/alerts"
	"ezpanel/internal/module/backup"
	"ezpanel/internal/module/certificate"
	"ezpanel/internal/module/cron"
	"ezpanel/internal/module/database"
	"ezpanel/internal/module/docker"
	"ezpanel/internal/module/file"
	"ezpanel/internal/module/security"
	"ezpanel/internal/module/waf"
	"ezpanel/internal/module/website"
	"ezpanel/internal/pkgmgr"
	"ezpanel/internal/response"
	"ezpanel/internal/store"
	"ezpanel/internal/task"
	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type Services struct {
	Alerts       *alerts.Module
	Backups      *backup.Module
	Cron         *cron.Module
	Docker       *docker.Module
	Database     *database.Module
	Websites     *website.Module
	Packages     *pkgmgr.Manager
	Security     *security.Module
	WAF          *waf.Module
	Certificates *certificate.Module
	Files        *file.Module
}

const grantPrefix = "plugin.capabilities."

type capabilityGrant struct {
	AllResources        []string          `json:"allResources,omitempty"`
	SiteIDs             []string          `json:"siteIds"`
	ContainerIDs        []string          `json:"containerIds"`
	DatabaseIDs         []string          `json:"databaseIds"`
	CertificateIDs      []string          `json:"certificateIds"`
	ComposeNames        []string          `json:"composeNames"`
	AppIDs              []string          `json:"appIds"`
	Credentials         map[string]string `json:"credentials"`
	ChannelID           string            `json:"channelId"`
	Schedules           map[string]string `json:"schedules"`
	Events              []string          `json:"events"`
	DisabledPermissions []string          `json:"disabledPermissions,omitempty"`
}

func (g capabilityGrant) permissionDisabled(code string) bool {
	return slices.Contains(g.DisabledPermissions, code)
}

func (m *Module) checkPermission(id string, mf Manifest, perm string) error {
	if !slices.Contains(mf.Permissions, perm) {
		return errors.New(permissionDeniedMessage(perm, false))
	}
	grant, err := m.grant(id)
	if err != nil {
		return errors.New("无法读取插件授权")
	}
	if grant.permissionDisabled(perm) {
		return errors.New(permissionDeniedMessage(perm, true))
	}
	return nil
}

var eventPermissions = map[string]string{
	"site.backup.finished":         "backups.read",
	"certificate.renewal.finished": "certificates.read",
	"site.project.finished":        "sites.read",
	"compose.task.finished":        "compose.read",
	"database.backup.finished":     "databases.backup",
}

func validateCapabilities(m Manifest, permissions map[string]bool) error {
	for kind, title := range m.ResourceActions {
		if !slices.Contains([]string{"sites", "containers", "databases", "certificates"}, kind) ||
			!permissions[kind+".read"] || strings.TrimSpace(title) == "" || len([]rune(title)) > 64 {
			return errors.New("资源菜单类型、标题或读权限无效")
		}
	}
	if len(m.CredentialSlots) > 16 || permissions["credentials.read"] != (len(m.CredentialSlots) > 0) {
		return errors.New("凭据绑定需要 credentials.read 权限，最多 16 项")
	}
	slots := map[string]bool{}
	for _, slot := range m.CredentialSlots {
		if !idPattern.MatchString(slot.ID) || strings.TrimSpace(slot.Title) == "" || len([]rune(slot.Title)) > 64 || slots[slot.ID] {
			return errors.New("凭据槽名称无效或重复")
		}
		slots[slot.ID] = true
	}
	if len(m.Jobs) > 20 || len(m.Jobs) > 0 && !permissions["jobs.run"] {
		return errors.New("定时任务需要 jobs.run 权限，最多声明 20 项")
	}
	ids := map[string]bool{}
	webhook := false
	for _, job := range m.Jobs {
		webhook = webhook || job.Webhook
		if !idPattern.MatchString(job.ID) || strings.TrimSpace(job.Title) == "" || len([]rune(job.Title)) > 64 || ids[job.ID] {
			return errors.New("定时任务的 ID 或标题无效")
		}
		ids[job.ID] = true
		steps := map[string]bool{}
		if len(job.Steps) > 10 {
			return errors.New("工作流最多 10 个步骤")
		}
		for _, step := range job.Steps {
			if !idPattern.MatchString(step) || steps[step] {
				return errors.New("工作流步骤名称无效或重复")
			}
			steps[step] = true
		}
		keys := map[string]bool{}
		if len(job.Params) > 8 {
			return errors.New("每个任务最多声明 8 个参数")
		}
		for _, field := range job.Params {
			if !envPattern.MatchString(field.Key) || keys[field.Key] || field.Secret || field.Type == "password" || len(field.Default) > 512 || strings.ContainsRune(field.Default, 0) {
				return errors.New("任务参数无效，凭据请通过插件设置提供")
			}
			if err := validateEnvField(field); err != nil {
				return err
			}
			keys[field.Key] = true
		}
	}
	if webhook != permissions["jobs.webhook"] || webhook && !slices.ContainsFunc(m.Env, func(field Env) bool {
		return field.Key == webhookSecretKey && (field.Secret || field.Type == "password") && field.Required && field.Default == ""
	}) {
		return errors.New("Webhook 任务需要 jobs.webhook 权限及无默认值的必填秘密字段 WEBHOOK_SECRET")
	}
	events := map[string]bool{}
	for _, name := range m.Events {
		permission, ok := eventPermissions[name]
		if !ok || events[name] || !permissions["events.subscribe"] || !permissions[permission] {
			return errors.New("事件无效、重复或缺少订阅权限：" + name)
		}
		events[name] = true
	}
	if m.SiteAction != nil && (!permissions["sites.read"] || strings.TrimSpace(m.SiteAction.Title) == "" || len([]rune(m.SiteAction.Title)) > 64) {
		return errors.New("网站设置入口需要有效标题及 sites.read 权限")
	}
	if m.FileAction != nil && (!permissions["fs.read"] || strings.TrimSpace(m.FileAction.Title) == "" || len([]rune(m.FileAction.Title)) > 64) {
		return errors.New("文件菜单需要有效标题及 fs.read 权限")
	}
	if widget := m.Dashboard; permissions["dashboard.widget"] != (widget != nil) || widget != nil && (strings.TrimSpace(widget.Title) == "" || len([]rune(widget.Title)) > 64) {
		return errors.New("概览卡片需要有效标题及 dashboard.widget 权限")
	}
	if provider := m.NotificationProvider; permissions["notifications.provider"] != (provider != nil) || provider != nil && (strings.TrimSpace(provider.Title) == "" || len([]rune(provider.Title)) > 64) {
		return errors.New("通知渠道需要有效标题及 notifications.provider 权限")
	}
	if provider := m.DNSProvider; permissions["acme.dns"] != (provider != nil) || provider != nil && (strings.TrimSpace(provider.Title) == "" || len([]rune(provider.Title)) > 64) {
		return errors.New("DNS 验证需要有效标题及 acme.dns 权限")
	}
	if permissions["services.provide"] != (len(m.Services) > 0) || len(m.Services) > 20 {
		return errors.New("插件服务需要 services.provide 权限，最多声明 20 项")
	}
	serviceNames := map[string]bool{}
	for _, name := range m.Services {
		if !idPattern.MatchString(name) || serviceNames[name] {
			return errors.New("插件服务名称无效或重复")
		}
		serviceNames[name] = true
	}
	if permissions["monitoring.provide"] != (len(m.Checks) > 0) || len(m.Checks) > 20 {
		return errors.New("自定义检测需要 monitoring.provide 权限，最多声明 20 项")
	}
	checkIDs := map[string]bool{}
	for _, check := range m.Checks {
		if !idPattern.MatchString(check.ID) || strings.TrimSpace(check.Title) == "" || len([]rune(check.Title)) > 64 || checkIDs[check.ID] {
			return errors.New("自定义检测名称无效或重复")
		}
		checkIDs[check.ID] = true
	}
	if permissions["backup.storage"] != (m.BackupProvider != nil) {
		return errors.New("backup.storage 权限与 backupProvider 必须同时声明")
	}
	if m.BackupProvider != nil {
		return validateBackupProvider(*m.BackupProvider)
	}
	return nil
}

func hasCapabilities(m Manifest) bool {
	return slices.ContainsFunc(resourceScopes, func(scope resourceScope) bool { return scope.declared(m) }) ||
		slices.Contains(m.Permissions, "notifications.send") || len(m.Jobs) > 0 || len(m.Events) > 0 || len(m.CredentialSlots) > 0
}

func (m *Module) grant(id string) (capabilityGrant, error) {
	var out capabilityGrant
	var row store.Setting
	err := m.db.First(&row, "key = ?", grantPrefix+id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return out, nil
	}
	if err != nil {
		return out, err
	}
	return out, json.Unmarshal([]byte(row.Value), &out)
}

func (m *Module) capabilities(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	var mf Manifest
	grant, err := m.grant(p.ID)
	if err != nil || json.Unmarshal([]byte(p.Manifest), &mf) != nil {
		response.Fail(c, response.System, "无法读取插件授权")
		return
	}
	scopes := []gin.H{}
	for _, scope := range resourceScopes {
		if !scope.declared(mf) {
			continue
		}
		var declaredPerms, disabledPerms []string
		for _, perm := range scope.Permissions {
			if slices.Contains(mf.Permissions, perm) {
				declaredPerms = append(declaredPerms, perm)
				if grant.permissionDisabled(perm) {
					disabledPerms = append(disabledPerms, perm)
				}
			}
		}
		options, err := m.resourceCatalog(c.Request.Context(), scope.Kind)
		scopes = append(scopes, gin.H{
			"kind":                scope.Kind,
			"label":               scope.Label,
			"options":             options,
			"unavailable":         err != nil,
			"disabled":            len(declaredPerms) > 0 && len(disabledPerms) == len(declaredPerms),
			"disabledPermissions": disabledPerms,
		})
	}
	var channels []alerts.PluginChannel
	if slices.Contains(mf.Permissions, "notifications.send") {
		channels, err = m.services.Alerts.PluginChannels(c.Request.Context())
		if err != nil {
			response.Fail(c, response.System, "无法读取通知渠道")
			return
		}
	}
	var rows []store.Task
	if err := m.db.Where("module = ?", "plugin:"+p.ID).Order("started_at DESC").Limit(20).Find(&rows).Error; err != nil {
		response.Fail(c, response.System, "无法读取插件任务")
		return
	}
	history := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		history = append(history, task.Summary(row))
	}
	deliveries := []eventDelivery{}
	if len(mf.Events) > 0 {
		if err := m.db.Where("plugin_id = ?", p.ID).Order("created_at DESC, id DESC").Limit(20).Find(&deliveries).Error; err != nil {
			response.Fail(c, response.System, "无法读取事件投递记录")
			return
		}
	}
	if len(mf.CredentialSlots) > 0 {
		var bindings []credentialBinding
		if err := m.db.Where("plugin_id = ?", p.ID).Find(&bindings).Error; err != nil {
			response.Fail(c, response.System, "无法读取插件凭据授权")
			return
		}
		grant.Credentials = map[string]string{}
		for _, binding := range bindings {
			grant.Credentials[binding.Slot] = binding.CredentialID
		}
	}
	response.OK(c, gin.H{"grant": grant, "scopes": scopes, "channels": channels, "credentialSlots": mf.CredentialSlots,
		"notifications": slices.Contains(mf.Permissions, "notifications.send"), "jobs": mf.Jobs, "events": mf.Events,
		"tasks": history, "deliveries": deliveries, "running": p.Enabled && m.status(p) == "running",
		"disabledPermissions": grant.DisabledPermissions})
}

func (m *Module) saveCapabilities(c *gin.Context) {
	var in capabilityGrant
	if !response.Bind(c, &in) {
		return
	}
	id := c.Param("id")
	release, err := m.tasks.Acquire("plugin:" + id)
	if err != nil {
		response.Fail(c, response.Conflict, "插件忙")
		return
	}
	defer release()
	p, ok := m.row(c)
	if !ok {
		return
	}
	if p.Operation != nil {
		response.Fail(c, response.Conflict, "插件存在未恢复的操作记录")
		return
	}
	var mf Manifest
	if json.Unmarshal([]byte(p.Manifest), &mf) != nil {
		response.Fail(c, response.System, "无法读取插件清单")
		return
	}
	existing, err := loadTransitionGrant(m.db, id)
	if err != nil {
		response.Fail(c, response.System, "无法读取插件当前授权")
		return
	}
	in.DisabledPermissions = slices.Clone(existing.DisabledPermissions)
	if err := m.validateGrant(c, mf, &in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	if err := validateGrantPermissions(mf, existing, in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	if err := m.saveGrants(id, mf, in); err != nil {
		response.Fail(c, response.Invalid, err.Error())
		return
	}
	m.replaceSchedules(id, mf, in)
	response.OK(c, nil)
}

func validateGrantPermissions(mf Manifest, existing, in capabilityGrant) error {
	for _, scope := range resourceScopes {
		var declared, disabled int
		for _, perm := range scope.Permissions {
			if slices.Contains(mf.Permissions, perm) {
				declared++
				if existing.permissionDisabled(perm) {
					disabled++
				}
			}
		}
		if declared > 0 && disabled == declared {
			wasAll := slices.Contains(existing.AllResources, scope.Kind)
			isAll := slices.Contains(in.AllResources, scope.Kind)
			oldIDs := slices.Clone(*existing.resourceIDs(scope.Kind))
			slices.Sort(oldIDs)
			newIDs := *in.resourceIDs(scope.Kind)
			if (isAll || len(newIDs) > 0) && (isAll != wasAll || !slices.Equal(oldIDs, newIDs)) {
				return errors.New(scope.Label + "授权对应的能力权限已全部关闭，请先在「能力」页开启")
			}
		}
	}
	if existing.permissionDisabled("credentials.read") {
		for slot, credID := range in.Credentials {
			if credID != "" && credID != existing.Credentials[slot] {
				return errors.New(permissionDeniedMessage("credentials.read", true))
			}
		}
	}
	if existing.permissionDisabled("notifications.send") && in.ChannelID != "" && in.ChannelID != existing.ChannelID {
		return errors.New(permissionDeniedMessage("notifications.send", true))
	}
	for _, ev := range in.Events {
		if slices.Contains(existing.Events, ev) {
			continue
		}
		if existing.permissionDisabled("events.subscribe") {
			return errors.New(permissionDeniedMessage("events.subscribe", true))
		}
		if perm := eventPermissions[ev]; perm != "" && existing.permissionDisabled(perm) {
			return errors.New(permissionDeniedMessage(perm, true))
		}
	}
	if existing.permissionDisabled("jobs.run") {
		for jobID, expr := range in.Schedules {
			if expr != "" && expr != existing.Schedules[jobID] {
				return errors.New(permissionDeniedMessage("jobs.run", true))
			}
		}
	}
	return nil
}

func (m *Module) validateGrant(c *gin.Context, mf Manifest, in *capabilityGrant) error {
	slices.Sort(in.AllResources)
	in.AllResources = slices.Compact(in.AllResources)
	for _, kind := range in.AllResources {
		if !slices.ContainsFunc(resourceScopes, func(scope resourceScope) bool {
			return scope.Kind == kind && scope.declared(mf)
		}) {
			return errors.New("插件未声明该资源类别：" + kind)
		}
	}
	for _, scope := range resourceScopes {
		ids := in.resourceIDs(scope.Kind)
		if slices.Contains(in.AllResources, scope.Kind) {
			*ids = nil
			continue
		}
		if len(*ids) == 0 {
			continue
		}
		if !scope.declared(mf) {
			return errors.New("插件未声明" + scope.Label + "权限")
		}
		slices.Sort(*ids)
		*ids = slices.Compact(*ids)
		rows, err := m.resourceCatalog(c.Request.Context(), scope.Kind)
		for _, id := range *ids {
			if !slices.ContainsFunc(rows, func(row resource) bool { return row.ID == id }) {
				if err != nil {
					return errors.New("暂时无法校验" + scope.Label + "授权")
				}
				return errors.New(scope.Label + "授权包含无效资源")
			}
		}
	}
	if in.ChannelID != "" {
		if !slices.Contains(mf.Permissions, "notifications.send") {
			return errors.New("插件未声明通知权限")
		}
		channels, err := m.services.Alerts.PluginChannels(c.Request.Context())
		if err != nil || !slices.ContainsFunc(channels, func(ch alerts.PluginChannel) bool { return ch.ID == in.ChannelID }) {
			return errors.New("请选择已启用的通知渠道")
		}
	}
	slices.Sort(in.Events)
	in.Events = slices.Compact(in.Events)
	for _, event := range in.Events {
		if !slices.Contains(mf.Events, event) {
			return errors.New("插件未声明该事件：" + event)
		}
	}
	for id, expression := range in.Schedules {
		expression = strings.TrimSpace(expression)
		if expression == "" {
			delete(in.Schedules, id)
			continue
		}
		index := slices.IndexFunc(mf.Jobs, func(job Job) bool { return job.ID == id })
		if index < 0 {
			return errors.New("插件未声明该计划任务：" + id)
		}
		if slices.ContainsFunc(mf.Jobs[index].Params, func(field Env) bool { return field.Required && field.Default == "" }) {
			return errors.New("定时执行需要为必填参数声明默认值：" + id)
		}
		if err := m.services.Cron.ValidatePluginSchedule(expression); err != nil {
			return fmt.Errorf("%s：%w", id, err)
		}
		in.Schedules[id] = expression
	}
	return nil
}

func (m *Module) runJob(c *gin.Context) {
	p, ok := m.row(c)
	if !ok {
		return
	}
	var input struct {
		Params map[string]string
	}
	if c.Request.ContentLength != 0 && !response.Bind(c, &input) {
		return
	}
	id, err := m.submitJob(p.ID, c.Param("job"), auth.Current(c).User.Username, m.auth.ClientIP(c), "", "manual", input.Params)
	if err != nil {
		response.Fail(c, response.Conflict, err.Error())
		return
	}
	c.Set("panel.taskSubmitted", true)
	response.OK(c, gin.H{"taskId": id})
}

func (m *Module) siteActions(c *gin.Context) {
	var rows []store.Plugin
	if m.db.Where("enabled = ?", true).Find(&rows).Error != nil {
		response.Fail(c, response.System, "无法读取插件入口")
		return
	}
	out := []gin.H{}
	for _, p := range rows {
		var mf Manifest
		if m.status(p) != "running" || json.Unmarshal([]byte(p.Manifest), &mf) != nil || mf.SiteAction == nil {
			continue
		}
		grant, err := m.grant(p.ID)
		if err == nil && !grant.permissionDisabled("sites.read") && grant.allowsResource("sites", c.Param("site")) {
			out = append(out, gin.H{"pluginId": p.ID, "title": mf.SiteAction.Title, "version": p.Version, "embed": mf.SiteAction.Embed})
		}
	}
	response.OK(c, out)
}
