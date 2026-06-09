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
  echo "  保存后：禁用 -> 启用节点"
}

verify_service_listening() {
  local port="$1"
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
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
