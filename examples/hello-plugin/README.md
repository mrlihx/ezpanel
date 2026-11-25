Hello 插件仅演示可信插件。只申请 sysinfo.read，后端通过 SDK 调用同一 Host sysinfo 快照；不申请虚假的 exec/fs 权限。示例不得用于不可信代码隔离。
请先按 docs/plugin-dev.md 构建并打包，再通过管理页 inspect/install/config/enable。不会自动安装或启用，不从包中执行安装钩子。
