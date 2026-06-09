#!/usr/bin/env bash
# remnawave-node-lite-go Alpine Linux 一键安装（OpenRC）
set -euo pipefail

VERSION="0.8.8"
PREFIX="/usr/local/bin"
ETC_DIR="/etc/remnanode"
DATA_DIR="/var/lib/remnanode"
LOG_DIR="/var/log/remnanode"
OPENRC_SVC="/etc/init.d/remnawave-node"
RUN_WRAPPER="${PREFIX}/remnawave-node-run"
BIN_NAME="remnanode-lite"
NODE_ENV="${ETC_DIR}/node.env"
SECRET_FILE="${ETC_DIR}/secret.key"
REPO="${RNL_REPO:-ike-sh/remnawave-node-lite-go}"
_SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
# shellcheck source=install-env-helpers.sh
source "${_SCRIPT_DIR}/install-env-helpers.sh"
TAG="$(resolve_install_tag "$REPO" "v${VERSION}")"
INSTALL_XRAY="${RNL_INSTALL_XRAY:-1}"
SKIP_XRAY="${RNL_SKIP_XRAY:-0}"
SECRET_FILE_ARG=""

YES=0
DRY_RUN=0
LOW_MEMORY=0
PORT_EXPLICIT=0
STAGE="初始化"

usage() {
  cat <<EOF
用法：install-node-alpine.sh [--yes] [--dry-run] [--skip-xray] [--low-memory] [--port PORT] [--secret-file PATH] [--help] [--version]

Remnawave Node Lite (Go) ${VERSION} — Alpine Linux / OpenRC 一键安装

环境变量：
  RNL_REPO          GitHub 仓库，默认 ike-sh/remnawave-node-lite-go
  RNL_TAG           Release 标签，默认 v${VERSION}
  RNL_INSTALL_XRAY  是否安装 rw-core，默认 1
  RNL_SKIP_XRAY     设为 1 跳过 rw-core 安装
  SECRET_KEY        非交互模式可直接传入（写入 secret.key）
  NODE_PORT         监听端口，默认 2222（与 --port 等效）
  LOW_MEMORY        设为 1 启用低内存模式

示例：
  NODE_PORT=8443 curl -fsSL .../install-node-alpine.sh | bash
  curl -fsSL .../install-node-alpine.sh | bash -s -- --port 8443 --low-memory
EOF
}

version() {
  echo "remnawave-node-lite alpine install ${VERSION}"
}

while [ $# -gt 0 ]; do
  case "$1" in
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
  read -r -p "NODE 监听端口（Panel 连接用，默认 2222）: " input || input=""
  NODE_PORT="${input:-2222}"
  validate_port "$NODE_PORT"
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
    echo "[dry-run] apk add --no-cache bash curl tar ca-certificates libcap openrc"
    return 0
  fi
  apk add --no-cache bash curl tar ca-certificates libcap openrc
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

apply_capabilities() {
  step "授予 CAP_NET_ADMIN（nftables / ss -K）"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] setcap cap_net_admin+ep ${PREFIX}/${BIN_NAME}"
    return 0
  fi
  if ! command -v setcap >/dev/null 2>&1; then
    echo "警告：未找到 setcap，nftables 插件可能不可用。" >&2
    return 0
  fi
  setcap cap_net_admin+ep "${PREFIX}/${BIN_NAME}"
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

  render_env_template "$port" "$low_mem" "install-node-alpine.sh" >"$NODE_ENV"
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

  write_secret_from_env
  if secret_configured; then
    return 0
  fi

  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi

  print_env_config_hint "rc-service remnawave-node restart"
}

install_openrc() {
  step "安装 OpenRC 服务"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 安装 ${RUN_WRAPPER} 与 ${OPENRC_SVC}"
    return 0
  fi

  if [ -f "${script_dir}/../deploy/remnawave-node-run.sh" ]; then
    install -m 0755 "${script_dir}/../deploy/remnawave-node-run.sh" "$RUN_WRAPPER"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/deploy/remnawave-node-run.sh" -o "$RUN_WRAPPER"
    chmod 0755 "$RUN_WRAPPER"
  fi

  if [ -f "${script_dir}/../deploy/remnawave-node.openrc" ]; then
    install -m 0755 "${script_dir}/../deploy/remnawave-node.openrc" "$OPENRC_SVC"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/deploy/remnawave-node.openrc" -o "$OPENRC_SVC"
    chmod 0755 "$OPENRC_SVC"
  fi

  rc-update add remnawave-node default 2>/dev/null || true
}

install_helpers() {
  step "安装日志辅助命令"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] xlogs / xerrors"
    return 0
  fi

  cat >"${PREFIX}/xlogs" <<'EOF'
#!/bin/sh
exec tail -n +1 -f /var/log/remnanode/xray.out.log
EOF
  cat >"${PREFIX}/xerrors" <<'EOF'
#!/bin/sh
exec tail -n +1 -f /var/log/remnanode/xray.err.log
EOF
  chmod +x "${PREFIX}/xlogs" "${PREFIX}/xerrors"
}

start_service() {
  if ! secret_configured; then
    echo "⚠ Secret Key 未配置，跳过启动服务。"
    echo "  请编辑 ${NODE_ENV} 填入 NODE_PORT 与 SECRET_KEY 后：rc-service remnawave-node restart"
    return 0
  fi

  step "启动 remnawave-node 服务"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] rc-service remnawave-node restart"
    return 0
  fi

  rc-service remnawave-node restart
  sleep 1
  rc-service remnawave-node status || true
}

main() {
  require_root
  require_alpine

  local arch
  arch="$(detect_arch)"

  install_packages
  setup_directories
  download_binary "$arch"
  apply_capabilities
  install_xray
  prompt_node_port
  setup_env_file
  setup_secret_file
  install_openrc
  install_helpers
  start_service

  echo
  echo "Alpine 安装完成。"
  echo "  二进制：    ${PREFIX}/${BIN_NAME}"
  echo "  环境配置：  ${NODE_ENV}"
  echo "  监听端口：  $(configured_node_port)（Panel 须填相同端口）"
  echo "  配置文件：  ${NODE_ENV}（NODE_PORT + SECRET_KEY）"
  echo "  服务管理：  rc-service remnawave-node {start|stop|restart|status}"
  echo "  日志：      tail -f /var/log/remnanode/openrc.log"
  if ! secret_configured; then
    print_env_config_hint "rc-service remnawave-node restart"
  fi
}

main "$@"
