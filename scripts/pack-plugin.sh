#!/bin/bash
# 只读取声明文件并生成 ZIP，不运行包内程序或安装钩子。
set -Eeuo pipefail
[[ $# -ge 1 ]] || { printf '用法：pack-plugin.sh [-f|--force] [-o <输出路径>] [--dev] <插件包目录>\n' >&2; exit 1; }
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
cd -- "$repo"
go run ./cmd/plugin-pack "$@"
