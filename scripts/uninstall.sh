#!/usr/bin/env bash
# remnawave-node-lite-go 卸载脚本（systemd / Alpine OpenRC）
set -euo pipefail

VERSION="0.8.30"
PREFIX="/usr/local/bin"
BIN_NAME="remnanode-lite"
RUN_WRAPPER="${PREFIX}/remnawave-node-run"
UNIT="/etc/systemd/system/remnawave-node.service"
OPENRC_SVC="/etc/init.d/remnawave-node"
ETC_DIR="/etc/remnanode"
LOG_DIR="/var/log/remnanode"
DATA_DIR="/var/lib/remnanode"
GEO_DIR="/usr/local/share/xray"
XRAY_BIN="/usr/local/bin/rw-core"
XRAY_LEGACY="/usr/local/bin/xray"

YES=0
DRY_RUN=0
PURGE_CONFIG=0
PURGE_LOGS=0
PURGE_DATA=0
PURGE_XRAY=0
STAGE="初始化"

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
  echo "卸载失败：${STAGE}" >&2
  echo "失败命令：${BASH_COMMAND}" >&2
  exit $?
}

trap on_error ERR

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
  step "清理运行时（rw-core 进程 / socket）"
  run pkill -x rw-core 2>/dev/null || true
  run pkill -f '/usr/local/bin/rw-core' 2>/dev/null || true
  run rm -rf /run/remnanode 2>/dev/null || true
  run rm -f /run/remnawave-internal-*.sock 2>/dev/null || true
  if [ "$PURGE_CONFIG" -eq 1 ]; then
    run rm -f "${ETC_DIR}/node.env.bak."* 2>/dev/null || true
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
  echo "  • 删除辅助命令：xlogs, xerrors, ${RUN_WRAPPER}"
  [ "$PURGE_CONFIG" -eq 1 ] && echo "  • 删除配置：${ETC_DIR}"
  [ "$PURGE_LOGS" -eq 1 ] && echo "  • 删除日志：${LOG_DIR}"
  [ "$PURGE_DATA" -eq 1 ] && echo "  • 删除数据：${DATA_DIR}"
  if [ "$PURGE_XRAY" -eq 1 ]; then
    echo "  • 删除 rw-core：${XRAY_BIN}"
    echo "  • 删除 geo：${GEO_DIR}"
    [ -e "$XRAY_LEGACY" ] && echo "  • 删除 xray：${XRAY_LEGACY}"
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
  step "停止服务"
  if is_alpine || [ -f "$OPENRC_SVC" ]; then
    run rc-service remnawave-node stop 2>/dev/null || true
    run rc-update del remnawave-node default 2>/dev/null || true
  fi
  if [ -f "$UNIT" ]; then
    run systemctl stop remnawave-node.service 2>/dev/null || true
    run systemctl disable remnawave-node.service 2>/dev/null || true
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
  run rm -f "${PREFIX}/xlogs" "${PREFIX}/xerrors"
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
  run rm -f "$XRAY_BIN"
  if [ -L "$XRAY_LEGACY" ] || [ -f "$XRAY_LEGACY" ]; then
    run rm -f "$XRAY_LEGACY"
  fi
  run rm -rf "$GEO_DIR"
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

  stop_service
  cleanup_runtime
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
    echo
    echo "重新安装："
    if is_alpine; then
      echo "  curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v${VERSION}/scripts/install-node-alpine.sh | bash"
    else
      echo "  curl -fsSL https://raw.githubusercontent.com/ike-sh/remnawave-node-lite-go/v${VERSION}/scripts/install-node.sh | sudo bash"
    fi
  fi
}

main "$@"
