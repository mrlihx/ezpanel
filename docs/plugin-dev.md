# EzPanel 插件开发指南（SDK 1 / Host v1）

EzPanel 插件分为 `static`（纯前端）与 `process`（前端 + 独立后端进程）两类。新安装插件默认启用**前端隔离**（`iframe` 沙箱）与 **Linux 进程沙箱**（独立 UID/GID + `systemd-run`，声明 `exec.run` 的兼容包除外）；高风险 Host 权限在安装后**默认关闭**，由管理员按需在应用商店「设置 → 能力」中开启。

> 仅安装可信来源插件。源码构建核心版本为 `0.0.0-dev`（兼容核心 `0.0.0`），正式发布使用 `x.y.z`。

---

## 1. 5 分钟快速开始

### 1.1 最小 `plugin.yaml`

纯前端插件（省略 `type`、`ui`、`sdkVersion`、`minPanelVersion`、`permissions` 均使用默认值）：

```yaml
id: my-static-plugin
name: 我的静态插件
version: 0.1.0
author: your-name
menu:
  title: 我的插件
  icon: AppstoreOutlined
  order: 100
```

带 Go 后端的进程插件（包含 `entry` 时自动识别为 `type: process`）：

```yaml
id: my-plugin
name: 我的插件
version: 0.1.0
author: your-name
homepage: https://example.com
entry:
  linux-amd64: bin/linux-amd64/server
  linux-arm64: bin/linux-arm64/server
menu:
  title: 我的插件
  icon: AppstoreOutlined
  order: 100
permissions: [sysinfo.read]
```

完整示例见 [EzPanel 演示插件](../examples/ezpanel-demo-plugin/plugin.yaml)。

### 1.2 构建与打包

#### 推荐：一键构建并打包（`build-plugin.sh`）

在仓库根目录执行（Node 24 LTS、Go）：

```bash
scripts/build-plugin.sh -f examples/ezpanel-demo-plugin
```

`scripts/build-plugin.sh` 会按约定目录自动完成全部流程并输出 `<id>-<version>.zip`：
1. 若存在 `frontend/package.json`，自动安装依赖（缺少 `node_modules` 时）并执行 `npm run build`；
2. 自动收集前端产物（按 `package/web` → `web` → `frontend/dist` 顺序）；
3. 若存在 `server/go.mod`，自动按 `plugin.yaml` 中 `entry` 声明交叉编译全部架构（`CGO_ENABLED=0`）；
4. 调用 `scripts/pack-plugin.sh` 完成清单校验与打 ZIP。

支持参数：`-f` / `--force`（覆盖同名 ZIP）、`-o <输出路径>`、`--dev`（允许仅含当前平台架构入口）。

#### 手动编译 + 纯打包（`pack-plugin.sh`）

若插件使用非 Go 语言或在 CI 中已编好产物，可手动构建后直接调用 `scripts/pack-plugin.sh` 校验并打 ZIP（不执行任何包内命令或构建脚本）：

```bash
# 1. 构建前端（此示例输出 package/web/entry.js）
npm --prefix examples/ezpanel-demo-plugin/frontend ci
npm --prefix examples/ezpanel-demo-plugin/frontend run build

# 2. 编译 Linux 双架构后端
mkdir -p examples/ezpanel-demo-plugin/package/bin/linux-amd64 examples/ezpanel-demo-plugin/package/bin/linux-arm64
(cd examples/ezpanel-demo-plugin/server && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -o ../package/bin/linux-amd64/server .)
(cd examples/ezpanel-demo-plugin/server && CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath -o ../package/bin/linux-arm64/server .)

# 3. 打包 ZIP
cp examples/ezpanel-demo-plugin/plugin.yaml examples/ezpanel-demo-plugin/README.md examples/ezpanel-demo-plugin/package/
scripts/pack-plugin.sh -f examples/ezpanel-demo-plugin/package
```

打包限制：ZIP ≤ 200 MiB、解压总字节 ≤ 500 MiB、单文件 ≤ 200 MiB、文件数 ≤ 5000；仅打包 `plugin.yaml`、声明入口、`web/` 下普通文件与可选 `README.md`，不执行包内代码或安装钩子。

