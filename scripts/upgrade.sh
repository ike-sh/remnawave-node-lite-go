#!/usr/bin/env bash
# remnawave-node-lite-go 升级脚本（保留 node.env 与 rw-core）
# shellcheck source-path=SCRIPTDIR
set -Eeuo pipefail

VERSION="0.1.0"
PREFIX="/usr/local/bin"
ETC_DIR="/etc/remnanode"
UNIT="/etc/systemd/system/remnawave-node.service"
OPENRC_SVC="/etc/init.d/remnawave-node"
BIN_NAME="remnanode-lite"
NODE_ENV="${ETC_DIR}/node.env"
SECRET_FILE="${ETC_DIR}/secret.key"
DATA_DIR="/var/lib/remnanode"
LOG_DIR="/var/log/remnanode"
SERVICE_USER="remnanode"
SERVICE_GROUP="remnanode"
XRAY_BIN="/usr/local/lib/remnanode/rw-core"
GEO_DIR="/usr/local/share/remnanode/xray"
ASN_DIR="/usr/local/share/remnanode/asn"
SUPPORT_LINK="/usr/local/lib/remnanode/support-current"
REPO="${RNL_REPO:-Luxiaba/remnawave-node-lite-go}"
BOOTSTRAP_TAG="${RNL_TAG:-v${VERSION}}"
if ! command -v curl >/dev/null 2>&1; then
  echo "缺少命令：curl" >&2
  exit 1
fi
if [ -n "${BASH_SOURCE[0]:-}" ]; then
  _HELPERS_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_DIR}/install-env-helpers.sh"
