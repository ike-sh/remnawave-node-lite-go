# shellcheck shell=bash
# Shared env/secret helpers for install-node.sh and install-node-alpine.sh
# Expects: NODE_ENV, SECRET_FILE, DRY_RUN

resolve_release_tag() {
  local repo="${1:?}"
  local fallback="${2:?}"
  local tag=""
  if command -v curl >/dev/null 2>&1; then
    tag="$(curl -fsSL -H "Accept: application/vnd.github+json" \
      "https://api.github.com/repos/${repo}/releases/latest" 2>/dev/null \
      | tr -d '\n' \
      | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' \
      | head -n1)" || true
  fi
  if [ -n "$tag" ]; then
    printf '%s' "$tag"
  else
    printf '%s' "$fallback"
  fi
}

resolve_install_tag() {
  local repo="${1:?}"
  local fallback="${2:?}"
  if [ -n "${RNL_TAG:-}" ]; then
    printf '%s' "$RNL_TAG"
  else
    resolve_release_tag "$repo" "$fallback"
  fi
}

secret_from_env_file() {
  if [ ! -f "$NODE_ENV" ]; then
    return 1
  fi
  local line val
  line="$(grep -E '^[[:space:]]*SECRET_KEY=' "$NODE_ENV" 2>/dev/null | head -n 1 || true)"
  [ -n "$line" ] || return 1
  val="${line#SECRET_KEY=}"
  val="$(printf '%s' "$val" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")"
  [ -n "$val" ]
}

secret_configured() {
  if secret_from_env_file; then
    return 0
  fi
  [ -f "$SECRET_FILE" ] && [ -s "$SECRET_FILE" ]
}

write_secret_to_env() {
  local value="$1"
  if [ -z "$value" ]; then
    return 0
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 写入 ${NODE_ENV} SECRET_KEY=..."
    return 0
  fi
  if [ ! -f "$NODE_ENV" ]; then
    echo "找不到 ${NODE_ENV}，请先创建环境配置。" >&2
    exit 1
  fi
  local tmp
  tmp="$(mktemp)"
  grep -v '^SECRET_KEY=' "$NODE_ENV" | grep -v '^SECRET_KEY_FILE=' >"$tmp" || true
  {
    cat "$tmp"
    printf 'SECRET_KEY="%s"\n' "$value"
  } >"$NODE_ENV"
  rm -f "$tmp"
  chmod 600 "$NODE_ENV"
  echo "已写入 SECRET_KEY 到 ${NODE_ENV}"
}

enable_secret_key_file() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 启用 ${NODE_ENV} SECRET_KEY_FILE=${SECRET_FILE}"
    return 0
  fi
  if [ ! -f "$NODE_ENV" ]; then
    return 0
  fi
  local tmp
  tmp="$(mktemp)"
  grep -v '^SECRET_KEY=' "$NODE_ENV" | grep -v '^SECRET_KEY_FILE=' | grep -v '^# SECRET_KEY_FILE=' >"$tmp" || true
  {
    cat "$tmp"
    echo "SECRET_KEY="
    echo "SECRET_KEY_FILE=${SECRET_FILE}"
  } >"$NODE_ENV"
  rm -f "$tmp"
  chmod 600 "$NODE_ENV"
}

write_secret_from_source() {
  local src="$1"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 写入 ${SECRET_FILE} <- ${src}"
    return 0
  fi
  install -m 0600 -D /dev/null "$SECRET_FILE"
  if [ "$src" = "-" ]; then
    cat >"$SECRET_FILE"
  else
    install -m 0600 "$src" "$SECRET_FILE"
  fi
  enable_secret_key_file
}

write_secret_from_env() {
  local value="${SECRET_KEY:-}"
  if [ -z "$value" ]; then
    return 0
  fi
  write_secret_to_env "$value"
}

ensure_internal_socket_in_env() {
  if [ ! -f "$NODE_ENV" ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if grep -q '^INTERNAL_SOCKET_PATH=.' "$NODE_ENV" 2>/dev/null; then
    return 0
  fi
  if grep -q '^INTERNAL_SOCKET_PATH=' "$NODE_ENV" 2>/dev/null; then
    sed -i 's|^INTERNAL_SOCKET_PATH=.*|INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock|' "$NODE_ENV"
  else
    echo 'INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock' >>"$NODE_ENV"
  fi
}

prompt_secret_key() {
  if secret_configured; then
    return 0
  fi

  write_secret_from_env
  if secret_configured; then
    return 0
  fi

  if [ -n "$SECRET_FILE_ARG" ]; then
    return 0
  fi

  if [ "$YES" -eq 1 ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi

  echo
  echo "请粘贴 Panel 节点页下发的 Secret Key（整段 base64，粘贴后按 Enter）："
  echo "（若 Panel 节点已启用，装完后请在 Panel 禁用→启用一次，或安装前保持禁用）"
  local secret=""
  if [ -t 0 ]; then
    read -r secret
  elif [ -r /dev/tty ]; then
    read -r secret </dev/tty
  fi

  if [ -n "$secret" ]; then
    write_secret_to_env "$secret"
    return 0
  fi

  print_env_config_hint "${RESTART_CMD:-systemctl restart remnawave-node}"
}

cleanup_runtime() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] cleanup runtime sockets and rw-core"
    return 0
  fi
  pkill -x rw-core 2>/dev/null || pkill -f '/usr/local/bin/rw-core' 2>/dev/null || true
  rm -rf /run/remnanode 2>/dev/null || true
  rm -f /run/remnawave-internal-*.sock 2>/dev/null || true
}