### 1.3 导入、升级与启停

1. 在面板「应用商店 → 导入扩展应用」上传 ZIP（面板自动检视清单；若插件已安装则自动进入升级流程）。
2. 查看权限、风险提示与授权变化后确认安装/升级；**新安装插件默认为未启用状态**。
3. 前往插件「设置 → 配置」填写必填项 → 「设置 → 能力」按需开启权限 → 「设置 → 授权」配置资源范围 → 「设置 → 服务 → 服务控制」点击启动。

---

## 2. `plugin.yaml` 字段速查

清单校验见 [manifest.go](../internal/plugin/manifest.go)：拒绝未知字段、重复键、类型隐式转换与 YAML 别名；`env.default` 必须为字符串；`static` 禁止后端声明。

### 基础与运行字段

| 字段 | 必填 | 默认值 | 说明与约束 |
| --- | --- | --- | --- |
| `id` | 是 | - | `^[a-z][a-z0-9-]{2,31}$`，不可使用 `api`/`ui`/`host`/`panel`/`plugins` |
| `name` | 是 | - | 1–64 字符 |
| `version` | 是 | - | 严格 `x.y.z`（无前导零/预发布后缀） |
| `description` | 否 | `""` | ≤ 512 字符 |
| `author` | 否 | `""` | ≤ 128 字符 |
| `homepage` | 否 | `""` | 作者主页，仅允许 `http://` 或 `https://`，设置页可点击跳转 |
| `type` | 否 | 有 `entry` 为 `process`，否则 `static` | `static` 或 `process` |
| `minPanelVersion` | 否 | `"0.0.0"` | 严格 `x.y.z`，校验主版本一致且面板版本 ≥ 该值 |
| `sdkVersion` | 否 | `1` | 整数 `1` |
| `ui` | 否 | `"web/entry.js"` | 必须为 `web/` 下存在的 `.js` 文件 |
| `openMode` | 否 | `"embed"` | `embed`（面板框架内打开）或 `new_tab`（浏览器新标签页打开，仍保留侧栏） |
| `entry` | 进程插件必填 | - | 需包含 `linux-amd64` / `linux-arm64` 相对路径，不依赖 ZIP 执行位 |
| `permissions` | 否 | `[]` | 声明所需 Host 权限（见第 4 节） |
| `fsRoots` | 声明 `fs.*` 时必填 | `[]` | 限制 `Host` 文件读写根目录 |
| `execAllow` | 声明 `exec.run` 时必填 | `[]` | 兼容模式特权命令绝对路径白名单（禁止 Shell/解释器跳板） |
| `dependencies` | 否 | `{}` | 依赖的其他插件及最低版本，最多 20 项，如 `{ shared-storage: "1.2.0" }` |

> 开发者**无需**在 `plugin.yaml` 填写 `uiMode`。安装时默认开启前端隔离，管理员可在「设置 → 服务 → 服务控制」自由切换「前端隔离」开关（升级/回滚自动保留管理员选择）。旧版 `settings`（`modal`/`drawer`/`none`、`width`/`height`）继续兼容解析。

### 左侧菜单与资源菜单

```yaml
menu:
  title: 网盘工具
  icon: folder
  order: 10
  children:
    - {title: 文件列表, path: files}
    - {title: 同步历史, path: history}
permissions: [sites.read, containers.read, databases.read, certificates.read]
resourceActions:
  sites: 检查当前网站
  containers: 检查当前容器
  databases: 检查当前数据库
  certificates: 检查当前证书
```

- 省略 `children` 时为单级入口；二级菜单使用相对路径（最多 16 项），组件通过 `sdk.route` 读取子路由。菜单图标仅接受面板白名单，未知值回退。
- 资源菜单通过 `sdk.context?.resource` 传入 `{ kind, id, name, engine }`。MySQL / PostgreSQL / MongoDB 的 `id` 为实例或连接 ID、`name` 为库名；SQLite 的 `id` 为文件登记 ID。

