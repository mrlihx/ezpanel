# EzPanel演示插件（ezpanel-demo-plugin）

- **插件 ID**：`ezpanel-demo-plugin`
- **当前版本**：`0.2.1`
- **作者**：EzPanel
- **作者主页**：<https://github.com/mrlihx/ezpanel>

本插件是遵循 **EzPanel SDK 1 / Host v1** 规范构建的官方风格全能力 Demo 插件，集中展示了清单配置、隔离前端、Linux systemd 沙箱、全部 Host v1 API、面板回调接口、应用商店设置页签映射以及四大扩展渠道。

> **关于运行模式说明**：本插件采用推荐的隔离页面（`uiMode: isolated`，面板同时仍兼容 `legacy` 原生嵌入模式）。面板主布局已通过 `--panel-content-height` 自适应独立插件页高度，插件内部弹窗使用作用域包装配合自动换行样式，避免日志长行溢出。

## 能力覆盖矩阵

| 类别 | 展示能力 | 实现位置 |
| --- | --- | --- |
| **清单与运行环境** | `uiMode: isolated`、`homepage`、`openMode: embed`（支持 `embed`/`new_tab`）、Linux `sandbox`（CPU/内存/进程数/loopback）、二级菜单 `menu.children` | `plugin.yaml` |
| **设置控件类型** | 完整覆盖 `text`、`password`（`secret: true`）、`number`、`boolean`、`select`、`file`、`directory` | `plugin.yaml`、`server/main.go` |
| **前端 PanelSDK** | `sdk.api`（含 `AbortSignal` 与二进制正文）、`sdk.navigate`、`sdk.context`（`siteId`/`filePaths`/`resource`）、`pickFile`/`pickDirectory`/`pickResource`、`showTask`、`message`、`modal`、`dark`、懒加载分块与 `dispose()` | `frontend/src/entry.tsx`、`frontend/src/views/*` |
| **入口与扩展视图** | 概览卡片（`GET /api/dashboard`）、网站设置内嵌页（`siteAction.embed: true`）、文件多选菜单（`fileAction.multiple: true`）、四大资源菜单（`resourceActions`） | `plugin.yaml`、`server/handlers_core.go` |
| **Host 核心能力** | `Host.Info`、`Host.KV*`（List/Get/Set/Delete）、`Host.Log`、`Host.Sysinfo`（含负载与磁盘进度）、`Host.MonitoringHistory`（近 1 小时明细） | `server/handlers_core.go`、`frontend/src/views/OverviewView.tsx` |
| **资源管控 API** | 网站（列表/详情/日志筛选/启停/重启/备份/历史/校验/恢复）、容器（列表/日志/采样/启停重启）、数据库（列表/库名/备份/历史/恢复）、证书（列表/绑定含强制 HTTPS/续签）、Compose（列表/部署）、系统应用（列表/状态/启停重启重载） | `server/handlers_resources.go`、`frontend/src/views/ResourcesView.tsx` |
| **受控文件 API** | `FSList`、`FSStat`、`FSHash`、`FSRead`、`FSWrite`、`FSMkdir`、`FSDelete`、`FSOpen`、`FSUpload`（限定 `fsRoots: [/tmp]`） | `server/handlers_files.go`、`frontend/src/views/FilesView.tsx` |
| **任务与 Webhook** | `p.OnJob`（参数化任务 `quick-inspect` + 多步骤工作流 `staged-workflow`）、`Host.JobProgress`、HMAC-SHA256 防重放 Webhook | `server/main.go`、`frontend/src/views/AutomationView.tsx` |
| **事件与通知** | `p.OnEvent`（订阅全部 5 类事件并按 `event.ID` 幂等去重）、`Host.Notify` | `server/state.go`、`server/main.go` |
| **面板扩展渠道** | 备份存储渠道（`p.OnBackupStorage`）、通知渠道（`p.OnNotification`）、ACME DNS 渠道（`p.OnDNSChange`）、Compose 部署钩子（`p.OnDeployment`）、自定义监控探针（`p.OnMonitorCheck`）、OIDC 登录入口（`ssoProvider`） | `server/storage.go`、`server/main.go`、`frontend/src/views/ProvidersView.tsx` |
| **凭据、安全与审计** | `Host.Credential`（脱敏指纹展示）、`Host.SecurityStatus`、`Host.ScanHardening`、`Host.AuditHistory`、`Host.Task` | `server/handlers_extensions.go`、`frontend/src/views/SecurityView.tsx` |
| **插件间服务提供** | `services.provide`（`echo-service`、`summary-service`）与 `p.OnService` | `server/main.go` |