else
  if ! [[ "$REPO" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]] \
    || ! [[ "$BOOTSTRAP_TAG" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "非法 RNL_REPO 或 RNL_TAG，拒绝下载 bootstrap helper" >&2
    exit 2
  fi
  _HELPERS_TMP="$(mktemp -d)"
  curl --fail --location --silent --show-error --proto '=https' --tlsv1.2 \
    "https://raw.githubusercontent.com/${REPO}/${BOOTSTRAP_TAG}/scripts/install-env-helpers.sh" \
    -o "${_HELPERS_TMP}/install-env-helpers.sh"
  # shellcheck source=install-env-helpers.sh
  source "${_HELPERS_TMP}/install-env-helpers.sh"
  rm -rf "${_HELPERS_TMP}"
fi
TAG="$(resolve_install_tag "$REPO" "v${VERSION}")"
UPGRADE_XRAY="${RNL_UPGRADE_XRAY:-0}"

YES=0
DRY_RUN=0
STAGE="初始化"
BACKUP_DIR=""
ROLLBACK_ARMED=0
SERVICE_WAS_ACTIVE=0

usage() {
  cat <<EOF
用法：upgrade.sh [--yes] [--dry-run] [--upgrade-xray] [--help] [--version]

Remnawave Node Lite (Go) 升级到 ${TAG}

环境变量：
  RNL_REPO           GitHub 仓库，默认 Luxiaba/remnawave-node-lite-go
  RNL_TAG            Release 标签；未设置时固定为 v${VERSION}
  RNL_UPGRADE_XRAY   设为 1 时同时运行 install-xray.sh
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
  trap - ERR
  echo "升级失败：${STAGE}" >&2
  echo "失败命令：${command}" >&2
  if [ "$ROLLBACK_ARMED" -eq 1 ]; then
    rollback_upgrade || echo "自动回滚未完整成功，请检查 ${BACKUP_DIR}" >&2
  fi
  exit "$status"
}

trap 'on_error $? "$BASH_COMMAND"' ERR

step() {
  STAGE="$1"
  echo "==> $1"
}

is_alpine() {
  [ -f /etc/alpine-release ]
}

require_root() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 运行：sudo bash upgrade.sh" >&2
    exit 1
  fi
}

require_command() {
  if ! command -v "$1" >/dev/null 2>&1; then
    echo "缺少命令：$1" >&2
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
  echo "当前：$(current_version)"
  echo "目标：${TAG}"
  read -r -p "继续升级？[y/N] " ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "已取消。"; exit 0 ;;
  esac
}

service_is_active() {
  if is_alpine; then
    rc-service remnawave-node status >/dev/null 2>&1
  else
    systemctl is-active --quiet remnawave-node.service
  fi
}

backup_path() {
  local source="$1" name="$2"
  if [ -e "$source" ] || [ -L "$source" ]; then
    cp -a "$source" "$BACKUP_DIR/$name"
  else
    : >"$BACKUP_DIR/$name.absent"
  fi
}

begin_upgrade_transaction() {
  step "创建升级事务备份"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 备份 binary / service / support / 可选 rw-core 资产"
    return 0
  fi

  BACKUP_DIR="$(mktemp -d /tmp/remnanode-upgrade.XXXXXX)"
  backup_path "${PREFIX}/${BIN_NAME}" binary
  backup_path "$NODE_ENV" node-env
  backup_path "$UNIT" systemd-unit
  backup_path "$OPENRC_SVC" openrc-service
  if [ -L "$SUPPORT_LINK" ]; then
    local support_target support_path
    support_target="$(readlink "$SUPPORT_LINK")"
    if ! [[ "$support_target" =~ ^support/[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
      echo "拒绝备份异常 support 链接：${SUPPORT_LINK} -> ${support_target}" >&2
      return 1
    fi
    support_path="/usr/local/lib/remnanode/${support_target}"
    if [ ! -d "$support_path" ] || find "$support_path" -type l -print -quit | grep -q .; then
      echo "拒绝备份缺失或含链接的 support 目录：${support_path}" >&2
      return 1
    fi
    printf '%s\n' "$support_target" >"$BACKUP_DIR/support-link"
    mkdir "$BACKUP_DIR/support-content"
    cp -a "$support_path/." "$BACKUP_DIR/support-content/"
  elif [ -e "$SUPPORT_LINK" ]; then
    echo "拒绝覆盖非符号链接的 ${SUPPORT_LINK}" >&2
    return 1
  else
    : >"$BACKUP_DIR/support-link.absent"
  fi
  if [ "$UPGRADE_XRAY" -eq 1 ]; then
    backup_path "$XRAY_BIN" rw-core
    backup_path "$GEO_DIR" geo
    backup_path "$ASN_DIR" asn
  fi
  ROLLBACK_ARMED=1
}

restore_path() {
  local backup="$1" target="$2"
  if [ -e "$BACKUP_DIR/$backup" ] || [ -L "$BACKUP_DIR/$backup" ]; then
    rm -rf "$target"
    cp -a "$BACKUP_DIR/$backup" "$target"
  elif [ -f "$BACKUP_DIR/$backup.absent" ]; then
    rm -rf "$target"
  fi
}

rollback_upgrade() {
  local failed=0
  echo "==> 自动回滚升级" >&2
  if is_alpine; then
    rc-service remnawave-node stop >/dev/null 2>&1 || true
  else
    systemctl stop remnawave-node.service >/dev/null 2>&1 || true
  fi

  restore_path binary "${PREFIX}/${BIN_NAME}" || failed=1
  restore_path node-env "$NODE_ENV" || failed=1
  restore_path systemd-unit "$UNIT" || failed=1
  restore_path openrc-service "$OPENRC_SVC" || failed=1
  rm -f "$SUPPORT_LINK" || failed=1
  rm -rf "/usr/local/lib/remnanode/support/$TAG" || failed=1
  if [ -f "$BACKUP_DIR/support-link" ]; then
    local support_target
    support_target="/usr/local/lib/remnanode/$(cat "$BACKUP_DIR/support-link")"
    rm -rf "$support_target" || failed=1
    cp -a "$BACKUP_DIR/support-content" "$support_target" || failed=1
    ln -s "$(cat "$BACKUP_DIR/support-link")" "$SUPPORT_LINK" || failed=1
  fi
  if [ "$UPGRADE_XRAY" -eq 1 ]; then
    restore_path rw-core "$XRAY_BIN" || failed=1
    restore_path geo "$GEO_DIR" || failed=1
    restore_path asn "$ASN_DIR" || failed=1
  fi

  if ! is_alpine; then
    systemctl daemon-reload >/dev/null 2>&1 || failed=1
  fi
  if [ "$SERVICE_WAS_ACTIVE" -eq 1 ]; then
    if is_alpine; then
      rc-service remnawave-node start >/dev/null 2>&1 || failed=1
    else
      systemctl start remnawave-node.service >/dev/null 2>&1 || failed=1
    fi
    local port
    port="$(read_env_value NODE_PORT "$NODE_ENV")"
    [ -n "$port" ] || port=2222
    if ! wait_for_service_stable "$port" 30; then
      echo "回滚后的服务未在 :${port} 恢复监听" >&2
      failed=1
    fi
  fi
  ROLLBACK_ARMED=0
  if [ "$failed" -ne 0 ]; then
    echo "回滚不完整；备份目录：${BACKUP_DIR}" >&2
    return 1
  fi
  echo "已恢复升级前文件与服务。备份目录：${BACKUP_DIR}" >&2
}

commit_upgrade_transaction() {
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  ROLLBACK_ARMED=0
  local support_root="/usr/local/lib/remnanode/support"
  local support_dir
  for support_dir in "$support_root"/*; do
    [ -e "$support_dir" ] || continue
    if [ "$support_dir" != "$support_root/$TAG" ] && ! rm -rf "$support_dir"; then
      echo "警告：无法清理旧 support 目录 ${support_dir}" >&2
    fi
  done
  if [ -n "$BACKUP_DIR" ]; then
    rm -rf "$BACKUP_DIR" || echo "警告：无法清理事务备份 ${BACKUP_DIR}" >&2
    BACKUP_DIR=""
  fi
}

download_binary() {
  local arch="$1"
  step "下载 ${BIN_NAME} ${TAG} (linux/${arch})"
  install_release_binary "$REPO" "$TAG" "$arch" "${PREFIX}/${BIN_NAME}"
}

upgrade_xray() {
  if [ "$UPGRADE_XRAY" -ne 1 ]; then
    echo "跳过 rw-core 升级（设 RNL_UPGRADE_XRAY=1 或 --upgrade-xray 可启用）。"
    return 0
  fi

  step "升级 rw-core"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 执行目标 Release 中已校验的 install-xray.sh"
    return 0
  fi
  local support
  support="$(installed_support_file scripts/install-xray.sh)"
  [ -f "$support" ] || { echo "缺少已校验 install-xray.sh" >&2; return 1; }
  RNL_REPO="$REPO" RNL_TAG="$TAG" bash "$support"
}

refresh_systemd() {
  if is_alpine; then
    return 0
  fi

  step "刷新 systemd unit"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 更新 ${UNIT}"
    return 0
  fi

  local support
  support="$(installed_support_file deploy/remnawave-node.service)"
  [ -f "$support" ] || { echo "缺少已校验 systemd unit" >&2; return 1; }
  install -m 0644 "$support" "$UNIT"
  systemctl daemon-reload
}

refresh_openrc() {
  if ! is_alpine; then
    return 0
  fi

  step "刷新 OpenRC 服务文件"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 更新 ${OPENRC_SVC}"
    return 0
  fi

  local support
  support="$(installed_support_file deploy/remnawave-node.openrc)"
  [ -f "$support" ] || { echo "缺少已校验 OpenRC service" >&2; return 1; }
  install -m 0755 "$support" "$OPENRC_SVC"
}

restart_service() {
  step "重启 remnawave-node"
  if [ "$DRY_RUN" -eq 1 ]; then
    if is_alpine; then
      echo "[dry-run] rc-service remnawave-node restart"
    else
      echo "[dry-run] systemctl restart remnawave-node"
    fi
    return 0
  fi
  if [ ! -f "$NODE_ENV" ]; then
    echo "未找到 ${NODE_ENV}，请先运行 install 脚本。" >&2
    exit 1
  fi

  if [ "$SERVICE_WAS_ACTIVE" -eq 0 ]; then
    echo "服务升级前未运行，保留 stopped 状态。"
    return 0
  fi

  if is_alpine; then
    rc-service remnawave-node restart
    sleep 1
    rc-service remnawave-node status || true
  else
    systemctl restart remnawave-node.service
    sleep 1
    systemctl --no-pager status remnawave-node.service || true
  fi
}

verify_upgrade() {
  if [ "$DRY_RUN" -eq 1 ] || [ "$SERVICE_WAS_ACTIVE" -eq 0 ]; then
    return 0
  fi
  local port
  port="$(read_env_value NODE_PORT "$NODE_ENV")"
  [ -n "$port" ] || port=2222
  verify_service_listening "$port"
}

main() {
  require_root
  require_command curl

  if is_alpine; then
    require_command rc-service
  else
    require_command systemctl
  fi

  if [ ! -f "${PREFIX}/${BIN_NAME}" ] && [ "$DRY_RUN" -eq 0 ]; then
    if is_alpine; then
      echo "未检测到已安装的 ${BIN_NAME}，请先运行 install-node-alpine.sh。" >&2
    else
      echo "未检测到已安装的 ${BIN_NAME}，请先运行 install-node.sh。" >&2
    fi
    exit 1
  fi

  confirm_upgrade

  if [ "$DRY_RUN" -eq 0 ] && service_is_active; then
    SERVICE_WAS_ACTIVE=1
  fi

  local arch
  arch="$(detect_arch)"

  echo "升级前：$(current_version)"
  ensure_service_account
  setup_service_directories
  begin_upgrade_transaction
  download_binary "$arch"
  upgrade_xray
  migrate_owned_asset_paths
  refresh_systemd
  refresh_openrc
  normalize_service_permissions
  restart_service
  verify_upgrade
  commit_upgrade_transaction

  echo
  echo "升级完成。"
  echo "  当前版本：$(current_version)"
  echo "  配置保留：${NODE_ENV}"
  if is_alpine; then
    echo "  日志：    tail -f /var/log/remnanode/openrc.log"
  else
    echo "  日志：    journalctl -u remnawave-node -f"
  fi
}

main "$@"
