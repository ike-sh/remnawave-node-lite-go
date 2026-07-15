# shellcheck shell=bash
# Shared env/secret helpers for install-node.sh and install-node-alpine.sh
# Expects: NODE_ENV, SECRET_FILE, DRY_RUN, DATA_DIR, LOG_DIR

validate_release_coordinates() {
  local repo="$1" tag="$2"
  if ! [[ "$repo" =~ ^[A-Za-z0-9][A-Za-z0-9_.-]*/[A-Za-z0-9][A-Za-z0-9_.-]*$ ]]; then
    echo "非法 GitHub 仓库名：${repo}" >&2
    return 2
  fi
  if ! [[ "$tag" =~ ^[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "非法 Release 标签：${tag}" >&2
    return 2
  fi
}

resolve_install_tag() {
  local repo="${1:?}"
  local fallback="${2:?}"
  local tag
  if [ -n "${RNL_TAG:-}" ]; then
    tag="$RNL_TAG"
  else
    tag="$fallback"
  fi
  validate_release_coordinates "$repo" "$tag" || return
  printf '%s' "$tag"
}

download_https_file() {
  local url="$1" output="$2"
  case "$url" in
    https://*) ;;
    *) echo "拒绝非 HTTPS 下载：${url}" >&2; return 1 ;;
  esac
  curl --fail --location --silent --show-error \
    --proto '=https' --tlsv1.2 --retry 3 --retry-all-errors \
    "$url" --output "$output"
}

file_sha256() {
  local file="$1"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  else
    shasum -a 256 "$file" | awk '{print $1}'
  fi
}

verify_file_sha256() {
  local file="$1" expected="$2" actual
  if ! [[ "$expected" =~ ^[0-9a-fA-F]{64}$ ]]; then
    echo "无效 SHA-256：${expected}" >&2
    return 1
  fi
  actual="$(file_sha256 "$file")"
  if [ "${actual,,}" != "${expected,,}" ]; then
    echo "SHA-256 校验失败：${file} got=${actual} want=${expected}" >&2
    return 1
  fi
}

release_binary_version_matches_tag() {
  local output="$1" tag="$2"
  [[ "$output" == "remnawave-node-lite-go ${tag#v} ("* ]]
}

install_release_binary() (
  set -euo pipefail
  local repo="$1" tag="$2" arch="$3" target="$4"
  local name="remnanode-lite_linux_${arch}.tar.gz"
  local base="https://github.com/${repo}/releases/download/${tag}"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 下载并校验 ${base}/${name}"
    echo "[dry-run] 原子安装 ${target}"
    exit 0
  fi

  validate_release_coordinates "$repo" "$tag"

  tmp=""
  expected=""
  extracted=""
  staged=""
  support_root=""
  support_stage=""
  support_link=""
  tmp="$(mktemp -d)"
  staged="${target}.new.$$"
  trap '[ -z "${tmp:-}" ] || rm -rf "$tmp"; [ -z "${staged:-}" ] || rm -f "$staged"; [ -z "${support_stage:-}" ] || rm -rf "$support_stage"; [ -z "${support_link:-}" ] || rm -f "${support_link}.new.$$"' EXIT

  download_https_file "${base}/${name}" "$tmp/archive.tar.gz"
  expected="${RNL_RELEASE_SHA256:-}"
  if [ -z "$expected" ]; then
    download_https_file "${base}/SHA256SUMS" "$tmp/SHA256SUMS"
    expected="$(awk -v name="$name" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp/SHA256SUMS")"
  fi
  if [ -z "$expected" ]; then
    echo "SHA256SUMS 未包含 ${name}" >&2
    exit 1
  fi
  verify_file_sha256 "$tmp/archive.tar.gz" "$expected"

  if tar -tzf "$tmp/archive.tar.gz" | grep -Eq '(^/|(^|/)\.\.(/|$))'; then
    echo "Release 归档包含不安全路径" >&2
    exit 1
  fi
  if tar -tvzf "$tmp/archive.tar.gz" | awk '
    { type = substr($1, 1, 1); if (type != "-" && type != "d") bad = 1 }
    END { exit(bad ? 0 : 1) }
  '; then
    echo "Release 归档包含符号链接、硬链接或特殊文件" >&2
    exit 1
  fi
  tar -xzf "$tmp/archive.tar.gz" -C "$tmp"
  extracted="$tmp/remnanode-lite"
  [ -f "$extracted" ] && [ ! -L "$extracted" ] || {
    echo "Release 归档缺少常规文件 remnanode-lite" >&2
    exit 1
  }
  chmod 0755 "$extracted"
  version_output="$("$extracted" version)"
  if ! release_binary_version_matches_tag "$version_output" "$tag"; then
    echo "Release 二进制版本与标签 ${tag} 不一致" >&2
    exit 1
  fi

  for support_file in \
    support/deploy/remnawave-node.service \
    support/deploy/remnawave-node.openrc \
    support/scripts/install-env-helpers.sh \
    support/scripts/install-xray.sh \
    support/scripts/upgrade.sh \
    support/scripts/uninstall.sh; do
    [ -f "$tmp/$support_file" ] && [ ! -L "$tmp/$support_file" ] || {
      echo "Release 归档缺少常规文件 ${support_file}" >&2
      exit 1
    }
  done

  install -o root -g root -m 0755 "$extracted" "$staged"
  mv -f "$staged" "$target"

  support_root="/usr/local/lib/remnanode/support"
  support_stage="${support_root}/.${tag}.$$"
  support_link="/usr/local/lib/remnanode/support-current"
  install -d -o root -g root -m 0755 "$support_stage/deploy" "$support_stage/scripts"
  install -o root -g root -m 0644 "$tmp/support/deploy/remnawave-node.service" "$support_stage/deploy/"
  install -o root -g root -m 0755 "$tmp/support/deploy/remnawave-node.openrc" "$support_stage/deploy/"
  install -o root -g root -m 0644 "$tmp/support/scripts/install-env-helpers.sh" "$support_stage/scripts/"
  install -o root -g root -m 0755 \
    "$tmp/support/scripts/install-xray.sh" \
    "$tmp/support/scripts/upgrade.sh" \
    "$tmp/support/scripts/uninstall.sh" \
    "$support_stage/scripts/"
  rm -rf "${support_root:?}/$tag"
  mv "$support_stage" "$support_root/$tag"
  ln -sfn "support/$tag" "${support_link}.new.$$"
  mv -fT "${support_link}.new.$$" "$support_link"
  "$target" version
)

installed_support_file() {
  local relative="$1"
  printf '/usr/local/lib/remnanode/support-current/%s' "$relative"
}

service_account_name() {
  printf '%s' "${SERVICE_USER:-remnanode}"
}

service_group_name() {
  printf '%s' "${SERVICE_GROUP:-remnanode}"
}

ensure_service_account() {
  local user group home shell group_gid membership
  user="$(service_account_name)"
  group="$(service_group_name)"
  home="${DATA_DIR:-/var/lib/remnanode}"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 创建系统用户 ${user}:${group}（home=${home}）"
    return 0
  fi

  if ! grep -q "^${group}:" /etc/group 2>/dev/null; then
    if [ -f /etc/alpine-release ]; then
      addgroup -S "$group"
    else
      groupadd --system "$group"
    fi
  fi
  group_gid="$(awk -F: -v name="$group" '$1 == name { print $3; exit }' /etc/group)"
  if ! [[ "$group_gid" =~ ^[0-9]+$ ]] || [ "$group_gid" -eq 0 ]; then
    echo "拒绝使用缺失或 GID 0 的 ${group} 组" >&2
    return 1
  fi

  if id "$user" >/dev/null 2>&1; then
    if [ "$(id -u "$user")" -eq 0 ]; then
      echo "拒绝使用 UID 0 的 ${user} 账号" >&2
      return 1
    fi
    local existing_home
    existing_home="$(awk -F: -v name="$user" '$1 == name { print $6 }' /etc/passwd)"
    if [ -n "$existing_home" ] && [ "$existing_home" != "$home" ]; then
      echo "现有用户 ${user} 的 home 为 ${existing_home}，预期 ${home}；拒绝接管" >&2
      return 1
    fi
  else
    if [ -f /etc/alpine-release ]; then
      adduser -S -D -H -h "$home" -s /sbin/nologin -G "$group" "$user"
    else
      shell="$(command -v nologin || true)"
      [ -n "$shell" ] || shell=/bin/false
      useradd --system --gid "$group" --home-dir "$home" --no-create-home --shell "$shell" "$user"
    fi
  fi

  if [ "$(id -gn "$user")" != "$group" ]; then
    echo "现有用户 ${user} 的主组不是 ${group}；拒绝接管" >&2
    return 1
  fi
  for membership in $(id -nG "$user"); do
    if [ "$membership" != "$group" ]; then
      echo "现有用户 ${user} 属于额外组 ${membership}；拒绝接管" >&2
      return 1
    fi
  done
}

setup_service_directories() {
  local user group
  user="$(service_account_name)"
  group="$(service_group_name)"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 创建专用目录并设置 ${user}:${group} 权限"
    return 0
  fi

  install -d -o root -g "$group" -m 0750 "$(dirname "$NODE_ENV")"
  install -d -o "$user" -g "$group" -m 0750 \
    "${DATA_DIR:-/var/lib/remnanode}" "${LOG_DIR:-/var/log/remnanode}"
  install -d -o root -g root -m 0755 \
    /usr/local/lib/remnanode \
    /usr/local/share/remnanode/xray \
    /usr/local/share/remnanode/asn
}

secure_config_file() {
  local path="$1"
  [ -e "$path" ] || return 0
  chown root:"$(service_group_name)" "$path"
  chmod 0640 "$path"
}

normalize_service_permissions() {
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 规范化 remnanode 配置、状态与日志权限"
    return 0
  fi

  secure_config_file "$NODE_ENV"
  secure_config_file "$SECRET_FILE"
  chown -R "$(service_account_name):$(service_group_name)" \
    "${DATA_DIR:-/var/lib/remnanode}" "${LOG_DIR:-/var/log/remnanode}"
  chmod 0750 "${DATA_DIR:-/var/lib/remnanode}" "${LOG_DIR:-/var/log/remnanode}"
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
  secure_config_file "$NODE_ENV"
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
  secure_config_file "$NODE_ENV"
}

write_secret_from_source() {
  local src="$1"
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 写入 ${SECRET_FILE} <- ${src}"
    return 0
  fi
  install -m 0640 -D /dev/null "$SECRET_FILE"
  if [ "$src" = "-" ]; then
    cat >"$SECRET_FILE"
  else
    install -m 0640 "$src" "$SECRET_FILE"
  fi
  secure_config_file "$SECRET_FILE"
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

migrate_owned_asset_paths() {
  [ -f "$NODE_ENV" ] || return 0
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 将旧版通用 rw-core/geo/ASN 路径迁移到项目专属目录"
    return 0
  fi

  local changed=0
  if grep -q '^XRAY_BIN=/usr/local/bin/rw-core$' "$NODE_ENV"; then
    if [ -x /usr/local/lib/remnanode/rw-core ]; then
      sed -i 's|^XRAY_BIN=/usr/local/bin/rw-core$|XRAY_BIN=/usr/local/lib/remnanode/rw-core|' "$NODE_ENV"
      changed=1
    else
      echo "保留旧 XRAY_BIN：项目私有 rw-core 尚未安装。" >&2
    fi
  fi
  if grep -q '^GEO_DIR=/usr/local/share/xray$' "$NODE_ENV"; then
    if [ -f /usr/local/share/remnanode/xray/geoip.dat ] \
      && [ -f /usr/local/share/remnanode/xray/geosite.dat ]; then
      sed -i 's|^GEO_DIR=/usr/local/share/xray$|GEO_DIR=/usr/local/share/remnanode/xray|' "$NODE_ENV"
      changed=1
    else
      echo "保留旧 GEO_DIR：项目私有 geo 资产尚未安装。" >&2
    fi
  fi
  if grep -q '^ASN_DB_PATH=/usr/local/share/asn/asn-prefixes.bin$' "$NODE_ENV"; then
    if [ -f /usr/local/share/remnanode/asn/asn-prefixes.bin ]; then
      sed -i 's|^ASN_DB_PATH=/usr/local/share/asn/asn-prefixes.bin$|ASN_DB_PATH=/usr/local/share/remnanode/asn/asn-prefixes.bin|' "$NODE_ENV"
      changed=1
    else
      echo "保留旧 ASN_DB_PATH：项目私有 ASN 数据尚未安装。" >&2
    fi
  fi
  if [ "$changed" -eq 1 ]; then
    secure_config_file "$NODE_ENV"
    echo "已将旧版共享资产路径迁移到 /usr/local/{lib,share}/remnanode。"
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
  echo "（节点已启用时，装完后约 10s 内 Panel 将自动上线）"
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
    echo "[dry-run] cleanup project runtime sockets"
    return 0
  fi
  rm -rf /run/remnanode 2>/dev/null || true
  rm -f /run/remnawave-internal-*.sock 2>/dev/null || true
}

print_pre_install_panel_hint() {
  echo
  echo "━━━━━━━━ Panel 接入提示 ━━━━━━━━"
  echo "  推荐顺序："
  echo "    1) Panel 创建节点，复制 Secret Key"
  echo "    2) 完成本脚本安装并粘贴 Secret Key"
  echo "    3) 看到 OK: TCP 已监听 后，在 Panel 启用节点"
  echo
  echo "  节点已启用时：装完后 Panel 每 10s 健康检查，约 10s 内自动上线。"
  echo "  若超过 30s 仍离线：检查防火墙，或 Panel 禁用→启用一次。"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
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
  echo "  节点已就绪。Panel 通常 10s 内自动上线。"
  echo "  若仍离线：检查防火墙 / Secret Key，或 Panel 禁用→启用一次。"
  echo "  服务器 reboot 后由 Panel 健康检查重新下发配置并自动上线。"
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
  echo "  SECRET_KEY='eyJ...' NODE_PORT=8443 bash install-*.sh --install --yes"
}

read_env_value() {
  local key="$1" file="$2"
  local line val
  [ -f "$file" ] || return 0
  line="$(grep -E "^[[:space:]]*${key}=" "$file" 2>/dev/null | head -n 1 || true)"
  [ -n "$line" ] || return 0
  val="${line#*=}"
  val="$(printf '%s' "$val" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//' -e 's/^"//' -e 's/"$//' -e "s/^'//" -e "s/'$//")"
  [ -n "$val" ] || return 0
  printf '%s' "$val"
}

install_geo_extra_files() {
  local geo_dir="${GEO_DIR:-/usr/local/share/remnanode/xray}"
  local env_file="${NODE_ENV:-/etc/remnanode/node.env}"
  local geo_zapret ip_zapret
  if [ -z "${GEO_ZAPRET_FILE:-}" ]; then
    geo_zapret="$(read_env_value GEO_ZAPRET_FILE "$env_file")"
  else
    geo_zapret="$GEO_ZAPRET_FILE"
  fi
  if [ -z "${IP_ZAPRET_FILE:-}" ]; then
    ip_zapret="$(read_env_value IP_ZAPRET_FILE "$env_file")"
  else
    ip_zapret="$IP_ZAPRET_FILE"
  fi

  local copied=0
  install_one_geo_extra() {
    local src="$1" dest_name="$2"
    [ -n "$src" ] || return 0
    [ -f "$src" ] || { echo "警告：找不到 ${src}（跳过 ${dest_name}）" >&2; return 0; }
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] 复制 ${src} -> ${geo_dir}/${dest_name}"
      return 0
    fi
    mkdir -p "$geo_dir"
    cp -f "$src" "${geo_dir}/${dest_name}"
    chmod 0644 "${geo_dir}/${dest_name}"
    echo "已安装 ${dest_name} -> ${geo_dir}/${dest_name}"
    copied=1
  }

  install_one_geo_extra "$geo_zapret" "geo-zapret.dat"
  install_one_geo_extra "$ip_zapret" "ip-zapret.dat"

  if [ "$copied" -eq 0 ]; then
    return 0
  fi
  echo "提示：Xray 路由使用 ext:geo-zapret.dat:zapret / ext:ip-zapret.dat:zapret 引用上述文件。"
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

XRAY_BIN=/usr/local/lib/remnanode/rw-core
GEO_DIR=/usr/local/share/remnanode/xray
LOG_DIR=${LOG_DIR}
ASN_DB_PATH=/usr/local/share/remnanode/asn/asn-prefixes.bin
INTERNAL_SOCKET_PATH=/run/remnanode/internal.sock
INTERNAL_REST_TOKEN=
LOW_MEMORY=${low_mem}
BODY_LIMIT_MB=

# 可选：自定义 rw-core 下载 URL（对齐官方 CUSTOM_CORE_URL）
# CUSTOM_CORE_URL=https://example.com/xray-custom
# CUSTOM_CORE_SHA256=<64-hex>

# 可选：compact ASN 数据库；URL 与 SHA-256 必须同时设置
# ASN_DB_URL=https://example.com/asn-prefixes.bin
# ASN_DB_SHA256=<64-hex>

# 可选：zapret 规则文件（复制到 GEO_DIR，供 ext:geo-zapret.dat 引用）
# GEO_ZAPRET_FILE=/path/to/geo-zapret.dat
# IP_ZAPRET_FILE=/path/to/ip-zapret.dat
EOF
}