> **说明**：Linux 新装插件默认启用 `sandbox` 进程沙箱，与兼容模式的 `exec.run` / `execAllow` 互斥；本插件采用现代沙箱模式，因此不声明 `exec.run`。若需演示兼容模式特权桥接，须移除 `sandbox` 声明。

## 构建与打包

在当前目录执行：

```bash
./build.sh
```

脚本将依次完成：
1. 安装前端依赖（共享 `react` / `react-dom` / `antd` / `@ant-design/icons` 均设为 external）并执行 `tsc --noEmit && vite build`；
2. 交叉编译 `linux-amd64` 与 `linux-arm64` 静态后端二进制；
3. 调用面板仓库 `scripts/pack-plugin.sh`（使用 `internal/plugin.ReadManifest` 进行严格清单与目录校验）生成 `ezpanel-demo-plugin-0.2.1.zip`。

## 安装与使用指引（应用商店设置窗口）

1. **上传与安装**：在 EzPanel「应用商店 → 导入扩展应用」上传 `ezpanel-demo-plugin-0.2.1.zip`，核对清单权限、风险提示与沙箱边界并确认安装。
2. **「设置 → 服务」**：查看运行状态、版本、插件 ID 与作者主页链接（`https://github.com/mrlihx/ezpanel`），执行启用、停用、重启、更新或卸载。
3. **「设置 → 配置」**：根据字段说明填写必填项 `WEBHOOK_SECRET`（至少 32 字节随机字符串，例如 `openssl rand -hex 24`）及其他 7 种类型演示配置项（支持 JSON 导入/导出）。
4. **「设置 → 授权」**：
   - 网站、容器、数据库、证书、Compose 项目与系统应用均可分别配置「未授权」「全部」或「指定」授权范围；
   - 选择已启用的通知渠道，并将外部凭据绑定到 `demo-api-key` 槽位；
   - 勾选需要订阅的面板事件，并为 `quick-inspect` / `staged-workflow` 手动传参运行或设置 Cron。
5. **「设置 → 能力 / 日志」**：在「能力」页签核验权限矩阵，在「日志」页签实时查看后端脱敏输出。
6. **测试 Webhook 触发**：

```bash
TS=$(date +%s)
PATH_STR="/plugin-webhooks/ezpanel-demo-plugin/staged-workflow"
BODY='{"params":{"NOTE":"webhook-test"}}'
SIG=$(printf '%s\n%s\n%s' "$PATH_STR" "$TS" "$BODY" | openssl dgst -sha256 -hmac "$WEBHOOK_SECRET" -hex | awk '{print $NF}')
curl -X POST "https://YOUR_PANEL$PATH_STR" \
  -H "Content-Type: application/json" \
  -H "X-Panel-Request: 1" \
  -H "X-Ezpanel-Timestamp: $TS" \
  -H "X-Ezpanel-Signature: $SIG" \
  -d "$BODY"
```

## 跨插件服务调用示例

由于单个插件不能声明依赖自身，本插件扮演服务提供方（`services: [echo-service, summary-service]`）。另一个插件可在清单中声明依赖与权限后通过 `Host.CallService` 调用：

```yaml
dependencies:
  ezpanel-demo-plugin: "0.2.1"
permissions:
  - services.call
```

```go
var out map[string]any
err := p.Host.CallService(ctx, "ezpanel-demo-plugin", "echo-service", map[string]string{"hello": "world"}, &out)
```

## 离线签名发布（可选）

```bash
openssl genpkey -algorithm Ed25519 -out signing.key
openssl pkey -in signing.key -pubout -out ezpanel-demo-plugin.pub
openssl dgst -sha256 -binary -out ezpanel-demo-plugin.digest ezpanel-demo-plugin-0.2.1.zip
openssl pkeyutl -sign -rawin -inkey signing.key -in ezpanel-demo-plugin.digest -out ezpanel-demo-plugin.sig
```
