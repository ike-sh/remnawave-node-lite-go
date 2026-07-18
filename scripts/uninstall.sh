#!/usr/bin/env bash
# remnawave-node-lite-go 卸载脚本（systemd / Alpine OpenRC）
# shellcheck source-path=SCRIPTDIR
set -Eeuo pipefail

VERSION="0.1.0"
PREFIX="/usr/local/bin"
BIN_NAME="remnanode-lite"
RUN_WRAPPER="${PREFIX}/remnawave-node-run"
XLOGS="${PREFIX}/remnanode-xlogs"
XERRORS="${PREFIX}/remnanode-xerrors"
UNIT="/etc/systemd/system/remnawave-node.service"
OPENRC_SVC="/etc/init.d/remnawave-node"
ETC_DIR="/etc/remnanode"
LOG_DIR="/var/log/remnanode"
DATA_DIR="/var/lib/remnanode"
OWNED_LIB_DIR="/usr/local/lib/remnanode"
OWNED_SHARE_DIR="/usr/local/share/remnanode"
GEO_DIR="${OWNED_SHARE_DIR}/xray"
ASN_DIR="${OWNED_SHARE_DIR}/asn"
XRAY_BIN="${OWNED_LIB_DIR}/rw-core"
RNL_REPO="${RNL_REPO:-Luxiaba/remnawave-node-lite-go}"
RNL_TAG="${RNL_TAG:-v${VERSION}}"

YES=0
DRY_RUN=0
PURGE_CONFIG=0
PURGE_LOGS=0
PURGE_DATA=0
PURGE_XRAY=0
STAGE="初始化"
CONFIGURED_XRAY_BIN="$XRAY_BIN"

usage() {
  cat <<EOF
用法：uninstall.sh [选项]

Remnawave Node Lite (Go) 卸载 ${VERSION}

选项：
  --yes, -y           跳过确认（非交互）
  --dry-run           仅预览将删除的内容
  --purge             删除配置 + 日志 + 数据（保留 rw-core）
  --purge-all         删除全部（含 rw-core / geo 数据）
  --full              完全卸载（等同 --purge-all --yes，不逐项询问）
  --keep-config       仅卸载服务与二进制，保留 ${ETC_DIR}
  --help, -h          显示帮助

交互模式（默认）会逐项询问是否删除配置、日志、数据、rw-core。
Alpine 使用 OpenRC；其他发行版使用 systemd。
EOF
}

version() {
  echo "remnawave-node-lite uninstall ${VERSION}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
    --purge)
      PURGE_CONFIG=1
      PURGE_LOGS=1
      PURGE_DATA=1
      ;;
    --purge-all)
      PURGE_CONFIG=1
      PURGE_LOGS=1
      PURGE_DATA=1
      PURGE_XRAY=1
      ;;
    --full)
      PURGE_CONFIG=1
      PURGE_LOGS=1
      PURGE_DATA=1
      PURGE_XRAY=1
      YES=1
      ;;
    --keep-config) PURGE_CONFIG=0 ;;
    --help|-h) usage; exit 0 ;;
    --version) version; exit 0 ;;
    *)
      echo "未知参数：$1" >&2
      usage
      exit 1
      ;;
  esac
  shift
done

