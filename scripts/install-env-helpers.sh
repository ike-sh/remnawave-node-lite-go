# shellcheck shell=bash
# Shared env/secret helpers for install-node.sh and install-node-alpine.sh
# Expects: PREFIX, BIN_NAME, NODE_ENV, SECRET_FILE, DRY_RUN, DATA_DIR, LOG_DIR

readonly RNL_SECRET_MAX_BYTES=262144
readonly RNL_RELEASE_ARCHIVE_MAX_BYTES=67108864
readonly RNL_RELEASE_EXTRACT_MAX_BYTES=134217728
readonly RNL_RELEASE_FILE_MAX_COUNT=64
readonly RNL_RELEASE_WORK_BYTES=402653184
readonly RNL_GEO_EXTRA_MAX_BYTES=67108864
readonly RNL_DOWNLOAD_CONNECT_TIMEOUT_SECONDS=15
readonly RNL_DOWNLOAD_MAX_TIME_SECONDS=300
readonly RNL_DOWNLOAD_SPEED_LIMIT_BYTES=1024
readonly RNL_DOWNLOAD_SPEED_TIME_SECONDS=60
readonly RNL_ARCHIVE_TIMEOUT_SECONDS=120

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
  local max_bytes="${3:-$RNL_RELEASE_ARCHIVE_MAX_BYTES}"
  local attempt size curl_status head_status
  local -a pipeline_status
  case "$url" in
    https://*) ;;
    *) echo "拒绝非 HTTPS 下载：${url}" >&2; return 1 ;;
  esac
  if ! [[ "$max_bytes" =~ ^[1-9][0-9]*$ ]] \
    || [ "${#max_bytes}" -gt 10 ] || [ "$max_bytes" -gt 1073741824 ]; then
    echo "无效下载大小上限：${max_bytes}" >&2
    return 2
  fi

  for attempt in 1 2 3; do
    rm -f "$output"
    set +o pipefail
    installer_run_without_lock curl --fail --location --silent --show-error \
      --proto '=https' --tlsv1.2 \
      --connect-timeout "$RNL_DOWNLOAD_CONNECT_TIMEOUT_SECONDS" \
      --max-time "$RNL_DOWNLOAD_MAX_TIME_SECONDS" \
      --speed-limit "$RNL_DOWNLOAD_SPEED_LIMIT_BYTES" \
      --speed-time "$RNL_DOWNLOAD_SPEED_TIME_SECONDS" \
      "$url" \
      | installer_run_without_lock head -c $((max_bytes + 1)) >"$output"
    pipeline_status=("${PIPESTATUS[@]}")
    set -o pipefail
    curl_status="${pipeline_status[0]:-1}"
    head_status="${pipeline_status[1]:-1}"
    size="$(file_size_bytes "$output")"

    if ! [[ "$size" =~ ^[0-9]+$ ]] || [ "$size" -gt "$max_bytes" ]; then
      rm -f "$output"
      echo "下载文件超过硬上限：${size:-unknown} bytes > ${max_bytes} bytes" >&2
      return 1
    fi
    if [ "$curl_status" -eq 0 ] && [ "$head_status" -eq 0 ]; then
      return 0
    fi
    rm -f "$output"
    [ "$attempt" -eq 3 ] || sleep "$attempt"
  done
  return 1
}

file_size_bytes() {
  local file="$1"
  wc -c <"$file" | tr -d '[:space:]'
}

require_file_size_at_most() {
  local file="$1" max_bytes="$2" label="${3:-文件}"
  local size
  [ -f "$file" ] || {
    echo "${label}不存在：${file}" >&2
    return 1
  }
  size="$(file_size_bytes "$file")"
  if ! [[ "$size" =~ ^[0-9]+$ ]] || [ "$size" -gt "$max_bytes" ]; then
    echo "${label}超过硬上限：${size:-unknown} bytes > ${max_bytes} bytes" >&2
    return 1
  fi
}

installer_temp_root() {
  printf '%s' "${RNL_TMP_ROOT:-/var/lib/remnanode-installer}"
}

validate_installer_temp_root_path() {
  local root="$1" component current="/"
  local -a components
  case "$root" in
    /) echo "拒绝使用 / 作为安装临时根" >&2; return 1 ;;
    /*) ;;
    *) echo "安装临时目录必须是绝对路径：${root}" >&2; return 2 ;;
  esac
  if [[ "$root" == */ ]] || [[ "$root" == *//* ]] \
    || [[ "$root" == *$'\n'* ]] || [[ "$root" == *$'\r'* ]]; then
    echo "安装临时目录路径不规范：${root}" >&2
    return 2
  fi

  if ! installer_ancestor_is_safe /; then
    echo "安装临时目录祖先不属于 root:root 或可被非 root 写入：/" >&2
    return 1
  fi
  IFS=/ read -r -a components <<<"${root#/}"
  for component in "${components[@]}"; do
    case "$component" in
      ''|.|..) echo "安装临时目录包含不安全路径分量：${root}" >&2; return 2 ;;
    esac
    current="${current%/}/${component}"
    if [ -L "$current" ]; then
      echo "安装临时目录包含符号链接祖先：${current}" >&2
      return 1
    fi
    if [ -e "$current" ] && [ ! -d "$current" ]; then
      echo "安装临时目录祖先不是目录：${current}" >&2
      return 1
    fi
    if [ -e "$current" ] && ! installer_ancestor_is_safe "$current"; then
      echo "安装临时目录祖先不属于 root:root 或可被非 root 写入：${current}" >&2
      return 1
    fi
  done
}

installer_path_owner_ids() {
  local path="$1" owner
  if owner="$(stat -c '%u:%g' "$path" 2>/dev/null)"; then
    printf '%s' "$owner"
    return 0
  fi
  stat -f '%u:%g' "$path" 2>/dev/null
}

installer_path_has_root_owner() {
  [ "$(installer_path_owner_ids "$1")" = "0:0" ]
}

installer_path_mode() {
  local path="$1" mode
  if mode="$(stat -c '%a' "$path" 2>/dev/null)"; then
    printf '%s' "$mode"
    return 0
  fi
  stat -f '%Lp' "$path" 2>/dev/null
}

installer_path_link_count() {
  local path="$1" count
  if count="$(stat -c '%h' "$path" 2>/dev/null)"; then
    printf '%s' "$count"
    return 0
  fi
  stat -f '%l' "$path" 2>/dev/null
}

