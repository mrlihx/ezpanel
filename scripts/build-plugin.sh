#!/bin/bash
# 一键构建插件前端与 Go 后端并调用 pack-plugin.sh 生成离线安装包。
set -Eeuo pipefail

usage() {
  cat >&2 <<'EOF'
用法：build-plugin.sh [-f|--force] [-o <输出路径>] [--dev] <插件源码目录>
  -f, --force   覆盖已存在的输出 ZIP
  -o <路径>     指定输出 ZIP 路径（默认：<插件源码目录>/<id>-<version>.zip）
  --dev         透传 --dev 给 pack-plugin.sh（允许仅包含当前平台架构入口）
EOF
}

force=false
dev=false
out_path=""
src_arg=""

while [[ $# -gt 0 ]]; do
  case "$1" in
    -f|--force)
      force=true
      shift
      ;;
    --dev)
      dev=true
      shift
      ;;
    -o)
      [[ $# -ge 2 ]] || { usage; exit 1; }
      out_path=$2
      shift 2
      ;;
    -o=*)
      out_path=${1#*=}
      shift
      ;;
    -h|--help)
      usage
      exit 0
      ;;
    -*)
      printf '未知参数：%s\n' "$1" >&2
      usage
      exit 1
      ;;
    *)
      [[ -z $src_arg ]] || { usage; exit 1; }
      src_arg=$1
      shift
      ;;
  esac
done

[[ -n $src_arg ]] || { usage; exit 1; }
[[ -d $src_arg ]] || { printf '插件目录不存在：%s\n' "$src_arg" >&2; exit 1; }

src=$(cd -- "$src_arg" && pwd)
repo=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")/.." && pwd)
manifest="$src/plugin.yaml"
[[ -f $manifest ]] || { printf '缺少 plugin.yaml：%s\n' "$manifest" >&2; exit 1; }

if [[ -n $out_path ]]; then
  out_dir=$(dirname -- "$out_path")
  mkdir -p -- "$out_dir"
  out_path="$(cd -- "$out_dir" && pwd)/$(basename -- "$out_path")"
fi

parse_scalar() {
  local key=$1
  awk -v k="$key" '
    $0 ~ "^" k ":[[:space:]]*" {
      val = $0
      sub("^" k ":[[:space:]]*", "", val)
      sub(/[[:space:]]+#.*$/, "", val)
      gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
      gsub(/^["'\'']|["'\'']$/, "", val)
      print val
      exit
    }
  ' "$manifest"
}

plugin_id=$(parse_scalar id)
plugin_ver=$(parse_scalar version)
[[ -n $plugin_id && -n $plugin_ver ]] || { printf '无法从 plugin.yaml 解析 id 或 version\n' >&2; exit 1; }

if [[ -z $out_path ]]; then
  out_path="$src/$plugin_id-$plugin_ver.zip"
fi

# 1. 按需构建前端
if [[ -f "$src/frontend/package.json" ]]; then
  printf '==> 构建前端 (%s/frontend)\n' "$src"
  if [[ ! -d "$src/frontend/node_modules" ]]; then
    if [[ -f "$src/frontend/package-lock.json" ]]; then
      npm --prefix "$src/frontend" ci
    else
      npm --prefix "$src/frontend" install
    fi
  fi
  npm --prefix "$src/frontend" run build
fi

# 2. 准备临时组装目录
stage=$(mktemp -d)
trap 'rm -rf -- "$stage"' EXIT

cp "$manifest" "$stage/plugin.yaml"
if [[ -f "$src/README.md" ]]; then
  cp "$src/README.md" "$stage/README.md"
fi

web_src=""
for candidate in "$src/package/web" "$src/web" "$src/frontend/dist"; do
  if [[ -d $candidate ]]; then
    web_src=$candidate
    break
  fi
done
[[ -n $web_src ]] || { printf '未找到前端产物目录（已检查 package/web、web、frontend/dist）\n' >&2; exit 1; }
mkdir -p "$stage/web"
cp -R "$web_src/." "$stage/web/"
find "$stage/web" -name '.DS_Store' -delete

# 3. 按需编译或收集后端入口
entries=$(awk '
  /^entry:[[:space:]]*$/ { in_entry=1; next }
  in_entry && /^[^[:space:]#]/ { in_entry=0 }
  in_entry && /^[[:space:]]+[a-z0-9-]+:[[:space:]]*/ {
    line=$0
    sub(/^[[:space:]]+/, "", line)
    sub(/[[:space:]]+#.*$/, "", line)
    split(line, kv, /:[[:space:]]*/)
    key=kv[1]
    val=substr(line, length(key)+2)
    gsub(/^[[:space:]]+|[[:space:]]+$/, "", val)
    gsub(/^["'\'']|["'\'']$/, "", val)
    if (key != "" && val != "") print key "\t" val
  }
' "$manifest")

if [[ -n $entries ]]; then
  while IFS=$'\t' read -r arch relpath; do
    [[ -n $arch && -n $relpath ]] || continue
    target="$stage/$relpath"
    mkdir -p -- "$(dirname -- "$target")"
    if [[ -f "$src/server/go.mod" ]]; then
      goos=${arch%%-*}
      goarch=${arch#*-}
      printf '==> 编译后端 %s (%s -> %s)\n' "$arch" "$src/server" "$relpath"
      (cd -- "$src/server" && CGO_ENABLED=0 GOOS="$goos" GOARCH="$goarch" go build -trimpath -o "$target" .)
    elif [[ -f "$src/package/$relpath" ]]; then
      cp "$src/package/$relpath" "$target"
    elif [[ -f "$src/$relpath" ]]; then
      cp "$src/$relpath" "$target"
    else
      printf '缺少后端入口文件或 server/go.mod：%s\n' "$relpath" >&2
      exit 1
    fi
  done <<< "$entries"
fi

# 4. 调用 pack-plugin.sh 校验并生成 ZIP
pack_args=()
if $force; then pack_args+=(-f); fi
if $dev; then pack_args+=(--dev); fi
pack_args+=(-o "$out_path" "$stage")

printf '==> 打包插件 (%s)\n' "$out_path"
"$repo/scripts/pack-plugin.sh" "${pack_args[@]}"
