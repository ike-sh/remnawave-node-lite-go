#!/usr/bin/env bash
set -euo pipefail

UNIT="/etc/systemd/system/remnawave-node.service"
PREFIX="/usr/local/bin"
BIN_NAME="remnanode-lite"
ETC_DIR="/etc/remnanode"
LOG_DIR="/var/log/remnanode"
DATA_DIR="/var/lib/remnanode"

usage() {
  cat <<'EOF'
用法：uninstall.sh [--purge] [--yes]

  --purge   同时删除配置、日志和数据目录
  --yes     跳过确认
EOF
}

PURGE=0
YES=0
while [ $# -gt 0 ]; do
  case "$1" in
    --purge) PURGE=1 ;;
    --yes|-y) YES=1 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "未知参数：$1" >&2; usage; exit 1 ;;
  esac
  shift
done

if [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行。" >&2
  exit 1
fi

if [ "$YES" -ne 1 ]; then
  echo "将卸载 remnawave-node-lite（Go）。"
  [ "$PURGE" -eq 1 ] && echo "⚠ --purge 会删除 ${ETC_DIR}、${LOG_DIR}、${DATA_DIR}"
  read -r -p "继续？[y/N] " ans
  case "$ans" in
    y|Y|yes|YES) ;;
    *) echo "已取消。"; exit 0 ;;
  esac
fi

systemctl stop remnawave-node.service 2>/dev/null || true
systemctl disable remnawave-node.service 2>/dev/null || true
rm -f "$UNIT"
systemctl daemon-reload

rm -f "${PREFIX}/${BIN_NAME}"
rm -f /usr/local/bin/xlogs /usr/local/bin/xerrors

if [ "$PURGE" -eq 1 ]; then
  rm -rf "$ETC_DIR" "$LOG_DIR" "$DATA_DIR"
fi

echo "卸载完成。"
