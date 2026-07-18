#!/usr/bin/env bash
# remnawave-node-lite-go Alpine Linux 一键安装（OpenRC）
# shellcheck source-path=SCRIPTDIR
set -Eeuo pipefail

VERSION="0.1.0"
PREFIX="/usr/local/bin"
ETC_DIR="/etc/remnanode"
DATA_DIR="/var/lib/remnanode"
LOG_DIR="/var/log/remnanode"
OPENRC_SVC="/etc/init.d/remnawave-node"
BIN_NAME="remnanode-lite"
NODE_ENV="${ETC_DIR}/node.env"
SECRET_FILE="${ETC_DIR}/secret.key"
SERVICE_USER="remnanode"
SERVICE_GROUP="remnanode"
REPO="${RNL_REPO:-Luxiaba/remnawave-node-lite-go}"
BOOTSTRAP_TAG="${RNL_TAG:-v${VERSION}}"
RESTART_CMD="rc-service remnawave-node restart"
export RESTART_CMD

if ! command -v curl >/dev/null 2>&1; then
  echo "缺少命令：curl（Alpine: apk add --no-cache curl bash）" >&2
  exit 1
fi
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-env-helpers.sh" ]; then
  _HELPERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_DIR}/install-env-helpers.sh"
else
  if ! [[ "$REPO" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] \
    || ! [[ "$BOOTSTRAP_TAG" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "非法 RNL_REPO 或 RNL_TAG，拒绝下载 bootstrap helper" >&2
    exit 2
  fi
  _HELPERS_TMP="$(mktemp -d /var/tmp/remnanode-bootstrap.XXXXXX)"
  trap 'rm -rf "${_HELPERS_TMP:-}"' EXIT
  set +o pipefail
  curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
    --connect-timeout 15 --max-time 60 --speed-limit 1024 --speed-time 30 \
    --max-filesize 1048576 \
    "https://raw.githubusercontent.com/${REPO}/${BOOTSTRAP_TAG}/scripts/install-env-helpers.sh" \
    | head -c 1048577 >"${_HELPERS_TMP}/install-env-helpers.sh"
  _HELPERS_DOWNLOAD_STATUS=("${PIPESTATUS[@]}")
  set -o pipefail
  _HELPERS_DOWNLOAD_BYTES="$(wc -c <"${_HELPERS_TMP}/install-env-helpers.sh" | tr -d '[:space:]')"
  if [ "${_HELPERS_DOWNLOAD_STATUS[0]:-1}" -ne 0 ] \
    || [ "${_HELPERS_DOWNLOAD_STATUS[1]:-1}" -ne 0 ] \
    || [ "$_HELPERS_DOWNLOAD_BYTES" -gt 1048576 ]; then
    echo "bootstrap helper 下载失败或超过 1048576 bytes 硬上限" >&2
    exit 1
  fi
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_TMP}/install-env-helpers.sh"
  rm -rf "${_HELPERS_TMP}"
  trap - EXIT
fi
TAG="$(resolve_install_tag "$REPO" "v${VERSION}")"
INSTALL_XRAY="${RNL_INSTALL_XRAY:-1}"
SKIP_XRAY="${RNL_SKIP_XRAY:-0}"
SECRET_FILE_ARG=""

YES=0
DRY_RUN=0
LOW_MEMORY=0
PORT_EXPLICIT=0
ACTION=""
UNINSTALL_MODE=""
STAGE="初始化"
DELEGATE_TO_UPGRADE=0

usage() {
  cat <<EOF
用法：install-node-alpine.sh [选项]

Remnawave Node Lite (Go) ${VERSION} — Alpine / OpenRC 安装 / 升级 / 卸载

无参数时在终端显示菜单；非交互请指定动作：
  --install           全新安装；检测到完整安装时走事务升级
  --upgrade           事务升级 Node/service/support（默认保留 rw-core）
  --uninstall         卸载

其它选项：
  --yes, -y           跳过确认
  --dry-run           预览
  --skip-xray         跳过 rw-core
  --low-memory        低内存模式
  --port PORT         监听端口（默认 2222）
  --secret-file PATH  从文件导入 Secret Key
  --help, -h          帮助
  --version           版本

一键入口（Alpine 无 sudo，root 下直接 bash）：
  apk add --no-cache curl bash
  curl -fsSL https://raw.githubusercontent.com/${REPO}/v${VERSION}/scripts/install-node-alpine.sh | bash
EOF
}

version() {
  echo "remnawave-node-lite alpine install ${VERSION}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --install) ACTION=install ;;
    --upgrade) ACTION=upgrade ;;
    --uninstall) ACTION=uninstall ;;
    --menu) ACTION=menu ;;
    --yes|-y) YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
    --skip-xray) SKIP_XRAY=1 ;;
    --low-memory) LOW_MEMORY=1 ;;
    --port)
      NODE_PORT="${2:-}"
      if [ -z "$NODE_PORT" ]; then
        echo "--port 需要端口号" >&2
        exit 1
      fi
      PORT_EXPLICIT=1
      shift 2
      continue
      ;;
    --secret-file)
      SECRET_FILE_ARG="${2:-}"
      if [ -z "$SECRET_FILE_ARG" ]; then
        echo "--secret-file 需要文件路径" >&2
        exit 1
      fi
      shift 2
      continue
      ;;
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
  echo "安装失败：${STAGE}" >&2
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

