# 可信插件开发（SDK 1 / Host v1）

插件前端在管理员页面执行，拥有当前管理员访问能力。后端与其他 process 插件共用 panel-plugin 用户、可自行联网并访问该用户有权访问的文件；降权不构成插件间隔离。fsRoots/execAllow 只约束面板提供的 root 特权入口，允许的程序及参数仍可能访问其他资源。仅安装可信来源插件。

## 版本与清单

源码构建显示 `0.0.0-dev`，兼容核心为 `0.0.0`。示例 minPanelVersion 为 `0.0.0`，没有伪造 `1.0.0` 面板版本。正式发布注入严格 x.y.z。插件 version/minPanelVersion 不接受前导零、预发布或构建后缀；最低版本和主版本均检查，SDK 当前必须为整数 1。

清单字段和约束见 [manifest.go](../internal/plugin/manifest.go)，可从[示例清单](../examples/hello-plugin/plugin.yaml)开始。清单拒绝未知字段、重复键、类型隐式转换与 YAML 别名；env default 必须为字符串。static 不允许后端声明。UI 可改为 web 下任意实际 .js 文件；菜单图标只接受基座白名单，未知值回退。后端入口需覆盖目标 Linux 架构，不依赖 ZIP 执行位。

## 构建示例

在仓库根目录执行（Node 24 LTS、Go；所有命令仅编译/打包，不启动插件）：

```bash
npm --prefix examples/hello-plugin/frontend ci
npm --prefix examples/hello-plugin/frontend run build
mkdir -p examples/hello-plugin/package/bin/linux-amd64 examples/hello-plugin/package/bin/linux-arm64
(cd examples/hello-plugin/server && GOOS=linux GOARCH=amd64 go build -trimpath -o ../package/bin/linux-amd64/server .)
(cd examples/hello-plugin/server && GOOS=linux GOARCH=arm64 go build -trimpath -o ../package/bin/linux-arm64/server .)
cp examples/hello-plugin/plugin.yaml examples/hello-plugin/README.md examples/hello-plugin/package/
scripts/pack-plugin.sh examples/hello-plugin/package
```

输出位于 examples/hello-plugin/hello-plugin-0.1.0.zip，已存在同名文件时拒绝覆盖。打包工具只包含 plugin.yaml、声明入口、web/ 全部普通文件与可选 README.md，不读取安装钩子、不执行包内程序。web 不应放源代码、密钥或其他不准备公开的文件。限制为 200MiB ZIP、500MiB 实际文件总量、200MiB 单文件、5000 文件；安装 inspect 再按解压真实字节校验。打包的目录/命令授权检查是构建机本地预检，目标面板 inspect 仍重新确认目标机器真实路径与授权范围。

示例前端仅使用现有固定版本 React 18.3.1、ReactDOM 18.3.1、Ant Design 5.29.3、icons 5.6.1，与基座 SDK1 模板锁文件兼容。不得动态补入第二套 React。前端 postinstall 写入 node_modules/go.mod 作为 Go 递归扫描边界，不是插件安装钩子。

## 前端契约

Vite 必须输出浏览器 ESM、base `./`；react、react/jsx-runtime、react/jsx-dev-runtime、react-dom、react-dom/client、antd、@ant-design/icons 必须 external，不导入基座内部源码路径。基座 HTML 在模块脚本前注册实际 `/shared/v1/*.js` importmap，先发布 `globalThis.__PANEL_SHARED__` 再加载插件，桥接模块静态命名导出均来自基座同一实例。

默认导出 React 组件，接收 sdk/ts/panel-sdk.d.ts 的 `PanelSDK`。message/modal 的类型仅取公开 App.useApp 返回值，不使用 AntD 内部文件。sdk.api 仅接受插件相对 API 路径，自动加来源检查头和会话，解包 data；失败为带 code 的错误，支持传入 AbortSignal。sdk.navigate 只跳转本插件子路径，不创建第二个 BrowserRouter。

web/entry.css 为可选入口样式；示例显式输出该文件。分块、图片、字体使用内容哈希与包内相对引用，不输出绝对 /assets 路径。所有样式限定在 `[data-panel-plugin="hello-plugin"]` 等插件自己的根节点内，禁止覆盖 html/body/全局 AntD。卸载由基座移除已挂载的该版本样式，重入会恢复已记录的动态分块样式；不要依赖全局 CSS 副作用。主题变化通过 sdk.dark 重渲染，组件仍在主应用 ConfigProvider/App 内。

