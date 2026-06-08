#!/usr/bin/env bash
# remnawave-node-lite-go 升级脚本（保留 node.env 与 rw-core）
set -euo pipefail

VERSION="0.8.1"PREFIX="/usr/local/bin"
ETC_DIR="/etc/remnanode"
UNIT="/etc/systemd/system/remnawave-node.service"
BIN_NAME="remnanode-lite"
NODE_ENV="${ETC_DIR}/node.env"
REPO="${RNL_REPO:-ike-sh/remnawave-node-lite-go}"
TAG="${RNL_TAG:-v${VERSION}}"
UPGRADE_XRAY="${RNL_UPGRADE_XRAY:-0}"

YES=0
DRY_RUN=0
STAGE="初始�?

usage() {
  cat <<EOF
用法：upgrade.sh [--yes] [--dry-run] [--upgrade-xray] [--help] [--version]

Remnawave Node Lite (Go) 升级�?${TAG}

环境变量�?  RNL_REPO           GitHub 仓库，默�?ike-sh/remnawave-node-lite-go
  RNL_TAG            Release 标签，默�?v${VERSION}
  RNL_UPGRADE_XRAY   设为 1 时同时运�?install-xray.sh
EOF
}

version() {
  echo "remnawave-node-lite upgrade ${VERSION}"
}

while [ $# -gt 0 ]; do
  case "$1" in
    --yes|-y) YES=1 ;;
    --dry-run) DRY_RUN=1 ;;
    --upgrade-xray) UPGRADE_XRAY=1 ;;
    --help|-h) usage; exit 0 ;;
    --version) version; exit 0 ;;
    *)
      echo "未知参数�?1" >&2
      usage
      exit 1
      ;;
  esac
  shift
done

on_error() {
  echo "升级失败�?{STAGE}" >&2
  echo "失败命令�?{BASH_COMMAND}" >&2
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
    echo "请使�?root 运行：sudo bash upgrade.sh" >&2
    exit 1
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令�?1" >&2
    exit 1
  fi
}

detect_arch() {
  case "$(uname -m)" in
    x86_64|amd64) echo "amd64" ;;
    aarch64|arm64) echo "arm64" ;;
    *)
      echo "不支持的架构�?(uname -m)" >&2
      exit 1
      ;;
  esac
}

current_version() {
  if [ -x "${PREFIX}/${BIN_NAME}" ]; then
    "${PREFIX}/${BIN_NAME}" version 2>/dev/null || echo "unknown"
  else
    echo "not installed"
  fi
}

confirm_upgrade() {
  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  echo "当前�?(current_version)"
  echo "目标�?{TAG}"
  read -r -p "继续升级？[y/N] " ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "已取消�?; exit 0 ;;
  esac
}

backup_binary() {
  step "备份当前二进�?
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] cp ${PREFIX}/${BIN_NAME} ${PREFIX}/${BIN_NAME}.bak"
    return 0
  fi
  if [ -f "${PREFIX}/${BIN_NAME}" ]; then
    cp -a "${PREFIX}/${BIN_NAME}" "${PREFIX}/${BIN_NAME}.bak.$(date +%Y%m%d%H%M%S)"
  fi
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

upgrade_xray() {
  if [ "$UPGRADE_XRAY" -ne 1 ]; then
    echo "跳过 rw-core 升级（设 RNL_UPGRADE_XRAY=1 �?--upgrade-xray 可启用）�?
    return 0
  fi

  step "升级 rw-core"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  if [ -f "${script_dir}/install-xray.sh" ]; then
    bash "${script_dir}/install-xray.sh"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/scripts/install-xray.sh" | bash
  fi
}

refresh_systemd() {
  step "刷新 systemd unit"
  local script_dir
  script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 更新 ${UNIT}"
    return 0
  fi

  if [ -f "${script_dir}/../deploy/remnawave-node.service" ]; then
    install -m 0644 "${script_dir}/../deploy/remnawave-node.service" "$UNIT"
  else
    curl -fsSL "https://raw.githubusercontent.com/${REPO}/${TAG}/deploy/remnawave-node.service" -o "$UNIT"
  fi
  systemctl daemon-reload
}

restart_service() {
  step "重启 remnawave-node"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] systemctl restart remnawave-node"
    return 0
  fi
  if [ ! -f "$NODE_ENV" ]; then
    echo "�?未找�?${NODE_ENV}，请先运�?install-node.sh" >&2
    exit 1
  fi
  systemctl restart remnawave-node.service
  sleep 1
  systemctl --no-pager status remnawave-node.service || true
}

main() {
  require_root
  require_command curl
  require_command systemctl

  if [ ! -f "${PREFIX}/${BIN_NAME}" ] && [ "$DRY_RUN" -eq 0 ]; then
    echo "未检测到已安装的 ${BIN_NAME}，请先运�?install-node.sh" >&2
    exit 1
  fi

  confirm_upgrade

  local arch
  arch="$(detect_arch)"

  echo "升级前：$(current_version)"
  backup_binary
  download_binary "$arch"
  upgrade_xray
  refresh_systemd
  restart_service

  echo
  echo "升级完成�?
  echo "  当前版本�?(current_version)"
  echo "  配置保留�?{NODE_ENV}"
  echo "  日志�?    journalctl -u remnawave-node -f"
  echo
  echo "若升级后异常，可恢复备份�?
  echo "  ls ${PREFIX}/${BIN_NAME}.bak.*"
}

main "$@"
