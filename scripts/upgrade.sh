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
UPGRADE_XRAY="${RNL_UPGRADE_XRAY:-0}"
ENSURE_SERVICE_STARTED="${RNL_ENSURE_SERVICE_STARTED:-0}"
ENSURE_SERVICE_ENABLED="${RNL_ENSURE_SERVICE_ENABLED:-0}"

YES=0
DRY_RUN=0
STAGE="初始化"
BACKUP_DIR=""
ROLLBACK_ARMED=0
SERVICE_WAS_ACTIVE=0
SERVICE_SHOULD_BE_ACTIVE=0
SERVICE_WAS_ENABLED=0
SERVICE_ENABLED_STATE_CAPTURED=0
SERVICE_ENABLE_MUTATION_ATTEMPTED=0
LOW_MEMORY=0

usage() {
  cat <<EOF
用法：upgrade.sh [--yes] [--dry-run] [--upgrade-xray] [--low-memory] [--help] [--version]

Remnawave Node Lite (Go) 升级到 ${TAG}

环境变量：
  RNL_REPO           GitHub 仓库，默认 Luxiaba/remnawave-node-lite-go
  RNL_TAG            Release 标签；未设置时固定为 v${VERSION}
  RNL_UPGRADE_XRAY   设为 1 时同时运行 install-xray.sh
  RNL_ENSURE_SERVICE_STARTED
                     仅由 install 入口设置；配置有效时确保恢复安装后启动服务
  RNL_ENSURE_SERVICE_ENABLED
                     仅由 install 入口设置；确保服务已注册为开机启动

选项：
  --low-memory       强制迁移为 LOW_MEMORY=1（适用于 512MiB 节点）
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
    --low-memory) LOW_MEMORY=1 ;;
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

cleanup_unarmed_upgrade_backup() {
  local root backup_name
  [ "$ROLLBACK_ARMED" -eq 0 ] || {
    echo "拒绝清理已 armed 的升级备份：${BACKUP_DIR}" >&2
    return 1
  }
  [ -n "$BACKUP_DIR" ] || return 0

  root="$(installer_temp_root)" || return
  validate_installer_temp_root_path "$root" || return
  validate_installer_temp_root_marker "$root" || return
  [ "$(dirname "$BACKUP_DIR")" = "$root" ] || {
    echo "拒绝清理安装临时根以外的升级备份：${BACKUP_DIR}" >&2
    return 1
  }
  backup_name="$(basename "$BACKUP_DIR")" || return
  [[ "$backup_name" =~ ^upgrade\.[A-Za-z0-9]{6}$ ]] || {
    echo "拒绝清理名称异常的升级备份：${BACKUP_DIR}" >&2
    return 1
  }
  if [ ! -e "$BACKUP_DIR" ] && [ ! -L "$BACKUP_DIR" ]; then
    BACKUP_DIR=""
    return 0
  fi
  [ -d "$BACKUP_DIR" ] && [ ! -L "$BACKUP_DIR" ] || {
    echo "拒绝清理非普通目录升级备份：${BACKUP_DIR}" >&2
    return 1
  }
  validate_existing_owned_directory "$BACKUP_DIR" 0 0 || return
  rm -rf -- "$BACKUP_DIR" || return
  BACKUP_DIR=""
}

on_error() {
  local status="${1:-1}"
  local command="${2:-unknown}"
  trap - ERR
  echo "升级失败：${STAGE}" >&2
  echo "失败命令：${command}" >&2
  if [ "$ROLLBACK_ARMED" -eq 1 ]; then
    rollback_upgrade || echo "自动回滚未完整成功，请检查 ${BACKUP_DIR}" >&2
  elif [ -n "$BACKUP_DIR" ]; then
    cleanup_unarmed_upgrade_backup \
      || echo "未能安全清理未 armed 的升级备份：${BACKUP_DIR}" >&2
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
  local platform state
  if is_alpine; then
    platform=openrc
  else
    platform=systemd
  fi
  state="$(probe_remnanode_service_state "$platform")" || return 2
  case "$state" in
    active) return 0 ;;
    inactive) return 1 ;;
    *) return 2 ;;
  esac
}

