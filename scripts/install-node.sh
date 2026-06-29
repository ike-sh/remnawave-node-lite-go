#!/usr/bin/env bash
# remnawave-node-lite-go 一键安装脚本
set -euo pipefail

VERSION="1.1.0"
PREFIX="/usr/local/bin"
ETC_DIR="/etc/remnanode"
DATA_DIR="/var/lib/remnanode"
LOG_DIR="/var/log/remnanode"
UNIT="/etc/systemd/system/remnawave-node.service"
BIN_NAME="remnanode-lite"
NODE_ENV="${ETC_DIR}/node.env"
SECRET_FILE="${ETC_DIR}/secret.key"
REPO="${RNL_REPO:-ike-sh/remnawave-node-lite-go}"  # must match internal/version/version.go releaseRepo
if ! command -v curl >/dev/null 2>&1; then
  echo "缺少命令：curl" >&2
  exit 1
fi
if [ -n "${BASH_SOURCE[0]:-}" ] && [ -f "$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)/install-env-helpers.sh" ]; then
  _HELPERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_DIR}/install-env-helpers.sh"
else
  _HELPERS_TMP="$(mktemp -d)"
  curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/scripts/install-env-helpers.sh" \
    -o "${_HELPERS_TMP}/install-env-helpers.sh"
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_TMP}/install-env-helpers.sh"
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

usage() {
  cat <<EOF
用法：install-node.sh [选项]

Remnawave Node Lite (Go) ${VERSION} — 安装 / 升级 / 卸载

无参数时在终端显示菜单；非交互请指定动作：
  --install           安装（或覆盖升级二进制，保留 node.env）
  --upgrade           仅升级二进制
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

一键入口（推荐）：
  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install-node.sh | sudo bash
EOF
}

version() {
  echo "remnawave-node-lite install ${VERSION}"
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
  echo "安装失败：${STAGE}" >&2
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
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/main/scripts/${name}" | bash -s -- "$@"
  fi
}

show_menu() {
  echo
  echo "Remnawave Node Lite ${VERSION} (contract 2.7.0)"
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
    upgrade)
      run_sibling_script upgrade.sh --yes
      ;;
    uninstall)
      show_uninstall_menu
      if [ "${UNINSTALL_MODE:-}" = "full" ]; then
        run_sibling_script uninstall.sh --full
      else
        run_sibling_script uninstall.sh --keep-config --yes
      fi
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
    echo "请使用 root 运行：sudo bash install-node.sh" >&2
    exit 1
  fi
}

redirect_alpine() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ -f /etc/alpine-release ]; then
    echo "检测到 Alpine Linux，请使用专用安装脚本："
    echo "  curl -fsSL https://raw.githubusercontent.com/${REPO}/main/scripts/install-node-alpine.sh | bash"
    exit 1
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令：$1" >&2
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
  if [ -f "$NODE_ENV" ] && grep -q '^NODE_PORT=' "$NODE_ENV" 2>/dev/null; then
    grep '^NODE_PORT=' "$NODE_ENV" | head -n 1 | cut -d= -f2-
  else
    effective_node_port
  fi
}

prompt_node_port() {
  if [ -n "${NODE_PORT:-}" ] || [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  echo
  local input=""
  if [ -t 0 ]; then
    read -r -p "NODE 监听端口（Panel 连接用，默认 2222）: " input || input=""
  elif [ -r /dev/tty ]; then
    read -r -p "NODE 监听端口（Panel 连接用，默认 2222）: " input </dev/tty || input=""
  fi
  NODE_PORT="${input:-2222}"
  validate_port "$NODE_PORT"
}

confirm_install() {
  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ ! -x "${PREFIX}/${BIN_NAME}" ] && [ ! -f "$NODE_ENV" ]; then
    return 0
  fi
  echo
  echo "检测到本机已安装 remnawave-node-lite。"
  echo "  1) 升级（保留 ${NODE_ENV}）"
  echo "  2) 全新安装（删除配置/日志后重装）"
  echo "  3) 取消"
  local choice=""
  read_tty choice "请选择 [1-3]: " || {
    echo "非交互环境请用: --yes 或 menu 选升级" >&2
    exit 1
  }
  case "$choice" in
    1) ;;
    2)
      if [ "$DRY_RUN" -eq 1 ]; then
        echo "[dry-run] 删除 ${ETC_DIR} ${LOG_DIR} ${DATA_DIR}"
      else
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
  if grep -q '^NODE_PORT=' "$NODE_ENV"; then
    sed -i "s/^NODE_PORT=.*/NODE_PORT=${port}/" "$NODE_ENV"
  else
    echo "NODE_PORT=${port}" >>"$NODE_ENV"
  fi
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

