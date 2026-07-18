#!/usr/bin/env bash
# remnawave-node-lite-go 卸载脚本（systemd / Alpine OpenRC）
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
    "${PREFIX}/${BIN_NAME}" version 2>/dev/null || echo "unknown"
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
      if rc-service remnawave-node status >/dev/null 2>&1; then
        printf 'active'
        return 0
      else
        status=$?
      fi
      [ "$status" -eq 3 ] && printf 'inactive' || printf 'error'
      ;;
    systemd)
      if ! output="$(systemctl show --no-pager \
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