### 配置项 `env` 与 Linux 沙箱

- **`env` 字段**：`key`（`^[A-Z][A-Z0-9_]{0,63}$`）、`label`、`default`（字符串）、`required`、`secret`、`type`（`text` / `password` / `number` / `boolean` / `select` / `file` / `directory`）、`options`、`help`。支持 JSON 导入/导出（导出自动省略 `secret`）。
- **Linux 进程沙箱**（`type: process` 默认启用；声明 `exec.run` 的兼容模式不可设置 `sandbox`）：

```yaml
sandbox:
  cpuPercent: 100   # 0-1600，省略或 0 默认 100（一核）
  memoryMiB: 256    # 16-16384，默认 256
  maxProcesses: 64  # 8-1024，默认 64
  network: loopback # loopback（默认）或 outbound
```

---

## 3. 前端开发与 `PanelSDK`

类型定义见 [panel-sdk.d.ts](../sdk/ts/panel-sdk.d.ts)。

### 构建约束
- Vite 输出浏览器 ESM，设置 `base: './'`。
- 必须 external：`react`、`react/jsx-runtime`、`react/jsx-dev-runtime`、`react-dom`、`react-dom/client`、`antd`、`@ant-design/icons`（与面板固定版本 React 18.3.1 / Ant Design 5.29.3 / icons 5.6.1 共享，不得打包第二套 React）。
- 样式请限定在 `[data-panel-plugin="<id>"]` 根节点下，`web/entry.css` 为可选入口样式；分块/字体/图片使用包内相对路径。
- 在 `effect` 中清理定时器/订阅/`AbortController`；可导出 `dispose()` 供卸载清理。

### `PanelSDK` 接口速查

| 属性/方法 | 说明 |
| --- | --- |
| `sdk.pluginId` / `sdk.dark` / `sdk.route` | 插件 ID、当前暗色主题、当前子路由 |
| `sdk.context` | 上下文：`siteId`、`filePaths`、`resource` |
| `sdk.api<T>(path, init?)` | 请求插件后端 `/api/*`（自动带会话与校验头并解包 `data`；支持 `AbortSignal`；失败抛出含 `code` 的 `PanelAPIError`） |
| `sdk.navigate(path)` | 跳转本插件子路由（勿自建 `BrowserRouter`） |
| `sdk.pickFile(initialPath?)` / `sdk.pickDirectory(initialPath?)` | 打开面板文件/目录选择器，取消返回 `null` |
| `sdk.pickResource(kind)` | 选择授权范围内的 `sites` / `containers` / `databases` / `certificates` / `channels` |
| `sdk.showTask(taskId)` | 打开面板任务日志抽屉 |
| `sdk.message` / `sdk.modal` | 来自 `App.useApp()` 的消息与弹窗实例 |

> 隔离模式与原生嵌入模式使用同一套 `PanelSDK`：隔离模式下插件运行在沙箱 `iframe`，无法直接读取主页面 DOM 或直接 `fetch` 面板 API，请始终通过 `sdk.api` 与上述 SDK 方法交互。

---

## 4. Go SDK 与 Host API

Go SDK 模块为 `github.com/mrlihx/ezpanel/sdk/go`（仅依赖标准库）：

```sh
go get github.com/mrlihx/ezpanel/sdk/go/panelsdk@v0.1.0
```

最小后端示例：

```go
p, err := panelsdk.New()
if err != nil {
    log.Fatal(err)
}
p.HandleFunc("GET /api/overview", func(w http.ResponseWriter, r *http.Request) {
    info, err := p.Host.Sysinfo(r.Context())
    if err != nil {
        panelsdk.WriteError(w, err) // 透传 Host 错误（含明确的缺权说明）
        return
    }
    panelsdk.WriteJSON(w, info)
})
log.Fatal(p.Run())
```