installer_ancestor_is_safe() {
  local path="$1" mode
  installer_path_has_root_owner "$path" || return 1
  mode="$(installer_path_mode "$path")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || return 1
  [ $((8#$mode & 022)) -eq 0 ]
}

installer_temp_root_is_empty() {
  local root="$1" entry
  entry="$(find "$root" -mindepth 1 -maxdepth 1 -print -quit)" || return 1
  [ -z "$entry" ]
}

validate_installer_temp_root_marker() {
  local root="$1" marker="${1}/.remnanode-installer-root"
  local expected="remnanode-installer-root-v1" size mode links
  installer_ancestor_is_safe "$root" || {
    echo "安装临时根必须属于 root:root 且不可被 group/other 写入：${root}" >&2
    return 1
  }
  [ -f "$marker" ] && [ ! -L "$marker" ] || {
    echo "非空安装临时根缺少普通 marker：${marker}" >&2
    return 1
  }
  if ! installer_path_has_root_owner "$marker"; then
    echo "安装临时根 marker 必须属于 root:root：${marker}" >&2
    return 1
  fi
  links="$(installer_path_link_count "$marker")" || return 1
  [ "$links" = 1 ] || {
    echo "安装临时根 marker 存在硬链接：${marker}" >&2
    return 1
  }
  mode="$(installer_path_mode "$marker")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || {
    echo "安装临时根 marker 可被 group/other 写入：${marker}" >&2
    return 1
  }
  size="$(file_size_bytes "$marker")"
  if [ "$size" -ne $((${#expected} + 1)) ] || [ "$(cat "$marker")" != "$expected" ]; then
    echo "安装临时根 marker 内容无效：${marker}" >&2
    return 1
  fi
}

ensure_installer_temp_root() {
  local root marker expected="remnanode-installer-root-v1"
  root="$(installer_temp_root)"
  validate_installer_temp_root_path "$root" || return
  if [ ! -e "$root" ]; then
    (umask 077; mkdir -p "$root") || return
  fi
  validate_installer_temp_root_path "$root" || return
  [ -d "$root" ] || {
    echo "安装临时根不是目录：${root}" >&2
    return 1
  }
  if ! installer_path_has_root_owner "$root"; then
    echo "安装临时根必须在使用前属于 root:root：${root}" >&2
    return 1
  fi

  marker="${root}/.remnanode-installer-root"
  if installer_temp_root_is_empty "$root"; then
    if ! (umask 077; set -o noclobber; printf '%s\n' "$expected" >"$marker") 2>/dev/null; then
      echo "无法原子创建安装临时根 marker：${marker}" >&2
      return 1
    fi
  fi
  validate_installer_temp_root_marker "$root" || return

  chmod 0700 "$root" || return
  chmod 0600 "$marker" || return
  validate_installer_temp_root_path "$root" || return
  validate_installer_temp_root_marker "$root"
}

make_installer_temp_dir() {
  local prefix="${1:-work}" root
  ensure_installer_temp_root || return
  root="$(installer_temp_root)"
  mktemp -d "${root}/${prefix}.XXXXXX"
}

require_free_bytes() {
  local path="$1" required="$2" label="${3:-安装事务}"
  local available_kb available
  if ! [[ "$required" =~ ^[1-9][0-9]*$ ]]; then
    echo "无效磁盘预算：${required}" >&2
    return 2
  fi
  available_kb="$(df -Pk "$path" | awk 'NR == 2 { print $4; exit }')"
  if ! [[ "$available_kb" =~ ^[0-9]+$ ]]; then
    echo "无法读取 ${path} 的可用磁盘空间" >&2
    return 1
  fi
  available=$((available_kb * 1024))
  if [ "$available" -lt "$required" ]; then
    echo "${label}空间不足：${path} 可用 ${available} bytes，需要至少 ${required} bytes" >&2
    return 1
  fi
}

existing_parent() {
  local path="$1" parent
  parent="$path"
  while [ ! -e "$parent" ]; do
    [ "$parent" != / ] || break
    parent="$(dirname "$parent")"
  done
  printf '%s' "$parent"
}

validate_managed_absolute_path() {
  local path="$1"
  case "$path" in
    /|'') echo "拒绝使用空路径或 / 作为受管路径" >&2; return 1 ;;
    /*) ;;
    *) echo "受管路径必须是绝对路径：${path}" >&2; return 2 ;;
  esac
  if [[ "$path" == */ ]] || [[ "$path" == *//* ]] \
    || [[ "$path" == *$'\n'* ]] || [[ "$path" == *$'\r'* ]] \
    || [[ "/${path#/}/" == */./* ]] || [[ "/${path#/}/" == */../* ]]; then
    echo "受管路径不规范：${path}" >&2
    return 2
  fi
}

managed_ancestor_is_safe() {
  local path="$1" owner mode uid
  [ -d "$path" ] && [ ! -L "$path" ] || return 1
  owner="$(installer_path_owner_ids "$path")" || return 1
  uid=${owner%%:*}
  [ "$uid" = 0 ] || return 1
  mode="$(installer_path_mode "$path")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || return 1
  [ $((8#$mode & 022)) -eq 0 ]
}

installer_lock_path() {
  # Tests may override this function after sourcing the helper. Production
  # entrypoints intentionally have no environment-controlled path override.
  printf '%s' /run/lock/remnanode-installer.lock
}

installer_lock_owner_ids() {
  local path="$1" owner
  if owner="$(stat -c '%u:%g' "$path" 2>/dev/null)"; then
    printf '%s' "$owner"
    return 0
  fi
  stat -f '%u:%g' "$path" 2>/dev/null
}

installer_lock_has_root_owner() {
  [ "$(installer_lock_owner_ids "$1")" = 0:0 ]
}

installer_lock_mode() {
  local path="$1" mode
  if mode="$(stat -c '%a' "$path" 2>/dev/null)"; then
    printf '%s' "$mode"
    return 0
  fi
  stat -f '%Lp' "$path" 2>/dev/null
}

installer_lock_link_count() {
  local path="$1" links
  if links="$(stat -c '%h' "$path" 2>/dev/null)"; then
    printf '%s' "$links"
    return 0
  fi
  stat -f '%l' "$path" 2>/dev/null
}

installer_lock_file_has_security_properties() {
  local path="$1" expected_links="$2" mode links
  [ -f "$path" ] && [ ! -L "$path" ] || {
    echo "installer lock is not a regular file: ${path}" >&2
    return 1
  }
  installer_lock_has_root_owner "$path" || {
    echo "installer lock must be owned by root:root: ${path}" >&2
    return 1
  }
  links="$(installer_lock_link_count "$path")" || return
  [ "$links" = "$expected_links" ] || {
    echo "installer lock has an unexpected link count: ${path}" >&2
    return 1
  }
  mode="$(installer_lock_mode "$path")" || return
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode)) -eq $((8#0600)) ] || {
    echo "installer lock must have mode 0600: ${path}" >&2
    return 1
  }
}

installer_lock_file_is_safe() {
  installer_lock_file_has_security_properties "$1" 1
}

installer_recover_interrupted_lock_creation() {
  local path="$1" directory candidate path_id candidate_id match="" count=0
  directory="$(dirname "$path")" || return
  installer_lock_file_has_security_properties "$path" 2 || return
  path_id="$(installer_lock_device_inode "$path")" || return

  for candidate in "$directory"/.rnl-lock-stage.*; do
    [ -e "$candidate" ] || [ -L "$candidate" ] || continue
    installer_lock_file_has_security_properties "$candidate" 2 \
      >/dev/null 2>&1 || continue
    candidate_id="$(installer_lock_device_inode "$candidate")" || continue
    [ "$candidate_id" = "$path_id" ] || continue
    match="$candidate"
    count=$((count + 1))
  done
  [ "$count" -eq 1 ] || {
    echo "cannot uniquely recover interrupted installer lock creation: ${path}" >&2
    return 1
  }

  rm -f -- "$match" || {
    echo "cannot remove interrupted installer lock staging inode: ${match}" >&2
    return 1
  }
  installer_lock_file_is_safe "$path"
}

installer_validate_or_recover_lock_file() {
  local path="$1" links
  [ -f "$path" ] && [ ! -L "$path" ] || {
    installer_lock_file_is_safe "$path"
    return
  }
  links="$(installer_lock_link_count "$path")" || return
  if [ "$links" = 2 ]; then
    installer_recover_interrupted_lock_creation "$path"
  else
    installer_lock_file_is_safe "$path"
  fi
}

installer_lock_directory_mode_is_safe() {
  local path="$1" mode="$2"
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] || return 1
  # root:root group-writable directories (notably /run/lock mode 0775 on
  # Debian-family systems) remain root-controlled. Other-writable locations
  # additionally require sticky replacement protection.
  if [ $((8#$mode & 002)) -eq 0 ]; then
    return 0
  fi
  # Writable lock directories are accepted only with sticky protection, so
  # non-root group members cannot replace the root-owned lock inode.
  [ "$path" = /run/lock ] && [ $((8#$mode & 01000)) -ne 0 ]
}

installer_lock_directory_is_safe() {
  local path="$1" mode
  [ -d "$path" ] && [ ! -L "$path" ] || return 1
  installer_lock_has_root_owner "$path" || return 1
  mode="$(installer_lock_mode "$path")" || return
  installer_lock_directory_mode_is_safe "$path" "$mode"
}

installer_lock_device_inode() {
  local path="$1" identity
  if identity="$(stat -Lc '%d:%i' "$path" 2>/dev/null)"; then
    printf '%s' "$identity"
    return 0
  fi
  stat -f '%d:%i' "$path" 2>/dev/null
}

installer_lock_fd_path() {
  local fd="$1"
  if [ -e "/proc/$$/fd/${fd}" ] || [ -L "/proc/$$/fd/${fd}" ]; then
    printf '/proc/%s/fd/%s' "$$" "$fd"
    return 0
  fi
  if [ -e "/dev/fd/${fd}" ] || [ -L "/dev/fd/${fd}" ]; then
    printf '/dev/fd/%s' "$fd"
    return 0
  fi
  return 1
}

installer_validate_lock_directory() {
  local path="$1" directory
  [ "$path" = /run/lock/remnanode-installer.lock ] || {
    # Only an in-process function override can reach this branch. It keeps the
    # production environment contract fixed while allowing isolated tests.
    validate_managed_absolute_path "$path" || return
  }
  directory="$(dirname "$path")" || return
  if [ "$path" = /run/lock/remnanode-installer.lock ]; then
    installer_lock_directory_is_safe /run || {
      echo "installer lock parent is unsafe: /run" >&2
      return 1
    }
  fi
  if [ ! -e "$directory" ] && [ "$path" = /run/lock/remnanode-installer.lock ]; then
    install -d -o root -g root -m 0755 "$directory" || return
  fi
  installer_lock_directory_is_safe "$directory" || {
    echo "installer lock directory must be root-controlled: ${directory}" >&2
    return 1
  }
}

installer_validate_lock_fd() {
  local fd="$1" path="$2" expected="${3:-}" fd_path path_id fd_id
  [[ "$fd" =~ ^[0-9]+$ ]] && [ "${#fd}" -le 6 ] && [ "$fd" -ge 10 ] || {
    echo "invalid inherited installer lock descriptor: ${fd}" >&2
    return 1
  }
  installer_validate_lock_directory "$path" || return
  installer_lock_file_is_safe "$path" || return
  fd_path="$(installer_lock_fd_path "$fd")" || {
    echo "inherited installer lock descriptor is closed: ${fd}" >&2
    return 1
  }
  path_id="$(installer_lock_device_inode "$path")" || return
  fd_id="$(installer_lock_device_inode "$fd_path")" || return
  [ "$fd_id" = "$path_id" ] || {
    echo "inherited installer lock descriptor points to a different inode" >&2
    return 1
  }
  if [ -n "$expected" ] && [ "$expected" != "$path_id" ]; then
    echo "inherited installer lock identity changed" >&2
    return 1
  fi
  INSTALLER_LOCK_ID="$path_id"
}

installer_close_lock_fd() {
  local fd="${INSTALLER_LOCK_FD:-${RNL_INSTALLER_LOCK_FD:-}}"
  if [[ "$fd" =~ ^[0-9]+$ ]] && [ "${#fd}" -le 6 ] && [ "$fd" -ge 10 ]; then
    exec {fd}>&-
  fi
  INSTALLER_LOCK_FD=""
  INSTALLER_LOCK_ID=""
  unset RNL_INSTALLER_LOCK_FD RNL_INSTALLER_LOCK_ID
}

installer_run_without_lock() (
  installer_close_lock_fd
  "$@"
)

installer_run_nested() (
  local fd="${INSTALLER_LOCK_FD:-}" identity="${INSTALLER_LOCK_ID:-}"
  if [ -z "$fd" ]; then
    "$@"
    return
  fi
  installer_validate_lock_fd "$fd" "$(installer_lock_path)" "$identity" || return
  # shellcheck disable=SC2030
  export RNL_INSTALLER_LOCK_FD="$fd"
  # shellcheck disable=SC2030
  export RNL_INSTALLER_LOCK_ID="$identity"
  "$@"
)

installer_acquire_lock() {
  local path current_fd current_id inherited_fd inherited_id old_umask
  local fd_path path_id fd_id directory staging=""
  export -n INSTALLER_LOCK_FD INSTALLER_LOCK_ID 2>/dev/null || true
  path="$(installer_lock_path)" || return
  command -v flock >/dev/null 2>&1 || {
    echo "missing command: flock (install util-linux)" >&2
    return 1
  }
  current_fd="${INSTALLER_LOCK_FD:-}"
  current_id="${INSTALLER_LOCK_ID:-}"
  if [ -n "$current_fd" ] || [ -n "$current_id" ]; then
    [ -n "$current_fd" ] && [ -n "$current_id" ] || {
      echo "incomplete current installer lock metadata" >&2
      return 1
    }
    installer_validate_lock_fd "$current_fd" "$path" "$current_id" || return
    if ! flock -n "$current_fd"; then
      echo "current installer lock descriptor does not own the lock" >&2
      return 1
    fi
    INSTALLER_LOCK_FD="$current_fd"
    return 0
  fi
  # shellcheck disable=SC2031
  inherited_fd="${RNL_INSTALLER_LOCK_FD:-}"
  # shellcheck disable=SC2031
  inherited_id="${RNL_INSTALLER_LOCK_ID:-}"
  unset RNL_INSTALLER_LOCK_FD RNL_INSTALLER_LOCK_ID

  if [ -n "$inherited_fd" ] || [ -n "$inherited_id" ]; then
    [ -n "$inherited_fd" ] && [ -n "$inherited_id" ] || {
      echo "incomplete inherited installer lock metadata" >&2
      return 1
    }
    installer_validate_lock_fd "$inherited_fd" "$path" "$inherited_id" || return
    if ! flock -n "$inherited_fd"; then
      echo "inherited installer lock descriptor does not own the lock" >&2
      return 1
    fi
    INSTALLER_LOCK_FD="$inherited_fd"
    return 0
  fi

  installer_validate_lock_directory "$path" || return
  if [ -e "$path" ] || [ -L "$path" ]; then
    installer_validate_or_recover_lock_file "$path" || return
  else
    directory="$(dirname "$path")" || return
    staging="$(umask 077; mktemp "${directory}/.rnl-lock-stage.XXXXXX")" || return
    if ! installer_lock_file_is_safe "$staging"; then
      rm -f "$staging"
      return 1
    fi
    if ln "$staging" "$path" 2>/dev/null; then
      if ! rm -f "$staging"; then
        echo "cannot remove installer lock staging inode: ${staging}" >&2
        return 1
      fi
      staging=""
    else
      rm -f "$staging" || return
      staging=""
    fi
    # link(2) never follows or opens a competing symlink/FIFO/device. A root
    # installer that won the same race is accepted; every other object fails.
    installer_lock_file_is_safe "$path" || return
  fi
  old_umask="$(umask)"
  umask 077
  if ! exec {INSTALLER_LOCK_FD}<>"$path"; then
    umask "$old_umask"
    echo "cannot open installer lock: ${path}" >&2
    return 1
  fi
  umask "$old_umask"
  if ! installer_validate_lock_fd "$INSTALLER_LOCK_FD" "$path"; then
    installer_close_lock_fd
    return 1
  fi
  if ! flock -n "$INSTALLER_LOCK_FD"; then
    echo "another remnanode installer operation is already running" >&2
    installer_close_lock_fd
    return 1
  fi
  # Revalidate after flock so replacement cannot silently split contenders
  # across different inodes. The root-controlled directory makes this stable.
  fd_path="$(installer_lock_fd_path "$INSTALLER_LOCK_FD")" || {
    installer_close_lock_fd
    return 1
  }
  path_id="$(installer_lock_device_inode "$path")" || {
    installer_close_lock_fd
    return 1
  }
  fd_id="$(installer_lock_device_inode "$fd_path")" || {
    installer_close_lock_fd
    return 1
  }
  if [ "$path_id" != "$fd_id" ]; then
    echo "installer lock inode changed while acquiring the lock" >&2
    installer_close_lock_fd
    return 1
  fi
}

validate_managed_parent_path() {
  local path="$1" parent component current="/"
  local -a components
  validate_managed_absolute_path "$path" || return
  parent="$(dirname "$path")"
  managed_ancestor_is_safe / || {
    echo "受管路径祖先不安全：/" >&2
    return 1
  }
  [ "$parent" = / ] && return 0

  IFS=/ read -r -a components <<<"${parent#/}"
  for component in "${components[@]}"; do
    current="${current%/}/${component}"
    if [ -L "$current" ]; then
      echo "受管路径包含符号链接祖先：${current}" >&2
      return 1
    fi
    if [ -e "$current" ]; then
      if ! managed_ancestor_is_safe "$current"; then
        echo "受管路径祖先必须由 root 控制且不可被 group/other 写入：${current}" >&2
        return 1
      fi
    fi
  done
}

managed_path_has_owner() {
  local path="$1" uid="$2" gid="$3"
  [ "$(installer_path_owner_ids "$path")" = "${uid}:${gid}" ]
}

managed_path_link_count() {
  installer_path_link_count "$1"
}

validate_existing_owned_directory() {
  local path="$1" uid="$2" gid="$3" mode
  validate_managed_parent_path "$path" || return
  if [ ! -e "$path" ] && [ ! -L "$path" ]; then
    return 0
  fi
  [ -d "$path" ] && [ ! -L "$path" ] || {
    echo "受管目录不是普通目录或是符号链接：${path}" >&2
    return 1
  }
  managed_path_has_owner "$path" "$uid" "$gid" || {
    echo "受管目录 owner 不符合预期：${path}" >&2
    return 1
  }
  mode="$(installer_path_mode "$path")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || {
    echo "受管目录可被 group/other 写入：${path}" >&2
    return 1
  }
}

ensure_owned_directory() {
  local path="$1" user="$2" group="$3" mode="$4" uid gid
  uid="$(id -u "$user")" || return 1
  if command -v getent >/dev/null 2>&1; then
    gid="$(getent group "$group" | awk -F: -v name="$group" '$1 == name { print $3; exit }')"
  else
    gid="$(awk -F: -v name="$group" '$1 == name { print $3; exit }' /etc/group)"
  fi
  [[ "$gid" =~ ^[0-9]+$ ]] || { echo "找不到受管目录目标组：${group}" >&2; return 1; }
  validate_existing_owned_directory "$path" "$uid" "$gid" || return
  if [ ! -d "$path" ]; then
    install -d -o "$user" -g "$group" -m "$mode" "$path" || return
  fi
  validate_existing_owned_directory "$path" "$uid" "$gid" || return
  chmod "$mode" "$path"
}

validate_managed_regular_file() {
  local path="$1" mode links owner uid
  validate_managed_parent_path "$path" || return
  [ -f "$path" ] && [ ! -L "$path" ] || {
    echo "受管配置不是普通文件或是符号链接：${path}" >&2
    return 1
  }
  links="$(managed_path_link_count "$path")" || return 1
  [ "$links" = 1 ] || { echo "受管配置存在硬链接：${path}" >&2; return 1; }
  owner="$(installer_path_owner_ids "$path")" || return 1
  uid=${owner%%:*}
  [ "$uid" = 0 ] || { echo "受管配置必须属于 root：${path}" >&2; return 1; }
  mode="$(installer_path_mode "$path")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || {
    echo "受管配置可被 group/other 写入：${path}" >&2
    return 1
  }
}

validate_managed_install_file() {
  local path="$1" label="${2:-受管文件}" mode links
  [ -f "$path" ] && [ ! -L "$path" ] || {
    echo "${label}不是普通文件或是符号链接：${path}" >&2
    return 1
  }
  links="$(managed_path_link_count "$path")" || return 1
  [ "$links" = 1 ] || {
    echo "${label}存在硬链接：${path}" >&2
    return 1
  }
  managed_path_has_owner "$path" 0 0 || {
    echo "${label}必须属于 root:root：${path}" >&2
    return 1
  }
  mode="$(installer_path_mode "$path")" || return 1
  [[ "$mode" =~ ^[0-7]{3,4}$ ]] && [ $((8#$mode & 022)) -eq 0 ] || {
    echo "${label}可被 group/other 写入：${path}" >&2
    return 1
  }
}

install_managed_file() (
  local source="${1:-}" target="${2:-}" requested_mode="${3:-}"
  local target_dir target_name tmp="" actual_mode

  if [ "$#" -ne 3 ] || ! [[ "$requested_mode" =~ ^0?[0-7]{3}$ ]] \
    || [ $((8#$requested_mode & 022)) -ne 0 ]; then
    echo "受管文件安装参数或 mode 无效" >&2
    return 2
  fi
  [ "$source" != "$target" ] || {
    echo "受管文件 source 与 target 不能相同：${source}" >&2
    return 2
  }

  validate_managed_parent_path "$source" || return
  validate_managed_install_file "$source" "受管文件 source" || return
  validate_managed_parent_path "$target" || return
  if [ -e "$target" ] || [ -L "$target" ]; then
    validate_managed_install_file "$target" "受管文件 target" || return
  fi

  target_dir="$(dirname "$target")" || return
  target_name="$(basename "$target")" || return
  tmp="$(mktemp "${target_dir}/.${target_name}.XXXXXX")" || return
  trap 'if [ -n "${tmp:-}" ]; then rm -f -- "$tmp"; fi' EXIT

  validate_managed_install_file "$tmp" "受管文件 staging" || return
  cp -- "$source" "$tmp" || return
  chown root:root "$tmp" || return
  chmod "$requested_mode" "$tmp" || return
  validate_managed_install_file "$tmp" "受管文件 staging" || return
  actual_mode="$(installer_path_mode "$tmp")" || return
  [ $((8#$actual_mode)) -eq $((8#$requested_mode)) ] || {
    echo "受管文件 staging mode 不符合预期：${tmp}" >&2
    return 1
  }

  # Revalidate immutable inputs immediately before replacing the old inode.
  validate_managed_parent_path "$source" || return
  validate_managed_install_file "$source" "受管文件 source" || return
  validate_managed_parent_path "$target" || return
  if [ -e "$target" ] || [ -L "$target" ]; then
    validate_managed_install_file "$target" "受管文件 target" || return
  fi
  mv -f -- "$tmp" "$target" || return
  tmp=""

  validate_managed_parent_path "$target" || return
  validate_managed_install_file "$target" "受管文件 target" || return
  actual_mode="$(installer_path_mode "$target")" || return
  [ $((8#$actual_mode)) -eq $((8#$requested_mode)) ] || {
    echo "受管文件 target mode 不符合预期：${target}" >&2
    return 1
  }
)

run_with_timeout() {
  local seconds="$1"
  shift
  command -v timeout >/dev/null 2>&1 || {
    echo "缺少命令：timeout" >&2
    return 1
  }
  installer_run_without_lock timeout "$seconds" "$@"
}

archive_unpacked_bytes() {
  local archive="$1"
  run_with_timeout "$RNL_ARCHIVE_TIMEOUT_SECONDS" tar -tvzf "$archive" \
    | awk -v limit="$RNL_RELEASE_EXTRACT_MAX_BYTES" '
    function add_size(value) {
      found = 1
      if (value > limit || total > limit - value) {
        overflow = 1
      } else if (!overflow) {
        total += value
      }
    }
    $3 ~ /^[0-9]+$/ { add_size($3); next }
    $5 ~ /^[0-9]+$/ { add_size($5) }
    END {
      if (!found) exit 1
      printf "%.0f", overflow ? limit + 1 : total
    }
  '
}

validate_release_archive_budget() {
  local archive="$1" unpacked count
  require_file_size_at_most "$archive" "$RNL_RELEASE_ARCHIVE_MAX_BYTES" "Release 归档" || return
  unpacked="$(archive_unpacked_bytes "$archive")" || {
    echo "无法计算 Release 归档解压大小" >&2
    return 1
  }
  count="$(run_with_timeout "$RNL_ARCHIVE_TIMEOUT_SECONDS" tar -tzf "$archive" \
    | awk 'END { print NR + 0 }')"
  if ! [[ "$unpacked" =~ ^[0-9]+$ ]] || [ "$unpacked" -gt "$RNL_RELEASE_EXTRACT_MAX_BYTES" ]; then
    echo "Release 归档解压大小超过硬上限：${unpacked} bytes" >&2
    return 1
  fi
  if ! [[ "$count" =~ ^[0-9]+$ ]] || [ "$count" -gt "$RNL_RELEASE_FILE_MAX_COUNT" ]; then
    echo "Release 归档文件数超过硬上限：${count}" >&2
    return 1
  fi
}

release_archive_has_unsafe_paths() {
  local archive="$1"
  run_with_timeout "$RNL_ARCHIVE_TIMEOUT_SECONDS" tar -tzf "$archive" | awk '
    /(^\/|(^|\/)\.\.($|\/))/ { unsafe = 1 }
    END { exit(unsafe ? 0 : 1) }
  '
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

validate_release_support_link() {
  local link="$1" target
  validate_managed_parent_path "$link" || return
  if [ ! -e "$link" ] && [ ! -L "$link" ]; then
    return 0
  fi
  [ -L "$link" ] || {
    echo "Release support-current 必须是符号链接或不存在：${link}" >&2
    return 1
  }
  installer_path_has_root_owner "$link" || {
    echo "Release support-current 必须属于 root:root：${link}" >&2
    return 1
  }
  target="$(readlink "$link")" || return 1
  if ! [[ "$target" =~ ^support/[A-Za-z0-9][A-Za-z0-9._-]*$ ]]; then
    echo "Release support-current 指向不安全位置：${target}" >&2
    return 1
  fi
}

ensure_release_support_layout() {
  local support_root="$1" support_link="$2"
  ensure_owned_directory "$(dirname "$support_root")" root root 0755 || return
  ensure_owned_directory "$support_root" root root 0755 || return
  validate_existing_owned_directory "$support_root" 0 0 || return
  validate_release_support_link "$support_link"
}

remove_existing_support_release() {
  local support_root="$1" tag="$2" release_path="${1}/${2}"
  validate_release_coordinates placeholder/repository "$tag" || return
  validate_existing_owned_directory "$support_root" 0 0 || return
  if [ ! -e "$release_path" ] && [ ! -L "$release_path" ]; then
    return 0
  fi
  validate_existing_owned_directory "$release_path" 0 0 || {
    echo "拒绝清理不安全的 Release support 目录：${release_path}" >&2
    return 1
  }
  rm -rf "$release_path"
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
  extracted_bytes=""
  version_output=""
  staged=""
  support_root="/usr/local/lib/remnanode/support"
  support_stage=""
  support_link="/usr/local/lib/remnanode/support-current"
  ensure_release_support_layout "$support_root" "$support_link"
  tmp="$(make_installer_temp_dir release)"
  trap '[ -z "${tmp:-}" ] || rm -rf "$tmp"; [ -z "${staged:-}" ] || rm -f "$staged"; [ -z "${support_stage:-}" ] || rm -rf "$support_stage"; [ -z "${support_link:-}" ] || rm -f "${support_link}.new.$$"' EXIT
  require_free_bytes "$tmp" "$RNL_RELEASE_WORK_BYTES" "Release 下载与解压"
  require_free_bytes "$(existing_parent "$(dirname "$target")")" \
    "$RNL_RELEASE_EXTRACT_MAX_BYTES" "Release 目标文件系统"
  staged="${target}.new.$$"

  download_https_file "${base}/${name}" "$tmp/archive.tar.gz" "$RNL_RELEASE_ARCHIVE_MAX_BYTES"
  expected="${RNL_RELEASE_SHA256:-}"
  if [ -z "$expected" ]; then
    download_https_file "${base}/SHA256SUMS" "$tmp/SHA256SUMS" 1048576
    expected="$(awk -v name="$name" '$2 == name || $2 == "*" name { print $1; exit }' "$tmp/SHA256SUMS")"
  fi
  if [ -z "$expected" ]; then
    echo "SHA256SUMS 未包含 ${name}" >&2
    exit 1
  fi
  verify_file_sha256 "$tmp/archive.tar.gz" "$expected"
  validate_release_archive_budget "$tmp/archive.tar.gz"

  if release_archive_has_unsafe_paths "$tmp/archive.tar.gz"; then
    echo "Release 归档包含不安全路径" >&2
    exit 1
  fi
  if run_with_timeout "$RNL_ARCHIVE_TIMEOUT_SECONDS" tar -tvzf "$tmp/archive.tar.gz" | awk '
    { type = substr($1, 1, 1); if (type != "-" && type != "d") bad = 1 }
    END { exit(bad ? 0 : 1) }
  '; then
    echo "Release 归档包含符号链接、硬链接或特殊文件" >&2
    exit 1
  fi
  run_with_timeout "$RNL_ARCHIVE_TIMEOUT_SECONDS" tar -xzf "$tmp/archive.tar.gz" -C "$tmp"
  extracted_bytes="$(( $(du -sk "$tmp" | awk '{ print $1 }') * 1024 ))"
  if [ "$extracted_bytes" -gt "$RNL_RELEASE_WORK_BYTES" ]; then
    echo "Release 解压目录超过事务硬上限：${extracted_bytes} bytes" >&2
    exit 1
  fi
  extracted="$tmp/remnanode-lite"
  [ -f "$extracted" ] && [ ! -L "$extracted" ] || {
    echo "Release 归档缺少常规文件 remnanode-lite" >&2
    exit 1
  }
  chmod 0755 "$extracted"
  version_output="$(installer_run_without_lock "$extracted" version)"
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

  require_binary_not_running "$target"
  install -o root -g root -m 0755 "$extracted" "$staged"
  mv -f "$staged" "$target"

  support_stage="$(mktemp -d "${support_root}/.${tag}.XXXXXX")"
  validate_existing_owned_directory "$support_stage" 0 0
  install -d -o root -g root -m 0755 "$support_stage/deploy" "$support_stage/scripts"
  install -o root -g root -m 0644 "$tmp/support/deploy/remnawave-node.service" "$support_stage/deploy/"
  install -o root -g root -m 0755 "$tmp/support/deploy/remnawave-node.openrc" "$support_stage/deploy/"
  install -o root -g root -m 0644 "$tmp/support/scripts/install-env-helpers.sh" "$support_stage/scripts/"
  install -o root -g root -m 0755 \
    "$tmp/support/scripts/install-xray.sh" \
    "$tmp/support/scripts/upgrade.sh" \
    "$tmp/support/scripts/uninstall.sh" \
    "$support_stage/scripts/"
  remove_existing_support_release "$support_root" "$tag"
  mv "$support_stage" "$support_root/$tag"
  support_stage=""
  validate_existing_owned_directory "$support_root/$tag" 0 0
  validate_release_support_link "$support_link"
  rm -f "${support_link}.new.$$"
  ln -sfn "support/$tag" "${support_link}.new.$$"
  mv -fT "${support_link}.new.$$" "$support_link"
  validate_release_support_link "$support_link"
  installer_run_without_lock "$target" version
)

resolve_installed_support_file() {
  local support_link="$1" relative="$2" support_base link_target
  case "/${relative}/" in
    *//*|*/./*|*/../*) echo "非法 support 相对路径：${relative}" >&2; return 2 ;;
  esac
  [ -n "$relative" ] && [[ "$relative" != /* ]] \
    && [[ "$relative" != *$'\n'* ]] && [[ "$relative" != *$'\r'* ]] || {
    echo "非法 support 相对路径：${relative}" >&2
    return 2
  }
  validate_release_support_link "$support_link" || return
  link_target="$(readlink "$support_link")" || return
  support_base="$(dirname "$support_link")" || return
  printf '%s/%s/%s' "$support_base" "$link_target" "$relative"
}

installed_support_file() {
  resolve_installed_support_file \
    /usr/local/lib/remnanode/support-current "$1"
}

service_account_name() {
  printf '%s' "${SERVICE_USER:-remnanode}"
}

service_group_name() {
  printf '%s' "${SERVICE_GROUP:-remnanode}"
}

validate_service_group_exclusive() {
  local user="$1" group="$2" expected_gid="$3"
  local passwd_file="${4:-/etc/passwd}" group_file="${5:-/etc/group}"
  local name gid members member found=0
  local -a member_list

  while IFS=: read -r name _ gid members _; do
    [ "$name" = "$group" ] || continue
    found=$((found + 1))
    [ "$gid" = "$expected_gid" ] || {
      echo "组 ${group} 的 GID 在校验期间发生变化" >&2
      return 1
    }
    IFS=, read -r -a member_list <<<"$members"
    for member in "${member_list[@]}"; do
      [ -z "$member" ] || [ "$member" = "$user" ] || {
        echo "拒绝使用包含其他成员 ${member} 的 ${group} 组" >&2
        return 1
      }
    done
  done <"$group_file"
  [ "$found" -eq 1 ] || {
    echo "组 ${group} 在 ${group_file} 中必须唯一存在" >&2
    return 1
  }

  while IFS=: read -r name _ _ gid _; do
    if [ "$gid" = "$expected_gid" ] && [ "$name" != "$user" ]; then
      echo "拒绝使用同时作为其他用户 ${name} 主组的 ${group} 组" >&2
      return 1
    fi
  done <"$passwd_file"
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
  validate_service_group_exclusive "$user" "$group" "$group_gid"

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
  validate_service_group_exclusive "$user" "$group" "$group_gid"
}

setup_service_directories() {
  local user group
  user="$(service_account_name)"
  group="$(service_group_name)"

  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 创建专用目录并设置 ${user}:${group} 权限"
    return 0
  fi

  ensure_owned_directory "$(dirname "$NODE_ENV")" root "$group" 0750
  ensure_owned_directory "${DATA_DIR:-/var/lib/remnanode}" "$user" "$group" 0750
  ensure_owned_directory "${LOG_DIR:-/var/log/remnanode}" "$user" "$group" 0750
  ensure_owned_directory /usr/local/lib/remnanode root root 0755
  ensure_owned_directory /usr/local/share/remnanode/xray root root 0755
  ensure_owned_directory /usr/local/share/remnanode/asn root root 0755
  ensure_installer_temp_root || return
  secure_config_file "$NODE_ENV"
  secure_config_file "$SECRET_FILE"
}

secure_config_file() {
  local path="$1"
  if [ ! -e "$path" ] && [ ! -L "$path" ]; then
    return 0
  fi
  validate_managed_regular_file "$path" || return
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
  ensure_owned_directory "${DATA_DIR:-/var/lib/remnanode}" \
    "$(service_account_name)" "$(service_group_name)" 0750
  ensure_owned_directory "${LOG_DIR:-/var/log/remnanode}" \
    "$(service_account_name)" "$(service_group_name)" 0750
  ensure_owned_directory /usr/local/lib/remnanode root root 0755
  ensure_owned_directory /usr/local/share/remnanode/xray root root 0755
  ensure_owned_directory /usr/local/share/remnanode/asn root root 0755
  ensure_installer_temp_root || return
}

secret_from_env_file() {
  if [ ! -f "$NODE_ENV" ]; then
    return 1
  fi
  local val
  val="$(read_env_value SECRET_KEY "$NODE_ENV")"
  [ -n "$val" ]
}

secret_configured() {
  if secret_from_env_file; then
    return 0
  fi
  [ -f "$SECRET_FILE" ] && [ -s "$SECRET_FILE" ]
}

secret_validator_binary() {
  local validator="${PREFIX:?PREFIX is required}/${BIN_NAME:?BIN_NAME is required}"
  if [ ! -x "$validator" ]; then
    echo "找不到 Secret Key 校验器：${validator}" >&2
    return 1
  fi
  printf '%s' "$validator"
}

validate_secret_key() {
  local value="$1" length remainder validator
  length="${#value}"
  if [ "$length" -eq 0 ] || [ "$length" -gt "$RNL_SECRET_MAX_BYTES" ]; then
    echo "SECRET_KEY 长度必须在 1..${RNL_SECRET_MAX_BYTES} bytes" >&2
    return 1
  fi
  if ! [[ "$value" =~ ^[A-Za-z0-9+/_-]+={0,2}$ ]]; then
    echo "SECRET_KEY 必须是单行 base64/base64url，拒绝 shell 字符或空白" >&2
    return 1
  fi
  remainder=$((length % 4))
  if [[ "$value" == *=* ]]; then
    if [ "$remainder" -ne 0 ]; then
      echo "SECRET_KEY base64 padding 无效" >&2
      return 1
    fi
  elif [ "$remainder" -eq 1 ]; then
    echo "SECRET_KEY base64 长度无效" >&2
    return 1
  fi

  validator="$(secret_validator_binary)" || return
  if ! printf '%s' "$value" \
    | installer_run_without_lock "$validator" validate-secret; then
    echo "SECRET_KEY 未通过严格 JSON 校验" >&2
    return 1
  fi
}

read_secret_source_canonical() {
  local src="$1" output="$2" validator
  validator="$(secret_validator_binary)" || return
  if ! installer_run_without_lock \
    "$validator" canonicalize-secret "$src" >"$output"; then
    rm -f "$output"
    echo "Secret Key 输入未通过安全读取与严格校验：${src}" >&2
    return 1
  fi
}

validate_secret_file() {
  local path="${1:-$SECRET_FILE}" validator
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  validator="$(secret_validator_binary)" || return
  if ! installer_run_without_lock \
    "$validator" canonicalize-secret "$path" >/dev/null; then
    echo "Secret Key 文件未通过安全读取与严格校验：${path}" >&2
    return 1
  fi
}

set_env_value() {
  local key="$1" value="$2" tmp
  if ! [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || [[ "$value" == *$'\n'* ]] || [[ "$value" == *$'\r'* ]]; then
    echo "拒绝写入无效环境配置：${key}" >&2
    return 2
  fi
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 设置 ${NODE_ENV} ${key}=${value}"
    return 0
  fi
  [ -f "$NODE_ENV" ] || {
    echo "找不到 ${NODE_ENV}，请先创建环境配置。" >&2
    return 1
  }
  tmp="$(mktemp "$(dirname "$NODE_ENV")/.node.env.XXXXXX")"
  awk -v key="$key" '$0 !~ ("^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=") { print }' "$NODE_ENV" >"$tmp"
  printf '%s=%s\n' "$key" "$value" >>"$tmp"
  secure_config_file "$tmp"
  mv -f "$tmp" "$NODE_ENV"
}

normalize_env_key_assignment() {
  local key="$1" file="${2:-$NODE_ENV}" count tmp
  [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || return 2
  [ -f "$file" ] || return 0
  count="$(awk -v key="$key" '
    BEGIN { pattern = "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" }
    $0 ~ pattern { count++ }
    END { print count + 0 }
  ' "$file")"
  [ "$count" -gt 1 ] || return 0
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 合并 ${file} 中重复的 ${key}（保留最后一项）"
    return 0
  fi
  tmp="$(mktemp "$(dirname "$file")/.node.env.XXXXXX")"
  if ! awk -v key="$key" '
    BEGIN { pattern = "^[[:space:]]*(export[[:space:]]+)?" key "[[:space:]]*=" }
    $0 ~ pattern { last = $0; found = 1; next }
    { print }
    END { if (found) print last }
  ' "$file" >"$tmp"; then
    rm -f "$tmp"
    return 1
  fi
  secure_config_file "$tmp"
  mv -f "$tmp" "$file"
  echo "已合并 ${file} 中重复的 ${key}（保留最后一项）"
}

normalize_runtime_environment() {
  local key
  for key in \
    NODE_PORT NODE_BIND_ADDR SECRET_KEY SECRET_KEY_FILE XRAY_BIN GEO_DIR LOG_DIR \
    INTERNAL_SOCKET_PATH INTERNAL_REST_TOKEN ASN_DB_PATH DISABLE_HASHED_SET_CHECK \
    LOW_MEMORY BODY_LIMIT_MB CUSTOM_CORE_URL CUSTOM_CORE_SHA256 ASN_DB_URL \
    ASN_DB_SHA256 GEO_ZAPRET_FILE IP_ZAPRET_FILE GOMEMLIMIT NODE_CONTRACT_VERSION \
    XRAY_CORE_VERSION; do
    normalize_env_key_assignment "$key"
  done
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
  tmp="$(mktemp "$(dirname "$NODE_ENV")/.node.env.XXXXXX")"
  awk '
    !/^[[:space:]]*(export[[:space:]]+)?SECRET_KEY[[:space:]]*=/ &&
    !/^[[:space:]]*(export[[:space:]]+)?SECRET_KEY_FILE[[:space:]]*=/ &&
    !/^[[:space:]]*#[[:space:]]*SECRET_KEY_FILE[[:space:]]*=/ { print }
  ' "$NODE_ENV" >"$tmp"
  {
    echo "SECRET_KEY="
    echo "SECRET_KEY_FILE=${SECRET_FILE}"
  } >>"$tmp"
  secure_config_file "$tmp"
  mv -f "$tmp" "$NODE_ENV"
}

write_secret_value() {
  local value="$1" tmp
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 验证并写入 Secret Key 到 ${SECRET_FILE}"
    return 0
  fi
  validate_secret_key "$value"
  validate_managed_parent_path "$SECRET_FILE" || return
  [ -d "$(dirname "$SECRET_FILE")" ] || {
    echo "Secret Key 父目录不存在：$(dirname "$SECRET_FILE")" >&2
    return 1
  }
  tmp="$(mktemp "$(dirname "$SECRET_FILE")/.secret.key.XXXXXX")"
  printf '%s' "$value" >"$tmp"
  if ! secure_config_file "$tmp" || ! mv -f "$tmp" "$SECRET_FILE"; then
    rm -f "$tmp"
    return 1
  fi
  enable_secret_key_file
}

write_secret_to_env() {
  local value="$1"
  [ -n "$value" ] || return 0
  write_secret_value "$value"
  echo "已将 Secret Key 安全写入 ${SECRET_FILE}"
}

write_secret_from_source() {
  local src="$1" tmp
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 验证并写入 ${SECRET_FILE} <- ${src}"
    return 0
  fi
  validate_managed_parent_path "$SECRET_FILE" || return
  [ -d "$(dirname "$SECRET_FILE")" ] || {
    echo "Secret Key 父目录不存在：$(dirname "$SECRET_FILE")" >&2
    return 1
  }
  tmp="$(mktemp "$(dirname "$SECRET_FILE")/.secret.input.XXXXXX")"
  read_secret_source_canonical "$src" "$tmp" || { rm -f "$tmp"; return 1; }
  if ! secure_config_file "$tmp" || ! mv -f "$tmp" "$SECRET_FILE"; then
    rm -f "$tmp"
    return 1
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

migrate_inline_secret_to_file() {
  local value
  [ -f "$NODE_ENV" ] || return 0
  value="$(read_env_value SECRET_KEY "$NODE_ENV")"
  [ -n "$value" ] || return 0
  if [ "$DRY_RUN" -eq 1 ]; then
    echo "[dry-run] 将 node.env 内联 SECRET_KEY 迁移到 ${SECRET_FILE}"
    return 0
  fi
  write_secret_value "$value"
  echo "已将 node.env 内联 SECRET_KEY 迁移到受限文件 ${SECRET_FILE}"
}

ensure_internal_socket_in_env() {
  if [ ! -f "$NODE_ENV" ] || [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if grep -q '^INTERNAL_SOCKET_PATH=.' "$NODE_ENV" 2>/dev/null; then
    return 0
  fi
  set_env_value INTERNAL_SOCKET_PATH /run/remnanode/internal.sock
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
  rm -f /run/remnawave-node-supervise.pid 2>/dev/null || true
  rm -f /run/remnawave-internal-*.sock 2>/dev/null || true
}

print_pre_install_panel_hint() {
  echo
  echo "━━━━━━━━ Panel 接入提示 ━━━━━━━━"
  echo "  推荐顺序："
  echo "    1) Panel 创建节点，复制 Secret Key"
  echo "    2) 完成本脚本安装并粘贴 Secret Key"
  echo "    3) 看到目标 remnanode-lite 进程持有 TCP 监听后，在 Panel 启用节点"
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

running_pids_for_binary() {
  local binary="$1" proc_root="${RNL_PROC_ROOT:-/proc}" exe pid target
  for exe in "$proc_root"/[0-9]*/exe; do
    [ -e "$exe" ] || [ -L "$exe" ] || continue
    pid="${exe%/exe}"
    pid="${pid##*/}"
    if [ -e "$binary" ] && [ "$exe" -ef "$binary" ]; then
      printf '%s\n' "$pid"
      continue
    fi
    target="$(readlink "$exe" 2>/dev/null || true)"
    if [ "$target" = "$binary" ] || [ "$target" = "$binary (deleted)" ]; then
      printf '%s\n' "$pid"
    fi
  done
}

require_binary_not_running() {
  local binary="$1" pids
  pids="$(running_pids_for_binary "$binary")"
  if [ -n "$pids" ]; then
    echo "拒绝替换运行中的 ${binary}: ${pids//$'\n'/,}" >&2
    return 1
  fi
}

canonical_binary_path() {
  local path="$1" resolved
  resolved="$(readlink -f "$path" 2>/dev/null || true)"
  if [ -n "$resolved" ]; then
    printf '%s' "$resolved"
  else
    printf '%s' "$path"
  fi
}

running_current_pids_for_binary() {
  local binary="$1" proc_root="${RNL_PROC_ROOT:-/proc}" exe pid
  [ -e "$binary" ] || return 0
  for exe in "$proc_root"/[0-9]*/exe; do
    [ -e "$exe" ] || continue
    if [ "$exe" -ef "$binary" ]; then
      pid="${exe%/exe}"
      printf '%s\n' "${pid##*/}"
    fi
  done
}

service_manager_active() {
  local platform="${1:-}" state
  if [ -z "$platform" ]; then
    platform="$(remnanode_service_platform)"
  fi
  state="$(probe_remnanode_service_state "$platform")" || return 2
  case "$state" in
    active) return 0 ;;
    inactive) return 1 ;;
    *) return 2 ;;
  esac
}

remnanode_service_platform() {
  if [ -f /etc/alpine-release ]; then
    printf 'openrc'
  else
    printf 'systemd'
  fi
}

probe_remnanode_service_state() {
  local platform="$1" output="" load_state="" active_state="" status
  case "$platform" in
    openrc)
      if installer_run_without_lock \
        rc-service remnawave-node status >/dev/null 2>&1; then
        printf 'active'
        return 0
      else
        status=$?
      fi
      if [ "$status" -eq 3 ]; then
        printf 'inactive'
      else
        printf 'error'
      fi
      ;;
    systemd)
      if ! output="$(installer_run_without_lock systemctl show --no-pager \
        --property=LoadState --property=ActiveState \
        remnawave-node.service)"; then
        printf 'error'
        return 0
      fi
      while IFS='=' read -r property value; do
        case "$property" in
          LoadState) load_state="$value" ;;
          ActiveState) active_state="$value" ;;
        esac
      done <<<"$output"
      case "$load_state" in
        loaded|masked|not-found) ;;
        *) printf 'error'; return 0 ;;
      esac
      case "$active_state" in
        active|reloading) printf 'active' ;;
        inactive|failed) printf 'inactive' ;;
        *) printf 'error' ;;
      esac
      ;;
    *)
      echo "未知服务管理器：${platform}" >&2
      return 2
      ;;
  esac
}