service_is_enabled() {
  local output status
  if is_alpine; then
    if output="$(RC_NOCOLOR=yes rc-update show default)"; then
      if grep -Eq '^[[:space:]]*remnawave-node([[:space:]]|$)' <<<"$output"; then
        return 0
      fi
      return 1
    fi
    return 2
  fi

  if output="$(systemctl is-enabled remnawave-node.service 2>/dev/null)"; then
    status=0
  else
    status=$?
  fi
  case "$output" in
    enabled) return 0 ;;
    disabled|enabled-runtime|linked|linked-runtime|alias|static|indirect|generated|transient|masked|masked-runtime|not-found|bad)
      return 1
      ;;
  esac
  [ "$status" -eq 0 ] && return 2
  return 2
}

backup_path() {
  local source="$1" name="$2"
  if [ -e "$source" ] || [ -L "$source" ]; then
    cp -a "$source" "$BACKUP_DIR/$name"
  else
    : >"$BACKUP_DIR/$name.absent"
  fi
}

path_disk_bytes() {
  local path="$1"
  if [ -e "$path" ] || [ -L "$path" ]; then
    printf '%s' "$(( $(du -sk "$path" | awk '{ print $1 }') * 1024 ))"
  else
    printf '0'
  fi
}

preflight_upgrade_space() {
  local root backup_bytes required support_path=""
  root="$(installer_temp_root)"
  ensure_installer_temp_root || return
  backup_bytes=$((
    $(path_disk_bytes "${PREFIX}/${BIN_NAME}") +
    $(path_disk_bytes "$NODE_ENV") +
    $(path_disk_bytes "$SECRET_FILE") +
    $(path_disk_bytes "$UNIT") +
    $(path_disk_bytes "$OPENRC_SVC")
  ))
  if [ -L "$SUPPORT_LINK" ]; then
    support_path="/usr/local/lib/remnanode/$(readlink "$SUPPORT_LINK")"
    backup_bytes=$((backup_bytes + $(path_disk_bytes "$support_path")))
  fi
  required=$((backup_bytes + RNL_RELEASE_WORK_BYTES + 134217728))
  if [ "$UPGRADE_XRAY" -eq 1 ]; then
    backup_bytes=$((
      backup_bytes +
      $(path_disk_bytes "$XRAY_BIN") +
      $(path_disk_bytes "$GEO_DIR") +
      $(path_disk_bytes "$ASN_DIR")
    ))
    required=$((backup_bytes + RNL_RELEASE_WORK_BYTES + 134217728))
  fi
  require_free_bytes "$root" "$required" "升级备份与工作集"
}

begin_upgrade_transaction() {
  step "创建升级事务备份"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 备份 binary / service / support / node.env / secret.key / 可选 rw-core 资产"
    return 0
  fi

  preflight_upgrade_space
  BACKUP_DIR="$(make_installer_temp_dir upgrade)"
  backup_path "${PREFIX}/${BIN_NAME}" binary
  backup_path "$NODE_ENV" node-env
  backup_path "$SECRET_FILE" secret-key
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
}

restore_path() {
  local backup="$1" target="$2" failed=0
  if [ -e "$BACKUP_DIR/$backup" ] || [ -L "$BACKUP_DIR/$backup" ]; then
    if ! rm -rf "$target"; then
      echo "回滚无法移除 ${target}" >&2
      failed=1
    elif ! cp -a "$BACKUP_DIR/$backup" "$target"; then
      echo "回滚无法恢复 ${target}" >&2
      failed=1
    fi
  elif [ -f "$BACKUP_DIR/$backup.absent" ]; then
    if ! rm -rf "$target"; then
      echo "回滚无法移除升级新增的 ${target}" >&2
      failed=1
    fi
  else
    echo "回滚缺少 ${target} 的备份记录" >&2
    failed=1
  fi
  return "$failed"
}

configured_xray_binary() {
  local configured
  configured="$(read_env_value XRAY_BIN "$NODE_ENV")"
  if [ -n "$configured" ]; then
    canonical_binary_path "$configured"
  else
    canonical_binary_path "$XRAY_BIN"
  fi
}

