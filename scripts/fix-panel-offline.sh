#!/usr/bin/env bash
set -euo pipefail

NODE_ENV=/etc/remnanode/node.env

[ "$(id -u)" -eq 0 ] || { echo "请用 root 运行" >&2; exit 1; }
[ -f "$NODE_ENV" ] || { echo "找不到 $NODE_ENV" >&2; exit 1; }
grep -q '^SECRET_KEY=.' "$NODE_ENV" || [ -s /etc/remnanode/secret.key ] || {
  echo "SECRET_KEY 未配置" >&2
  exit 1
}

cp -a "$NODE_ENV" "${NODE_ENV}.bak.$(date +%Y%m%d%H%M%S)"

if grep -q '^INTERNAL_SOCKET_PATH=' "$NODE_ENV"; then
  sed -i 's|^INTERNAL_SOCKET_PATH=.*|INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock|' "$NODE_ENV"
else
  echo 'INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock' >>"$NODE_ENV"
fi

mkdir -p /var/log/remnanode
systemctl restart remnawave-node
sleep 2

echo "service: $(systemctl is-active remnawave-node)"
pgrep -a rw-core || echo "rw-core: 未运行，请在 Panel 禁用再启用节点"
echo "Panel 同机请用地址 127.0.0.1:2222"