stop_remnanode_and_wait() {
  local node_binary="$1" xray_binary="$2" max_wait="${3:-35}"
  local platform="${4:-}" i=0 stop_failed=0 state
  if [ -z "$platform" ]; then
    platform="$(remnanode_service_platform)" || return
  fi
  state="$(probe_remnanode_service_state "$platform")" || return
  case "$state" in
    active)
      case "$platform" in
        openrc)
          rc-service remnawave-node stop >/dev/null 2>&1 || stop_failed=1
          ;;
        systemd)
          systemctl stop remnawave-node.service >/dev/null 2>&1 || stop_failed=1
          ;;
        *) return 2 ;;
      esac
      ;;
    inactive) ;;
    *)
      echo "无法可靠探测 remnawave-node 状态，拒绝停止后续操作" >&2
      return 1
      ;;
  esac
  while [ "$i" -lt "$max_wait" ]; do
    state="$(probe_remnanode_service_state "$platform")" || return
    case "$state" in
      inactive) break ;;
      active) ;;
      *)
        echo "停止期间无法可靠探测 remnawave-node 状态" >&2
        return 1
        ;;
    esac
    sleep 1
    i=$((i + 1))
  done
  state="$(probe_remnanode_service_state "$platform")" || return
  case "$state" in
    inactive) ;;
    active)
      echo "服务管理器仍报告 remnawave-node 运行中" >&2
      return 1
      ;;
    *)
      echo "停止后无法可靠确认 remnawave-node 状态" >&2
      return 1
      ;;
  esac
  if ! wait_for_owned_processes_stopped "$max_wait" "$node_binary" "$xray_binary"; then
    return 1
  fi
  if [ "$stop_failed" -ne 0 ]; then
    echo "服务停止命令失败；即使当前未发现进程，也拒绝执行破坏性后续操作" >&2
    return 1
  fi
}