bootstrap_helper_is_trusted() {
  local script="$1" helper="$2" script_owner helper_owner trusted_uid
  local logical_dir physical_dir current owner mode links size function_name
  [ -f "$script" ] && [ ! -L "$script" ] \
    && [ -f "$helper" ] && [ ! -L "$helper" ] || return 1
  script_owner="$(stat -c '%u:%g' "$script" 2>/dev/null \
    || stat -f '%u:%g' "$script" 2>/dev/null)" || return
  helper_owner="$(stat -c '%u:%g' "$helper" 2>/dev/null \
    || stat -f '%u:%g' "$helper" 2>/dev/null)" || return
  trusted_uid="${script_owner%%:*}"
  [ "${helper_owner%%:*}" = "$trusted_uid" ] || return 1
  for current in "$script" "$helper"; do
    mode="$(stat -c '%a' "$current" 2>/dev/null \
      || stat -f '%Lp' "$current" 2>/dev/null)" || return
    links="$(stat -c '%h' "$current" 2>/dev/null \
      || stat -f '%l' "$current" 2>/dev/null)" || return
    [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] \
      && [ "$links" = 1 ] || return 1
  done
  logical_dir="$(cd "$(dirname "$helper")" && pwd -L)" || return
  physical_dir="$(cd "$(dirname "$helper")" && pwd -P)" || return
  [ "$logical_dir" = "$physical_dir" ] || return 1
  current="$physical_dir"
  while :; do
    [ -d "$current" ] && [ ! -L "$current" ] || return 1
    owner="$(stat -c '%u:%g' "$current" 2>/dev/null \
      || stat -f '%u:%g' "$current" 2>/dev/null)" || return
    mode="$(stat -c '%a' "$current" 2>/dev/null \
      || stat -f '%Lp' "$current" 2>/dev/null)" || return
    { [ "${owner%%:*}" = 0 ] || [ "${owner%%:*}" = "$trusted_uid" ]; } \
      && [[ "$mode" =~ ^[0-7]{3,4}$ ]] \
      && [ $((8#$mode & 022)) -eq 0 ] || return 1
    [ "$current" = / ] && break
    current="$(dirname "$current")" || return
  done
  size="$(wc -c <"$helper" | tr -d '[:space:]')" || return
  [[ "$size" =~ ^[0-9]+$ ]] && [ "$size" -le 1048576 ] || return 1
  for function_name in \
    installer_acquire_lock installer_run_nested installer_run_without_lock; do
    grep -Eq "^${function_name}\\(\\) [({]$" "$helper" || return 1
  done
}

bootstrap_run_without_installer_lock() (
  local fd="${RNL_INSTALLER_LOCK_FD:-${INSTALLER_LOCK_FD:-}}"
  if [[ "$fd" =~ ^[0-9]+$ ]] && [ "${#fd}" -le 6 ] && [ "$fd" -ge 10 ]; then
    exec {fd}>&-
  fi
  unset RNL_INSTALLER_LOCK_FD RNL_INSTALLER_LOCK_ID
  "$@"
)

bootstrap_path_owner_ids() {
  local path="$1" owner
  if owner="$(stat -c '%u:%g' "$path" 2>/dev/null)"; then
    printf '%s' "$owner"
    return 0
  fi
  stat -f '%u:%g' "$path" 2>/dev/null
}

bootstrap_path_mode() {
  local path="$1" mode
  if mode="$(stat -c '%a' "$path" 2>/dev/null)"; then
    printf '%s' "$mode"
    return 0
  fi
  stat -f '%Lp' "$path" 2>/dev/null
}

bootstrap_installed_helpers() {
  local link=/usr/local/lib/remnanode/support-current target base component
  local helper mode links current=/
  local -a components
  [ -L "$link" ] && [ "$(bootstrap_path_owner_ids "$link")" = 0:0 ] || return 1
  target="$(readlink "$link")" || return
  [[ "$target" =~ ^support/[A-Za-z0-9][A-Za-z0-9._-]*$ ]] || return 1
  [ "$target" = "support/${RNL_TAG}" ] || return 1
  base="$(dirname "$link")/$target"
  IFS=/ read -r -a components <<<"${base#/}"
  for component in "${components[@]}" scripts; do
    current="${current%/}/${component}"
    [ -d "$current" ] && [ ! -L "$current" ] \
      && [ "$(bootstrap_path_owner_ids "$current")" = 0:0 ] || return 1
    mode="$(bootstrap_path_mode "$current")" || return
    [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || return 1
  done
  helper="${base}/scripts/install-env-helpers.sh"
  [ -f "$helper" ] && [ ! -L "$helper" ] \
    && [ "$(bootstrap_path_owner_ids "$helper")" = 0:0 ] || return 1
  if links="$(stat -c '%h' "$helper" 2>/dev/null)"; then
    :
  else
    links="$(stat -f '%l' "$helper" 2>/dev/null)" || return
  fi
  [ "$links" = 1 ] || return 1
  mode="$(bootstrap_path_mode "$helper")" || return
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || return 1
  bootstrap_helper_is_trusted "$helper" "$helper" || return
  printf '%s' "$helper"
}

load_installer_helpers() {
  local helpers_dir helpers_tmp helpers_bytes installed_helpers function_name
  local -a helpers_status
  if [ -n "${BASH_SOURCE[0]:-}" ]; then
    helpers_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    if bootstrap_helper_is_trusted \
      "${BASH_SOURCE[0]}" "${helpers_dir}/install-env-helpers.sh"; then
      # shellcheck source=install-env-helpers.sh
      source "${helpers_dir}/install-env-helpers.sh"
      return
    fi
  fi
  if installed_helpers="$(bootstrap_installed_helpers)"; then
    # shellcheck disable=SC1090
    source "$installed_helpers"
    return
  fi
  if ! [[ "$RNL_REPO" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] \
    || ! [[ "$RNL_TAG" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "非法 RNL_REPO 或 RNL_TAG，拒绝下载 installer helper" >&2
    return 2
  fi
  if ! command -v curl >/dev/null 2>&1 || ! command -v head >/dev/null 2>&1; then
    echo "独立卸载脚本需要 curl 与 head 获取 installer helper" >&2
    return 1
  fi
  helpers_tmp="$(mktemp -d /var/tmp/remnanode-bootstrap.XXXXXX)" || return
  set +o pipefail
  bootstrap_run_without_installer_lock \
    curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 60 --speed-limit 1024 --speed-time 30 \
    --max-filesize 1048576 \
    "https://raw.githubusercontent.com/${RNL_REPO}/${RNL_TAG}/scripts/install-env-helpers.sh" \
    | bootstrap_run_without_installer_lock head -c 1048577 \
      >"${helpers_tmp}/install-env-helpers.sh"
  helpers_status=("${PIPESTATUS[@]}")
  set -o pipefail
  helpers_bytes="$(wc -c <"${helpers_tmp}/install-env-helpers.sh" | tr -d '[:space:]')"
  if [ "${helpers_status[0]:-1}" -ne 0 ] \
    || [ "${helpers_status[1]:-1}" -ne 0 ] \
    || [ "$helpers_bytes" -gt 1048576 ]; then
    rm -rf "$helpers_tmp"
    echo "installer helper 下载失败或超过 1048576 bytes 硬上限" >&2
    return 1
  fi
  for function_name in \
    installer_acquire_lock installer_run_nested installer_run_without_lock; do
    grep -Eq "^${function_name}\\(\\) [({]$" \
      "${helpers_tmp}/install-env-helpers.sh" || {
      rm -rf "$helpers_tmp"
      echo "installer helper 缺少锁 API：${function_name}" >&2
      return 1
    }
  done
  # shellcheck disable=SC1091
  if ! source "${helpers_tmp}/install-env-helpers.sh"; then
    rm -rf "$helpers_tmp"
    return 1
  fi
  rm -rf "$helpers_tmp"
}

if [ "$DRY_RUN" -eq 0 ] && [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行（Alpine 通常无 sudo）：su - 后执行 bash uninstall.sh" >&2
  exit 1
fi
load_installer_helpers

on_error() {
  local status="${1:-1}"
  local command="${2:-unknown}"
  echo "卸载失败：${STAGE}" >&2
  echo "失败命令：${command}" >&2
  exit "$status"
}

trap 'on_error $? "$BASH_COMMAND"' ERR

step() {
  STAGE="$1"
  echo "==> $1"
}

run() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] $*"
  else
    "$@"
  fi
}

read_tty() {
  local _var="$1"
  local _prompt="${2:-}"
  local _line=""
  if [ -n "$_prompt" ]; then
    if [ -t 0 ]; then
      read -r -p "$_prompt" _line || _line=""
    elif [ -r /dev/tty ]; then
      read -r -p "$_prompt" _line </dev/tty || _line=""
    else
      return 1
    fi
  else
    if [ -t 0 ]; then
      read -r _line || _line=""
    elif [ -r /dev/tty ]; then
      read -r _line </dev/tty || _line=""
    else
      return 1
    fi
  fi
  printf -v "$_var" '%s' "$_line"
}

cleanup_runtime() {
  step "清理本项目运行时"
  run rm -rf /run/remnanode 2>/dev/null || true
  run rm -f /run/remnawave-node-supervise.pid 2>/dev/null || true
  run rm -f /run/remnawave-internal-*.sock 2>/dev/null || true
  if [ "$PURGE_CONFIG" -eq 1 ]; then
    run rm -f "${ETC_DIR}/node.env.bak."* 2>/dev/null || true
  fi
}

cleanup_firewall() {
  step "清理本项目 nftables 私有表"
  if ! command -v nft >/dev/null 2>&1; then
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 删除存在的 ip/remnanode 与 ip6/remnanode6"
    return 0
  fi
  if nft list table ip remnanode >/dev/null 2>&1; then
    nft delete table ip remnanode
  fi
  if nft list table ip6 remnanode6 >/dev/null 2>&1; then
    nft delete table ip6 remnanode6
  fi
}

is_alpine() {
  [ -f /etc/alpine-release ]
}

require_root() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 运行（Alpine 通常无 sudo）：su - 后执行 bash uninstall.sh" >&2
    exit 1
  fi
}

installed() {
  [ -x "${PREFIX}/${BIN_NAME}" ] || \
    [ -f "$UNIT" ] || \
    [ -f "$OPENRC_SVC" ] || \
    [ -d "$ETC_DIR" ]
}

detect_install_type() {
  if [ -f "$OPENRC_SVC" ] || is_alpine; then
    echo "openrc"
  elif [ -f "$UNIT" ]; then
    echo "systemd"
  else
    echo "unknown"
  fi
}

current_version() {
  if [ -x "${PREFIX}/${BIN_NAME}" ]; then
    installer_run_without_lock "${PREFIX}/${BIN_NAME}" version 2>/dev/null || echo "unknown"
  else
    echo "not installed"
  fi
}

read_env_value() {
  local key="$1" file="$2" line value
  [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || return 2
  [ -f "$file" ] || return 0
  line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?${key}[[:space:]]*=" "$file" 2>/dev/null | tail -n 1 || true)"
  [ -n "$line" ] || return 0
  value="${line#*=}"
  value="$(printf '%s' "$value" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  case "$value" in
    \"*\") value=${value#\"}; value=${value%\"} ;;
    \'*\') value=${value#\'}; value=${value%\'} ;;
  esac
  printf '%s' "$value"
}

running_pids_for_binary() {
  local binary="$1" exe pid target
  for exe in /proc/[0-9]*/exe; do
    [ -e "$exe" ] || [ -L "$exe" ] || continue
    pid="${exe%/exe}"
    pid="${pid##*/}"
    if [ -e "$binary" ] && [ "$exe" -ef "$binary" ]; then
      printf '%s\n' "$pid"
      continue
    fi
    target="$(readlink "$exe" 2>/dev/null || true)"
    if [ "$target" = "$binary" ] || [ "$target" = "$binary (deleted)" ]; then
      printf '%s\n' "$pid"
    fi
  done
}

probe_uninstall_service_state() {
  local platform="$1" output="" load_state="" active_state="" status
  case "$platform" in
    openrc)
      if installer_run_without_lock \
        rc-service remnawave-node status >/dev/null 2>&1; then
        printf 'active'
        return 0
      else
        status=$?
      fi
      [ "$status" -eq 3 ] && printf 'inactive' || printf 'error'
      ;;
    systemd)
      if ! output="$(installer_run_without_lock systemctl show --no-pager \
        --property=LoadState --property=ActiveState \
        remnawave-node.service)"; then
        printf 'error'
        return 0
      fi
      while IFS='=' read -r property value; do
        case "$property" in
          LoadState) load_state="$value" ;;
          ActiveState) active_state="$value" ;;
        esac
      done <<<"$output"
      case "$load_state:$active_state" in
        loaded:active|loaded:reloading|masked:active|masked:reloading|not-found:active|not-found:reloading)
          printf 'active'
          ;;
        loaded:inactive|loaded:failed|masked:inactive|masked:failed|not-found:inactive|not-found:failed)
          printf 'inactive'
          ;;
        *) printf 'error' ;;
      esac
      ;;
    *) return 2 ;;
  esac
}