所有订阅、定时器、事件监听、AbortController 应在 effect 清理。可选导出 dispose() 清理模块常驻资源，须能重复安全调用。ErrorBoundary 只处理可捕获渲染错误，不隔离无限循环、事件/异步错误或全局样式。重启相同版本不会保证清空浏览器 ESM 缓存，正式包同一版本不可替换内容。

## Go SDK 与 Host

sdk/go 是只使用标准库的独立本地 Go module；示例 server/go.mod 的 v0.0.0 + replace 只指向仓库 SDK，并非远程发布版本。`panelsdk.New()` 返回实例与环境校验错误，HandleFunc 注册 /api/ 路由，Run/RunContext 回环监听、健康检查、代理 token 校验和优雅关闭。所有 health/API 请求都校验每次启动的 X-Panel-Proxy-Token，API 同时要求面板覆盖的 pluginId/user 头，不能认为任意本机请求就是管理员。

固定环境由面板提供：PANEL_PLUGIN_ID/PORT/DATA/HOST_URL/PLUGIN_TOKEN/PROXY_TOKEN。SDK 不打印这些值。Host token 与代理 token 独立，每次启动轮换。HostClient 提供 Info/KVGet/KVSet/KVList/KVDelete/Exec/FSRead/FSWrite/FSMkdir/FSDelete/Sysinfo/Log，所有调用需要 context。Exec 返回 ExecResult 和 error，必须分别读取业务 APIError.Code 与进程 ExitCode，非零进程退出仍能读取输出/截断信息，禁止拼接 Shell。

Host 请求限额 20rps/20burst；KV 1000项、单值64KiB；文件仅 UTF-8/2MiB，写默认0600、支持0000-0777；exec 默认30s、最高300s、合计输出1MiB。Host 授权按 token 身份，不能传自报 pluginId 操作其他命名空间。启动期只有 info 可用，其余 Host API 须等 running；health 不应依赖启动前特权调用或进行不可逆迁移。

stdout/stderr 5MiB×3，只输出脱敏业务摘要；不要打印 token/env/配置、文件正文或秘密参数。基座可以脱敏已知配置秘密，无法识别插件自行产生的一切秘密。日志按完整行处理，超过64KiB的单行整体丢弃。Host 审计仅保存动作/目标元数据。

## 安装、升级与调试

通过管理页上传 ZIP → inspect → 查看清单、权限与信任边界 → 确认安装；安装完成为 disabled，不加载 JS。配置完必填项后点击确认启用。卸载时在确认弹窗中勾选是否清理插件私有数据即可完成。secret 在详情中仅返回 isSet，省略保留、显式空串清空；升级 secret 属性变化必须重新显式填写。

升级先检查包，展示新增/扩大授权及新必填值，确认后才停旧。快照只覆盖旧包/配置/KV/停止后的私有数据；新版失败完整恢复，不能回滚外部数据库、文件或网络副作用。启动/health 必须无不可逆外部迁移，本阶段不支持带此类迁移的自动升级。私有目录有 socket/设备等不可复制对象时快照会拒绝，不继续替换。

Linux 正式 process 使用 panel-plugin 非 root UID/GID、清空附加组、固定 env/HOME/cwd、独立进程组；用户不存在时启用失败。非 Linux 仅 --dev，不降权，也不假装能运行 Linux 二进制；本机 dev 包应额外声明当前 os-arch 入口。无论 dev 或正式都走 inspect/install，不直接向 plugins/ 放目录绕过检查。插件自己的 Vite 开发服务器可单独用于开发，但不构成面板正式包热更新或绕过鉴权承诺。

## 当前验证边界

本轮只完成编译、类型/lint、正式前端构建和静态打包检查，没有安装、启用、Host/代理、崩溃重试、升级回滚、降权或浏览器生命周期实测。基座与示例通过现有 TS 配置对公共 SDK 实际赋值检查；额外全依赖声明检查会命中锁定 AntD/rc-picker 自身声明与 React18 的冲突，未改依赖或增加跳过检查选项。不得把构建成功称为 React 单例、主题、OS 降权或业务恢复已实测。