### 权限开关、高风险默认关闭与缺权提示
- **高风险权限默认关闭**：`exec.run`、`fs.write`、`databases.restore`、`backups.restore`、`backup.storage`、`credentials.read` 在安装（或升级新增）后**默认关闭**。
- **独立开关与授权联动**：管理员可在「设置 → 能力」随时开关任意声明权限（即时生效）；关闭某权限后，「设置 → 授权」页对应的资源范围、凭据、通知、事件或任务配置会同步禁用。
- **明确缺权报错**：当插件调用被关闭或未声明的 Host API 时，Host 返回 HTTP 403 与明确中文错误（例如：`缺少「写入和删除文件」（fs.write）权限：该权限已在「应用商店 → 插件设置 → 能力」中关闭，请开启后重试`）。**插件后端应透传 Host 返回的 `APIError` 给前端展示**，避免吞掉错误信息。

### 运行限额与生命周期规范
- **鉴权**：`panelsdk` 自动校验 `X-Panel-Proxy-Token` 与面板注入的 `pluginId`/`user` 头；固定环境变量由面板注入，切勿打印 token 或秘密配置。
- **限额**：Host API 限流 20 rps / 20 burst；KV 最多 1000 项、单值 64 KiB；普通文件读写限 UTF-8 / 2 MiB（流式上传限 100 MiB）；`exec` 默认超时 30s（最高 300s）、总输出 1 MiB；日志单文件 5 MiB × 3、单行超过 64 KiB 丢弃。
- **启动期**：`health` 检查阶段仅 `/host/v1/info` 可用，其余 Host API 需等待插件进入 `running` 状态后调用。

### Host API 权限对照表

| 权限码 | Host 路径（前缀 `/host/v1`） | Go SDK | 说明 |
| --- | --- | --- | --- |
| `sysinfo.read` | `GET /sysinfo` | `Host.Sysinfo` | 读取 CPU/内存/磁盘/网络概况 |
| `fs.read` | `GET /fs/stat`、`list`、`hash`、`read`、`stream` | `Host.FSStat` / `FSList` / `FSHash` / `FSRead` / `FSOpen` / `FSOpenRange` | 读取 `fsRoots` 下文件与流（`stream` 支持 `offset`/`limit` 增量读取，单次 ≤ 50 MiB，超出时须用 `FSOpenRange` 分段读取） |
| `fs.write` *(高风险)* | `POST /fs/write`、`mkdir`、`delete`、`PUT /fs/stream` | `Host.FSWrite` / `FSMkdir` / `FSDelete` / `FSUpload` | 写入或删除 `fsRoots` 下文件 |
| `exec.run` *(高风险)* | `POST /exec` | `Host.Exec` | 兼容模式执行 `execAllow` 白名单命令 |
| `sites.read` | `GET /resources/sites`、`GET /sites/:id` | `Host.Sites`、`Host.SiteDetail` | 读取授权网站列表与详情 |
| `sites.logs.read` | `GET /sites/:id/logs` | `Host.SiteLogs` | 读取授权网站日志末尾 |
| `sites.control` / `sites.restart` | `POST /sites/:id/control`、`restart` | `Host.ControlSite`、`Host.RestartSite` | 启停或重启授权网站 |
| `containers.read` | `GET /resources/containers` | `Host.Containers` | 读取授权容器列表 |
| `containers.logs.read` / `metrics.read` | `GET /containers/:id/logs`、`stats` | `Host.ContainerLogs`、`Host.ContainerStats` | 读取容器日志与指标 |
| `containers.control` | `POST /containers/:id/control` | `Host.ControlContainer` | 启停或重启授权容器 |
| `databases.read` | `GET /resources/databases` | `Host.Databases` | 读取授权数据库实例与连接 |
| `databases.backup` | `GET /databases/:id/names`、`POST /databases/:id/backup` | `Host.DatabaseNames`、`CreateDatabaseBackup` | 列出库并提交 MySQL/MariaDB 备份 |
| `databases.restore` *(高风险)* | `GET /databases/:id/backups`、`POST /databases/:id/restore` | `Host.DatabaseBackups`、`RestoreDatabaseBackup` | 恢复授权数据库备份 |
| `certificates.read` / `bind` / `renew` | `GET /resources/certificates`、`POST /sites/:id/certificate`、`POST /certificates/:id/renew` | `Host.Certificates`、`BindSiteCertificate`、`RenewCertificate` | 查看、绑定与续签证书 |
| `backups.read` / `create` / `verify` | `GET /resources/backups`、`GET /sites/:id/backups`、`POST /sites/:id/backup`、`verify` | `Host.Backups`、`SiteBackupHistory`、`CreateSiteBackup`、`VerifySiteBackup` | 读取、创建与校验网站备份 |
| `backups.restore` *(高风险)* | `POST /sites/:id/backups/:backup/restore` | `Host.RestoreSiteBackup` | 站点停止时恢复网站备份 |
| `compose.read` / `deploy` | `GET /resources/compose`、`POST /compose/:name/deploy` | `Host.ComposeProjects`、`Host.DeployCompose` | 读取与部署 Compose 项目 |
| `apps.read` / `control` | `GET /resources/apps`、`GET /apps/:id`、`POST /apps/:id/control` | `Host.Applications`、`AppStatus`、`ControlApp` | 查看与控制面板应用 |
| `monitoring.read` | `GET /monitoring/history` | `Host.MonitoringHistory` | 读取最近一小时监控采样 |
| `security.read` / `hardening.scan` | `GET /security`、`POST /security/scan` | `Host.SecurityStatus`、`Host.ScanHardening` | 读取安全概况与发起加固扫描 |
| `notifications.send` | `POST /notify` | `Host.Notify` | 通过授权渠道发通知（1 次/分钟） |
| `credentials.read` *(高风险)* | `GET /credentials/:slot` | `Host.Credential` | 读取绑定的外部凭据明文 |
| `services.call` | `POST /services/:id/:name` | `Host.CallService` | 调用已声明依赖插件的服务 |
| `audit.read` | `GET /audit` | `Host.AuditHistory` | 读取本插件操作审计 |
| 自有状态与任务 | `/info`、`/log`、`/kv*`、`/tasks/:id` | `Host.Info` / `Log` / `KV*` / `Task` | 无需额外声明（`process` 插件自带） |

