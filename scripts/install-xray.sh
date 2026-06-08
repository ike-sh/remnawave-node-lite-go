#!/usr/bin/env bash
# 安装 rw-core（Xray）及 geo 资源文件
# 封装官方 Remnawave install-xray.sh
set -euo pipefail

XRAY_CORE_VERSION="${XRAY_CORE_VERSION:-v26.3.27}"
UPSTREAM_REPO="${UPSTREAM_REPO:-XTLS}"
INSTALL_SCRIPT="${INSTALL_SCRIPT:-https://raw.githubusercontent.com/remnawave/scripts/main/scripts/install-xray.sh}"

usage() {
  cat <<'EOF'
用法：install-xray.sh [--version v26.3.27] [--upstream XTLS] [--dry-run]

环境变量：
  XRAY_CORE_VERSION   rw-core 版本，默认 v26.3.27
  UPSTREAM_REPO       上游仓库标识，默认 XTLS
  INSTALL_SCRIPT      安装脚本 URL
EOF
}

DRY_RUN=0
while [ $# -gt 0 ]; do
  case "$1" in
    --version) XRAY_CORE_VERSION="$2"; shift 2 ;;
    --upstream) UPSTREAM_REPO="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *)
      echo "未知参数：$1" >&2
      usage
      exit 1
      ;;
  esac
done

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root 运行：sudo bash install-xray.sh" >&2
    exit 1
  fi
}

require_root

if [ "$DRY_RUN" -eq 1 ]; then
  echo "[dry-run] curl -fsSL ${INSTALL_SCRIPT} | sh -s -- ${XRAY_CORE_VERSION} ${UPSTREAM_REPO}"
  echo "[dry-run] ln -sf /usr/local/bin/xray /usr/local/bin/rw-core"
  exit 0
fi

echo "安装 rw-core ${XRAY_CORE_VERSION} (upstream=${UPSTREAM_REPO})..."
curl -fsSL "${INSTALL_SCRIPT}" | sh -s -- "${XRAY_CORE_VERSION}" "${UPSTREAM_REPO}"

if [ -x /usr/local/bin/xray ] && [ ! -e /usr/local/bin/rw-core ]; then
  ln -sf /usr/local/bin/xray /usr/local/bin/rw-core
  echo "已创建符号链接：/usr/local/bin/rw-core -> xray"
fi

if [ -x /usr/local/bin/rw-core ]; then
  echo "rw-core 版本：$(/usr/local/bin/rw-core version | head -n 1)"
else
  echo "警告：/usr/local/bin/rw-core 未找到，请检查安装日志。" >&2
  exit 1
fi

for dat in geoip.dat geosite.dat; do
  if [ ! -f "/usr/local/share/xray/${dat}" ]; then
    echo "警告：缺少 /usr/local/share/xray/${dat}" >&2
  fi
done

echo "rw-core 安装完成。"
