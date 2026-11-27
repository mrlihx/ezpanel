package plugin

import (
	"fmt"
	"maps"
	"slices"
	"sort"
)

const (
	highRisk  = "high"
	attention = "attention"
)

type permissionInfo struct {
	Label string `json:"label"`
	Scope string `json:"scope"`
	Risk  string `json:"risk,omitempty"`
}

// 清单校验和安装提示共用此表，新增权限时同时写明用途和范围。
var pluginPermissions = map[string]permissionInfo{
	"exec.run":                {"执行特权命令", "以面板服务权限执行白名单内的命令，影响取决于命令及参数。", highRisk},
	"fs.read":                 {"读取文件", "可读取下方授权目录的文件内容，包括其中的配置或业务数据。", attention},
	"fs.write":                {"写入和删除文件", "可在下方授权目录创建、覆盖或删除文件。", highRisk},
	"sysinfo.read":            {"读取服务器概况", "读取 CPU、内存、磁盘及网络地址等系统概况。", ""},
	"sites.read":              {"读取网站配置", "可读取管理员指定网站的概要和运行配置摘要。", ""},
	"sites.logs.read":         {"读取网站日志", "可读取指定网站的日志，日志可能包含业务数据。", ""},
	"sites.restart":           {"重启网站项目", "可重启指定网站的项目进程，影响服务可用性。", ""},
	"sites.control":           {"启停网站", "可启用或停用指定网站，影响访问。", ""},
	"containers.read":         {"读取容器列表", "可读取本机容器的名称、镜像和运行状态。", ""},
	"containers.logs.read":    {"读取容器日志", "可读取指定容器的日志，日志可能包含业务数据。", ""},
	"containers.metrics.read": {"读取容器指标", "可读取指定容器的资源使用指标。", ""},
	"containers.control":      {"管理容器运行状态", "可启动、停止或重启指定容器，影响服务可用性。", ""},
	"databases.read":          {"读取数据库实例概要", "可读取已纳管数据库实例和远程连接的概要。", ""},
	"databases.backup":        {"备份数据库", "可列出指定 MySQL 或 MariaDB 实例中的库并创建备份。", ""},
	"databases.restore":       {"恢复数据库", "可提交指定实例的数据库恢复，可能覆盖现有数据。", highRisk},
	"certificates.read":       {"读取证书概要", "可查看证书域名、签发来源和到期时间。", ""},
	"certificates.bind":       {"绑定网站证书", "可更改指定网站的证书和 HTTPS 设置。", ""},
	"certificates.renew":      {"续签证书", "可续签指定证书并更新现有绑定。", ""},
	"backups.read":            {"读取网站备份记录", "可读取指定网站的备份记录。", ""},
	"backups.create":          {"创建网站备份", "可对指定网站创建备份。", ""},
	"backups.verify":          {"校验网站备份", "可读取指定网站的归档并校验完整性。", ""},
	"backups.restore":         {"恢复网站备份", "可用备份替换已停止网站的当前文件。", highRisk},
	"backup.storage":          {"提供备份存储渠道", "渠道被选用后，插件会接触交给该渠道处理的备份归档。", highRisk},
	"notifications.send":      {"发送通知", "可通过管理员选定的通知渠道发送消息。", ""},
	"notifications.provider":  {"提供通知渠道", "渠道被选用后，插件会接收发送目标和通知内容。", ""},
	"jobs.run":                {"执行插件任务", "支持手动执行，管理员设置计划后可自动执行。", ""},
	"jobs.webhook":            {"允许 Webhook 触发任务", "启用后，持有签名凭据的外部调用者可触发指定插件任务。", ""},
	"events.subscribe":        {"订阅面板事件", "接收管理员授予的面板事件。", ""},
	"dashboard.widget":        {"提供概览入口", "在面板概览展示该插件的入口。", ""},
	"services.call":           {"调用其他插件服务", "可调用清单中已声明依赖的插件服务。", ""},
	"services.provide":        {"提供插件服务", "向声明依赖的其他插件提供清单中列出的服务。", ""},
	"compose.read":            {"读取 Compose 项目", "可查看面板纳管的 Compose 项目概要。", ""},
	"compose.deploy":          {"部署 Compose 项目", "可执行指定项目的部署，可能重建容器或中断运行中的服务。", attention},
	"deployment.hooks":        {"参与 Compose 部署", "可在指定项目部署前后执行回调，前置回调失败会阻止部署。", attention},
	"monitoring.read":         {"读取监控历史", "可读取面板近期的资源指标。", ""},
	"monitoring.provide":      {"提供监控检查", "可把声明的检查项目加入面板监控。", ""},
	"apps.read":               {"读取应用状态", "可查看应用的安装和运行状态。", ""},
	"apps.control":            {"管理应用运行状态", "可启停、重启或重载指定应用。", ""},
	"security.read":           {"读取安全状态", "可查看防火墙、WAF 和系统加固概要。", ""},
	"hardening.scan":          {"执行系统加固检查", "可按面板当前规则发起一次扫描。", ""},
	"audit.read":              {"读取插件操作审计", "可读取本插件的操作记录。", ""},
	"acme.dns":                {"提供 ACME DNS 验证", "选用该渠道签发证书时，插件负责创建和清理验证记录。", ""},
	"credentials.read":        {"读取外部凭据", "可读取管理员绑定到该插件的凭据明文。", highRisk},
}

func validPermission(code string) bool {
	_, ok := pluginPermissions[code]
	return ok
}

func permissionLabel(code string) string {
	if spec, ok := pluginPermissions[code]; ok && spec.Label != "" {
		return spec.Label
	}
	return code
}

