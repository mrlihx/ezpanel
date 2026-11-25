#!/bin/bash
# 只读取声明文件并生成 ZIP，不运行包内程序或安装钩子。
set -Eeuo pipefail
[[ $# == 1 ]] || { printf '用法：pack-plugin.sh <插件包目录>\n' >&2; exit 1; }
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
package=$(cd -- "$1" && pwd)
cd -- "$repo"
go run ./cmd/plugin-pack "$package"