script_dir() {
  if [ -n "${BASH_SOURCE[0]:-}" ]; then
    cd "$(dirname "${BASH_SOURCE[0]}")" && pwd
  else
    echo ""
  fi
}

run_sibling_script() {
  local name="$1"
  shift
  local dir
  dir="$(script_dir)"
  if [ -n "$dir" ] && [ -f "${dir}/${name}" ]; then
    bash "${dir}/${name}" "$@"
  else
    local support
    support="$(installed_support_file "scripts/${name}")"
    if [ ! -f "$support" ]; then
      echo "缺少已校验 support 脚本：${support}" >&2
      return 1
    fi
    bash "$support" "$@"
  fi
}

run_upgrade_transaction() {
  local upgrade_xray=1
  local -a args=(--yes)
  if [ "$SKIP_XRAY" -eq 1 ] || [ "$INSTALL_XRAY" -eq 0 ]; then
    upgrade_xray=0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    args+=(--dry-run)
  fi
  if [ "$LOW_MEMORY" -eq 1 ]; then
    args+=(--low-memory)
  fi

  echo "检测到完整安装，交由 upgrade.sh 执行可回滚升级。"
  RNL_REPO="$REPO" RNL_TAG="$TAG" RNL_UPGRADE_XRAY="$upgrade_xray" \
    RNL_ENSURE_SERVICE_STARTED=1 RNL_ENSURE_SERVICE_ENABLED=1 \
    run_sibling_script upgrade.sh "${args[@]}"
}

run_explicit_upgrade() {
  local -a args=(--yes)
  if [ "$DRY_RUN" -eq 1 ]; then
    args+=(--dry-run)
  fi
  if [ "$LOW_MEMORY" -eq 1 ]; then
    args+=(--low-memory)
  fi
  RNL_REPO="$REPO" RNL_TAG="$TAG" RNL_UPGRADE_XRAY=0 \
    run_sibling_script upgrade.sh "${args[@]}"
}

run_selected_uninstall() {
  local -a args
  if [ "${UNINSTALL_MODE:-}" = "full" ]; then
    args=(--full)
  else
    args=(--keep-config --yes)
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    args+=(--dry-run)
  fi
  run_sibling_script uninstall.sh "${args[@]}"
}

show_menu() {
  echo
  echo "Remnawave Node Lite ${VERSION} (contract 2.8.0) — Alpine"
  echo "  1) 安装"
  echo "  2) 升级"
  echo "  3) 卸载"
  echo "  4) 退出"
  echo
  local choice=""
  read_tty choice "请选择 [1-4]: " || {
    echo "无法读取输入。非交互请用: --install | --upgrade | --uninstall" >&2
    exit 1
  }
  case "$choice" in
    1) ACTION=install ;;
    2) ACTION=upgrade ;;
    3) ACTION=uninstall ;;
    4) exit 0 ;;
    *)
      echo "无效选择：${choice}" >&2
      exit 1
      ;;
  esac
}