single_service_pid() {
  local binary="${1:-/usr/local/bin/remnanode-lite}" pids count
  pids="$(running_current_pids_for_binary "$binary")"
  count="$(printf '%s\n' "$pids" | awk 'NF { count++ } END { print count + 0 }')"
  [ "$count" -eq 1 ] || return 1
  printf '%s' "$pids"
}

listener_owned_by_pid() {
  local port="$1" pid="$2"
  if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    return 2
  fi
  ss -H -ltnp 2>/dev/null | awk -v port="$port" -v pid="$pid" '
    $4 ~ (":" port "$") && index($0, "pid=" pid ",") { found = 1 }
    END { exit(found ? 0 : 1) }
  '
}

wait_for_service_stable() {
  local port="$1" max_wait="${2:-30}"
  local binary="${3:-/usr/local/bin/remnanode-lite}" platform="${4:-}"
  local i=0 pid="" state

  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
    echo "无效服务监听端口：${port}" >&2
    return 2
  fi
  if [ -z "$platform" ]; then
    platform="$(remnanode_service_platform)" || return
  fi

  while [ "$i" -lt "$max_wait" ]; do
    state="$(probe_remnanode_service_state "$platform")" || return
    if [ "$state" = error ]; then
      echo "服务启动验证期间无法可靠探测 remnawave-node 状态" >&2
      return 1
    fi
    pid="$(single_service_pid "$binary" || true)"
    if [ "$state" = active ] && [ -n "$pid" ] \
      && listener_owned_by_pid "$port" "$pid"; then
      return 0
    fi
    sleep 1
    i=$((i + 1))
  done
  return 1
}

