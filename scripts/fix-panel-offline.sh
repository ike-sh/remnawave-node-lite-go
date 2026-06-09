#!/usr/bin/env bash
# 修复 Panel 显示 "Required info is missing. Outdated version?" 且 rw-core 未运行
# 根因：Panel 能连节点 API，但 Xray 未启动（xrayInfo=null），与 node 版本无关
set -euo pipefail

NODE_ENV="${REMNANODE_ENV:-/etc/remnanode/node.env}"
LOG_DIR="/var/log/remnanode"
STABLE_SOCK="/run/remnanode/internal.sock"

require_root() {
  if [ "$(id -u)" -ne 0 ]; then
    echo "请使用 root：sudo bash fix-panel-offline.sh" >&2
    exit 1
  fi
}

step() { echo "==> $1"; }

ensure_env() {
  if [ ! -f "$NODE_ENV" ]; then
    echo "找不到 ${NODE_ENV}，请先运行 install-node.sh" >&2
    exit 1
  fi

  step "备份 ${NODE_ENV}"
  cp -a "$NODE_ENV" "${NODE_ENV}.bak.$(date +%Y%m%d%H%M%S)"

  step "写入稳定 internal socket 路径（对齐 systemd RuntimeDirectory=remnanode）"
  if grep -q '^INTERNAL_SOCKET_PATH=' "$NODE_ENV"; then
    sed -i "s|^INTERNAL_SOCKET_PATH=.*|INTERNAL_SOCKET_PATH=${STABLE_SOCK}|" "$NODE_ENV"
  else
    echo "INTERNAL_SOCKET_PATH=${STABLE_SOCK}" >>"$NODE_ENV"
  fi

  if ! grep -q '^SECRET_KEY=' "$NODE_ENV" || grep -q '^SECRET_KEY=$' "$NODE_ENV"; then
    if [ ! -s /etc/remnanode/secret.key ] 2>/dev/null; then
      echo "ERROR: SECRET_KEY 未配置。请从 Panel 节点页复制 Secret Key 写入 ${NODE_ENV}" >&2
      exit 1
    fi
  fi
}

ensure_log_dir() {
  step "确保日志目录 ${LOG_DIR}"
  mkdir -p "$LOG_DIR"
  chmod 755 "$LOG_DIR"
}

open_firewall() {
  if command -v ufw >/dev/null 2>&1 && ufw status 2>/dev/null | grep -qi 'Status: active'; then
    step "UFW 已启用，放行 TCP ${NODE_PORT:-2222}"
    local port
    port="$(grep -E '^NODE_PORT=' "$NODE_ENV" | head -n1 | cut -d= -f2 | tr -d ' "'\''" || echo 2222)"
    ufw allow "${port}/tcp" comment 'remnawave-node' >/dev/null 2>&1 || true
  fi
}

restart_node() {
  step "重启 remnawave-node"
  systemctl daemon-reload 2>/dev/null || true
  systemctl restart remnawave-node.service
  sleep 2
}

print_status() {
  local port
  port="$(grep -E '^NODE_PORT=' "$NODE_ENV" | head -n1 | cut -d= -f2 | tr -d ' "'\''" || echo 2222)"

  echo
  echo "━━━━━━━━ 诊断结果 ━━━━━━━━"
  echo "节点 API：$(systemctl is-active remnawave-node 2>/dev/null || echo unknown)  端口 :${port}"
  if ss -tlnp 2>/dev/null | grep -q ":${port} "; then
    echo "监听    ：:${port} OK"
  else
    echo "监听    ：:${port} 未监听 — 检查 journalctl -u remnawave-node"
  fi

  if pgrep -a rw-core >/dev/null 2>&1 || pgrep -a '/xray ' >/dev/null 2>&1; then
    echo "rw-core  ：运行中"
  else
    echo "rw-core  ：未运行（正常：需 Panel 启用节点后才会 spawn）"
  fi

  if [ -s "${LOG_DIR}/xray.err.log" ]; then
    echo "xray.err.log 最近错误："
    tail -5 "${LOG_DIR}/xray.err.log" | sed 's/^/  /'
  else
    echo "xray.err.log：(空) — Panel 尚未成功下发 startXray，或 Xray 未尝试启动"
  fi

  echo
  echo "━━━━━━━━ Panel 侧必做 ━━━━━━━━"
  echo "1. 若 Panel 与节点在同一台 halo：节点地址改为 127.0.0.1（不要用公网 IP）"
  echo "2. 确认已选 Config Profile 且至少 1 个 Inbound"
  echo "3. 禁用节点 → 等 5 秒 → 启用（触发 startXray）"
  echo "4. 启用后立即执行：journalctl -u remnawave-node -n 30 | grep xray/start"
  echo "   或：tail -20 ${LOG_DIR}/xray.err.log"
  echo
  echo "说明：Panel 报错 'Required info is missing. Outdated version?' = xrayInfo 为空，"
  echo "      不是版本过低；remnanode-lite contract 2.7.0 已满足 Panel 要求。"
}

require_root
ensure_env
ensure_log_dir
open_firewall
restart_node
print_status
