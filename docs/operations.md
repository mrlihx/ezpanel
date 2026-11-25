# 安装与运维

## 本地构建与自建发布源

本地搭建可用 `make build` 构建两个 Linux 架构的面板，并生成 `SHA256SUMS`。安装器也接受管理员明确信任的 HTTPS 发布目录。

```bash
sudo bash scripts/install.sh install --release-dir ./dist
sudo bash scripts/install.sh install --release-url https://your-release-host/path
```

官方 Release 安装会核验签名并检查 SHA-256。本地或自建发布源使用 SHA-256 校验，清单来源由操作者确认。

## 安全设计

- **访问入口**：全新安装默认监听 `0.0.0.0:<随机端口>`，首次安装生成随机安全入口和强密码（只在交互终端显示一次）。`ezpanel external-access --mode off` 可切回回环监听。
- **Origin 与入口保护**：内置 SourceGuard 校验浏览器 `Origin`，安全入口校验签发限时 `HttpOnly` Cookie；业务 API、WebSocket 与插件路由均强制认证。
- **凭据加密**：数据库密码、SSH 私钥、私有仓库凭据等敏感信息均采用 **AES-GCM** 加密存储；查看密码接口强制附带 `Cache-Control: no-store`。
- **网络与文件系统边界**：
  - `pathsafe` 保护系统关键目录免遭误删或路径穿越；
  - 计划任务 URL 与文件远程下载均拦截云元数据（`169.254.169.254`、`100.100.100.200`、`fd00:ec2::254`）及本机面板管理端口；
  - `nftables` 防火墙通过 `docker_guard` 覆盖默认 `docker0`、面板托管网桥 `ezpanel0` 以及 Compose `br-*` 桥接网络。
- **真实活动续期**：仅真实用户操作才延长空闲会话，后台监控轮询不会导致会话永不过期。

---

## TLS、反向代理与运维命令

### 配置文件示例（`/opt/ezpanel/data/config.yaml`，权限 `0600`）

```yaml
listenAddr: 127.0.0.1
port: 18081
publicUrl: https://panel.example.com
trustedProxies:
  - 127.0.0.1/32
# 原生 TLS 时填写证书链与私钥路径；由前置反向代理终止 TLS 时保持为空
tlsCert: ""
tlsKey: ""
timezone: Asia/Shanghai
```

- 经 HTTPS 反向代理访问时，请确保代理透传 `Host`、`X-Forwarded-Host`、`X-Forwarded-Proto`、`X-Forwarded-For` 以及 WebSocket 所需 `Upgrade` / `Connection` 请求头，并仅将实际代理地址写入 `trustedProxies`。

### 常用 CLI 运维命令

安装器将命令安装到 `/usr/local/bin/ezpanel`。默认数据目录为 `/opt/ezpanel`，配置相关命令可附加 `--install-dir <路径>`。

在终端直接运行 `ezpanel` 可打开运维菜单。输入数字并回车执行对应操作，`?` 重新显示菜单，`0` 退出。以 root 登录时直接运行 `ezpanel`，普通用户可用 `sudo ezpanel` 执行管理操作。

外网直连开关可在菜单选择，也可使用 `ezpanel external-access --mode on` 或 `--mode off`。它切换非回环网卡监听；独立反向代理的公网入口由反向代理管理。面板升级沿用已有监听设置。

```bash
sudo ezpanel
sudo ezpanel info
sudo ezpanel status
sudo ezpanel start       # 停止和重启分别使用 stop 和 restart
sudo ezpanel logs --follow
sudo ezpanel ready
sudo ezpanel reset-port --port 18080
sudo ezpanel reset-entry                 # 随机生成，也可指定 --entry
sudo ezpanel reset-username --username admin_new
sudo ezpanel reset-password              # 交互式输入，旧会话失效
sudo ezpanel unlock-login --ip 192.0.2.10
ezpanel help
ezpanel help reset-port
```

同一来源 IP 连续输错 5 次后锁定 1 小时。改入口或端口时，运行中的 systemd 面板会重启。通过反向代理访问时，改端口后请同步调整代理的上游端口。

---

## 升级、回滚与卸载

日常升级直接使用 [README 的一键部署命令](../README.md#一键部署)。版本相同时，可以选择仅重装程序，保留面板配置、数据库、插件和业务文件。需要固定发布时，可通过 `--version` 指定标签。

```bash
# 升级前检查活动任务与运行状态
sudo ezpanel upgrade-check

# 固定版本升级（自动在 .snapshots/ 保存旧二进制与 data/ 快照，失败自动回滚）
sudo bash ezpanel-install.sh upgrade --version v0.1.1-r2

# 指定快照手动回滚，将下面的目录名替换为实际快照目录名
sudo bash ezpanel-install.sh rollback --snapshot /opt/ezpanel/.snapshots/SNAPSHOT_NAME

# 卸载面板（默认仅移除服务与二进制，保留 data/、apps/ 与业务数据）
sudo bash ezpanel-install.sh uninstall

# 彻底清理面板私有数据与插件（仍始终保留网站、数据库、应用容器与业务备份）
sudo bash ezpanel-install.sh uninstall --purge
```

---