verify_service_listening() {
  local port="$1" binary="${2:-/usr/local/bin/remnanode-lite}" pid
  if [ "$DRY_RUN" -eq 1 ]; then
    return 0
  fi
  if ! wait_for_service_stable "$port" 30 "$binary"; then
    echo "错误: 目标进程 ${binary} 未在 30s 内以唯一服务实例持有 TCP :${port}" >&2
    return 1
  fi
  pid="$(single_service_pid "$binary")"
  echo "OK: ${binary} (pid=${pid}) 正在监听 TCP :${port}"
  ss -H -ltnp 2>/dev/null | grep -E ":${port}[[:space:]]" | grep -F "pid=${pid}," | head -n1 || true
}

wait_for_owned_processes_stopped() {
  local max_wait="$1"
  shift
  local i=0 binary pids running
  while [ "$i" -lt "$max_wait" ]; do
    running=0
    for binary in "$@"; do
      [ -n "$binary" ] || continue
      pids="$(running_pids_for_binary "$binary")"
      if [ -n "$pids" ]; then
        running=1
        break
      fi
    done
    [ "$running" -eq 0 ] && return 0
    sleep 1
    i=$((i + 1))
  done
  for binary in "$@"; do
    [ -n "$binary" ] || continue
    pids="$(running_pids_for_binary "$binary")"
    [ -z "$pids" ] || echo "仍有进程使用 ${binary}: ${pids//$'\n'/,}" >&2
  done
  return 1
}