show_uninstall_menu() {
  echo
  echo "卸载选项："
  echo "  1) 仅卸服务（保留 node.env / rw-core）"
  echo "  2) 完全卸载（配置+日志+rw-core 全删）"
  echo "  3) 返回"
  local choice=""
  read_tty choice "请选择 [1-3]: " || exit 1
  case "$choice" in
    1) UNINSTALL_MODE=keep ;;
    2) UNINSTALL_MODE=full ;;
    3) exit 0 ;;
    *)
      echo "无效选择" >&2
      exit 1
      ;;
  esac
}

dispatch_action() {
  case "$ACTION" in
    install) do_install ;;
    upgrade) run_explicit_upgrade ;;
    uninstall)
      show_uninstall_menu
      run_selected_uninstall
      ;;
    menu) show_menu; dispatch_action ;;
    *)
      echo "未知动作：${ACTION}" >&2
      usage
      exit 1
      ;;
  esac
}

require_root() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 运行（Alpine 通常无 sudo）：" >&2
    echo "  su -" >&2
    echo "  curl -fsSL .../install-node-alpine.sh | bash" >&2
    exit 1
  fi
}

require_alpine() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ ! -f /etc/alpine-release ]; then
    echo "此脚本仅适用于 Alpine Linux（未找到 /etc/alpine-release）。" >&2
    echo "Debian/Ubuntu 等请使用：scripts/install-node.sh" >&2
    exit 1
  fi
}

validate_port() {
  local port="$1"
  if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    echo "无效端口：${port}（有效范围 1-65535）" >&2
    exit 1
  fi
}

effective_node_port() {
  echo "${NODE_PORT:-2222}"
}

configured_node_port() {
  local configured
  configured="$(read_env_value NODE_PORT "$NODE_ENV")"
  if [ -n "$configured" ]; then
    printf '%s\n' "$configured"
    return 0
  fi
  effective_node_port
}

prompt_node_port() {
  if [ -n "${NODE_PORT:-}" ] || [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  echo
  local input=""
  read_tty input "NODE 监听端口（Panel 连接用，默认 2222）: " || input=""
  NODE_PORT="${input:-2222}"
  validate_port "$NODE_PORT"
}

confirm_install() {
  if [ ! -x "${PREFIX}/${BIN_NAME}" ] || [ ! -f "$NODE_ENV" ] \
    || [ ! -f "$OPENRC_SVC" ] || [ -L "$OPENRC_SVC" ]; then
    if [ -x "${PREFIX}/${BIN_NAME}" ] || [ -f "$NODE_ENV" ] || [ -e "$OPENRC_SVC" ]; then
      echo "检测到未完成的安装，继续执行安装恢复而不是委托 stopped-state 升级。"
    fi
    return 0
  fi

  if [ "$PORT_EXPLICIT" -eq 1 ] || [ -n "$SECRET_FILE_ARG" ]; then
    echo "已有安装的事务升级不接受 --port / --secret-file；请先升级，再单独修改 ${NODE_ENV}。" >&2
    return 1
  fi

  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    DELEGATE_TO_UPGRADE=1
    return 0
  fi
  echo
  echo "检测到本机已安装 remnawave-node-lite。"
  echo "  1) 升级（保留 ${NODE_ENV}）"
  echo "  2) 全新安装（删除配置/日志后重装）"
  echo "  3) 取消"
  local choice=""
  read_tty choice "请选择 [1-3]: " || {
    echo "非交互环境请用: --yes 或 --install" >&2
    exit 1
  }
  case "$choice" in
    1) DELEGATE_TO_UPGRADE=1 ;;
    2)
      if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] 删除 ${ETC_DIR} ${LOG_DIR} ${DATA_DIR}"
      else
        local configured_xray previous_port
        configured_xray="$(read_env_value XRAY_BIN "$NODE_ENV")" || return
        [ -n "$configured_xray" ] || configured_xray=/usr/local/lib/remnanode/rw-core
        configured_xray="$(canonical_binary_path "$configured_xray")" || return
        previous_port="$(configured_node_port)" || return
        stop_for_fresh_reinstall openrc "${PREFIX}/${BIN_NAME}" \
          "$configured_xray" "$previous_port" || return
        rm -rf "$ETC_DIR" "$LOG_DIR" "$DATA_DIR"
        cleanup_runtime
        rm -f "${ETC_DIR}.bak."* 2>/dev/null || true
        echo "已清除旧配置，开始全新安装。"
      fi
      ;;
    *)
      echo "已取消。"
      exit 0
      ;;
  esac
}