uninstall_service_manager_state() {
  local state aggregate=inactive
  if is_alpine || [ -f "$OPENRC_SVC" ]; then
    state="$(probe_uninstall_service_state openrc)" || return
    case "$state" in
      error) printf 'error'; return 0 ;;
      active) aggregate=active ;;
      inactive) ;;
      *) printf 'error'; return 0 ;;
    esac
  fi
  if [ -f "$UNIT" ]; then
    state="$(probe_uninstall_service_state systemd)" || return
    case "$state" in
      error) printf 'error'; return 0 ;;
      active) aggregate=active ;;
      inactive) ;;
      *) printf 'error'; return 0 ;;
    esac
  fi
  printf '%s' "$aggregate"
}

service_manager_active() {
  local state
  state="$(uninstall_service_manager_state)" || return 2
  case "$state" in
    active) return 0 ;;
    inactive) return 1 ;;
    *) return 2 ;;
  esac
}

wait_for_stop_confirmation() {
  local i=0 pids binary running manager_state
  while [ "$i" -lt 35 ]; do
    running=0
    manager_state="$(uninstall_service_manager_state)" || return
    case "$manager_state" in
      active) running=1 ;;
      inactive) ;;
      *)
        echo "无法可靠探测 remnawave-node 状态，拒绝确认停止" >&2
        return 1
        ;;
    esac
    for binary in "${PREFIX}/${BIN_NAME}" "$CONFIGURED_XRAY_BIN"; do
      pids="$(running_pids_for_binary "$binary")"
      [ -z "$pids" ] || running=1
    done
    [ "$running" -eq 0 ] && return 0
    sleep 1
    i=$((i + 1))
  done
  manager_state="$(uninstall_service_manager_state)" || return
  case "$manager_state" in
    active) echo "服务管理器仍报告 remnawave-node 运行中" >&2 ;;
    inactive) ;;
    *) echo "停止后无法可靠确认 remnawave-node 状态" >&2 ;;
  esac
  for binary in "${PREFIX}/${BIN_NAME}" "$CONFIGURED_XRAY_BIN"; do
    pids="$(running_pids_for_binary "$binary")"
    [ -z "$pids" ] || echo "仍有进程使用 ${binary}: ${pids//$'\n'/,}" >&2
  done
  return 1
}