---

## 5. 扩展能力指南

### 5.1 定时任务与 Webhook

```yaml
permissions: [jobs.run]
jobs:
  - id: backup-audit
    title: 巡检网站备份
    steps: [check-env, run-backup, send-notice] # 可选：顺序工作流，每步 ≤ 2 分钟，总计 ≤ 10 分钟
```

- 通过 `p.OnJob(func(ctx context.Context, job panelsdk.JobInvocation) (string, error) { ... })` 注册一次回调，按 `job.JobID` 与 `job.Step` 分发，`Host.JobProgress(ctx, job.TaskID, percent, message)` 写入进度日志。
- 任务可通过 `params` 声明最多 8 个非秘密参数；Cron 调度由管理员在「设置 → 授权」配置。
- **Webhook 触发**：在任务上设置 `webhook: true`，同时声明 `jobs.webhook` 权限与无默认值的必填密码字段 `WEBHOOK_SECRET`（≥ 32 字节）。外部调用 `POST /plugin-webhooks/<插件ID>/<任务ID>`，携带 `X-Panel-Request: 1`、`X-Ezpanel-Timestamp` 与 HMAC-SHA256 `X-Ezpanel-Signature`（签名原文：`路径\n时间戳\n原始请求体`）。

### 5.2 事件订阅

```yaml
permissions: [events.subscribe, backups.read]
events: [site.backup.finished]
```

通过 `p.OnEvent(func(ctx context.Context, event panelsdk.Event) error { ... })` 接收事件（按 `event.ID` 去重，单次 10 秒超时，最多重试 5 次，历史保留 7 天）：

| 事件名 | 触发时机 | 所需前置权限 |
| --- | --- | --- |
| `site.backup.finished` | 网站备份完成 | `backups.read` |
| `certificate.renewal.finished` | 证书续期完成 | `certificates.read` |
| `site.project.finished` | 站点 Node/Python/自启任务完成 | `sites.read` |
| `compose.task.finished` | Compose 任务完成 | `compose.read` |
| `database.backup.finished` | MySQL/MariaDB 备份完成 | `databases.backup` |