stop_service_for_maintenance() {
  local xray_binary platform
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 停止服务并确认 remnanode-lite/rw-core 全部退出"
    return 0
  fi
  if ! xray_binary="$(configured_xray_binary)"; then
    echo "拒绝停止服务：无法解析配置的 rw-core 路径" >&2
    return 1
  fi
  if is_alpine; then
    platform=openrc
  else
    platform=systemd
  fi
  if ! stop_remnanode_and_wait \
    "${PREFIX}/${BIN_NAME}" "$xray_binary" 35 "$platform"; then
    echo "拒绝继续：remnanode-lite 或其配置的 rw-core 未确认停止" >&2
    return 1
  fi
}

restore_service_enabled_state() {
  local failed=0 probe_status
  if [ "$SERVICE_ENABLED_STATE_CAPTURED" -ne 1 ] \
    || [ "$SERVICE_ENABLE_MUTATION_ATTEMPTED" -ne 1 ]; then
    return 0
  fi

  if [ "$SERVICE_WAS_ENABLED" -eq 1 ]; then
    if is_alpine; then
      if ! rc-update add remnawave-node default >/dev/null 2>&1; then
        echo "回滚无法恢复 OpenRC 开机注册" >&2
        failed=1
      fi
    elif ! systemctl enable remnawave-node.service >/dev/null 2>&1; then
      echo "回滚无法恢复 systemd 开机注册" >&2
      failed=1
    fi
  else
    if is_alpine; then
      if ! rc-update del remnawave-node default >/dev/null 2>&1; then
        echo "回滚无法移除升级新增的 OpenRC 开机注册" >&2
        failed=1
      fi
    elif ! systemctl disable remnawave-node.service >/dev/null 2>&1; then
      echo "回滚无法移除升级新增的 systemd 开机注册" >&2
      failed=1
    fi
  fi

  if service_is_enabled; then
    probe_status=0
  else
    probe_status=$?
  fi
  if [ "$SERVICE_WAS_ENABLED" -eq 1 ] && [ "$probe_status" -ne 0 ]; then
    echo "回滚后服务未确认恢复开机注册" >&2
    failed=1
  elif [ "$SERVICE_WAS_ENABLED" -eq 0 ] && [ "$probe_status" -ne 1 ]; then
    echo "回滚后服务未确认恢复 disabled 状态" >&2
    failed=1
  fi
  return "$failed"
}