prompt_yes_no() {
  local prompt="$1"
  local default="${2:-n}"
  if [ "$YES" -eq 1 ]; then
    return 0
  fi
  local hint="[y/N]"
  [ "$default" = "y" ] && hint="[Y/n]"
  local ans=""
  read_tty ans "${prompt} ${hint} " || ans=""
  ans="${ans:-$default}"
  case "$ans" in
    y|Y|yes|YES) return 0 ;;
    *) return 1 ;;
  esac
}

interactive_options() {
  if [ "$YES" -eq 1 ]; then
    return 0
  fi
  if [ "$PURGE_CONFIG" -eq 1 ] || [ "$PURGE_LOGS" -eq 1 ] || [ "$PURGE_DATA" -eq 1 ] || [ "$PURGE_XRAY" -eq 1 ]; then
    return 0
  fi

  echo
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " 卸载选项（回车=默认）"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo "当前版本：$(current_version)"
  echo "安装方式：$(detect_install_type)"
  echo

  prompt_yes_no "删除配置目录 ${ETC_DIR}（node.env / secret.key）？" n && PURGE_CONFIG=1
  prompt_yes_no "删除日志目录 ${LOG_DIR}？" n && PURGE_LOGS=1
  prompt_yes_no "删除数据目录 ${DATA_DIR}？" n && PURGE_DATA=1
  prompt_yes_no "删除 rw-core / Xray（${XRAY_BIN}）及 geo 数据？" n && PURGE_XRAY=1
  echo
}