### 5.3 面板界面扩展入口

- **网站设置入口**：声明 `sites.read` 与 `siteAction: { title: "备份巡检", embed: true }`（`embed: true` 嵌入为网站设置标签页），前端通过 `sdk.context?.siteId` 获取站点 ID。
- **文件菜单**：声明 `fs.read`、`fsRoots` 与 `fileAction: { title: "检查文件", multiple: true }`，前端通过 `sdk.context?.filePaths` 读取选中路径。
- **概览卡片**：声明 `dashboard.widget` 与 `dashboard: { title: "文件巡检" }`，面板每 60 秒请求插件 `GET /api/dashboard`（返回 `{code: 0, data: {value, description}}`）。

### 5.4 渠道、服务与钩子扩展

- **备份存储渠道**（高风险，`backup.storage` + `backupProvider`）：实现 `panelsdk.BackupStorage`（`Test` / `Upload` / `Open` / `Delete`）并通过 `p.OnBackupStorage` 注册。
- **通知渠道**（`notifications.provider` + `notificationProvider`）：通过 `p.OnNotification` 接收 `ProviderNotice` 并投递（15 秒超时）。
- **ACME DNS 验证渠道**（`acme.dns` + `dnsProvider`）：通过 `p.OnDNSChange` 处理 TXT 记录 `present` / `remove`。
- **外部凭据**（高风险，`credentials.read` + `credentialSlots`）：使用 `Host.Credential(ctx, slot)` 读取绑定明文。
- **OIDC 单点登录**（`ssoProvider: { title: "..." }`，`static` 插件亦可声明）：在面板「单点登录」配置签发者与客户端密钥。
- **插件间服务**：提供方声明 `services.provide` + `services` 并注册 `p.OnService`；调用方声明 `services.call` + `dependencies` 并调用 `Host.CallService`（请求 ≤ 16 KiB，响应 ≤ 32 KiB，超时 20s）。
- **自定义监控检测**（`monitoring.provide` + `checks`）：注册 `p.OnMonitorCheck`（10 秒超时）。
- **Compose 部署钩子**（`deployment.hooks`）：注册 `p.OnDeployment`，按插件 ID 顺序在部署前后执行（前置失败中止部署，单次 10s 超时）。

---

## 6. 离线签名与常见问题排查

### 离线 Ed25519 签名（可选）
管理员可将公钥放到安装目录 `plugins/.trusted-keys/<插件ID>.pub`（目录 `0700`、公钥 `0600`），发布方按以下方式生成 64 字节 `.sig`：

```bash
openssl genpkey -algorithm Ed25519 -out signing.key
openssl pkey -in signing.key -pubout -out my-plugin.pub
openssl dgst -sha256 -binary -out my-plugin.digest my-plugin.zip
openssl pkeyutl -sign -rawin -inkey signing.key -in my-plugin.digest -out my-plugin.sig
```

### 常见问题排查

| 现象或报错 | 原因与处理 |
| --- | --- |
| `清单格式无效或含未知/重复字段` | 检查是否写了不支持的字段、重复键或将数字/布尔直接作为 `env.default`（需加引号转为字符串） |
| `缺少当前架构入口` | 打包 formal ZIP 需提供 `linux-amd64` / `linux-arm64`；本机调试打包可加 `--dev` |
| `隔离插件不能声明特权命令` | 声明了 `sandbox` 时不能使用 `exec.run` / `execAllow`，请在插件进程内直接调用所需程序 |
| 运行时报 `缺少「...」（...）权限` | 高风险权限安装后默认关闭，或管理员在「设置 → 能力」关闭了该权限，开启即可即时恢复 |
| 「授权」页某项无法勾选/保存 | 对应的前置能力权限在「设置 → 能力」被关闭，请先开启对应能力权限 |
| 历史版本回滚 | 在「设置 → 服务 → 服务控制 → 恢复历史版本」切换，面板保留最近 1 份完整快照（含包、配置、KV 与私有数据） |