update_node_port_in_env() {
  local port="$1"
  validate_port "$port"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 更新 ${NODE_ENV} NODE_PORT=${port}"
    return 0
  fi
  set_env_value NODE_PORT "$port"
  echo "已设置 NODE_PORT=${port}"
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "不支持的架构：$(uname -m)" >&2
      exit 1
      ;;
  esac
}

install_packages() {
  step "安装 Alpine 依赖包"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] apk add --no-cache bash curl tar unzip ca-certificates libcap openrc iproute2 nftables"
    return 0
  fi
  require_free_bytes / 536870912 "系统依赖与安装工作区"
  apk add --no-cache bash curl tar unzip ca-certificates libcap openrc iproute2 nftables
}

download_binary() {
  local arch="$1"
  step "下载 ${BIN_NAME} ${TAG} (linux/${arch})"
  install_release_binary "$REPO" "$TAG" "$arch" "${PREFIX}/${BIN_NAME}"
}

install_xray() {
  if [ "$SKIP_XRAY" -eq 1 ] || [ "$INSTALL_XRAY" -eq 0 ]; then
    echo "跳过 rw-core 安装。"
    return 0
  fi

  step "安装 rw-core (Xray core)"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 执行目标 Release 中已校验的 install-xray.sh"
    return 0
  fi
  local support
  support="$(installed_support_file scripts/install-xray.sh)"
  [ -f "$support" ] || { echo "缺少已校验 install-xray.sh" >&2; return 1; }
  RNL_REPO="$REPO" RNL_TAG="$TAG" bash "$support"
}

setup_directories() {
  step "创建目录"
  setup_service_directories
}

setup_env_file() {
  step "配置 ${NODE_ENV}"
  local port
  port="$(effective_node_port)"
  validate_port "$port"

  if [ -f "$NODE_ENV" ]; then
    if [ "$PORT_EXPLICIT" -eq 1 ] || [ -n "${NODE_PORT:-}" ]; then
      update_node_port_in_env "$port"
    else
      echo "保留现有配置：${NODE_ENV}（NODE_PORT=$(configured_node_port)）"
    fi
    if [ "$LOW_MEMORY" -eq 1 ]; then
      set_env_value LOW_MEMORY 1
      echo "已启用 LOW_MEMORY=1"
    fi
    return 0
  fi

  local low_mem="${LOW_MEMORY:-0}"
  if [ "$LOW_MEMORY" -eq 1 ]; then
    low_mem=1
  fi

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 创建 ${NODE_ENV}"
    return 0
  fi

  render_env_template "$port" "$low_mem" "install-node-alpine.sh" >"$NODE_ENV"
  secure_config_file "$NODE_ENV"
  echo "已创建 ${NODE_ENV}"
}

setup_secret_file() {
  step "配置 Secret Key"

  migrate_inline_secret_to_file

  if secret_configured; then
    if [ -s "$SECRET_FILE" ]; then
      validate_secret_file "$SECRET_FILE"
    fi
    if secret_from_env_file; then
      echo "保留现有 SECRET_KEY（${NODE_ENV}）"
    else
      echo "保留现有 Secret Key：${SECRET_FILE}"
    fi
    return 0
  fi

  if [ -n "$SECRET_FILE_ARG" ]; then
    if [ ! -f "$SECRET_FILE_ARG" ]; then
      echo "找不到 --secret-file 指定路径：${SECRET_FILE_ARG}" >&2
      exit 1
    fi
    write_secret_from_source "$SECRET_FILE_ARG"
    echo "已从文件导入 Secret Key（SECRET_KEY_FILE 模式）。"
    return 0
  fi

  prompt_secret_key
}

install_openrc() {
  step "安装 OpenRC 服务"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 安装 ${OPENRC_SVC}"
    return 0
  fi

  local support
  support="$(installed_support_file deploy/remnawave-node.openrc)" || return
  [ -f "$support" ] || { echo "缺少已校验 OpenRC service" >&2; return 1; }
  install_managed_file "$support" "$OPENRC_SVC" 0755 || return

  rc-update add remnawave-node default || return
}