func permissionDeniedMessage(code string, disabledByAdmin bool) string {
	label := permissionLabel(code)
	if disabledByAdmin {
		return fmt.Sprintf("缺少「%s」（%s）权限：该权限已在「应用商店 → 插件设置 → 能力」中关闭，请开启后重试", label, code)
	}
	return fmt.Sprintf("缺少「%s」（%s）权限：插件未在清单中声明该权限", label, code)
}

func defaultDisabledPermissions(m Manifest) []string {
	var disabled []string
	for _, code := range m.Permissions {
		if spec, ok := pluginPermissions[code]; ok && spec.Risk == highRisk {
			disabled = append(disabled, code)
		}
	}
	sort.Strings(disabled)
	return slices.Compact(disabled)
}

type previewPermission struct {
	Code    string `json:"code"`
	Enabled *bool  `json:"enabled,omitempty"`
	permissionInfo
}

type previewCapability struct {
	Label string   `json:"label"`
	Items []string `json:"items"`
}

type permissionPreview struct {
	Permissions  []previewPermission `json:"permissions"`
	Capabilities []previewCapability `json:"capabilities"`
	RuntimeRisks []permissionInfo    `json:"runtimeRisks"`
}

func previewManifest(m Manifest) permissionPreview {
	return previewManifestWithDisabled(m, nil, false)
}

func previewManifestWithDisabled(m Manifest, disabled []string, includeEnabled bool) permissionPreview {
	preview := permissionPreview{
		Permissions:  make([]previewPermission, 0, len(m.Permissions)),
		Capabilities: []previewCapability{}, RuntimeRisks: []permissionInfo{},
	}
	for _, code := range m.Permissions {
		spec, ok := pluginPermissions[code]
		if !ok {
			spec = permissionInfo{code, "当前面板无法说明此权限，请核对插件兼容性。", attention}
		}
		item := previewPermission{Code: code, permissionInfo: spec}
		if includeEnabled {
			enabled := !slices.Contains(disabled, code)
			item.Enabled = &enabled
		}
		preview.Permissions = append(preview.Permissions, item)
	}
	add := func(label string, items ...string) {
		if len(items) > 0 {
			preview.Capabilities = append(preview.Capabilities, previewCapability{label, items})
		}
	}
	if m.Menu != nil {
		items := []string{m.Menu.Title}
		for _, child := range m.Menu.Children {
			items = append(items, m.Menu.Title+" / "+child.Title)
		}
		add("侧边菜单", items...)
	}
	if m.FileAction != nil {
		add("文件菜单", m.FileAction.Title)
	}
	if m.SiteAction != nil {
		add("网站设置入口", m.SiteAction.Title)
	}
	for _, group := range []struct{ key, title string }{{"sites", "网站操作"}, {"containers", "容器操作"}, {"databases", "数据库操作"}, {"certificates", "证书操作"}} {
		if title := m.ResourceActions[group.key]; title != "" {
			add(group.title, title)
		}
	}
	for _, entry := range []struct {
		label string
		value *ExtensionTitle
	}{
		{"概览入口", m.Dashboard}, {"通知渠道", m.NotificationProvider}, {"ACME DNS 验证渠道", m.DNSProvider}, {"单点登录渠道", m.SSOProvider},
	} {
		if entry.value != nil {
			add(entry.label, entry.value.Title)
		}
	}
	if m.BackupProvider != nil {
		add("备份存储渠道", m.BackupProvider.Name)
	}
	if m.SSOProvider != nil {
		preview.RuntimeRisks = append(preview.RuntimeRisks, permissionInfo{"提供单点登录渠道", "管理员选用该渠道后，关联的身份提供商将参与面板登录。", highRisk})
	}
	jobs := make([]string, 0, len(m.Jobs))
	for _, job := range m.Jobs {
		label := job.Title
		if job.Webhook {
			label += "（支持 Webhook）"
		}
		jobs = append(jobs, label)
	}
	add("插件任务", jobs...)
	add("可订阅事件", m.Events...)
	add("对外提供的插件服务", m.Services...)
	if slices.Contains(m.Permissions, "deployment.hooks") {
		add("部署钩子", "Compose 部署前后回调")
	}
	checks := make([]string, 0, len(m.Checks))
	for _, check := range m.Checks {
		checks = append(checks, check.Title)
	}
	add("监控检查", checks...)
	credentials := make([]string, 0, len(m.CredentialSlots))
	for _, slot := range m.CredentialSlots {
		credentials = append(credentials, slot.Title)
	}
	add("可绑定的凭据", credentials...)
	dependencies := make([]string, 0, len(m.Dependencies))
	for _, id := range slices.Sorted(maps.Keys(m.Dependencies)) {
		dependencies = append(dependencies, id+" ≥ "+m.Dependencies[id])
	}
	add("依赖插件", dependencies...)

	if m.UIMode == "legacy" {
		preview.RuntimeRisks = append(preview.RuntimeRisks, permissionInfo{"前端原生模式", "关闭前端隔离后，插件页面与面板同源运行。", attention})
	}
	if m.Type == "process" {
		if m.Sandbox == nil {
			preview.RuntimeRisks = append(preview.RuntimeRisks, permissionInfo{"后端兼容模式", "兼容进程可以访问其运行用户有权读取或写入的资源。Linux 上的兼容插件共用该用户。", attention})
		} else if m.Sandbox.Network == "outbound" {
			preview.RuntimeRisks = append(preview.RuntimeRisks, permissionInfo{"允许出站网络", "插件进程可以连接外部服务，并发送其有权读取的数据。", attention})
		}
	}
	return preview
}
