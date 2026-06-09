#!/usr/bin/env bash
# Panel 报 Required info is missing = rw-core 未运行，不是版本问题
set -euo pipefail

NODE_ENV=/etc/remnanode/node.env

if [ "$(id -u)" -ne 0 ]; then
  echo "请用 root 运行" >&2
  exit 1
fi

if [ ! -f "$NODE_ENV" ]; then
  echo "找不到 $NODE_ENV" >&2
  exit 1
fi

if ! grep -q '^SECRET_KEY=.' "$NODE_ENV" && [ ! -s /etc/remnanode/secret.key ]; then
  echo "SECRET_KEY 未配置，请从 Panel 复制到 $NODE_ENV" >&2
  exit 1
fi

cp -a "$NODE_ENV" "${NODE_ENV}.bak.$(date +%Y%m%d%H%M%S)"

if grep -q '^INTERNAL_SOCKET_PATH=' "$NODE_ENV"; then
  sed -i 's|^INTERNAL_SOCKET_PATH=.*|INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock|' "$NODE_ENV"
else
  echo 'INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock' >>"$NODE_ENV"
fi

mkdir -p /var/log/remnanode
chmod 755 /var/log/remnanode

if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi 'Status: active'; then
  ufw allow 2222/tcp >/dev/null 2>&1 || true
fi

systemctl restart remnawave-node
sleep 2

echo "remnanawave-node: $(systemctl is-active remnawave-node)"
ss -tlnp 2>/dev/null | grep ':2222 ' || echo "警告: :2222 未监听"
pgrep -a rw-core || echo "rw-core: 未运行（Panel 启用节点后才会启动）"

echo
echo "Panel 必做（同机部署地址用 127.0.0.1，不要用公网 IP）："
echo "  1. 节点地址 127.0.0.1  端口 2222"
echo "  2. 已选 Config Profile + 至少 1 个 Inbound"
echo "  3. 禁用节点 -> 等 5 秒 -> 启用"
echo "  4. 然后: tail -20 /var/log/remnanode/xray.err.log"