stop_for_fresh_reinstall() {
  local platform="$1" node_binary="$2" xray_binary="$3" port="$4"
  local original_state current_state

  original_state="$(probe_remnanode_service_state "$platform")" || return
  case "$original_state" in
    active)
      if ! [[ "$port" =~ ^[0-9]+$ ]] || [ "$port" -lt 1 ] || [ "$port" -gt 65535 ]; then
        echo "无法验证旧服务监听端口，拒绝停止：${port}" >&2
        return 1
      fi
      ;;
    inactive) ;;
    error)
      echo "无法可靠探测旧 remnawave-node 状态，拒绝全新安装" >&2
      return 1
      ;;
    *)
      echo "服务状态探测返回无效状态：${original_state}" >&2
      return 1
      ;;
  esac

  if stop_remnanode_and_wait "$node_binary" "$xray_binary" 35 "$platform"; then
    return 0
  fi
  echo "未确认旧服务与 rw-core 正常停止，拒绝清除现有安装" >&2

  [ "$original_state" = active ] || return 1
  current_state="$(probe_remnanode_service_state "$platform")" || return 1
  if [ "$current_state" != inactive ]; then
    echo "旧服务管理器未确认 inactive，不尝试补偿启动" >&2
    return 1
  fi
  if ! wait_for_owned_processes_stopped 1 "$node_binary" "$xray_binary"; then
    echo "旧 Node/rw-core 仍在运行，不尝试补偿启动" >&2
    return 1
  fi

  case "$platform" in
    openrc) installer_run_without_lock rc-service remnawave-node start >/dev/null 2>&1 ;;
    systemd) systemctl start remnawave-node.service >/dev/null 2>&1 ;;
    *) return 1 ;;
  esac || {
    echo "旧 remnawave-node 补偿启动失败" >&2
    return 1
  }
  if ! wait_for_service_stable "$port" 30 "$node_binary" "$platform"; then
    echo "旧 remnawave-node 补偿启动后未通过端口验证" >&2
    return 1
  fi
  echo "旧 remnawave-node 已恢复；本次全新安装仍中止" >&2
  return 1
}