print_plan() {
  echo "将执行："
  echo "  • 停止并移除服务（$(detect_install_type)）"
  echo "  • 删除二进制：${PREFIX}/${BIN_NAME}"
  echo "  • 删除辅助命令：${XLOGS}, ${XERRORS}"
  [ "$PURGE_CONFIG" -eq 1 ] && echo "  • 删除配置：${ETC_DIR}"
  [ "$PURGE_LOGS" -eq 1 ] && echo "  • 删除日志：${LOG_DIR}"
  [ "$PURGE_DATA" -eq 1 ] && echo "  • 删除数据：${DATA_DIR}"
  if [ "$PURGE_XRAY" -eq 1 ]; then
    echo "  • 删除 rw-core：${XRAY_BIN}"
    echo "  • 删除 geo：${GEO_DIR}"
    echo "  • 删除 ASN 数据：${ASN_DIR}"
    echo "  • 仅删除本项目专属目录，不删除通用 /usr/local/bin/xray 或 /usr/local/share/xray"
  fi
  echo
}

confirm_uninstall() {
  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  print_plan
  prompt_yes_no "确认卸载？" n || {
    echo "已取消。"
    exit 0
  }
}

stop_service() {
  local stop_failed=0 openrc_state=inactive systemd_state=inactive
  step "停止服务"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 停止服务并确认 remnanode-lite/rw-core 全部退出"
    return 0
  fi
  if is_alpine || [ -f "$OPENRC_SVC" ]; then
    command -v rc-service >/dev/null 2>&1 || {
      echo "存在 OpenRC 服务文件但缺少 rc-service，拒绝继续卸载" >&2
      return 1
    }
    openrc_state="$(probe_uninstall_service_state openrc)" || return
  fi
  if [ -f "$UNIT" ]; then
    command -v systemctl >/dev/null 2>&1 || {
      echo "存在 systemd unit 但缺少 systemctl，拒绝继续卸载" >&2
      return 1
    }
    systemd_state="$(probe_uninstall_service_state systemd)" || return
  fi
  if [ "$openrc_state" = error ] || [ "$systemd_state" = error ]; then
    echo "无法可靠探测 remnawave-node 状态；保留全部文件与数据" >&2
    return 1
  fi
  [ "$openrc_state" = inactive ] || [ "$openrc_state" = active ] || return 1
  [ "$systemd_state" = inactive ] || [ "$systemd_state" = active ] || return 1
  if [ "$openrc_state" = active ]; then
    rc-service remnawave-node stop >/dev/null 2>&1 || stop_failed=1
  fi
  if [ "$systemd_state" = active ]; then
    systemctl stop remnawave-node.service >/dev/null 2>&1 || stop_failed=1
  fi
  if ! wait_for_stop_confirmation; then
    echo "未确认服务与 rw-core 停止；保留防火墙、服务文件和全部数据" >&2
    return 1
  fi
  if [ "$stop_failed" -ne 0 ]; then
    echo "服务停止命令失败；保留防火墙、服务文件和全部数据" >&2
    return 1
  fi
  if is_alpine || [ -f "$OPENRC_SVC" ]; then
    rc-update del remnawave-node default 2>/dev/null || true
  fi
  if [ -f "$UNIT" ]; then
    systemctl disable remnawave-node.service >/dev/null 2>&1 || true
  fi
}

