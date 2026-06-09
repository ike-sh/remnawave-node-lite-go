#!/usr/bin/env bash
# remnawave-node-lite-go 一键安装脚本
set -euo pipefail

VERSION="0.8.11"
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
if [ -n "${BASH_SOURCE[0]:-}" ]; then
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
STAGE="初始化"

usage() {
  cat <<EOF
用法：install-node.sh [--yes] [--dry-run] [--skip-xray] [--low-memory] [--port PORT] [--secret-file PATH] [--help] [--version]

Remnawave Node Lite (Go) ${VERSION} 一键安装

Secret Key（Panel 下发，通常很长）推荐写入独立文件，避免 .env 单行过长：
  ${SECRET_FILE}

环境变量：
  RNL_REPO          GitHub 仓库，默认 ike-sh/remnawave-node-lite-go
  RNL_TAG           Release 标签；未设置时自动取 GitHub 最新 Release（回退 v${VERSION}）
  RNL_INSTALL_XRAY  是否安装 rw-core，默认 1
  RNL_SKIP_XRAY     设为 1 跳过 rw-core 安装
  SECRET_KEY        非交互模式可直接传入（写入 secret.key）
  NODE_PORT         监听端口，默认 2222（与 --port 等效）
  LOW_MEMORY        设为 1 启用低内存模式（64MB body limit + GOMEMLIMIT）

示例：
  NODE_PORT=8443 curl -fsSL .../install-node.sh | sudo bash
  curl -fsSL .../install-node.sh | sudo bash -s -- --port 8443 --yes
EOF
}

version() {
  echo "remnawave-node-lite install ${VERSION}"
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

  write_secret_from_env
  if secret_configured; then
    return 0
  fi

  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi

  print_env_config_hint "sudo systemctl restart remnawave-node"
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
  redirect_alpine
  require_command curl
  require_command systemctl

  local arch
  arch="$(detect_arch)"

  setup_directories
  download_binary "$arch"
  install_xray
  prompt_node_port
  setup_env_file
  setup_secret_file
  install_systemd
  install_helpers
  start_service

  echo
  echo "安装完成。"
  echo "  二进制：  ${PREFIX}/${BIN_NAME}"
  echo "  环境配置：${NODE_ENV}"
  echo "  监听端口：$(configured_node_port)（Panel 须填相同端口）"
  echo "  配置文件：${NODE_ENV}（NODE_PORT + SECRET_KEY）"
  echo "  日志：    journalctl -u remnawave-node -f"
  echo "  Xray：    xlogs / xerrors"
  if ! secret_configured; then
    print_env_config_hint "sudo systemctl restart remnawave-node"
  fi
}

main "$@"