print_pre_install_panel_hint() {
  echo
  echo "━━━━━━━━ Panel 接入提示（首次安装）━━━━━━━━"
  echo "  Panel 在【保存/启用】节点时会立即尝试连接；安装期间（尤其下载 Xray）"
  echo "  本机尚未监听端口，连接失败后 Panel 不会自动重试。"
  echo
  echo "  推荐顺序（装完即可上线，无需手动切换）："
  echo "    1) Panel 创建节点，复制 Secret Key"
  echo "    2) 保持节点【禁用】，或先不填真实 IP"
  echo "    3) 完成本脚本安装"
  echo "    4) 看到下方 OK: TCP 已监听 后，在 Panel【启用】节点"
  echo
  echo "  若安装前已在 Panel 保存并启用：装完后请【禁用 → 启用】一次"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
}

print_panel_address_hint() {
  local port="$1"
  local pub_ip=""
  pub_ip="$(ip -4 -o addr show scope global 2>/dev/null | awk '{print $4}' | cut -d/ -f1 | head -n1 || true)"

  echo
  echo "━━━━━━━━ Panel 对接（必读）━━━━━━━━"
  echo "  节点端口: ${port}"
  if [ -n "$pub_ip" ]; then
    echo "  本机公网 IP（参考）: ${pub_ip}"
  fi
  echo "  Panel 在其它服务器：地址填 Panel 能 ping/tcp 通的本机 IP"
  echo "  Panel 服务器上自测:"
  echo "    nc -zv -w 5 <节点IP> ${port}"
  echo
  echo "  节点已就绪。若 Panel 仍显示离线："
  echo "    · 安装前未禁用 →【禁用 → 启用】一次"
  echo "    · 安装前已禁用 → 直接【启用】即可"
  echo "  首次成功启用后，服务器 reboot 将自动恢复（last-start.json）"
}

wait_for_service_stable() {
  local port="$1"
  local max_wait="${2:-30}"
  local i=0

  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi

  while [ "$i" -lt "$max_wait" ]; do
    if ss -tln 2>/dev/null | grep -q ":${port} "; then
      if command -v systemctl >/dev/null 2>&1; then
        if systemctl is-active --quiet remnawave-node.service 2>/dev/null; then
          return 0
        fi
      elif command -v rc-service >/dev/null 2>&1; then
        if rc-service remnawave-node status 2>/dev/null | grep -qi 'started'; then
          return 0
        fi
      else
        return 0
      fi
    fi
    sleep 1
    i=$((i + 1))
  done
  return 1
}

verify_service_listening() {
  local port="$1"
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if ! wait_for_service_stable "$port" 30; then
    echo "错误: :${port} 在 30s 内未就绪，请检查服务状态（systemctl/rc-service remnawave-node）" >&2
    return 1
  fi
  if ss -tln 2>/dev/null | grep -q ":${port} "; then
    echo "OK: TCP :${port} 已监听"
    ss -tlnp 2>/dev/null | grep ":${port} " | head -n1 || true
    return 0
  fi
  echo "错误: :${port} 未监听，请检查服务状态（systemctl/rc-service remnawave-node）" >&2
  return 1
}

print_env_config_hint() {
  local restart_cmd="$1"
  echo
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " 配置节点（编辑 node.env，变量名同官方 environment）"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo
  echo "编辑 ${NODE_ENV}，修改两项即可："
  echo "  NODE_PORT=2222          # 与 Panel 添加节点时的端口一致"
  echo '  SECRET_KEY="eyJ..."     # Panel 下发的 Secret Key（整段粘贴）'
  echo
  echo "完成后执行：${restart_cmd}"
  echo
  echo "也可安装时传入："
  echo "  SECRET_KEY='eyJ...' NODE_PORT=8443 bash install-*.sh --yes"
}

render_env_template() {
  local port="$1"
  local low_mem="$2"
  local installer="$3"
  cat <<EOF
# Remnawave Node Lite — 由 ${installer} 生成
# 借鉴官方 environment 变量名，仅需修改下面两项：

NODE_PORT=${port}
SECRET_KEY=

# 可选：密钥极长时可改用独立文件（取消下行注释并清空 SECRET_KEY）
# SECRET_KEY_FILE=${SECRET_FILE}

XTLS_API_PORT=61000
XRAY_BIN=/usr/local/bin/rw-core
GEO_DIR=/usr/local/share/xray
LOG_DIR=${LOG_DIR}
INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock
INTERNAL_REST_TOKEN=
LOW_MEMORY=${low_mem}
BODY_LIMIT_MB=
EOF
}