remove_service_files() {
  step "移除服务文件"
  if [ -f "$OPENRC_SVC" ]; then
    run rm -f "$OPENRC_SVC"
  fi
  if [ -f "$UNIT" ]; then
    run rm -f "$UNIT"
    run systemctl daemon-reload 2>/dev/null || true
  fi
}

remove_binaries() {
  step "删除二进制与辅助命令"
  run rm -f "${PREFIX}/${BIN_NAME}"
  run rm -f "${RUN_WRAPPER}"
  run rm -f "$XLOGS" "$XERRORS"
}

remove_optional_dirs() {
  if [ "$PURGE_CONFIG" -eq 1 ]; then
    step "删除配置 ${ETC_DIR}"
    run rm -rf "$ETC_DIR"
  else
    echo "保留配置：${ETC_DIR}"
  fi

  if [ "$PURGE_LOGS" -eq 1 ]; then
    step "删除日志 ${LOG_DIR}"
    run rm -rf "$LOG_DIR"
  else
    echo "保留日志：${LOG_DIR}"
  fi

  if [ "$PURGE_DATA" -eq 1 ]; then
    step "删除数据 ${DATA_DIR}"
    run rm -rf "$DATA_DIR"
  else
    echo "保留数据：${DATA_DIR}"
  fi
}

