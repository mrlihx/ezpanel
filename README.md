# EzPanel

基于 Go 和 React 的 Linux 服务器运维面板。通过网页管理网站、数据库、Docker 编排、文件和系统服务。前端随 Go 二进制一起分发。

[![Build and Release](https://github.com/mrlihx/ezpanel/actions/workflows/build.yml/badge.svg)](https://github.com/mrlihx/ezpanel/actions/workflows/build.yml)
[![GPL-3.0](https://img.shields.io/badge/License-GPL--3.0-blue)](LICENSE)

## 一键部署

支持 Linux amd64、arm64，推荐 Ubuntu 22.04+ 或 Debian 12+。需要 systemd、Bash、curl、OpenSSL 和 root 权限。Docker 可按需安装。

新安装和以后升级都用同一条命令。在目标服务器的交互终端运行

```bash
curl -fsSL https://raw.githubusercontent.com/mrlihx/ezpanel/main/scripts/install.sh -o ezpanel-install.sh \
  && sudo bash ezpanel-install.sh
```

已经以 root 登录时可省略 `sudo`。脚本优先选择最新正式版，尚无正式版时使用最新[预览版](https://github.com/mrlihx/ezpanel/releases)。安装器识别当前架构，并在运行二进制前验证签名和 SHA-256。

首次安装时，请记录终端显示的密码、监听端口和安全入口。默认目录为 `/opt/ezpanel`。已有实例发现新版本时，脚本会提示确认并先创建备份。版本相同时，可输入 `REINSTALL` 仅重装程序，保留系统服务、面板配置和所有数据。

### 首次访问

全新安装默认监听 `0.0.0.0` 和一个随机 TCP 端口。用浏览器打开终端给出的外网直连地址，把 `<服务器公网IP>` 换成实际公网 IP。有云安全组或系统防火墙时，需允许该 TCP 端口。需要找回端口和入口时，在服务器运行 `sudo ezpanel info`。

首次直连使用 HTTP。可按[运维文档](docs/operations.md#tls反向代理与运维命令)配置 HTTPS。

## 终端运维

以 root 登录后输入 `ezpanel`，选数字并回车即可操作，`0` 退出。常用子命令

| 命令 | 用途 |
| --- | --- |
| `ezpanel info` | 查看面板地址、账号和版本 |
| `ezpanel status` | 查看服务状态 |
| `ezpanel start` / `ezpanel stop` / `ezpanel restart` | 管理服务 |
| `ezpanel logs --follow` | 跟踪面板日志 |
| `ezpanel external-access --mode on` / `--mode off` | 开启或关闭外网直连 |
| `ezpanel help` | 查看命令、参数和中文说明 |

## 主要功能

- **网站**｜PHP、Go、Python、Node.js 与反向代理，支持 SSL 证书、日志和站点 WAF。
- **数据库**｜管理 MySQL、PostgreSQL、MongoDB、Redis 和 SQLite，按引擎提供备份、恢复或数据浏览。
- **容器与应用**｜管理 Docker 容器、镜像和网络，支持 Compose 编排、应用商店和多实例。
- **系统工具**｜在线终端、文件管理、防火墙、进程监控、计划任务、告警和操作审计。

## 文档

- [安装、HTTPS 反向代理、升级与回滚](docs/operations.md)
- [源码构建、架构与自动发布](docs/development.md)
- [插件开发](docs/plugin-dev.md)
- [所有版本和下载](https://github.com/mrlihx/ezpanel/releases)

## 许可证

EzPanel 采用 [GPL-3.0](LICENSE)。每个 Release 为对应二进制提供源码归档和第三方依赖许可说明。
