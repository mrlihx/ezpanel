# 目录实时保护

该包通过 BPF LSM 在文件创建、写入、删除、重命名和权限修改前判定策略。用户空间只负责编译排除模式和切换策略；不通过扫描后删除文件模拟拦截。

- Linux 需要启用 BPF LSM、Security Path hooks、BTF，并具备 `bpf_path_d_path` kfunc（Linux 6.12 起提供）。实际加载与所有挂钩成功后才能认为保护生效。
- `New` 只读取内核能力与已有固定映射，不挂接新程序。`Sync` 用一个 map-of-maps 指针原子切换完整策略。
- BPF links 及映射固定在 `/sys/fs/bpf/ezpanel_hardening_<安装目录摘要>`，面板进程退出后仍生效；`Sync(nil)` 清空策略并移除固定对象。`Close` 仅释放进程句柄。
- 保护目录与其现有挂载点合计最多 32 个，每个目录最多 16 个排除模式，每个模式最多 255 字节。目录路径最多 1023 字节；访问路径缓冲为 Linux PATH_MAX（4096）。模式编译后的自动机最多 512 个状态，超限明确报错。
- `*`、`?` 不跨目录，`**` 是独立路径段；无 `/` 的模式按任意层级名称匹配；匹配到目录后整个子树排除。`uploads/**` 匹配 `uploads` 本身，未来目录可以创建。`[]` 支持 ASCII 字符组，中文可直接写入模式，`?` 按 UTF-8 字符匹配。
- 已开启的共享可写 mmap 不会被追溯撤销；新增共享可写 mmap、已有只读 mmap 改为可写会被拦截。硬链接创建会检查源、目标两侧，重命名也会阻止移动保护目录的祖先目录。策略按解析后的路径匹配，不宣称能抵御拥有完整 root/BPF 管理权限的攻击者。

`bpf/guard.o` 为与 CPU 架构无关的小端 CO-RE 对象，适用于面板支持的 Linux amd64/arm64。用户服务器无需编译器。修改 C 程序后，在带 `clang`、`libbpf-dev`、`bpftool` 的 Linux 构建机运行 `bash bpf/generate.sh`；生成用的 `vmlinux.h` 仅存在临时目录。

本机纯 Go 匹配测试：`go test ./internal/hardening/dirguard`。专用 Linux 测试环境：`EZPANEL_TEST_DIRECTORY_LSM=1 go test ./internal/hardening/dirguard -run TestNativeDirectoryPolicies -v`，所有保护对象为自动清理的临时目录。
