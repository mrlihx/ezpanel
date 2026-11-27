#!/bin/bash
# 只管理面板自身；业务网站、数据库与系统软件始终保留。
set -Eeuo pipefail
umask 077
release_repo=https://github.com/mrlihx/ezpanel
release_api=https://api.github.com/repos/mrlihx/ezpanel/releases
version_pattern='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-r[1-9][0-9]*)?$'

# ── 终端样式与输出辅助 ─────────────────────────────────────────────────────
if [[ -t 1 && -z ${NO_COLOR:-} ]]; then
  C_RESET=$'\033[0m'
  C_BOLD=$'\033[1m'
  C_DIM=$'\033[2m'
  C_CYAN=$'\033[36m'
  C_BLUE=$'\033[34m'
  C_GREEN=$'\033[32m'
  C_YELLOW=$'\033[33m'
  C_RED=$'\033[31m'
  C_WHITE=$'\033[97m'
else
  C_RESET='' C_BOLD='' C_DIM='' C_CYAN='' C_BLUE='' C_GREEN='' C_YELLOW='' C_RED='' C_WHITE=''
fi

ui_info() { printf '  %s→%s %s\n' "$C_CYAN" "$C_RESET" "$*"; }
ui_ok()   { printf '  %s✓%s %s\n' "$C_GREEN" "$C_RESET" "$*"; }
ui_warn() { printf '  %s!%s %s\n' "$C_YELLOW" "$C_RESET" "$*"; }
ui_step() { printf '\n%s%s[%s]%s %s%s%s\n' "$C_BOLD" "$C_CYAN" "$1" "$C_RESET" "$C_BOLD" "$2" "$C_RESET"; }
fail()    { printf '\n%s%s错误：%s%s\n' "$C_BOLD" "$C_RED" "$*" "$C_RESET" >&2; exit 1; }

# 支持管道执行（curl ... | bash）时仍可从 /dev/tty 读取交互输入
prompt_read() {
  local __var=$1
  if [[ -t 0 ]]; then
    IFS= read -r "$__var"
  elif [[ -r /dev/tty ]]; then
    IFS= read -r "$__var" < /dev/tty
  else
    return 1
  fi
}

can_interact() {
  [[ -t 1 ]] && { [[ -t 0 ]] || [[ -r /dev/tty ]]; }
}

print_banner() {
  printf '\n'
  printf '%s%s ███████╗ ███████╗ ██████╗   █████╗  ███╗   ██╗ ███████╗ ██╗     %s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
  printf '%s%s ██╔════╝ ╚══███╔╝ ██╔══██╗ ██╔══██╗ ████╗  ██║ ██╔════╝ ██║     %s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
  printf '%s%s █████╗     ███╔╝  ██████╔╝ ███████║ ██╔██╗ ██║ █████╗   ██║     %s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
  printf '%s%s ██╔══╝    ███╔╝   ██╔═══╝  ██╔══██║ ██║╚██╗██║ ██╔══╝   ██║     %s\n' "$C_BOLD" "$C_BLUE" "$C_RESET"
  printf '%s%s ███████╗ ███████╗ ██║      ██║  ██║ ██║ ╚████║ ███████╗ ███████╗%s\n' "$C_BOLD" "$C_BLUE" "$C_RESET"
  printf '%s%s ╚══════╝ ╚══════╝ ╚═╝      ╚═╝  ╚═╝ ╚═╝  ╚═══╝ ╚══════╝ ╚══════╝%s\n' "$C_BOLD" "$C_BLUE" "$C_RESET"
  printf '\n  %sLinux 服务器运维面板 · 一键安装管理脚本%s\n' "$C_DIM" "$C_RESET"
  printf '  %s────────────────────────────────────────────────────────────────%s\n' "$C_DIM" "$C_RESET"
}

detect_os_pretty() {
  if [[ -r /etc/os-release ]]; then
    # shellcheck disable=SC1091
    local PRETTY_NAME=''
    . /etc/os-release 2>/dev/null || true
    if [[ -n $PRETTY_NAME ]]; then
      printf '%s' "$PRETTY_NAME"
      return
    fi
  fi
  uname -sr
}

usage() {
  cat <<'HELP'
用法：install.sh [install|upgrade|rollback|uninstall] [选项]
无子命令直接运行时进入交互菜单，自动检测已安装版本与官方最新版本。
  --version v0.1.2-r1            指定官方发布标签，默认选择最新可用版本
  --install-dir /opt/ezpanel     面板专用绝对目录
  --release-dir /path/to/dist    本地发布物目录（含 SHA256SUMS）
  --release-url https://...      明确信任的自建 HTTPS 发布目录
  --snapshot /path/to/snapshot   rollback 使用的完整快照
  --purge                        uninstall 时明确删除面板私有数据
官方发布验证签名和 SHA-256。本地或自建发布源须由操作者确认清单来源。
HELP
}