remove_xray() {
  if [ "$PURGE_XRAY" -ne 1 ]; then
    echo "保留 rw-core：${XRAY_BIN}"
    return 0
  fi
  step "删除 rw-core 与 geo 数据"
  run rm -rf "$OWNED_LIB_DIR" "$OWNED_SHARE_DIR"
}

main() {
  require_root
  if [ "$DRY_RUN" -eq 0 ]; then
    installer_acquire_lock || return
  fi

  if ! installed; then
    echo "未检测到 remnawave-node-lite 安装痕迹。"
    exit 0
  fi

  interactive_options
  confirm_uninstall
  print_plan

  CONFIGURED_XRAY_BIN="$(read_env_value XRAY_BIN "${ETC_DIR}/node.env")"
  [ -n "$CONFIGURED_XRAY_BIN" ] || CONFIGURED_XRAY_BIN="$XRAY_BIN"
  CONFIGURED_XRAY_BIN="$(readlink -f "$CONFIGURED_XRAY_BIN" 2>/dev/null || printf '%s' "$CONFIGURED_XRAY_BIN")"

  stop_service
  cleanup_runtime
  cleanup_firewall
  remove_service_files
  remove_binaries
  remove_optional_dirs
  remove_xray
  cleanup_runtime

  echo
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "预览完成（dry-run），未实际删除。"
  else
    echo "卸载完成。"
    [ "$PURGE_CONFIG" -eq 0 ] && [ -d "$ETC_DIR" ] && echo "  配置保留：${ETC_DIR}（重装可复用）"
    [ "$PURGE_XRAY" -eq 0 ] && [ -x "$XRAY_BIN" ] && echo "  rw-core 保留：${XRAY_BIN}"
    echo "  系统用户 remnanode 保留，供保留配置或后续重装复用。"
    echo
    echo "重新安装："
    if is_alpine; then
      echo "  curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v${VERSION}/scripts/install-node-alpine.sh | bash"
    else
      echo "  curl -fsSL https://raw.githubusercontent.com/Luxiaba/remnawave-node-lite-go/v${VERSION}/scripts/install-node.sh | sudo bash"
    fi
  fi
}

main "$@"
