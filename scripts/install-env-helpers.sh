# shellcheck shell=bash
# Shared env/secret helpers for install-node.sh and install-node-alpine.sh
# Expects: NODE_ENV, SECRET_FILE, DRY_RUN

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
INTERNAL_SOCKET_PATH=
INTERNAL_REST_TOKEN=
LOW_MEMORY=${low_mem}
BODY_LIMIT_MB=
EOF
}