verify_installed_version_tag() {
  local binary="$1" tag="$2" output
  output="$(installer_run_without_lock "$binary" version 2>/dev/null)" || return 1
  if ! release_binary_version_matches_tag "$output" "$tag"; then
    echo "运行目标版本不匹配：got=${output} want=${tag}" >&2
    return 1
  fi
}

print_env_config_hint() {
  local restart_cmd="$1"
  echo
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo " 配置节点（编辑 node.env，变量名同官方 environment）"
  echo "━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━━"
  echo
  echo "编辑 ${NODE_ENV}，确认监听端口；Secret Key 请写入受限文件："
  echo "  NODE_PORT=2222          # 与 Panel 添加节点时的端口一致"
  echo "  SECRET_KEY_FILE=${SECRET_FILE}"
  if [ -f /etc/alpine-release ]; then
    printf '%s\n' "  printf '%s' 'eyJ...' > ${SECRET_FILE}"
    echo "  chown root:$(service_group_name) ${SECRET_FILE} && chmod 0640 ${SECRET_FILE}"
  else
    printf '%s\n' "  printf '%s' 'eyJ...' | sudo tee ${SECRET_FILE} >/dev/null"
    echo "  sudo chown root:$(service_group_name) ${SECRET_FILE} && sudo chmod 0640 ${SECRET_FILE}"
  fi
  echo
  echo "完成后执行：${restart_cmd}"
  echo
  echo "也可安装时传入："
  echo "  SECRET_KEY='eyJ...' NODE_PORT=8443 bash install-*.sh --install --yes"
}