# ── 解析参数 ───────────────────────────────────────────────────────────────
explicit_action=false
action=install
if [[ $# -gt 0 && $1 != -* ]]; then
  explicit_action=true
  action=$1
  shift
fi
install=/opt/ezpanel
version=
release_dir=
release_url=
snapshot=
purge=false
while [[ $# -gt 0 ]]; do
  case "$1" in
    --install-dir|--version|--release-dir|--release-url|--snapshot)
      [[ $# -ge 2 ]] || fail "$1 缺少参数"
      case "$1" in
        --install-dir) install=$2 ;;
        --version) version=$2; [[ -n $version ]] || fail '发布版本不能为空' ;;
        --release-dir) release_dir=$2 ;;
        --release-url) release_url=$2 ;;
        --snapshot) snapshot=$2 ;;
      esac
      shift 2 ;;
    --purge) purge=true; shift ;;
    -h|--help) usage; exit 0 ;;
    *) fail "未知参数 $1" ;;
  esac
done
case "$action" in install|upgrade|rollback|uninstall) ;; *) usage; fail '未知操作' ;; esac
[[ -z $release_dir || -z $release_url ]] || fail '本地目录与下载地址只能选择一种'
if [[ -n $version ]]; then
  [[ $action == install || $action == upgrade ]] || fail '--version 仅支持安装或升级'
  [[ -z $release_dir && -z $release_url ]] || fail '--version 仅用于官方发布源'
  [[ $version == latest || $version =~ $version_pattern ]] || fail '请使用 vX.Y.Z、vX.Y.Z-rN 或 latest'
fi
[[ $(uname -s) == Linux && $EUID -eq 0 ]] || fail '需要 Linux 本机 root 权限运行'
command -v systemctl >/dev/null || fail '需要 systemd'
[[ -d /run/systemd/system ]] || fail 'systemd 未运行'
[[ $install =~ ^/[a-zA-Z0-9_./-]+$ && $install != / && $install != *'/../'* && $install != */.. ]] || fail '安装目录须为无空格的规范绝对路径'
[[ ! -L $install ]] || fail '安装目录不能是符号链接'
install=$(realpath -m -- "$install")
case "$install" in /|/etc|/usr|/var|/www|/opt|/root|/home|/bin|/sbin|/lib|/lib64|/boot|/dev|/proc|/sys|/run|/tmp) fail '必须使用面板专用子目录' ;; esac
unit_name=ezpanel.service
unit=/etc/systemd/system/$unit_name
binary=/usr/local/bin/ezpanel
bin_link=/usr/bin/ezpanel
legacy_cli_link=/usr/local/bin/epanel
if [[ -f $unit ]] && ! grep -Fxq "WorkingDirectory=$install" "$unit"; then
  fail 'ezpanel.service 属于其他安装目录，拒绝覆盖'
fi
case $(uname -m) in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail '仅提供 Linux amd64/arm64 发布物' ;; esac
work=$(mktemp -d /tmp/ezpanel-install.XXXXXXXX)
chmod 700 "$work"
rollback_needed=false
restoring=false
stopped_for_snapshot=false
replacing_program=false

# ── 系统依赖检测与自动安装 ─────────────────────────────────────────────────
pkg_mgr=''
apt_updated=false
detect_pkg_mgr() {
  if [[ -n $pkg_mgr ]]; then return 0; fi
  if command -v apt-get >/dev/null 2>&1; then
    pkg_mgr=apt
  elif command -v dnf >/dev/null 2>&1; then
    pkg_mgr=dnf
  elif command -v yum >/dev/null 2>&1; then
    pkg_mgr=yum
  else
    pkg_mgr=unknown
  fi
}

install_os_packages() {
  local pkgs=("$@")
  [[ ${#pkgs[@]} -gt 0 ]] || return 0
  detect_pkg_mgr
  case "$pkg_mgr" in
    apt)
      if [[ $apt_updated == false ]]; then
        DEBIAN_FRONTEND=noninteractive apt-get update -qq >/dev/null 2>&1 || DEBIAN_FRONTEND=noninteractive apt-get update -qq
        apt_updated=true
      fi
      DEBIAN_FRONTEND=noninteractive apt-get install -y -qq --no-install-recommends "${pkgs[@]}" >/dev/null 2>&1 || \
        DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "${pkgs[@]}"
      ;;
    dnf)
      dnf install -y -q "${pkgs[@]}" >/dev/null 2>&1 || dnf install -y "${pkgs[@]}"
      ;;
    yum)
      yum install -y -q "${pkgs[@]}" >/dev/null 2>&1 || yum install -y "${pkgs[@]}"
      ;;
    *)
      return 1
      ;;
  esac
}