download_binary() {
  local arch="$1"
  local url="https://github.com/${REPO}/releases/download/${TAG}/remnanode-lite_linux_${arch}.tar.gz"
  local tmp
  tmp="$(mktemp -d)"

  step "下载 ${BIN_NAME} ${TAG} (linux/${arch})"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] curl -fsSL ${url}"
    echo "[dry-run] install ${PREFIX}/${BIN_NAME}"
    rm -rf "$tmp"
    return 0
  fi

  curl -fsSL "${url}" -o "${tmp}/archive.tar.gz"
  tar -xzf "${tmp}/archive.tar.gz" -C "${tmp}"
  install -m 0755 "${tmp}/${BIN_NAME}" "${PREFIX}/${BIN_NAME}"
  rm -rf "$tmp"

  "${PREFIX}/${BIN_NAME}" version
}

install_xray() {
  if [ "$SKIP_XRAY" -eq 1 ] || [ "$INSTALL_XRAY" -eq 0 ]; then
    echo "跳过 rw-core 安装。"
    return 0
  fi

  step "安装 rw-core (Xray core)"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [ -f "${script_dir}/install-xray.sh" ]; then
    bash "${script_dir}/install-xray.sh"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/scripts/install-xray.sh" | bash
  fi
}

setup_directories() {
  step "创建目录"
  run mkdir -p "$ETC_DIR" "$DATA_DIR" "$LOG_DIR"
  run chmod 0755 "$ETC_DIR" "$DATA_DIR" "$LOG_DIR"
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

  render_env_template "$port" "$low_mem" "install-node.sh" >"$NODE_ENV"
  chmod 600 "$NODE_ENV"
  echo "已创建 ${NODE_ENV}"
}

setup_secret_file() {
  step "配置 Secret Key"

  if secret_configured; then
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

install_systemd() {
  step "安装 systemd 服务"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 安装 ${UNIT}"
    return 0
  fi

  if [ -f "${script_dir}/../deploy/remnawave-node.service" ]; then
    install -m 0644 "${script_dir}/../deploy/remnawave-node.service" "$UNIT"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/deploy/remnawave-node.service" -o "$UNIT"
  fi

  systemctl daemon-reload
  systemctl enable remnawave-node.service
}

install_helpers() {
  step "安装日志辅助命令"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] xlogs / xerrors"
    return 0
  fi

  cat >/usr/local/bin/xlogs <<'EOF'
#!/bin/sh
exec tail -n +1 -f /var/log/remnanode/xray.out.log
EOF
  cat >/usr/local/bin/xerrors <<'EOF'
#!/bin/sh
exec tail -n +1 -f /var/log/remnanode/xray.err.log
EOF
  chmod +x /usr/local/bin/xlogs /usr/local/bin/xerrors
}

start_service() {
  if ! secret_configured; then
    echo "⚠ Secret Key 未配置，跳过启动服务。"
    echo "  请编辑 ${NODE_ENV} 填入 NODE_PORT 与 SECRET_KEY 后：systemctl restart remnawave-node"
    return 0
  fi

  step "启动 remnawave-node 服务"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] systemctl restart remnawave-node"
    return 0
  fi

  systemctl restart remnawave-node.service
  sleep 1
  systemctl --no-pager status remnawave-node.service || true
}

main() {
  require_root
  if [ -z "$ACTION" ]; then
    show_menu
  fi
  dispatch_action
}

do_install() {
  require_root
  redirect_alpine
  require_command curl
  require_command systemctl

  local arch
  arch="$(detect_arch)"

  setup_directories
  confirm_install
  print_pre_install_panel_hint
  download_binary "$arch"
  install_xray
  install_geo_extra_files
  prompt_node_port
  setup_env_file
  ensure_internal_socket_in_env
  setup_secret_file
  install_systemd
  install_helpers
  start_service
  verify_service_listening "$(configured_node_port)"
  print_panel_address_hint "$(configured_node_port)"

  echo
  echo "安装完成。"
  echo "  二进制：  ${PREFIX}/${BIN_NAME}"
  echo "  环境配置：${NODE_ENV}"
  echo "  监听端口：$(configured_node_port)（Panel 须填相同端口）"
  echo "  配置文件：${NODE_ENV}（NODE_PORT + SECRET_KEY）"
  echo "  日志：    journalctl -u remnawave-node -f"
  echo "  Xray：    xlogs / xerrors"
  echo "  管理：    再次运行 install-node.sh 可升级或卸载"
  if ! secret_configured; then
    print_env_config_hint "sudo systemctl restart remnawave-node"
  fi
}

main "$@"