read_env_value() {
  local key="$1" file="$2"
  local line val
  [[ "$key" =~ ^[A-Z][A-Z0-9_]*$ ]] || return 2
  [ -f "$file" ] || return 0
  line="$(grep -E "^[[:space:]]*(export[[:space:]]+)?${key}[[:space:]]*=" "$file" 2>/dev/null | tail -n 1 || true)"
  [ -n "$line" ] || return 0
  val="${line#*=}"
  val="$(printf '%s' "$val" | sed -e 's/^[[:space:]]*//' -e 's/[[:space:]]*$//')"
  case "$val" in
    \"*\") val=${val#\"}; val=${val%\"} ;;
    \'*\') val=${val#\'}; val=${val%\'} ;;
  esac
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
    local src="$1" dest_name="$2" size size_after staged_size target staged
    [ -n "$src" ] || return 0
    [ -f "$src" ] || { echo "警告：找不到 ${src}（跳过 ${dest_name}）" >&2; return 0; }
    require_file_size_at_most "$src" "$RNL_GEO_EXTRA_MAX_BYTES" "$dest_name 源文件" || return
    size="$(file_size_bytes "$src")"
    if [ "$size" -eq 0 ]; then
      echo "拒绝安装空的 ${dest_name} 源文件：${src}" >&2
      return 1
    fi
    if [ "$DRY_RUN" -eq 1 ]; then
      echo "[dry-run] 有界原子安装 ${src} -> ${geo_dir}/${dest_name}"
      return 0
    fi

    require_free_bytes "$(existing_parent "$geo_dir")" $((RNL_GEO_EXTRA_MAX_BYTES + 1048576)) \
      "$dest_name 目标 staging" || return
    install -d -o root -g root -m 0755 "$geo_dir"
    target="${geo_dir}/${dest_name}"
    staged="${target}.new.$$"
    rm -f "$staged"
    if ! head -c $((RNL_GEO_EXTRA_MAX_BYTES + 1)) <"$src" >"$staged"; then
      rm -f "$staged"
      return 1
    fi
    size_after="$(file_size_bytes "$src")"
    staged_size="$(file_size_bytes "$staged")"
    if [ "$size_after" != "$size" ] || [ "$staged_size" != "$size" ] \
      || ! require_file_size_at_most "$staged" "$RNL_GEO_EXTRA_MAX_BYTES" "$dest_name staging"; then
      rm -f "$staged"
      echo "${dest_name} 源文件在复制期间发生变化或超过硬上限" >&2
      return 1
    fi
    chown root:root "$staged"
    chmod 0644 "$staged"
    if ! mv -f "$staged" "$target"; then
      rm -f "$staged"
      return 1
    fi
    echo "已安装 ${dest_name} -> ${target}"
    copied=1
  }

  install_one_geo_extra "$geo_zapret" "geo-zapret.dat" || return
  install_one_geo_extra "$ip_zapret" "ip-zapret.dat" || return

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
SECRET_KEY_FILE=${SECRET_FILE}

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