ensure_bootstrap_deps() {
  local missing=()
  command -v curl >/dev/null 2>&1 || missing+=(curl)
  command -v openssl >/dev/null 2>&1 || missing+=(openssl)
  if [[ ${#missing[@]} -gt 0 ]]; then
    ui_info "检测到缺少基础下载工具（${missing[*]}），正在自动安装..."
    install_os_packages "${missing[@]}" ca-certificates || fail "自动安装基础依赖（${missing[*]}）失败，请手工安装后重试"
    ui_ok "基础下载工具已就绪"
  fi
}

ensure_system_dependencies() {
  detect_pkg_mgr
  local -a required_pkgs=()
  local -a check_items=(
    "curl:curl:下载与更新"
    "openssl:openssl:发布包验签与证书"
    "tar:tar:网站与目录备份"
    "nft:nftables:系统防火墙"
    "unzip:unzip:ZIP 解压缩"
    "xz:xz:XZ 解压缩"
    "bzip2:bzip2:BZ2 解压缩"
    "git:git:远程 Git 拉取"
  )
  local item cmd pkg desc
  for item in "${check_items[@]}"; do
    IFS=: read -r cmd pkg desc <<< "$item"
    if [[ $pkg == xz && $pkg_mgr == apt ]]; then
      pkg=xz-utils
    fi
    if command -v "$cmd" >/dev/null 2>&1; then
      printf '  %s✓%s %-10s %s已安装%s\n' "$C_GREEN" "$C_RESET" "$cmd" "$C_DIM" "$C_RESET"
    else
      printf '  %s↓%s %-10s %s未安装（%s），将自动安装%s\n' "$C_YELLOW" "$C_RESET" "$cmd" "$C_DIM" "$desc" "$C_RESET"
      required_pkgs+=("$pkg")
    fi
  done
  if [[ ${#required_pkgs[@]} -eq 0 ]]; then
    ui_ok "系统依赖全部满足，跳过安装"
  else
    ui_info "正在通过 ${pkg_mgr} 安装缺失依赖: ${required_pkgs[*]} ..."
    install_os_packages "${required_pkgs[@]}" ca-certificates || fail "系统依赖安装失败，请检查系统软件源配置"
    ui_ok "核心系统依赖安装完成"
  fi
  # 可选 7z 支持（不阻塞主安装流程）
  if ! command -v 7z >/dev/null 2>&1 && ! command -v 7za >/dev/null 2>&1; then
    local pkg7z=p7zip
    if [[ $pkg_mgr == apt ]]; then pkg7z=p7zip-full; fi
    if install_os_packages "$pkg7z" >/dev/null 2>&1; then
      ui_ok "已顺带安装 7z 解压缩支持 ($pkg7z)"
    fi
  fi
}

# ── 服务与快照管理辅助 ─────────────────────────────────────────────────────
ensure_bin_link() {
  if [[ -L $bin_link || ! -e $bin_link ]]; then
    ln -sfn -- "$binary" "$bin_link"
  else
    printf '警告：%s 已存在且非符号链接，未覆盖。\n' "$bin_link" >&2
  fi
}

remove_bin_link() {
  if [[ -L $bin_link && $(readlink -- "$bin_link") == "$binary" ]]; then
    rm -f -- "$bin_link"
  fi
  if [[ -L $legacy_cli_link && $(readlink -- "$legacy_cli_link") == "$binary" ]]; then
    rm -f -- "$legacy_cli_link"
  fi
}

wait_ready() {
  local service=${1:-$unit_name} exe=${2:-$binary} attempt
  for attempt in {1..20}; do
    if systemctl is-active --quiet "$service" && "$exe" ready --install-dir "$install" >/dev/null 2>&1; then return 0; fi
    sleep 1
  done
  return 1
}

install_program() {
  cp -- "$work/$artifact" "$binary.new"
  chmod 755 "$binary.new"
  mv -f -- "$binary.new" "$binary"
}

restore_snapshot() {
  # 完成标记最后写入，任何不完整快照都不能移动当前数据。
  [[ -f $snapshot/.complete && -d $snapshot/data ]] || { printf '快照不完整，拒绝恢复。\n' >&2; return 1; }
  if [[ -f $snapshot/binary.present && ! -f $snapshot/ezpanel ]]; then return 1; fi
  if [[ -f $snapshot/unit.present && ! -f $snapshot/$unit_name ]]; then return 1; fi
  restoring=true
  if [[ -f $unit ]]; then systemctl stop "$unit_name" || return 1; fi
  local name failed_dir
  failed_dir="$snapshot/failed-$(date -u +%Y%m%dT%H%M%SZ)-$$"
  mkdir -- "$failed_dir" || return 1
  for name in data plugins; do
    if [[ -e $install/$name ]]; then mv -- "$install/$name" "$failed_dir/$name" || return 1; fi
    if [[ -e $snapshot/$name ]]; then cp -a -- "$snapshot/$name" "$install/$name" || return 1; fi
  done
  if [[ -f $snapshot/binary.present ]]; then
    mkdir -p -- /usr/local/bin || return 1
    cp -a -- "$snapshot/ezpanel" "$binary" || return 1
    ensure_bin_link || return 1
  else
    remove_bin_link || return 1
    rm -f -- "$binary" || return 1
  fi
  if [[ -f $snapshot/unit.present ]]; then
    cp -a -- "$snapshot/$unit_name" "$unit" || return 1
  elif [[ -f $unit ]]; then
    systemctl disable "$unit_name" || return 1
    rm -f -- "$unit" || return 1
  fi
  systemctl daemon-reload || return 1
  if [[ -f $snapshot/binary.present && -f $snapshot/unit.present ]]; then
    if ! systemctl enable "$unit_name" || ! systemctl start "$unit_name" || ! wait_ready "$unit_name" "$binary"; then
      printf '回滚文件已恢复，但旧服务未就绪，请检查 journalctl -u %s。快照：%s\n' "$unit_name" "$snapshot" >&2
      return 1
    fi
  else
    printf '已恢复原保留数据状态；原安装无二进制或 unit，服务保持停止。\n'
  fi
  rollback_needed=false
  printf '已恢复完整快照，失败版本数据保留在：%s\n' "$failed_dir"
}

finish() {
  local code=$?
  trap - EXIT
  if [[ $code -ne 0 && $replacing_program == true ]]; then
    if cp -a -- "$work/previous-program" "$binary.new" && mv -f -- "$binary.new" "$binary"; then
      printf '已恢复重装前的程序。\n' >&2
      if [[ $stopped_for_snapshot == true ]]; then
        systemctl restart "$unit_name" || printf '旧服务重启失败，请检查 journal。\n' >&2
      fi
    else
      printf '恢复重装前的程序失败。原程序保留在 %s/previous-program。\n' "$work" >&2
      exit "$code"
    fi
  elif [[ $code -ne 0 && $rollback_needed == true && $restoring == false ]]; then
    printf '操作失败，恢复升级前快照。\n' >&2
    restore_snapshot || code=1
  elif [[ $code -ne 0 && $stopped_for_snapshot == true && $restoring == false ]]; then
    systemctl start "$unit_name" || printf '旧服务重启失败，请检查 journal。\n' >&2
  fi
  rm -rf -- "$work"
  exit "$code"
}
trap finish EXIT

fetch_latest_official_tag() {
  ensure_bootstrap_deps
  local status
  status=$(curl --silent --show-error --proto '=https' --max-time 30 --retry 2 \
    --output "$work/release.json" --write-out '%{http_code}' "$release_api/latest") || return 1
  case $status in
    200) ;;
    404)
      # GitHub 仅在有正式版时提供 latest。没有正式版时读取公开发布列表，包含预览版。
      curl --fail --silent --show-error --proto '=https' --max-time 30 --retry 2 \
        --output "$work/release.json" "$release_api?per_page=1" || return 1
      ;;
    *) return 1 ;;
  esac
  local tag
  tag=$(sed -nE 's/^[[:space:]]*"tag_name"[[:space:]]*:[[:space:]]*"(v[0-9]+\.[0-9]+\.[0-9]+(-r[0-9]+)?)"[[:space:]]*,?[[:space:]]*$/\1/p' "$work/release.json" | head -n 1)
  [[ -n $tag && $tag =~ $version_pattern ]] || return 1
  printf '%s' "$tag"
}

run_uninstall() {
  [[ -x $binary && -d $install/data ]] || fail '未找到有效面板安装'
  printf '\n'
  ui_warn '仅删除面板服务和二进制程序；网站、业务备份、Nginx、数据库均保留。'
  printf '  输入 %sUNINSTALL%s 确认卸载：' "$C_RED" "$C_RESET"
  local answer=''
  prompt_read answer || fail '已取消'
  [[ $answer == UNINSTALL ]] || fail '已取消'
  if [[ $purge == false && $explicit_action == false ]] && can_interact; then
    printf '  是否同时彻底清理面板私有数据（凭据、证书、配置、快照）？[y/N]：'
    local purge_choice=''
    if prompt_read purge_choice && [[ $purge_choice == y || $purge_choice == Y ]]; then
      purge=true
    fi
  fi
  if [[ $purge == true ]]; then
    ui_warn '将丢失连接凭据、面板证书、配置、审计、插件及面板快照。'
    printf '  输入 %sPURGE %s%s 确认彻底清理：' "$C_RED" "$install" "$C_RESET"
    prompt_read answer || fail '已取消'
    [[ $answer == "PURGE $install" ]] || fail '已取消'
  fi
  systemctl disable --now "$unit_name"
  remove_bin_link
  rm -f -- "$unit" "$binary"
  systemctl daemon-reload
  if [[ $purge == true ]]; then
    for name in data plugins tmp trash .snapshots; do rm -rf -- "$install/$name"; done
  fi
  printf '\n'
  ui_ok 'EzPanel 已卸载，业务数据与业务服务已完整保留。'
  exit 0
}

# ── 显式 uninstall / rollback 直接处理 ────────────────────────────────────
if [[ $explicit_action == true && $action == uninstall ]]; then
  run_uninstall
fi
if [[ $action == rollback ]]; then
  [[ -n $snapshot ]] || fail 'rollback 必须指定 --snapshot'
  snapshot=$(realpath -e -- "$snapshot")
  [[ $snapshot == "$install/.snapshots/"* ]] || fail '只能使用此安装目录的面板快照'
  if systemctl is-active --quiet "$unit_name"; then "$binary" upgrade-check --install-dir "$install"; fi
  printf '将停止面板并恢复整份旧数据；升级后的面板修改将被移至快照诊断目录。\n'
  printf '输入 ROLLBACK 确认：'; prompt_read answer || fail '已取消'
  [[ $answer == ROLLBACK ]] || fail '已取消'
  restore_snapshot
  exit 0
fi

# ── 检测当前安装状态与官方最新版本 ────────────────────────────────────────
artifact="ezpanel-linux-$arch"
official=false
automatic_version=false
reinstall=false
menu_confirmed=false
installed_version=
installed_tag=
if [[ -d $install/data ]]; then
  [[ -f $install/data/config.yaml && -f $install/data/panel.db ]] || fail '发现不完整旧数据，禁止重新初始化；请先恢复完整备份'
  if [[ -x $binary && -f $unit ]]; then
    installed_version=$("$binary" version 2>/dev/null || true)
    installed_tag=$("$binary" version --tag 2>/dev/null || true)
    if [[ ! $installed_tag =~ $version_pattern && v$installed_version =~ $version_pattern ]]; then
      installed_tag=v$installed_version
    fi
  fi
fi

latest_tag=''
latest_version=''
if [[ -z $release_dir && -z $release_url ]]; then
  official=true
  if [[ -z $version || $version == latest ]]; then
    automatic_version=true
    latest_tag=$(fetch_latest_official_tag) || fail '无法读取 GitHub 最新发布版本，请检查网络或使用 --version 指定版本'
    version=$latest_tag
  else
    latest_tag=$version
  fi
  latest_version=${latest_tag#v}
  latest_version=${latest_version%%-r*}
fi

# ── 交互主菜单（直接运行脚本且处于交互终端时展示） ────────────────────────
if [[ $explicit_action == false ]] && can_interact; then
  print_banner
  os_pretty=$(detect_os_pretty)
  printf '  %-10s : %s (%s/%s)\n' "系统环境" "$os_pretty" "linux" "$arch"
  printf '  %-10s : %s\n' "安装目录" "$install"

  has_update=false
  is_latest=false
  if [[ -z $installed_version ]]; then
    printf '  %-10s : %s未安装%s\n' "当前版本" "$C_DIM" "$C_RESET"
  elif [[ -n $latest_version ]]; then
    if [[ $installed_tag == "$latest_tag" ]]; then
      is_latest=true
      printf '  %-10s : %s%s%s  %s(已是最新)%s\n' "当前版本" "$C_GREEN" "$installed_version" "$C_RESET" "$C_DIM" "$C_RESET"
    elif [[ $installed_tag =~ $version_pattern && $(printf '%s\n' "$installed_tag" "$latest_tag" | sort -V | tail -n 1) == "$latest_tag" ]]; then
      has_update=true
      printf '  %-10s : %s%s%s\n' "当前版本" "$C_YELLOW" "$installed_version" "$C_RESET"
    else
      is_latest=true
      printf '  %-10s : %s%s%s  %s(高于发布版)%s\n' "当前版本" "$C_GREEN" "$installed_version" "$C_RESET" "$C_DIM" "$C_RESET"
    fi
  else
    printf '  %-10s : %s%s%s\n' "当前版本" "$C_GREEN" "$installed_version" "$C_RESET"
  fi

  if [[ -n $latest_version ]]; then
    if [[ $has_update == true ]]; then
      printf '  %-10s : %s%s%s (%s)  %s★ 发现新版本%s\n' "最新版本" "$C_CYAN" "$latest_version" "$C_RESET" "$latest_tag" "$C_YELLOW" "$C_RESET"
    else
      printf '  %-10s : %s%s%s (%s)\n' "最新版本" "$C_CYAN" "$latest_version" "$C_RESET" "$latest_tag"
    fi
  elif [[ -n $release_dir ]]; then
    printf '  %-10s : %s本地目录 (%s)%s\n' "安装来源" "$C_CYAN" "$release_dir" "$C_RESET"
  else
    printf '  %-10s : %s自建源 (%s)%s\n' "安装来源" "$C_CYAN" "$release_url" "$C_RESET"
  fi
  printf '  %s────────────────────────────────────────────────────────────────%s\n\n' "$C_DIM" "$C_RESET"

  choice=''
  if [[ -z $installed_version ]]; then
    printf '  %s请选择要执行的操作：%s\n' "$C_BOLD" "$C_RESET"
    printf '    %s[1]%s 安装 EzPanel%s\n' "$C_GREEN" "$C_RESET" "${latest_version:+ ($latest_version)}"
    printf '    %s[0]%s 退出\n\n' "$C_DIM" "$C_RESET"
    printf '  请输入选项 [%s1%s]: ' "$C_CYAN" "$C_RESET"
    prompt_read choice || exit 0
    choice=${choice:-1}
    case "$choice" in
      1) action=install; menu_confirmed=true ;;
      0) printf '  已退出。\n'; exit 0 ;;
      *) fail "无效选项: $choice" ;;
    esac
  elif [[ $has_update == true ]]; then
    printf '  %s请选择要执行的操作：%s\n' "$C_BOLD" "$C_RESET"
    printf '    %s[1]%s 更新 EzPanel (%s -> %s)\n' "$C_GREEN" "$C_RESET" "${installed_tag#v}" "${latest_tag#v}"
    printf '    %s[9]%s 卸载 EzPanel\n' "$C_RED" "$C_RESET"
    printf '    %s[0]%s 退出\n\n' "$C_DIM" "$C_RESET"
    printf '  请输入选项 [%s1%s]: ' "$C_CYAN" "$C_RESET"
    prompt_read choice || exit 0
    choice=${choice:-1}
    case "$choice" in
      1) action=upgrade; menu_confirmed=true ;;
      9) run_uninstall ;;
      0) printf '  已退出。\n'; exit 0 ;;
      *) fail "无效选项: $choice" ;;
    esac
  else
    printf '  %s请选择要执行的操作：%s\n' "$C_BOLD" "$C_RESET"
    printf '    %s[1]%s 重新安装 EzPanel %s(保留配置与数据)%s\n' "$C_CYAN" "$C_RESET" "$C_DIM" "$C_RESET"
    printf '    %s[2]%s 卸载 EzPanel\n' "$C_RED" "$C_RESET"
    printf '    %s[0]%s 退出\n\n' "$C_DIM" "$C_RESET"
    printf '  请输入选项 [%s0%s]: ' "$C_CYAN" "$C_RESET"
    prompt_read choice || exit 0
    choice=${choice:-0}
    case "$choice" in
      1) action=install; reinstall=true; menu_confirmed=true ;;
      2) run_uninstall ;;
      0) printf '  已退出。\n'; exit 0 ;;
      *) fail "无效选项: $choice" ;;
    esac
  fi