rollback_upgrade() {
  local failed=0 support_link_value="" support_target="" port=""
  echo "==> 自动回滚升级" >&2
  if ! stop_service_for_maintenance; then
    echo "为避免替换运行中的二进制，未执行文件回滚；备份目录：${BACKUP_DIR}" >&2
    return 1
  fi

  restore_path binary "${PREFIX}/${BIN_NAME}" || failed=1
  restore_path node-env "$NODE_ENV" || failed=1
  restore_path secret-key "$SECRET_FILE" || failed=1
  restore_path systemd-unit "$UNIT" || failed=1
  restore_path openrc-service "$OPENRC_SVC" || failed=1
  if ! rm -f "$SUPPORT_LINK"; then
    echo "回滚无法移除 ${SUPPORT_LINK}" >&2
    failed=1
  fi
  if ! rm -rf "/usr/local/lib/remnanode/support/$TAG"; then
    echo "回滚无法移除升级 support 目录" >&2
    failed=1
  fi
  if [ -f "$BACKUP_DIR/support-link" ]; then
    if ! support_link_value="$(cat "$BACKUP_DIR/support-link")"; then
      echo "回滚无法读取原 support 链接" >&2
      failed=1
    elif ! [[ "$support_link_value" =~ ^support/[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
      echo "回滚拒绝异常 support 链接：${support_link_value}" >&2
      failed=1
    else
      support_target="/usr/local/lib/remnanode/${support_link_value}"
      if ! rm -rf "$support_target"; then
        echo "回滚无法移除 ${support_target}" >&2
        failed=1
      elif ! cp -a "$BACKUP_DIR/support-content" "$support_target"; then
        echo "回滚无法恢复 ${support_target}" >&2
        failed=1
      elif ! ln -s "$support_link_value" "$SUPPORT_LINK"; then
        echo "回滚无法恢复 ${SUPPORT_LINK}" >&2
        failed=1
      fi
    fi
  elif [ ! -f "$BACKUP_DIR/support-link.absent" ]; then
    echo "回滚缺少 support 链接备份记录" >&2
    failed=1
  fi
  if [ "$UPGRADE_XRAY" -eq 1 ]; then
    restore_path rw-core "$XRAY_BIN" || failed=1
    restore_path geo "$GEO_DIR" || failed=1
    restore_path asn "$ASN_DIR" || failed=1
  fi

  if ! is_alpine; then
    if ! systemctl daemon-reload >/dev/null 2>&1; then
      echo "回滚后 systemd daemon-reload 失败" >&2
      failed=1
    fi
  fi
  restore_service_enabled_state || failed=1
  if [ "$SERVICE_WAS_ACTIVE" -eq 1 ]; then
    if is_alpine; then
      if ! rc-service remnawave-node start >/dev/null 2>&1; then
        echo "回滚后 OpenRC 服务启动失败" >&2
        failed=1
      fi
    else
      if ! systemctl start remnawave-node.service >/dev/null 2>&1; then
        echo "回滚后 systemd 服务启动失败" >&2
        failed=1
      fi
    fi
    if ! port="$(read_env_value NODE_PORT "$NODE_ENV")"; then
      echo "回滚后无法读取 NODE_PORT" >&2
      failed=1
    elif [ -z "$port" ]; then
      port=2222
    fi
    if [ -n "$port" ] \
      && ! wait_for_service_stable "$port" 30 "${PREFIX}/${BIN_NAME}"; then
      echo "回滚后的目标服务进程未恢复 :${port} 监听" >&2
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
  RNL_REPO="$REPO" RNL_TAG="$TAG" \
    RNL_TMP_ROOT="$(installer_temp_root)" RNL_EXTERNAL_ASSET_ROLLBACK=1 \
    bash "$support"
}

migrate_runtime_configuration() {
  local configured total_kb target=0
  migrate_inline_secret_to_file
  if [ -s "$SECRET_FILE" ]; then
    validate_secret_file "$SECRET_FILE"
  fi

  configured="$(read_env_value LOW_MEMORY "$NODE_ENV")"
  if [ "$LOW_MEMORY" -eq 1 ]; then
    target=1
  elif [ -n "$configured" ]; then
    return 0
  else
    total_kb="$(awk '/MemTotal:/ { print $2; exit }' /proc/meminfo 2>/dev/null || true)"
    if [ -n "$total_kb" ] && [ "$total_kb" -le 524288 ]; then
      target=1
    fi
  fi
  set_env_value LOW_MEMORY "$target"
  if [ "$target" -eq 1 ]; then
    echo "已迁移为 LOW_MEMORY=1（512MiB 资源模式）"
  else
    echo "已补齐 LOW_MEMORY=0；可用 --low-memory 显式启用"
  fi
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
  support="$(installed_support_file deploy/remnawave-node.service)" || return
  [ -f "$support" ] || { echo "缺少已校验 systemd unit" >&2; return 1; }
  install_managed_file "$support" "$UNIT" 0644 || return
  systemctl daemon-reload || return
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
  support="$(installed_support_file deploy/remnawave-node.openrc)" || return
  [ -f "$support" ] || { echo "缺少已校验 OpenRC service" >&2; return 1; }
  install_managed_file "$support" "$OPENRC_SVC" 0755 || return
}

ensure_service_enabled() {
  local probe_status
  if [ "$ENSURE_SERVICE_ENABLED" -eq 0 ]; then
    return 0
  fi

  step "注册 remnawave-node 开机启动"
  if [ "$DRY_RUN" -eq 1 ]; then
    if is_alpine; then
      echo "[dry-run] rc-update add remnawave-node default 并确认注册"
    else
      echo "[dry-run] systemctl enable remnawave-node.service 并确认注册"
    fi
    return 0
  fi

  SERVICE_ENABLE_MUTATION_ATTEMPTED=1
  if is_alpine; then
    if ! rc-update add remnawave-node default; then
      echo "OpenRC 开机注册失败" >&2
      return 1
    fi
  elif ! systemctl enable remnawave-node.service; then
    echo "systemd 开机注册失败" >&2
    return 1
  fi

  if service_is_enabled; then
    probe_status=0
  else
    probe_status=$?
  fi
  if [ "$probe_status" -ne 0 ]; then
    if [ "$probe_status" -eq 1 ]; then
      echo "服务管理器未保留 remnawave-node 开机注册" >&2
    else
      echo "开机注册后无法可靠确认服务状态" >&2
    fi
    return 1
  fi
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
    return 1
  fi

  if [ "$SERVICE_SHOULD_BE_ACTIVE" -eq 0 ]; then
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
  if [ "$DRY_RUN" -eq 1 ] || [ "$SERVICE_SHOULD_BE_ACTIVE" -eq 0 ]; then
    return 0
  fi
  local port
  port="$(read_env_value NODE_PORT "$NODE_ENV")"
  [ -n "$port" ] || port=2222
  verify_installed_version_tag "${PREFIX}/${BIN_NAME}" "$TAG"
  verify_service_listening "$port" "${PREFIX}/${BIN_NAME}"
}

main() {
  require_root
  case "$ENSURE_SERVICE_STARTED" in
    0|1) ;;
    *) echo "RNL_ENSURE_SERVICE_STARTED must be 0 or 1" >&2; return 2 ;;
  esac
  case "$ENSURE_SERVICE_ENABLED" in
    0|1) ;;
    *) echo "RNL_ENSURE_SERVICE_ENABLED must be 0 or 1" >&2; return 2 ;;
  esac
  require_command curl
  require_command head
  require_command tar
  require_command timeout

  if [ "$DRY_RUN" -eq 0 ]; then
    if is_alpine; then
      require_command rc-service
    else
      require_command systemctl
    fi
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

  if [ "$DRY_RUN" -eq 0 ]; then
    local active_probe_status enabled_probe_status
    if service_is_active; then
      active_probe_status=0
    else
      active_probe_status=$?
    fi
    case "$active_probe_status" in
      0)
        SERVICE_WAS_ACTIVE=1
        SERVICE_SHOULD_BE_ACTIVE=1
        ;;
      1) ;;
      *)
        echo "无法可靠探测 remnawave-node 运行状态，拒绝升级" >&2
        return 1
        ;;
    esac

    if [ "$ENSURE_SERVICE_ENABLED" -eq 1 ]; then
      if service_is_enabled; then
        enabled_probe_status=0
      else
        enabled_probe_status=$?
      fi
      case "$enabled_probe_status" in
        0) SERVICE_WAS_ENABLED=1 ;;
        1) SERVICE_WAS_ENABLED=0 ;;
        *)
          echo "无法可靠探测 remnawave-node 开机注册状态，拒绝升级" >&2
          return 1
          ;;
      esac
      SERVICE_ENABLED_STATE_CAPTURED=1
    fi
  fi
  if [ "$SERVICE_SHOULD_BE_ACTIVE" -eq 0 ] \
    && [ "$ENSURE_SERVICE_STARTED" -eq 1 ] && secret_configured; then
    SERVICE_SHOULD_BE_ACTIVE=1
  fi

  local arch
  arch="$(detect_arch)"

  echo "升级前：$(current_version)"
  ensure_service_account
  setup_service_directories
  begin_upgrade_transaction
  if [ "$DRY_RUN" -eq 0 ]; then
    ROLLBACK_ARMED=1
  fi
  step "停止并确认现有 remnanode-lite/rw-core"
  stop_service_for_maintenance || return $?
  normalize_runtime_environment
  download_binary "$arch"
  upgrade_xray
  migrate_owned_asset_paths
  migrate_runtime_configuration
  refresh_systemd
  refresh_openrc
  ensure_service_enabled
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
