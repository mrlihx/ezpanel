#!/usr/bin/env bash
# 在带有 clang、libbpf-dev、bpftool 和内核 BTF 的 Linux 构建机运行。
set -euo pipefail
cd "$(dirname "$0")"
build_dir=$(mktemp -d)
trap 'rm -rf "$build_dir"' EXIT
bpftool btf dump file /sys/kernel/btf/vmlinux format c >"$build_dir/vmlinux.h"
clang -target bpf -O2 -g -Wno-missing-declarations -I"$build_dir" -I"/usr/include/$(gcc -dumpmachine)" -c guard.c -o guard.o