fi

# ── 官方版本比对（非菜单或显式子命令时执行） ──────────────────────────────
if [[ $official == true ]]; then
  release_version=${version#v}
  release_version=${release_version%%-r*}
  if [[ $menu_confirmed == false && $action == install && -n $installed_version ]]; then
    if [[ $installed_tag == "$version" ]]; then
      printf '当前版本已是 %s。重装只替换程序，保留配置及数据。\n' "$installed_version"
      printf '输入 REINSTALL 确认重装，直接回车退出：'
      if ! prompt_read answer || [[ $answer != REINSTALL ]]; then exit 0; fi
      reinstall=true
    elif [[ $automatic_version == true && $installed_tag =~ $version_pattern && $(printf '%s\n' "$installed_tag" "$version" | sort -V | tail -n 1) == "$installed_tag" ]]; then
      printf '当前版本 %s 高于可用版本 %s，保留已安装程序。\n' "$installed_version" "$release_version"
      exit 0
    fi
  fi
  release_url="$release_repo/releases/download/$version"
fi

# ── Step 1: 全新安装时检查并自动安装系统依赖 ──────────────────────────────
fresh_install=false
if [[ ! -d $install/data ]]; then
  fresh_install=true
  ui_step "1/5" "检查系统依赖..."
  ensure_system_dependencies
else
  ui_step "1/5" "检查基础环境..."
  ensure_bootstrap_deps
  ui_ok "基础环境已就绪"
fi

# ── Step 2: 下载并校验发布包 ──────────────────────────────────────────────
ui_step "2/5" "获取并校验发布包 (${version:-local} / $arch)..."
if [[ -n $release_url ]]; then
  [[ $release_url == https://* && $release_url != *'?'* && $release_url != *'#'* && $release_url != *'@'* ]] || fail '发布地址须为无凭据的HTTPS目录'
  command -v curl >/dev/null || fail '下载需要 curl'
  curl --fail --silent --show-error --proto '=https' --proto-redir '=https' --location --max-time 180 "$release_url/SHA256SUMS" --output "$work/SHA256SUMS"
  if [[ $official == true ]]; then
    command -v openssl >/dev/null || fail '验证官方发布签名需要 openssl'
    curl --fail --silent --show-error --proto '=https' --proto-redir '=https' --location --max-time 180 "$release_url/SHA256SUMS.sig" --output "$work/SHA256SUMS.sig"
    cat > "$work/release-signing.pem" <<'PUBLIC_KEY'
-----BEGIN PUBLIC KEY-----
MIIBojANBgkqhkiG9w0BAQEFAAOCAY8AMIIBigKCAYEAt+eYEoelJoOvpWvav2ov
A8CIp5+MafjGNox/ceb1O82lHUD5lqXZqO7cVlTICEwKPrL3vtzhWaJ3NeUTznM3
t/6vtGPHvMlhvSp6DS32Cyewl9k5AXuZb3czJY+JFmjy7wt6ByaOr3724rUI0HaZ
Ayy/RHIWn/z0CzbXYVdeKcQQO7/SAJ6SWezE+FRGcEVpArF2gcvLwQ/NAY6iIN8Z
5S5cavUd0DqqZqm9twa0Utjxe7eT/1htHP9u+V3mIoBkmoX3xL+e3ySoWjzeX6Yt
27c62Rcq8aMsazN87qj1N3H7ZaX3CCbS9okHptE36kcdOgsdTpByYPP5TgmKbMf+
rLBW9t2WGVs5bBqxHD4wwryLVZKdqyz+eQHfhXykeBZ2+pqlTmmx1ZfdxIFZFzMo
DAx5p2vnjS0ByDkfQ0X/RFl4RFBtkT3raZiIg5aXrVXd65JOKKpbsqKFYg17qfkM
Ylbws6P+LqytD+sDRE/MA3sSdRQYE2Yq0e7gjQAc9WWBAgMBAAE=
-----END PUBLIC KEY-----
PUBLIC_KEY
    openssl dgst -sha256 -verify "$work/release-signing.pem" -signature "$work/SHA256SUMS.sig" "$work/SHA256SUMS" >/dev/null || fail '官方发布签名无效，未替换面板'
    ui_ok "官方签名验证通过 (SHA256SUMS.sig)"
  fi
  ui_info "正在下载 $artifact ..."
  curl --fail --silent --show-error --proto '=https' --proto-redir '=https' --location --max-time 600 "$release_url/$artifact" --output "$work/$artifact"
else
  cp -- "$release_dir/SHA256SUMS" "$work/SHA256SUMS"
  cp -- "$release_dir/$artifact" "$work/$artifact"
fi
command -v sha256sum >/dev/null || fail '需要 sha256sum 校验发布物'
expected=$(awk -v file="$artifact" '$2 == file && length($1) == 64 && $1 ~ /^[0-9a-fA-F]+$/ { print tolower($1) }' "$work/SHA256SUMS")
[[ $expected =~ ^[0-9a-f]{64}$ ]] || fail '清单中匹配架构的SHA-256必须有且仅有一条'
actual=$(sha256sum "$work/$artifact"); actual=${actual%% *}
[[ $actual == "$expected" ]] || fail 'SHA-256 不匹配，未替换面板'
ui_ok "二进制校验通过 (SHA-256)"
chmod 755 "$work/$artifact"
artifact_version=$("$work/$artifact" version)
if [[ $official == true ]]; then
  [[ $artifact_version == "$release_version" ]] || fail '二进制版本与发布标签不一致，未替换面板'
  artifact_tag=$("$work/$artifact" version --tag 2>/dev/null || true)
  [[ -z $artifact_tag || $artifact_tag == "$version" ]] || fail '二进制修订号与发布标签不一致，未替换面板'
fi
ui_ok "程序版本校验通过 ($artifact_version)"

# ── 重装分支（同版本替换程序） ────────────────────────────────────────────
if [[ $reinstall == true ]]; then
  ui_step "3/5" "替换面板程序并重启服务..."
  reinstall_active=false
  if systemctl is-active --quiet "$unit_name"; then
    "$binary" upgrade-check --install-dir "$install"
    reinstall_active=true
  fi
  cp -a -- "$binary" "$work/previous-program"
  if [[ $reinstall_active == true ]]; then
    systemctl stop "$unit_name"
    stopped_for_snapshot=true
  fi
  replacing_program=true
  install_program
  ensure_bin_link
  if [[ $stopped_for_snapshot == true ]]; then
    systemctl start "$unit_name" && wait_ready || fail '重装后的服务未通过就绪检查'
  fi
  replacing_program=false
  printf '\n'
  ui_ok "EzPanel $artifact_version 已重新安装，原面板配置和数据已完整保留。"
  exit 0
fi

# ── Step 3: 准备升级快照或全新目录 ────────────────────────────────────────
ui_step "3/5" "安装面板程序与系统服务..."
if [[ -d $install/data ]]; then
  if [[ -x $binary ]]; then
    "$binary" upgrade-check --install-dir "$install"
  else
    [[ $action == install && ! -e $binary && ! -f $unit ]] || fail '只有默认卸载后的保留数据可通过 install 重新安装'
    ui_info '发现卸载后保留数据，将完整备份并沿用配置，不重新初始化。'
  fi
  if [[ $menu_confirmed == false ]]; then
    ui_warn '即将停止面板、备份整份私有数据并升级，不修改网站/数据库/系统软件。'
    printf '  输入 UPGRADE 确认：'; prompt_read answer || fail '已取消'
    [[ $answer == UPGRADE ]] || fail '已取消'
  fi
  if [[ -f $unit ]]; then
    systemctl stop "$unit_name"
    stopped_for_snapshot=true
  fi
  snapshot="$install/.snapshots/$(date -u +%Y%m%dT%H%M%SZ)-$$"
  mkdir -p -- "$snapshot"; chmod 700 "$install/.snapshots" "$snapshot"
  if [[ -x $binary ]]; then
    cp -a -- "$binary" "$snapshot/ezpanel"
    touch "$snapshot/binary.present"
  fi
  for name in data plugins; do
    if [[ -e $install/$name ]]; then cp -a -- "$install/$name" "$snapshot/$name"; fi
  done
  if [[ -f $unit ]]; then
    cp -a -- "$unit" "$snapshot/$unit_name"
    touch "$snapshot/unit.present"
  fi
  # 此前任一复制失败都会退出，未完成快照不能作为回滚来源。
  printf 'ezpanel-snapshot-v1\n' > "$snapshot/.complete"
  rollback_needed=true
  ui_ok "升级前快照已备份至 $snapshot"
else
  [[ $action == install ]] || fail '未找到已有安装，请使用 install'
  can_interact || fail '首次初始化必须在本机交互终端运行'
  if [[ -d $install && -n $(find "$install" -mindepth 1 -maxdepth 1 -print -quit) ]]; then fail '目标目录非空且不属于已初始化面板'; fi
  mkdir -p -- "$install"; chmod 755 "$install"
fi

# 所有可信 process 插件共用此低权限身份，不承诺插件之间的文件隔离。
if ! id panel-plugin >/dev/null 2>&1; then
  command -v useradd >/dev/null || fail '创建插件用户需要 useradd'
  nologin_shell=$(command -v nologin) || fail '创建插件用户需要 nologin'
  useradd --system --user-group --home-dir /nonexistent --shell "$nologin_shell" panel-plugin
fi
mkdir -p -- /usr/local/bin
install_program
ensure_bin_link
ui_ok "已安装至 $binary (软链 $bin_link)"

# ── Step 4: 初始化配置 ────────────────────────────────────────────────────
ui_step "4/5" "初始化面板配置与安全凭据..."
init_user=''
init_password=''
if [[ ! -d $install/data ]]; then
  init_out=$("$binary" server --install-dir "$install" --init)
  init_user=$(printf '%s\n' "$init_out" | sed -nE 's/^用户名:[[:space:]]*(.+)$/\1/p' | head -n 1)
  init_password=$(printf '%s\n' "$init_out" | sed -nE 's/^初始密码[^:：]*[:：][[:space:]]*(.+)$/\1/p' | head -n 1)
  ui_ok "面板初始化完成"
else
  ui_ok "沿用已有配置与数据"
fi

# ── Step 5: 写入 systemd 并启动服务 ───────────────────────────────────────
ui_step "5/5" "启动 $unit_name 并等待健康检查..."
cat > "$unit" <<UNIT
[Unit]
Description=ezpanel Server Management
After=network.target docker.service

[Service]
Type=simple
User=root
Group=root
WorkingDirectory=$install
ExecStart=$binary server --install-dir $install
Restart=on-failure
RestartSec=5
UMask=0077
KillMode=control-group
TimeoutStopSec=90

[Install]
WantedBy=multi-user.target
UNIT
chmod 644 "$unit"
systemctl daemon-reload
systemctl enable "$unit_name" >/dev/null 2>&1
if ! systemctl start "$unit_name" || ! wait_ready; then fail '新服务未通过真实入口/匿名会话就绪检查'; fi
rollback_needed=false
ui_ok "面板服务已启动并通过健康检查"

# ── 美化展示面板连接信息卡片 ──────────────────────────────────────────────
info_out=$("$binary" info --install-dir "$install" 2>/dev/null || true)
panel_url=$(printf '%s\n' "$info_out" | sed -nE 's/^访问地址:[[:space:]]*(.+)$/\1/p' | head -n 1)
listen_addr=$(printf '%s\n' "$info_out" | sed -nE 's/^监听:[[:space:]]*(.+)$/\1/p' | head -n 1)
tls_flag=$(printf '%s\n' "$info_out" | sed -nE 's/^TLS:[[:space:]]*(.+)$/\1/p' | head -n 1)

scheme=http
if [[ $tls_flag == true ]]; then scheme=https; fi
port=${listen_addr##*:}
entry_path=''
if [[ $panel_url == *://*/* ]]; then
  entry_path="/${panel_url#*://*/}"
fi
lan_ip=$(hostname -I 2>/dev/null | awk '{print $1}' || true)
pub_ip=''
if command -v curl >/dev/null 2>&1; then
  pub_ip=$(curl --silent --max-time 3 https://api.ipify.org 2>/dev/null || true)
  if [[ ! $pub_ip =~ ^[0-9]+\.[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    pub_ip=''
  fi
fi

title_text="EzPanel $artifact_version 安装成功"
if [[ $fresh_install == false ]]; then
  title_text="EzPanel $artifact_version 升级完成"
fi

printf '\n'
printf '%s%s╔════════════════════════════════════════════════════════════════╗%s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
printf '%s%s║%s  %s%s%s\n' "$C_BOLD" "$C_CYAN" "$C_RESET" "$C_BOLD$C_GREEN" "$title_text" "$C_RESET"
printf '%s%s╠════════════════════════════════════════════════════════════════╣%s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
if [[ -n $pub_ip && -n $port ]]; then
  printf '  %s外网访问地址%s : %s%s://%s:%s%s%s\n' "$C_BOLD" "$C_RESET" "$C_CYAN" "$scheme" "$pub_ip" "$port" "$entry_path" "$C_RESET"
fi
if [[ -n $lan_ip && -n $port ]]; then
  printf '  %s内网访问地址%s : %s%s://%s:%s%s%s\n' "$C_BOLD" "$C_RESET" "$C_CYAN" "$scheme" "$lan_ip" "$port" "$entry_path" "$C_RESET"
elif [[ -n $panel_url ]]; then
  printf '  %s面板访问地址%s : %s%s%s\n' "$C_BOLD" "$C_RESET" "$C_CYAN" "$panel_url" "$C_RESET"
fi
if [[ -n $port ]]; then
  printf '  %s监听端口    %s : %s\n' "$C_BOLD" "$C_RESET" "$port"
fi
if [[ -n $entry_path ]]; then
  printf '  %s安全入口    %s : %s\n' "$C_BOLD" "$C_RESET" "$entry_path"
fi
if [[ -n $init_user ]]; then
  printf '  %s初始用户名  %s : %s%s%s\n' "$C_BOLD" "$C_RESET" "$C_GREEN" "$init_user" "$C_RESET"
fi
if [[ -n $init_password ]]; then
  printf '  %s初始密码    %s : %s%s%s  %s(仅显示一次，请妥善保存)%s\n' "$C_BOLD" "$C_RESET" "$C_YELLOW" "$init_password" "$C_RESET" "$C_DIM" "$C_RESET"
fi
printf '%s%s╠════════════════════════════════════════════════════════════════╣%s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
printf '  %s常用管理命令%s :\n' "$C_BOLD" "$C_RESET"
printf '    %sezpanel menu%s    打开面板命令行管理菜单\n' "$C_CYAN" "$C_RESET"
printf '    %sezpanel info%s    查看当前访问地址与监听信息\n' "$C_CYAN" "$C_RESET"
printf '    %sjournalctl -u %s -n 100 --no-pager%s\n' "$C_DIM" "$unit_name" "$C_RESET"
printf '%s%s╚════════════════════════════════════════════════════════════════╝%s\n' "$C_BOLD" "$C_CYAN" "$C_RESET"
if [[ -n $port ]]; then
  printf '  %s提示：若云服务器无法访问，请在云安全组与防火墙放行 %s 端口。%s\n' "$C_YELLOW" "$port" "$C_RESET"
fi
if [[ -n $snapshot ]]; then
  printf '  %s升级前完整快照：%s（含秘密，请妥善保护）%s\n' "$C_DIM" "$snapshot" "$C_RESET"
fi
printf '\n'