install_log_helper_command() (
  local target="$1" log_file="$2" tmp=""

  validate_managed_parent_path "$target" || return
  if [ -e "$target" ] || [ -L "$target" ]; then
    validate_managed_regular_file "$target" || return
  fi

  tmp="$(mktemp "$(dirname "$target")/.$(basename "$target").XXXXXX")" || return
  trap 'if [ -n "${tmp:-}" ]; then rm -f -- "$tmp"; fi' EXIT
  [ -f "$tmp" ] && [ ! -L "$tmp" ] || {
    echo "日志辅助命令 staging 不是普通文件：${tmp}" >&2
    return 1
  }
  printf '%s\n' '#!/bin/sh' "exec tail -n +1 -f ${log_file}" >"$tmp" || return
  chmod 0755 "$tmp" || return
  chown root:root "$tmp" || return
  validate_managed_regular_file "$tmp" || return
  mv -f -- "$tmp" "$target" || return
  tmp=""
  validate_managed_regular_file "$target"
)

install_helpers() {
  step "安装日志辅助命令"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] remnanode-xlogs / remnanode-xerrors"
    return 0
  fi

  install_log_helper_command "${PREFIX}/remnanode-xlogs" \
    /var/log/remnanode/xray.out.log || return
  install_log_helper_command "${PREFIX}/remnanode-xerrors" \
    /var/log/remnanode/xray.err.log || return
}

start_service() {
  if ! secret_configured; then
    echo "⚠ Secret Key 未配置，跳过启动服务。"
    echo "  请将 Secret Key 写入 ${SECRET_FILE} 并确认 ${NODE_ENV} 中的 NODE_PORT 后：${RESTART_CMD}"
    return 0
  fi

  step "启动 remnawave-node 服务"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] ${RESTART_CMD}"
    return 0
  fi

  rc-service remnawave-node restart
  sleep 1
  rc-service remnawave-node status || true
}

main() {
  require_root
  require_alpine
  if [ -z "$ACTION" ]; then
    show_menu
  fi
  dispatch_action
}

detect_low_memory_auto() {
  if [ "$LOW_MEMORY" -eq 1 ]; then
    return 0
  fi
  local total_kb=""
  total_kb="$(awk '/MemTotal:/ {print $2}' /proc/meminfo 2>/dev/null || true)"
  if [ -n "$total_kb" ] && [ "$total_kb" -le 524288 ]; then
    LOW_MEMORY=1
    echo "检测到内存 ${total_kb}KB（≤512MB），自动启用低内存模式 LOW_MEMORY=1"
  fi
}

do_install() {
  require_root
  require_alpine

	confirm_install
	if [ "$DELEGATE_TO_UPGRADE" -eq 1 ]; then
		run_upgrade_transaction
		return 0
	fi
	detect_low_memory_auto

	local arch
	arch="$(detect_arch)"
	install_packages
	require_command head
	require_command tar
	require_command timeout
	ensure_service_account
	setup_directories
	print_pre_install_panel_hint
  download_binary "$arch"
  prompt_node_port
  setup_env_file
  normalize_runtime_environment
  ensure_internal_socket_in_env
  setup_secret_file
  install_xray
  migrate_owned_asset_paths
  install_geo_extra_files
  normalize_service_permissions
  install_openrc
  install_helpers
  start_service
  if secret_configured; then
    verify_service_listening "$(configured_node_port)"
    print_panel_address_hint "$(configured_node_port)"
  fi

  echo
  echo "Alpine 安装完成。"
  echo "  二进制：    ${PREFIX}/${BIN_NAME}"
  echo "  环境配置：  ${NODE_ENV}"
  echo "  监听端口：  $(configured_node_port)（Panel 须填相同端口）"
  echo "  服务管理：  rc-service remnawave-node {start|stop|restart|status}"
  echo "  日志：      tail -f /var/log/remnanode/openrc.log"
  echo "  Xray：      remnanode-xlogs / remnanode-xerrors"
  echo "  管理：      再次运行 install-node-alpine.sh 可升级或卸载"
  if ! secret_configured; then
    print_env_config_hint "$RESTART_CMD"
  fi
}

main "$@"
