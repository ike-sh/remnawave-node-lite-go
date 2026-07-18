#!/usr/bin/env bash
set -Eeuo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
# shellcheck source=scripts/install-env-helpers.sh
source "$ROOT_DIR/scripts/install-env-helpers.sh"

TMP_ROOT="$(mktemp -d)"
TMP_ROOT="$(cd "$TMP_ROOT" && pwd -P)"
trap 'rm -rf "$TMP_ROOT"' EXIT

NODE_ENV="$TMP_ROOT/node.env"
SECRET_FILE="$TMP_ROOT/secret.key"
DATA_DIR="$TMP_ROOT/data"
LOG_DIR="$TMP_ROOT/log"
RNL_TMP_ROOT="$TMP_ROOT/installer"
DRY_RUN=0
PREFIX="$TMP_ROOT/bin"
BIN_NAME="remnanode-lite"
mkdir -p "$PREFIX"
(cd "$ROOT_DIR" && go build -o "$PREFIX/$BIN_NAME" ./cmd/remnanode-lite)

# shellcheck disable=SC2016,SC2030,SC2031
installer_lock_test_protocol() (
  local lock_dir="$TMP_ROOT/installer-lock-protocol"
  local lock_path="$lock_dir/remnanode-installer.lock"
  local other_path="$lock_dir/other.lock" parent_fd parent_id other_fd closed_fd
  mkdir -m 0700 "$lock_dir"

  installer_lock_path() { printf '%s' "$lock_path"; }
  installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
  installer_lock_has_root_owner() { :; }
  # macOS fdesc reports a synthetic device for /dev/fd/N. The inode still
  # provides a stable identity for these portable protocol mocks.
  if [ "$(uname -s)" = Darwin ]; then
    installer_lock_device_inode() { stat -f '%i' "$1"; }
  fi
  flock() { :; }

  installer_acquire_lock
  parent_fd="$INSTALLER_LOCK_FD"
  parent_id="$INSTALLER_LOCK_ID"
  [[ "$parent_fd" =~ ^[0-9]+$ ]]
  [ -n "$parent_id" ]
  [ "$((8#$(installer_lock_mode "$lock_path")))" -eq "$((8#0600))" ]
  [ "$(installer_lock_link_count "$lock_path")" -eq 1 ]

  installer_run_without_lock bash -c '
    fd="$1"
    [ -z "${RNL_INSTALLER_LOCK_FD:-}" ]
    [ -z "${RNL_INSTALLER_LOCK_ID:-}" ]
    [ ! -e "/proc/self/fd/$fd" ] && [ ! -e "/dev/fd/$fd" ]
  ' bash "$parent_fd"
  installer_validate_lock_fd "$parent_fd" "$lock_path" "$parent_id"
  installer_run_nested bash -c '
    fd="${RNL_INSTALLER_LOCK_FD:?}"
    [ -n "${RNL_INSTALLER_LOCK_ID:?}" ]
    [ -e "/proc/self/fd/$fd" ] || [ -e "/dev/fd/$fd" ]
  '

  (
    export RNL_INSTALLER_LOCK_FD="$parent_fd" RNL_INSTALLER_LOCK_ID="$parent_id"
    INSTALLER_LOCK_FD=""
    INSTALLER_LOCK_ID=""
    installer_acquire_lock
    [ "$INSTALLER_LOCK_FD" = "$parent_fd" ]
    [ "$INSTALLER_LOCK_ID" = "$parent_id" ]
  )

  printf other >"$other_path"
  chmod 0600 "$other_path"
  exec {other_fd}<>"$other_path"
  if (
    export RNL_INSTALLER_LOCK_FD="$other_fd" RNL_INSTALLER_LOCK_ID="$parent_id"
    INSTALLER_LOCK_FD=""
    INSTALLER_LOCK_ID=""
    installer_acquire_lock >/dev/null 2>&1
  ); then
    echo "installer accepted an inherited descriptor for another inode" >&2
    exit 1
  fi
  exec {other_fd}>&-

  exec {closed_fd}<>"$lock_path"
  local closed_number="$closed_fd"
  exec {closed_fd}>&-
  if (
    export RNL_INSTALLER_LOCK_FD="$closed_number" RNL_INSTALLER_LOCK_ID="$parent_id"
    INSTALLER_LOCK_FD=""
    INSTALLER_LOCK_ID=""
    installer_acquire_lock >/dev/null 2>&1
  ); then
    echo "installer accepted a closed inherited descriptor" >&2
    exit 1
  fi
  if (
    export RNL_INSTALLER_LOCK_FD="$parent_fd"
    unset RNL_INSTALLER_LOCK_ID
    INSTALLER_LOCK_FD=""
    INSTALLER_LOCK_ID=""
    installer_acquire_lock >/dev/null 2>&1
  ); then
    echo "installer accepted incomplete inherited lock metadata" >&2
    exit 1
  fi
  for invalid_fd in invalid 9 1234567; do
    if (
      export RNL_INSTALLER_LOCK_FD="$invalid_fd" RNL_INSTALLER_LOCK_ID="$parent_id"
      INSTALLER_LOCK_FD=""
      INSTALLER_LOCK_ID=""
      installer_acquire_lock >/dev/null 2>&1
    ); then
      echo "installer accepted invalid inherited descriptor: $invalid_fd" >&2
      exit 1
    fi
  done
)
installer_lock_test_protocol

# shellcheck disable=SC2031
installer_lock_test_post_flock_stat_failure() (
  local lock_dir="$TMP_ROOT/installer-lock-post-stat"
  local lock_path="$lock_dir/remnanode-installer.lock"
  local counter="$lock_dir/stat-count" count
  mkdir -m 0700 "$lock_dir"
  : >"$counter"
  installer_lock_path() { printf '%s' "$lock_path"; }
  installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
  installer_lock_has_root_owner() { :; }
  flock() { :; }
  installer_lock_device_inode() {
    count="$(wc -l <"$counter" | tr -d '[:space:]')"
    printf 'call\n' >>"$counter"
    [ "$count" -lt 2 ] || return 71
    printf 'same-identity'
  }
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer ignored a post-flock identity stat failure" >&2
    exit 1
  fi
  [ -z "${INSTALLER_LOCK_FD:-}" ]
)
installer_lock_test_post_flock_stat_failure

# shellcheck disable=SC2329
installer_lock_test_file_safety() (
  local lock_dir="$TMP_ROOT/installer-lock-safety"
  local lock_path="$lock_dir/remnanode-installer.lock"
  local target="$lock_dir/target"
  mkdir -m 0700 "$lock_dir"
  installer_lock_path() { printf '%s' "$lock_path"; }
  installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
  installer_lock_has_root_owner() { :; }
  flock() { :; }
  if [ "$(uname -s)" = Darwin ]; then
    installer_lock_device_inode() { stat -f '%i' "$1"; }
  fi

  printf unsafe >"$target"
  chmod 0600 "$target"
  ln -s "$target" "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer lock followed a symlink" >&2
    exit 1
  fi
  rm -f "$lock_path"

  ln -s "$lock_dir/missing" "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer lock followed a dangling symlink" >&2
    exit 1
  fi
  rm -f "$lock_path"

  mkdir "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted a directory as its lock file" >&2
    exit 1
  fi
  rmdir "$lock_path"

  printf unsafe >"$lock_path"
  chmod 0600 "$lock_path"
  ln "$lock_path" "$lock_dir/alias"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted a hardlinked lock file" >&2
    exit 1
  fi
  rm -f "$lock_path" "$lock_dir/alias"

  local interrupted_stage="$lock_dir/.rnl-lock-stage.recover1"
  printf interrupted >"$interrupted_stage"
  chmod 0600 "$interrupted_stage"
  ln "$interrupted_stage" "$lock_path"
  [ "$(installer_lock_link_count "$lock_path")" -eq 2 ]
  installer_acquire_lock
  [ ! -e "$interrupted_stage" ]
  [ "$(installer_lock_link_count "$lock_path")" -eq 1 ]
  [ "$(cat "$lock_path")" = interrupted ]
  installer_close_lock_fd
  rm -f "$lock_path"

  local ambiguous_stage_one="$lock_dir/.rnl-lock-stage.ambiguous1"
  local ambiguous_stage_two="$lock_dir/.rnl-lock-stage.ambiguous2"
  printf ambiguous >"$ambiguous_stage_one"
  chmod 0600 "$ambiguous_stage_one"
  ln "$ambiguous_stage_one" "$lock_path"
  ln "$ambiguous_stage_one" "$ambiguous_stage_two"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer recovered an ambiguous staging hardlink set" >&2
    exit 1
  fi
  [ -e "$ambiguous_stage_one" ]
  [ -e "$ambiguous_stage_two" ]
  [ -e "$lock_path" ]
  rm -f "$lock_path" "$ambiguous_stage_one" "$ambiguous_stage_two"

  printf unsafe >"$lock_path"
  chmod 0644 "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted mode 0644 for its lock file" >&2
    exit 1
  fi
  chmod 0400 "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted mode 0400 for its lock file" >&2
    exit 1
  fi
  chmod 0660 "$lock_path"
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted a writable lock file" >&2
    exit 1
  fi
  chmod 0600 "$lock_path"

  printf preserved >"$lock_path"
  (
    installer_acquire_lock
    [ "$(cat "$lock_path")" = preserved ]
  )
  [ "$(cat "$lock_path")" = preserved ]

  installer_lock_has_root_owner() { return 1; }
  if installer_acquire_lock >/dev/null 2>&1; then
    echo "installer accepted a non-root-owned lock file" >&2
    exit 1
  fi
)
installer_lock_test_file_safety

# shellcheck disable=SC2329
installer_lock_test_directory_modes() (
  installer_lock_directory_mode_is_safe /run/lock 775
  installer_lock_directory_mode_is_safe /run/lock 755
  installer_lock_directory_mode_is_safe /run/lock 1777
  if installer_lock_directory_mode_is_safe /run/lock 777; then
    echo "installer accepted non-sticky other-writable /run/lock" >&2
    exit 1
  fi
)
installer_lock_test_directory_modes

# shellcheck disable=SC2030,SC2031,SC2329
if command -v flock >/dev/null 2>&1; then
  installer_lock_test_real_ofd() (
    local lock_dir="$TMP_ROOT/installer-lock-ofd"
    local lock_path="$lock_dir/remnanode-installer.lock"
    local parent_fd parent_id competing_fd
    mkdir -m 0700 "$lock_dir"
    installer_lock_path() { printf '%s' "$lock_path"; }
    installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
    installer_lock_has_root_owner() { :; }

    installer_acquire_lock
    parent_fd="$INSTALLER_LOCK_FD"
    parent_id="$INSTALLER_LOCK_ID"
    exec {competing_fd}<>"$lock_path"
    if flock -n "$competing_fd"; then
      echo "a second installer OFD acquired an already-held lock" >&2
      exit 1
    fi
    if (
      export RNL_INSTALLER_LOCK_FD="$competing_fd" RNL_INSTALLER_LOCK_ID="$parent_id"
      INSTALLER_LOCK_FD=""
      INSTALLER_LOCK_ID=""
      installer_acquire_lock >/dev/null 2>&1
    ); then
      echo "nested installer accepted a different OFD" >&2
      exit 1
    fi
    (
      export RNL_INSTALLER_LOCK_FD="$parent_fd" RNL_INSTALLER_LOCK_ID="$parent_id"
      INSTALLER_LOCK_FD=""
      INSTALLER_LOCK_ID=""
      installer_acquire_lock
    )
    if flock -n "$competing_fd"; then
      echo "nested installer release dropped the outer OFD lock" >&2
      exit 1
    fi
    exec {competing_fd}>&-
  )
  installer_lock_test_real_ofd

  installer_lock_test_outer_exit_releases() {
    local lock_dir="$TMP_ROOT/installer-lock-release"
    local lock_path="$lock_dir/remnanode-installer.lock" contender_fd
    mkdir -m 0700 "$lock_dir"
    (
      installer_lock_path() { printf '%s' "$lock_path"; }
      installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
      installer_lock_has_root_owner() { :; }
      installer_acquire_lock
    )
    exec {contender_fd}<>"$lock_path"
    flock -n "$contender_fd" || {
      echo "installer lock remained held after the outer shell exited" >&2
      return 1
    }
    exec {contender_fd}>&-
  }
  installer_lock_test_outer_exit_releases

  installer_lock_test_sigkill_releases() {
    local lock_dir="$TMP_ROOT/installer-lock-sigkill"
    local lock_path="$lock_dir/remnanode-installer.lock"
    local ready_file="$lock_dir/ready" release_fifo="$lock_dir/release"
    local holder_pid contender_fd attempt
    mkdir -m 0700 "$lock_dir"
    mkfifo "$release_fifo"
    (
      installer_lock_path() { printf '%s' "$lock_path"; }
      installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
      installer_lock_has_root_owner() { :; }
      installer_acquire_lock
      : >"$ready_file"
      read -r _ <"$release_fifo"
    ) &
    holder_pid=$!
    for attempt in {1..200}; do
      [ -e "$ready_file" ] && break
      kill -0 "$holder_pid" 2>/dev/null || break
      sleep 0.01
    done
    if [ ! -e "$ready_file" ]; then
      kill "$holder_pid" 2>/dev/null || true
      wait "$holder_pid" 2>/dev/null || true
      echo "SIGKILL lock holder did not become ready" >&2
      return 1
    fi
    kill -9 "$holder_pid"
    if wait "$holder_pid" 2>/dev/null; then
      echo "SIGKILL lock holder exited successfully" >&2
      return 1
    fi
    exec {contender_fd}<>"$lock_path"
    flock -n "$contender_fd" || {
      echo "installer lock remained held after SIGKILL" >&2
      return 1
    }
    exec {contender_fd}>&-
  }
  installer_lock_test_sigkill_releases

  installer_lock_test_sigkill_preserves_mutation_child() (
    local lock_dir="$TMP_ROOT/installer-lock-sigkill-child"
    local lock_path="$lock_dir/remnanode-installer.lock"
    local child_ready="$lock_dir/child-ready" release_child="$lock_dir/release-child"
    local child_done="$lock_dir/child-done" child_pid_file="$lock_dir/child-pid"
    local parent_resumed="$lock_dir/parent-resumed"
    local holder_pid="" contender_fd="" child_pid="" attempt acquired=0
    cleanup_sigkill_mutation_test() {
      : >"$release_child" 2>/dev/null || true
      if [ -s "$child_pid_file" ]; then
        child_pid="$(cat "$child_pid_file" 2>/dev/null || true)"
        [ -z "$child_pid" ] || kill "$child_pid" 2>/dev/null || true
      fi
      [ -z "$holder_pid" ] || kill "$holder_pid" 2>/dev/null || true
      [ -z "$holder_pid" ] || wait "$holder_pid" 2>/dev/null || true
      if [[ "$contender_fd" =~ ^[0-9]+$ ]]; then
        exec {contender_fd}>&-
      fi
    }
    trap cleanup_sigkill_mutation_test EXIT
    mkdir -m 0700 "$lock_dir"
    (
      installer_lock_path() { printf '%s' "$lock_path"; }
      installer_lock_directory_is_safe() { [ "$1" = "$lock_dir" ]; }
      installer_lock_has_root_owner() { :; }
      installer_acquire_lock
      bash -c '
        ready="$1"
        release="$2"
        done_file="$3"
        pid_file="$4"
        printf "%s" "$$" >"$pid_file"
        : >"$ready"
        while [ ! -e "$release" ]; do sleep 0.01; done
        : >"$done_file"
      ' bash "$child_ready" "$release_child" "$child_done" "$child_pid_file"
      : >"$parent_resumed"
    ) &
    holder_pid=$!
    for attempt in {1..200}; do
      [ -e "$child_ready" ] && break
      kill -0 "$holder_pid" 2>/dev/null || break
      sleep 0.01
    done
    if [ ! -e "$child_ready" ]; then
      echo "mutation child did not become ready" >&2
      return 1
    fi

    kill -9 "$holder_pid"
    if wait "$holder_pid" 2>/dev/null; then
      echo "mutation parent exited successfully after SIGKILL" >&2
      return 1
    fi
    holder_pid=""
    [ ! -e "$parent_resumed" ] || {
      echo "mutation parent resumed after it was killed" >&2
      return 1
    }
    exec {contender_fd}<>"$lock_path"
    if flock -n "$contender_fd"; then
      echo "contender acquired the lock while an orphaned mutation child was active" >&2
      return 1
    fi

    : >"$release_child"
    for attempt in {1..200}; do
      [ -e "$child_done" ] || sleep 0.01
      if flock -n "$contender_fd"; then
        acquired=1
        break
      fi
      sleep 0.01
    done
    [ "$acquired" -eq 1 ] || {
      echo "mutation child did not release the inherited installer lock" >&2
      return 1
    }
    [ -e "$child_done" ] || {
      echo "mutation child released the lock without completing" >&2
      return 1
    }
    exec {contender_fd}>&-
    contender_fd=""
    trap - EXIT
  )
  installer_lock_test_sigkill_preserves_mutation_child
fi

installer_lock_test_entrypoint_failure() {
  local installer_script="$1" function_name="$2"
  local mutation_marker="$TMP_ROOT/${installer_script}.mutation"
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n "/^${function_name}() {$/,/^}$/p" \
      "$ROOT_DIR/scripts/$installer_script")
    DRY_RUN=0
    ACTION=install
    ENSURE_SERVICE_STARTED=0
    ENSURE_SERVICE_ENABLED=0
    require_root() { :; }
    require_alpine() { :; }
    installer_acquire_lock() { return 75; }
    dispatch_action() { : >"$mutation_marker"; }
    require_command() { : >"$mutation_marker"; }
    installed() { : >"$mutation_marker"; }
    if "$function_name" >/dev/null 2>&1; then
      echo "$installer_script continued after installer lock failure" >&2
      exit 1
    else
      [ "$?" -eq 75 ]
    fi
    [ ! -e "$mutation_marker" ]
  )
}
installer_lock_test_entrypoint_failure install-node.sh main
installer_lock_test_entrypoint_failure install-node-alpine.sh main
installer_lock_test_entrypoint_failure upgrade.sh main
installer_lock_test_entrypoint_failure uninstall.sh main
installer_lock_test_entrypoint_failure install-xray.sh acquire_xray_installer_lock

lock_mock_path="$TMP_ROOT/lock-mock-path"
lock_mock_called="$TMP_ROOT/flock-called"
mkdir "$lock_mock_path"
# shellcheck disable=SC2016
printf '%s\n' '#!/bin/sh' ': >"${RNL_FLOCK_CALLED:?}"' 'exit 99' \
  >"$lock_mock_path/flock"
chmod 0755 "$lock_mock_path/flock"
for command_spec in \
  'install-node.sh --upgrade --yes --dry-run' \
  'install-node-alpine.sh --upgrade --yes --dry-run' \
  'upgrade.sh --yes --dry-run' \
  'install-xray.sh --dry-run' \
  'uninstall.sh --dry-run' \
  'install-node.sh --help' \
  'install-node-alpine.sh --help' \
  'upgrade.sh --help' \
  'install-xray.sh --help' \
  'uninstall.sh --help' \
  'install-node.sh --version' \
  'install-node-alpine.sh --version' \
  'upgrade.sh --version' \
  'uninstall.sh --version'; do
  read -r -a command_parts <<<"$command_spec"
  PATH="$lock_mock_path:$PATH" RNL_FLOCK_CALLED="$lock_mock_called" \
    bash "$ROOT_DIR/scripts/${command_parts[0]}" "${command_parts[@]:1}" \
    >/dev/null
done
[ ! -e "$lock_mock_called" ] || {
  echo "a non-changing installer path invoked flock" >&2
  exit 1
}

for installer_script in \
  install-node.sh install-node-alpine.sh upgrade.sh install-xray.sh uninstall.sh; do
  grep -Fq 'installer_acquire_lock' "$ROOT_DIR/scripts/$installer_script" || {
    echo "$installer_script does not acquire the shared installer lock" >&2
    exit 1
  }
done
grep -Fxq '  printf '\''%s'\'' /run/lock/remnanode-installer.lock' \
  "$ROOT_DIR/scripts/install-env-helpers.sh"
if grep -ERn 'rm[^#]*remnanode-installer\.lock' "$ROOT_DIR/scripts" >/dev/null; then
  echo "installer scripts must never unlink the shared lock file" >&2
  exit 1
fi
grep -Fq 'apk add --no-cache curl bash util-linux' "$ROOT_DIR/README.md"
grep -Fq 'util-linux' "$ROOT_DIR/scripts/install-node-alpine.sh"
grep -Fq 'util-linux' "$ROOT_DIR/scripts/install-node.sh"
if grep -Fq 'bootstrap_installer_lock_dependency' \
  "$ROOT_DIR/scripts/install-node-alpine.sh"; then
  echo "Alpine installer mutates packages before acquiring the shared lock" >&2
  exit 1
fi
alpine_package_log="$TMP_ROOT/alpine-package-argv"
(
  # shellcheck disable=SC1090
  source <(sed -n '/^install_packages() {$/,/^}/p' \
    "$ROOT_DIR/scripts/install-node-alpine.sh")
  DRY_RUN=0
  # Called indirectly by the extracted install_packages function.
  # shellcheck disable=SC2329
  step() { :; }
  # Called indirectly by the extracted install_packages function.
  # shellcheck disable=SC2329
  require_free_bytes() { :; }
  # Called indirectly by the extracted install_packages function.
  # shellcheck disable=SC2329
  apk() { printf 'apk %s\n' "$*" >"$alpine_package_log"; }
  install_packages
)
grep -Fxq \
  'apk add --no-cache bash curl tar unzip ca-certificates libcap openrc iproute2 nftables util-linux' \
  "$alpine_package_log"
for installer_script in install-node.sh install-node-alpine.sh; do
  grep -Fq 'installer_run_nested bash' "$ROOT_DIR/scripts/$installer_script"
done
# shellcheck disable=SC2016
grep -Fq 'installer_run_nested bash "$support"' "$ROOT_DIR/scripts/upgrade.sh"
for validator_function in \
  validate_secret_key read_secret_source_canonical validate_secret_file; do
  validator_body="$(sed -n "/^${validator_function}() {$/,/^}/p" \
    "$ROOT_DIR/scripts/install-env-helpers.sh")"
  grep -Fq 'installer_run_without_lock' <<<"$validator_body"
done
for mutation_command in apt-get apk rm; do
  if grep -ERn \
    "installer_run_without_lock[[:space:]]+${mutation_command}([[:space:]]|$)" \
    "$ROOT_DIR/scripts" >/dev/null; then
    echo "synchronous mutation command closes the shared installer lock: ${mutation_command}" >&2
    exit 1
  fi
done
if grep -ERn \
  'installer_run_without_lock[[:space:]]+rc-update[[:space:]]+(add|del)([[:space:]]|$)' \
  "$ROOT_DIR/scripts" >/dev/null; then
  echo "synchronous rc-update mutation closes the shared installer lock" >&2
  exit 1
fi
for standalone in install-xray.sh uninstall.sh; do
  bash -s -- --help <"$ROOT_DIR/scripts/$standalone" >/dev/null
done

loader_order_fixture="$TMP_ROOT/loader-order"
mkdir "$loader_order_fixture"
sed '/^main "\$@"$/d' "$ROOT_DIR/scripts/uninstall.sh" \
  >"$loader_order_fixture/uninstall.sh"
cp "$ROOT_DIR/scripts/install-env-helpers.sh" \
  "$loader_order_fixture/install-env-helpers.sh"
bash -c '
  set -Eeuo pipefail
  script="$1"
  source "$script" --dry-run
  cleanup_body="$(declare -f cleanup_runtime)"
  manager_body="$(declare -f service_manager_active)"
  grep -Fq "node.env.bak." <<<"$cleanup_body"
  grep -Fq "uninstall_service_manager_state" <<<"$manager_body"
  ! grep -Fq "remnanode_service_platform" <<<"$manager_body"
' bash "$loader_order_fixture/uninstall.sh"
read -r xray_load_line xray_collision_line < <(
  awk '
    /^  load_installer_helpers$/ && !load { load = NR }
    /^file_size_bytes\(\)/ && !collision { collision = NR }
    END { print load + 0, collision + 0 }
  ' "$ROOT_DIR/scripts/install-xray.sh"
)
if [ "$xray_load_line" -eq 0 ] || [ "$xray_collision_line" -eq 0 ] \
  || [ "$xray_load_line" -ge "$xray_collision_line" ]; then
  echo "install-xray loads shared helpers after defining colliding functions" >&2
  exit 1
fi

if RNL_TMP_ROOT=/ ensure_installer_temp_root >/dev/null 2>&1; then
  echo "installer temp root accepted /" >&2
  exit 1
fi

unmarked_root="$TMP_ROOT/unmarked-installer"
mkdir -p "$unmarked_root"
printf foreign >"$unmarked_root/foreign-file"
if RNL_TMP_ROOT="$unmarked_root" ensure_installer_temp_root >/dev/null 2>&1; then
  echo "non-empty installer temp root without marker was accepted" >&2
  exit 1
fi

if [ "$(id -u)" -ne 0 ]; then
  attacker_owned_empty="$TMP_ROOT/attacker-owned-empty-installer"
  mkdir -p "$attacker_owned_empty"
  if RNL_TMP_ROOT="$attacker_owned_empty" ensure_installer_temp_root >/dev/null 2>&1; then
    echo "attacker-owned empty installer temp root was claimed" >&2
    exit 1
  fi
  [ ! -e "$attacker_owned_empty/.remnanode-installer-root" ]
fi

symlink_parent="$TMP_ROOT/symlink-parent"
mkdir -p "$symlink_parent/real"
ln -s real "$symlink_parent/link"
if RNL_TMP_ROOT="$symlink_parent/link/installer" \
  ensure_installer_temp_root >/dev/null 2>&1; then
  echo "installer temp root accepted a symlink ancestor" >&2
  exit 1
fi

legal_installer_root="$TMP_ROOT/legal-installer"
(
  installer_path_has_root_owner() { :; }
  installer_ancestor_is_safe() { :; }
  # shellcheck disable=SC2030
  RNL_TMP_ROOT="$legal_installer_root"
  ensure_installer_temp_root
  grep -Fxq 'remnanode-installer-root-v1' \
    "$legal_installer_root/.remnanode-installer-root"
  mkdir "$legal_installer_root/existing-transaction"
  ensure_installer_temp_root
)

unsafe_mode_root="$TMP_ROOT/unsafe-mode-installer"
mkdir -m 0777 "$unsafe_mode_root"
if (
  installer_path_has_root_owner() { :; }
  RNL_TMP_ROOT="$unsafe_mode_root"
  ensure_installer_temp_root >/dev/null 2>&1
); then
  echo "group/other-writable installer temp root was accepted" >&2
  exit 1
fi
if (
  installer_path_has_root_owner() { :; }
  RNL_TMP_ROOT="$unsafe_mode_root"
  make_installer_temp_dir unsafe >/dev/null 2>&1
); then
  echo "temp directory creation ignored unsafe installer root validation" >&2
  exit 1
fi
if find "$unsafe_mode_root" -mindepth 1 -maxdepth 1 -name 'unsafe.*' -print | grep -q .; then
  echo "temp directory was created under an unsafe installer root" >&2
  exit 1
fi

hardlinked_marker_root="$TMP_ROOT/hardlinked-marker-installer"
mkdir -m 0700 "$hardlinked_marker_root"
printf 'remnanode-installer-root-v1\n' >"$hardlinked_marker_root/.remnanode-installer-root"
ln "$hardlinked_marker_root/.remnanode-installer-root" "$hardlinked_marker_root/marker-alias"
if (
  installer_path_has_root_owner() { :; }
  # shellcheck disable=SC2030
  RNL_TMP_ROOT="$hardlinked_marker_root"
  ensure_installer_temp_root >/dev/null 2>&1
); then
  echo "hardlinked installer marker was accepted" >&2
  exit 1
fi

if grep -Eq 'chown[[:space:]]+-R[[:space:]]+root:root.*installer_temp_root' \
  "$ROOT_DIR/scripts/install-env-helpers.sh"; then
  echo "installer temp root still uses recursive chown" >&2
  exit 1
fi

managed_root="$TMP_ROOT/managed-paths"
mkdir -p "$managed_root/real-dir" "$managed_root/unsafe-parent"
printf config >"$managed_root/real-config"
ln -s real-config "$managed_root/node.env"
ln -s real-config "$managed_root/secret.key"
ln "$managed_root/real-config" "$managed_root/hardlinked-config"
ln -s real-dir "$managed_root/data-link"
chmod 0770 "$managed_root/unsafe-parent"
(
  managed_ancestor_is_safe() { :; }
  installer_path_owner_ids() { printf '0:0'; }
  if validate_managed_regular_file "$managed_root/node.env" >/dev/null 2>&1; then
    echo "node.env symlink was accepted" >&2
    exit 1
  fi
  if validate_managed_regular_file "$managed_root/secret.key" >/dev/null 2>&1; then
    echo "secret symlink was accepted" >&2
    exit 1
  fi
  if validate_managed_regular_file "$managed_root/hardlinked-config" >/dev/null 2>&1; then
    echo "hardlinked managed config was accepted" >&2
    exit 1
  fi
  if validate_existing_owned_directory "$managed_root/data-link" 0 0 >/dev/null 2>&1; then
    echo "managed directory symlink was accepted" >&2
    exit 1
  fi
)
(
  installer_path_owner_ids() { printf '0:0'; }
  if validate_managed_parent_path "$managed_root/unsafe-parent/child" >/dev/null 2>&1; then
    echo "group-writable managed ancestor was accepted" >&2
    exit 1
  fi
)
(
  managed_ancestor_is_safe() { :; }
  managed_path_has_owner() { :; }
  chmod 0750 "$managed_root/real-dir"
  validate_existing_owned_directory "$managed_root/real-dir" 123 456
)

ln -s ../../outside "$managed_root/support-current-bad"
ln -s support/v0.1.0 "$managed_root/support-current-good"
(
  validate_managed_parent_path() { :; }
  installer_path_has_root_owner() { :; }
  if validate_release_support_link "$managed_root/support-current-bad" >/dev/null 2>&1; then
    echo "release support-current accepted an external target" >&2
    exit 1
  fi
  validate_release_support_link "$managed_root/support-current-good"
)

gid_log="$TMP_ROOT/ensure-owned-gids"
(
  id() { [ "$1" = -u ] && printf '123\n'; }
  getent() { [ "$1" = group ] && [ "$2" = target-group ] && printf 'target-group:x:456:\n'; }
  validate_existing_owned_directory() { printf '%s:%s:%s\n' "$1" "$2" "$3" >>"$gid_log"; }
  ensure_owned_directory "$managed_root/real-dir" service-user target-group 0750
)
grep -Fq ':123:456' "$gid_log"

passwd_fixture="$TMP_ROOT/passwd.fixture"
group_fixture="$TMP_ROOT/group.fixture"
printf 'remnanode:x:123:456::/var/lib/remnanode:/sbin/nologin\n' >"$passwd_fixture"
printf 'remnanode:x:456:\n' >"$group_fixture"
validate_service_group_exclusive remnanode remnanode 456 "$passwd_fixture" "$group_fixture"
printf 'remnanode:x:456:remnanode,alice\n' >"$group_fixture"
if validate_service_group_exclusive remnanode remnanode 456 \
  "$passwd_fixture" "$group_fixture" >/dev/null 2>&1; then
  echo "service group with an explicit foreign member was accepted" >&2
  exit 1
fi
printf 'remnanode:x:456:\n' >"$group_fixture"
printf '%s\n' \
  'remnanode:x:123:456::/var/lib/remnanode:/sbin/nologin' \
  'alice:x:124:456::/home/alice:/bin/sh' >"$passwd_fixture"
if validate_service_group_exclusive remnanode remnanode 456 \
  "$passwd_fixture" "$group_fixture" >/dev/null 2>&1; then
  echo "service group shared as another primary group was accepted" >&2
  exit 1
fi

test_partial_install_detection() {
  local installer_script="$1" service_variable="$2"
  # These variables are consumed by the dynamically sourced function.
  # shellcheck disable=SC2034
  local PREFIX="$TMP_ROOT/partial-${installer_script}/bin" BIN_NAME=remnanode-lite
  # shellcheck disable=SC2034
  local NODE_ENV="$TMP_ROOT/partial-${installer_script}/node.env"
  # shellcheck disable=SC2034
  local UNIT="$TMP_ROOT/partial-${installer_script}/service"
  # shellcheck disable=SC2034
  local OPENRC_SVC="$UNIT"
  # shellcheck disable=SC2034
  local YES=1 DRY_RUN=0 PORT_EXPLICIT=0 SECRET_FILE_ARG='' DELEGATE_TO_UPGRADE=0

  # shellcheck disable=SC1090
  source <(sed -n '/^confirm_install()/,/^}/p' "$ROOT_DIR/scripts/$installer_script")
  mkdir -p "$PREFIX" "$(dirname "$NODE_ENV")"
  : >"$PREFIX/$BIN_NAME"
  chmod 0755 "$PREFIX/$BIN_NAME"
  printf 'NODE_PORT=2222\n' >"$NODE_ENV"

  confirm_install >/dev/null
  [ "$DELEGATE_TO_UPGRADE" -eq 0 ] || {
    echo "$installer_script delegated a partial install without $service_variable" >&2
    return 1
  }

  : >"$UNIT"
  confirm_install >/dev/null
  [ "$DELEGATE_TO_UPGRADE" -eq 1 ] || {
    echo "$installer_script did not delegate a complete service layout" >&2
    return 1
  }

  DELEGATE_TO_UPGRADE=0
  rm -f "$UNIT"
  ln -s "$NODE_ENV" "$UNIT"
  confirm_install >/dev/null
  [ "$DELEGATE_TO_UPGRADE" -eq 0 ] || {
    echo "$installer_script accepted a symlink service definition" >&2
    return 1
  }
}
test_partial_install_detection install-node.sh UNIT
test_partial_install_detection install-node-alpine.sh OPENRC_SVC
for installer_script in install-node.sh install-node-alpine.sh; do
  grep -Fq 'RNL_ENSURE_SERVICE_STARTED=1' "$ROOT_DIR/scripts/$installer_script"
done

# Most tests run unprivileged and only need to assert restrictive file modes.
validate_managed_parent_path() {
  :
}
secure_config_file() {
  chmod 0600 "$1"
}

valid_json='{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}'
valid_secret="$(printf '%s' "$valid_json" | base64 | tr -d '\r\n')"

validate_secret_key "$valid_secret"
validate_secret_key "${valid_secret%%=*}"
if validate_secret_key 'abc";touch+/tmp/pwned' 2>/dev/null; then
  echo "unsafe SECRET_KEY unexpectedly passed validation" >&2
  exit 1
fi
if validate_secret_key 'e30=' 2>/dev/null; then
  echo "SECRET_KEY without required fields unexpectedly passed validation" >&2
  exit 1
fi
invalid_json='{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}trailing}'
invalid_secret="$(printf '%s' "$invalid_json" | base64 | tr -d '\r\n')"
RNL_SECRET_VALIDATOR=/bin/true
export RNL_SECRET_VALIDATOR
if validate_secret_key "$invalid_secret" 2>/dev/null; then
  echo "malformed SECRET_KEY JSON passed via an untrusted validator override" >&2
  exit 1
fi
unset RNL_SECRET_VALIDATOR
duplicate_json='{"caCertPem":"first","caCertPem":"second","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}'
duplicate_secret="$(printf '%s' "$duplicate_json" | base64 | tr -d '\r\n')"
if validate_secret_key "$duplicate_secret" 2>/dev/null; then
  echo "duplicate SECRET_KEY field unexpectedly passed validation" >&2
  exit 1
fi
wrong_type_json='{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":42}'
wrong_type_secret="$(printf '%s' "$wrong_type_json" | base64 | tr -d '\r\n')"
if validate_secret_key "$wrong_type_secret" 2>/dev/null; then
  echo "non-string SECRET_KEY field unexpectedly passed validation" >&2
  exit 1
fi

printf '%s\n' \
  'NODE_PORT=1111' \
  'XRAY_BIN=/first/rw-core' \
  'SECRET_KEY=not-selected' \
  'NODE_PORT=2222' \
  'XRAY_BIN=/last/rw-core' \
  "SECRET_KEY=${valid_secret}" >"$NODE_ENV"
[ "$(read_env_value NODE_PORT "$NODE_ENV")" = 2222 ]
[ "$(read_env_value XRAY_BIN "$NODE_ENV")" = /last/rw-core ]
[ "$(read_env_value SECRET_KEY "$NODE_ENV")" = "$valid_secret" ]
normalize_runtime_environment >/dev/null
[ "$(grep -c '^NODE_PORT=' "$NODE_ENV")" -eq 1 ]
[ "$(grep -c '^XRAY_BIN=' "$NODE_ENV")" -eq 1 ]
[ "$(grep -c '^SECRET_KEY=' "$NODE_ENV")" -eq 1 ]
[ "$(read_env_value SECRET_KEY "$NODE_ENV")" = "$valid_secret" ]

printf '%s\n' 'XRAY_BIN="paired"' 'XRAY_BIN="unterminated' >"$NODE_ENV"
[ "$(read_env_value XRAY_BIN "$NODE_ENV")" = '"unterminated' ]
printf "%s\n" "XRAY_BIN='paired'" "XRAY_BIN='unterminated" >"$NODE_ENV"
[ "$(read_env_value XRAY_BIN "$NODE_ENV")" = "'unterminated" ]
printf '%s\n' 'XRAY_BIN="paired"' >"$NODE_ENV"
[ "$(read_env_value XRAY_BIN "$NODE_ENV")" = paired ]

# shellcheck disable=SC1090
source <(sed -n '/^read_env_value()/,/^}/p' "$ROOT_DIR/scripts/uninstall.sh")
printf '%s\n' 'XRAY_BIN="paired"' 'XRAY_BIN="uninstall-unterminated' >"$NODE_ENV"
[ "$(read_env_value XRAY_BIN "$NODE_ENV")" = '"uninstall-unterminated' ]

printf 'NODE_PORT=2222\nSECRET_KEY=\n' >"$NODE_ENV"
write_secret_to_env "$valid_secret" >/dev/null
[ "$(tr -d '\r\n' <"$SECRET_FILE")" = "$valid_secret" ]
grep -Fxq 'SECRET_KEY=' "$NODE_ENV"
grep -Fxq "SECRET_KEY_FILE=${SECRET_FILE}" "$NODE_ENV"
if grep -Fq "$valid_secret" "$NODE_ENV"; then
  echo "SECRET_KEY leaked into node.env" >&2
  exit 1
fi

exact_prefix='{"caCertPem":"'
exact_suffix='","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key"}'
exact_raw_length=$((RNL_SECRET_MAX_BYTES * 3 / 4))
exact_filler_length=$((exact_raw_length - ${#exact_prefix} - ${#exact_suffix}))
exact_filler="$(head -c "$exact_filler_length" /dev/zero | tr '\0' a)"
exact_secret="$(printf '%s%s%s' "$exact_prefix" "$exact_filler" "$exact_suffix" \
  | base64 | tr -d '\r\n')"
[ "${#exact_secret}" -eq "$RNL_SECRET_MAX_BYTES" ]
write_secret_value "$exact_secret"
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]
validate_secret_file "$SECRET_FILE"

secret_source="$TMP_ROOT/secret-source"
printf '%s\n' "$exact_secret" >"$secret_source"
write_secret_from_source "$secret_source"
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]
[ "$(tail -c 1 "$SECRET_FILE")" != $'\n' ]
printf '%s\r\n' "$exact_secret" >"$secret_source"
validate_secret_file "$secret_source"
write_secret_from_source "$secret_source"
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]
printf '%s' "$exact_secret" | write_secret_from_source -
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]

if printf '%sA' "$exact_secret" | write_secret_from_source - >/dev/null 2>&1; then
  echo "stdin secret above the canonical limit was accepted" >&2
  exit 1
fi
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]
printf '%sA' "$exact_secret" >"$secret_source"
if write_secret_from_source "$secret_source" >/dev/null 2>&1; then
  echo "secret file above the canonical limit was accepted" >&2
  exit 1
fi
[ "$(file_size_bytes "$SECRET_FILE")" -eq "$RNL_SECRET_MAX_BYTES" ]

nul_secret="$TMP_ROOT/nul-secret"
nul_stderr="$TMP_ROOT/nul-secret.stderr"
printf 'abc\0def' >"$nul_secret"
if write_secret_from_source "$nul_secret" >/dev/null 2>"$nul_stderr"; then
  echo "NUL-containing secret source was accepted" >&2
  exit 1
fi
if grep -Fqi 'ignored null byte' "$nul_stderr"; then
  echo "NUL reached shell command substitution" >&2
  exit 1
fi
ln -s "$secret_source" "$TMP_ROOT/secret-source-link"
if write_secret_from_source "$TMP_ROOT/secret-source-link" >/dev/null 2>&1; then
  echo "symlink secret source was accepted" >&2
  exit 1
fi
if find "$TMP_ROOT" -maxdepth 1 \( -name '.secret.input.*' -o -name '.secret.raw.*' \
  -o -name '.secret.validate.*' \) -print | grep -q .; then
  echo "secret staging files remained after validation failure" >&2
  exit 1
fi

url_json="$(printf '{\"caCertPem\":\"\360\220\200\276\",\"jwtPublicKey\":\"jwt\",\"nodeCertPem\":\"cert\",\"nodeKeyPem\":\"key\"}')"
url_secret="$(printf '%s' "$url_json" | base64 | tr -d '\r\n' | tr '+/' '-_')"
[[ "$url_secret" == *-* || "$url_secret" == *_* ]]
validate_secret_key "$url_secret"
printf '%s\n' "$url_secret" >"$secret_source"
write_secret_from_source "$secret_source"
[ "$(cat "$SECRET_FILE")" = "$url_secret" ]
printf '%s' "$url_secret" | write_secret_from_source -
[ "$(cat "$SECRET_FILE")" = "$url_secret" ]
unknown_secret="$(printf '%s' \
  '{"caCertPem":"ca","jwtPublicKey":"jwt","nodeCertPem":"cert","nodeKeyPem":"key","future":{"version":2}}' \
  | base64 | tr -d '\r\n')"
validate_secret_key "$unknown_secret"

geo_source="$TMP_ROOT/geo-zapret-source.dat"
printf geo-data >"$geo_source"
(
  # shellcheck disable=SC2030
  GEO_DIR="$TMP_ROOT/geo-target"
  # shellcheck disable=SC2030
  GEO_ZAPRET_FILE="$geo_source"
  # shellcheck disable=SC2030
  IP_ZAPRET_FILE=""
  install() {
    local arg target=""
    for arg in "$@"; do target="$arg"; done
    mkdir -p "$target"
  }
  chown() { :; }
  install_geo_extra_files >/dev/null
  [ "$(cat "$GEO_DIR/geo-zapret.dat")" = geo-data ]
  if find "$GEO_DIR" -name '*.new.*' -print | grep -q .; then
    echo "geo staging file remained after atomic installation" >&2
    exit 1
  fi
)
oversized_geo="$TMP_ROOT/oversized-geo.dat"
dd if=/dev/zero of="$oversized_geo" bs=1 count=0 \
  seek=$((RNL_GEO_EXTRA_MAX_BYTES + 1)) 2>/dev/null
if GEO_DIR="$TMP_ROOT/geo-oversized-target" GEO_ZAPRET_FILE="$oversized_geo" \
  IP_ZAPRET_FILE="" install_geo_extra_files >/dev/null 2>&1; then
  echo "oversized local geo asset unexpectedly passed" >&2
  exit 1
fi

printf 'NODE_PORT=2222\nSECRET_KEY="%s"\nLOW_MEMORY=0\n' "$valid_secret" >"$NODE_ENV"
rm -f "$SECRET_FILE"
migrate_inline_secret_to_file >/dev/null
grep -Fxq 'SECRET_KEY=' "$NODE_ENV"
grep -Fxq "SECRET_KEY_FILE=${SECRET_FILE}" "$NODE_ENV"
[ "$(tr -d '\r\n' <"$SECRET_FILE")" = "$valid_secret" ]

set_env_value LOW_MEMORY 1
set_env_value LOW_MEMORY 1
[ "$(grep -c '^LOW_MEMORY=' "$NODE_ENV")" -eq 1 ]
grep -Fxq 'LOW_MEMORY=1' "$NODE_ENV"

# shellcheck disable=SC1090
source <(sed -n '/^migrate_runtime_configuration()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
printf 'NODE_PORT=2222\nSECRET_KEY=\nSECRET_KEY_FILE=%s\n' "$SECRET_FILE" >"$NODE_ENV"
export LOW_MEMORY=0
migrate_runtime_configuration >/dev/null
grep -Eq '^LOW_MEMORY=[01]$' "$NODE_ENV"
export LOW_MEMORY=1
migrate_runtime_configuration >/dev/null
grep -Fxq 'LOW_MEMORY=1' "$NODE_ENV"

mkdir -p "$TMP_ROOT/archive/support"
printf x >"$TMP_ROOT/archive/remnanode-lite"
tar -czf "$TMP_ROOT/release.tar.gz" -C "$TMP_ROOT/archive" .
validate_release_archive_budget "$TMP_ROOT/release.tar.gz"
many_dir="$TMP_ROOT/many"
mkdir -p "$many_dir"
for ((i = 0; i < 65; i++)); do
  : >"$many_dir/file-${i}"
done
tar -czf "$TMP_ROOT/many.tar.gz" -C "$many_dir" .
if validate_release_archive_budget "$TMP_ROOT/many.tar.gz" 2>/dev/null; then
  echo "release archive entry limit was not enforced" >&2
  exit 1
fi

dd if=/dev/zero of="$TMP_ROOT/oversized-release.tar.gz" bs=1 count=0 \
  seek=$((RNL_RELEASE_ARCHIVE_MAX_BYTES + 1)) 2>/dev/null
if validate_release_archive_budget "$TMP_ROOT/oversized-release.tar.gz" 2>/dev/null; then
  echo "release archive compressed-size limit was not enforced" >&2
  exit 1
fi

listing_consumed="$TMP_ROOT/archive-listing-consumed"
long_path="$(printf '%04000d' 0)"
(
  run_with_timeout() { shift; "$@"; }
  # shellcheck disable=SC2329
  tar() {
    local i
    printf '%s\n' '../escape' || return
    for ((i = 1; i < 64; i++)); do
      printf 'safe/%s/%02d\n' "$long_path" "$i" || return
    done
    : >"$listing_consumed"
  }
  release_archive_has_unsafe_paths ignored
)
if [ ! -e "$listing_consumed" ]; then
  echo "unsafe archive check did not consume the complete bounded listing" >&2
  exit 1
fi

curl_called="$TMP_ROOT/curl-called"
if (
  # shellcheck disable=SC2329
  curl() { : >"$curl_called"; }
  download_https_file http://example.invalid/plaintext "$TMP_ROOT/plaintext-download" 2 2>/dev/null
); then
  echo "non-HTTPS download unexpectedly passed" >&2
  exit 1
fi
[ ! -e "$curl_called" ]
if download_https_file https://example.invalid/bad-limit "$TMP_ROOT/bad-limit-download" 0 2>/dev/null; then
  echo "zero download limit unexpectedly passed" >&2
  exit 1
fi
if download_https_file https://example.invalid/huge-limit "$TMP_ROOT/huge-limit-download" \
  999999999999999999999 2>/dev/null; then
  echo "overflowing download limit unexpectedly passed" >&2
  exit 1
fi

if (
  # shellcheck disable=SC2329
  curl() {
    printf four
  }
  download_https_file https://example.invalid/oversized "$TMP_ROOT/oversized-download" 2 2>/dev/null
); then
  echo "download hard limit was not enforced" >&2
  exit 1
fi
[ ! -e "$TMP_ROOT/oversized-download" ]

(
  # shellcheck disable=SC2329
  curl() { printf ok; }
  download_https_file https://example.invalid/exact "$TMP_ROOT/exact-download" 2
)
[ "$(cat "$TMP_ROOT/exact-download")" = ok ]

if command -v zip >/dev/null 2>&1; then
  (
    export RNL_INSTALL_XRAY_LIBRARY_ONLY=1
    # shellcheck disable=SC1090
    source "$ROOT_DIR/scripts/install-xray.sh"
    xray_env="$TMP_ROOT/xray-unmatched.env"
    printf '%s\n' 'CUSTOM_CORE_URL="paired"' 'CUSTOM_CORE_URL="xray-unterminated' >"$xray_env"
    CUSTOM_CORE_URL=""
    load_env_var CUSTOM_CORE_URL "$xray_env"
    [ "$CUSTOM_CORE_URL" = '"xray-unterminated' ]
    if RNL_TMP_ROOT=/ ensure_installer_temp_root >/dev/null 2>&1; then
      echo "standalone rw-core installer accepted / as temp root" >&2
      exit 1
    fi
    (
      installer_path_has_root_owner() { :; }
      installer_ancestor_is_safe() { :; }
      # shellcheck disable=SC2031
      RNL_TMP_ROOT="$TMP_ROOT/legal-xray-installer"
      ensure_installer_temp_root
      grep -Fxq 'remnanode-installer-root-v1' \
        "$RNL_TMP_ROOT/.remnanode-installer-root"
      make_temp_dir >/dev/null
    )
    xray_unsafe_root="$TMP_ROOT/unsafe-xray-installer"
    mkdir -m 0777 "$xray_unsafe_root"
    if (
      installer_path_has_root_owner() { :; }
      RNL_TMP_ROOT="$xray_unsafe_root"
      ensure_installer_temp_root >/dev/null 2>&1
    ); then
      echo "standalone rw-core installer accepted a writable temp root" >&2
      exit 1
    fi
    if (
      # shellcheck disable=SC2329
      installer_path_has_root_owner() { :; }
      RNL_TMP_ROOT="$xray_unsafe_root"
      make_temp_dir >/dev/null 2>&1
    ); then
      echo "standalone temp creation ignored unsafe installer root validation" >&2
      exit 1
    fi
    if find "$xray_unsafe_root" -mindepth 1 -maxdepth 1 -name 'xray.*' -print | grep -q .; then
      echo "standalone temp directory was created under an unsafe installer root" >&2
      exit 1
    fi

    xray_paths="$TMP_ROOT/xray-managed-paths"
    mkdir -p "$xray_paths/real-parent" "$xray_paths/unsafe-parent"
    chmod 0770 "$xray_paths/unsafe-parent"
    ln -s real-parent "$xray_paths/link-parent"
    printf old >"$xray_paths/real-parent/rw-core"
    ln -s rw-core "$xray_paths/real-parent/rw-core-link"
    ln "$xray_paths/real-parent/rw-core" "$xray_paths/real-parent/rw-core-hardlink"
    (
      installer_path_has_root_owner() { :; }
      if xray_validate_parent_path "$xray_paths/link-parent/rw-core" >/dev/null 2>&1; then
        echo "rw-core target accepted a symlink ancestor" >&2
        exit 1
      fi
      if xray_validate_existing_file "$xray_paths/real-parent/rw-core-link" >/dev/null 2>&1; then
        echo "rw-core target accepted a symlink file" >&2
        exit 1
      fi
      if xray_validate_existing_file "$xray_paths/real-parent/rw-core-hardlink" >/dev/null 2>&1; then
        echo "rw-core target accepted a hardlinked file" >&2
        exit 1
      fi
      if xray_validate_parent_path "$xray_paths/unsafe-parent/rw-core" >/dev/null 2>&1; then
        echo "rw-core target accepted a group-writable ancestor" >&2
        exit 1
      fi
      printf replacement >"$xray_paths/replacement"
      if atomic_install "$xray_paths/replacement" "$xray_paths/link-parent/new-rw-core" 0755 \
        >/dev/null 2>&1; then
        echo "atomic rw-core install followed a symlink ancestor" >&2
        exit 1
      fi
      [ "$(cat "$xray_paths/real-parent/rw-core")" = old ]
    )
    # shellcheck disable=SC2329
    curl() { printf four; }
    if download_file https://example.invalid/oversized "$TMP_ROOT/xray-oversized-download" 2 2>/dev/null; then
      echo "rw-core download hard limit was not enforced" >&2
      exit 1
    fi
    [ ! -e "$TMP_ROOT/xray-oversized-download" ]
    xray_target="$TMP_ROOT/running-rw-core"
    xray_proc="$TMP_ROOT/xray-proc"
    printf core >"$xray_target"
    mkdir -p "$xray_proc/456"
    ln -s "$xray_target" "$xray_proc/456/exe"
    RNL_PROC_ROOT="$xray_proc"
    if require_install_target_stopped "$xray_target" 2>/dev/null; then
      echo "rw-core replacement accepted a running target" >&2
      exit 1
    fi
    unset RNL_PROC_ROOT
    zip_dir="$TMP_ROOT/zip"
    mkdir -p "$zip_dir"
    printf core >"$zip_dir/xray"
    printf ip >"$zip_dir/geoip.dat"
    printf site >"$zip_dir/geosite.dat"
    (cd "$zip_dir" && zip -q archive.zip xray geoip.dat geosite.dat)
    validate_zip_structure "$zip_dir/archive.zip"
    TMP_DIR="$zip_dir"
    ASN_SOURCE=""
    EXTERNAL_ASSET_ROLLBACK=1
    validate_xray_install_layout() { :; }
    require_free_space() { [[ "$2" =~ ^[1-9][0-9]*$ ]]; }
    preflight_asset_space "$zip_dir/archive.zip"
    extract_zip_entry_limited "$zip_dir/archive.zip" xray "$zip_dir/extracted" 16
    [ "$(cat "$zip_dir/extracted")" = core ]
    if extract_zip_entry_limited "$zip_dir/archive.zip" xray "$zip_dir/oversized" 2 2>/dev/null; then
      echo "zip extraction hard limit was not enforced" >&2
      exit 1
    fi

    (
      xray_space_temp="$TMP_ROOT/xray-space-budget-temp"
      xray_space_core="$TMP_ROOT/xray-space-budget-core"
      xray_space_geo="$TMP_ROOT/xray-space-budget-geo"
      xray_space_asn="$TMP_ROOT/xray-space-budget-asn"
      mkdir -p "$xray_space_temp/work" "$xray_space_core" \
        "$xray_space_geo" "$xray_space_asn"
      RNL_TMP_ROOT="$xray_space_temp"
      TMP_DIR="$xray_space_temp/work"
      XRAY_BIN="$xray_space_core/rw-core"
      GEO_DIR="$xray_space_geo"
      ASN_DB_PATH="$xray_space_asn/asn-prefixes.bin"
      ASN_SOURCE="$TMP_DIR/asn-prefixes.bin"
      printf asn-data >"$ASN_SOURCE"
      EXTERNAL_ASSET_ROLLBACK=1
      xray_space_checks="$TMP_ROOT/xray-space-budget-checks"
      : >"$xray_space_checks"
      filesystem_device_id() {
        case "$1" in
          "$xray_space_temp"|"$xray_space_temp"/*) printf temp ;;
          "$xray_space_core"|"$xray_space_core"/*|"$xray_space_geo"|"$xray_space_geo"/*)
            printf assets
            ;;
          "$xray_space_asn"|"$xray_space_asn"/*) printf asn ;;
          *) return 1 ;;
        esac
      }
      require_free_space() {
        printf '%s|%s\n' "$1" "$2" >>"$xray_space_checks"
      }
      preflight_asset_space "$zip_dir/archive.zip"
      [ "$(wc -l <"$xray_space_checks" | tr -d '[:space:]')" -eq 3 ]
      grep -Fxq "$xray_space_temp|$((XRAY_SPACE_SAFETY_BYTES + 10))" \
        "$xray_space_checks"
      grep -Fxq "$xray_space_core|$((XRAY_SPACE_SAFETY_BYTES + 10))" \
        "$xray_space_checks"
      grep -Fxq "$xray_space_asn|$((XRAY_SPACE_SAFETY_BYTES + 8))" \
        "$xray_space_checks"

      xray_cross_mount_marker="$TMP_ROOT/xray-cross-mount-space-checked"
      filesystem_device_id() {
        case "$1" in
          "$xray_space_temp"|"$xray_space_temp"/*) printf temp ;;
          "$xray_space_core"|"$xray_space_core"/*) printf core ;;
          "$xray_space_geo"|"$xray_space_geo"/*) printf geo ;;
          "$xray_space_asn"|"$xray_space_asn"/*) printf asn ;;
          *) return 1 ;;
        esac
      }
      require_free_space() {
        if [ "$1" = "$xray_space_geo" ]; then
          : >"$xray_cross_mount_marker"
          return 1
        fi
      }
      if preflight_asset_space "$zip_dir/archive.zip" >/dev/null 2>&1; then
        echo "rw-core preflight ignored a full geo target filesystem" >&2
        exit 1
      fi
      [ -e "$xray_cross_mount_marker" ]
    )

    (
      xray_txn_root="$TMP_ROOT/xray-rollback-preserve"
      TMP_DIR="$xray_txn_root/transaction"
      XRAY_BIN="$xray_txn_root/core/rw-core"
      GEO_DIR="$xray_txn_root/geo"
      ASN_SOURCE=""
      ASN_TRANSACTIONAL=0
      INSTALL_ARMED=1
      mkdir -p "$TMP_DIR/backup" "$(dirname "$XRAY_BIN")" "$GEO_DIR"
      printf old-core >"$TMP_DIR/backup/rw-core"
      printf old-ip >"$TMP_DIR/backup/geoip.dat"
      printf old-site >"$TMP_DIR/backup/geosite.dat"
      printf new-core >"$XRAY_BIN"
      printf new-ip >"$GEO_DIR/geoip.dat"
      printf new-site >"$GEO_DIR/geosite.dat"
      xray_validate_existing_file() { :; }
      xray_ensure_root_directory() { :; }
      mv() {
        local arg last=""
        for arg in "$@"; do last="$arg"; done
        if [ "$last" = "$GEO_DIR/geosite.dat" ]; then
          return 1
        fi
        command mv "$@"
      }
      xray_rollback_stderr="$TMP_ROOT/xray-rollback-preserve.stderr"
      if (set +e; false; cleanup) 2>"$xray_rollback_stderr"; then
        echo "incomplete rw-core rollback returned success" >&2
        exit 1
      fi
      [ -d "$TMP_DIR/backup" ]
      [ "$(cat "$TMP_DIR/backup/rw-core")" = old-core ]
      [ "$(cat "$TMP_DIR/backup/geoip.dat")" = old-ip ]
      [ "$(cat "$TMP_DIR/backup/geosite.dat")" = old-site ]
      [ "$(cat "$XRAY_BIN")" = old-core ]
      [ "$(cat "$GEO_DIR/geoip.dat")" = old-ip ]
      [ "$(cat "$GEO_DIR/geosite.dat")" = new-site ]
      grep -Fq "rollback artifacts preserved at: $TMP_DIR" "$xray_rollback_stderr"
    )

    (
      xray_absent_root="$TMP_ROOT/xray-rollback-absent"
      TMP_DIR="$xray_absent_root/transaction"
      xray_absent_target="$xray_absent_root/target/geoip.dat"
      mkdir -p "$TMP_DIR/backup" "$(dirname "$xray_absent_target")"
      : >"$TMP_DIR/backup/geoip.dat.absent"
      printf installed >"$xray_absent_target"
      xray_validate_existing_file() { :; }
      xray_ensure_root_directory() { :; }
      rm() {
        local arg
        for arg in "$@"; do
          [ "$arg" != "$xray_absent_target" ] || return 1
        done
        command rm "$@"
      }
      if restore_asset geoip.dat "$xray_absent_target" >/dev/null 2>&1; then
        echo "failed absent-state rollback mutation returned success" >&2
        exit 1
      fi
      [ -f "$TMP_DIR/backup/geoip.dat.absent" ]
      [ "$(cat "$xray_absent_target")" = installed ]
    )
  )
fi

if grep -Eq 'INSTALLER_TMP_DIR|rm[[:space:]]+-rf.*remnanode-installer' \
  "$ROOT_DIR/scripts/uninstall.sh"; then
  echo "uninstall purge must not remove the shared installer transaction root" >&2
  exit 1
fi
# shellcheck disable=SC2016
grep -Fq 'ensure_release_support_layout "$support_root" "$support_link"' \
  "$ROOT_DIR/scripts/install-env-helpers.sh"
grep -Fq 'validate_xray_install_layout || return' "$ROOT_DIR/scripts/install-xray.sh"
read -r release_tmp_line release_trap_line release_space_line < <(
  awk '
    /^install_release_binary\(\)/ { inside = 1 }
    inside && /tmp="\$\(make_installer_temp_dir release\)"/ { tmp_line = NR }
    inside && /trap .*support_stage/ { trap_line = NR }
    inside && /require_free_bytes "\$tmp"/ { space_line = NR }
    inside && /^\)$/ { exit }
    END { print tmp_line + 0, trap_line + 0, space_line + 0 }
  ' "$ROOT_DIR/scripts/install-env-helpers.sh"
)
if [ "$release_tmp_line" -eq 0 ] || [ "$release_trap_line" -le "$release_tmp_line" ] \
  || [ "$release_trap_line" -ge "$release_space_line" ]; then
  echo "release temp cleanup trap is not armed immediately after temp creation" >&2
  exit 1
fi

if grep -Eq '(^|[[:space:]])\.[[:space:]]+/etc/remnanode/node\.env' \
  "$ROOT_DIR/deploy/remnawave-node.openrc"; then
  echo "OpenRC service must not source node.env" >&2
  exit 1
fi
if grep -Eq '^required_files=' "$ROOT_DIR/deploy/remnawave-node.openrc"; then
  echo "OpenRC stop/status must not depend on required_files" >&2
  exit 1
fi
grep -Fq 'command="/usr/bin/env"' "$ROOT_DIR/deploy/remnawave-node.openrc"
grep -Fq 'command_args="-i PATH=' "$ROOT_DIR/deploy/remnawave-node.openrc"
grep -Fq 'REMNANODE_ENV=/etc/remnanode/node.env' "$ROOT_DIR/deploy/remnawave-node.openrc"
grep -Fq '[ -f /etc/remnanode/node.env ]' "$ROOT_DIR/deploy/remnawave-node.openrc"
grep -Fq '[ -x /usr/local/bin/remnanode-lite ]' "$ROOT_DIR/deploy/remnawave-node.openrc"
if grep -Eq 'load_runtime_environment|build_runtime_command_args' \
  "$ROOT_DIR/deploy/remnawave-node.openrc"; then
  echo "OpenRC must leave bounded node.env parsing to the Go process" >&2
  exit 1
fi
if grep -Eq '^EnvironmentFile=' "$ROOT_DIR/deploy/remnawave-node.service"; then
  echo "systemd must not export node.env into the process environment" >&2
  exit 1
fi
grep -Fq 'ExecStart=/usr/bin/env -i ' "$ROOT_DIR/deploy/remnawave-node.service"
grep -Fq 'REMNANODE_ENV=/etc/remnanode/node.env' "$ROOT_DIR/deploy/remnawave-node.service"
grep -Fq 'pidfile="/run/remnawave-node-supervise.pid"' \
  "$ROOT_DIR/deploy/remnawave-node.openrc"
grep -Fq 'rc_cgroup_cleanup=NO' "$ROOT_DIR/deploy/remnawave-node.openrc"
if grep -Eq '^pidfile="/run/remnanode/' "$ROOT_DIR/deploy/remnawave-node.openrc"; then
  echo "OpenRC supervisor pidfile must not live in the service-writable runtime directory" >&2
  exit 1
fi
for expected in \
  'memory.max 469762048' \
  'memory.swap.max 0' \
  'cpu.max 100000 100000' \
  'pids.max 256'; do
  grep -Fq "$expected" "$ROOT_DIR/deploy/remnawave-node.openrc"
done

openrc_stub="$TMP_ROOT/openrc-env-stub"
printf '#!/bin/sh\nenv\n' >"$openrc_stub"
chmod 0755 "$openrc_stub"
# A broken/missing node.env must not be read while OpenRC loads stop/status metadata.
(
  # shellcheck disable=SC1090
  source "$ROOT_DIR/deploy/remnawave-node.openrc"
  [ "$command" = /usr/bin/env ]
  declare -F stop_post >/dev/null
  read -r -a openrc_args <<<"$command_args"
  openrc_args[${#openrc_args[@]} - 1]="$openrc_stub"
  SECRET_KEY=caller-secret HTTP_PROXY=http://caller.invalid RNL_TEST=caller \
    GOMEMLIMIT=caller NODE_CONTRACT_VERSION=caller XRAY_CORE_VERSION=caller \
    /usr/bin/env "${openrc_args[@]}" >"$TMP_ROOT/openrc-child-env"
)
for expected in \
  'PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin' \
  'HOME=/var/lib/remnanode' \
  'USER=remnanode' \
  'LOGNAME=remnanode' \
  'REMNANODE_ENV=/etc/remnanode/node.env'; do
  grep -Fxq "$expected" "$TMP_ROOT/openrc-child-env"
done
if grep -Eq '^(SECRET_KEY|HTTP_PROXY|RNL_TEST|GOMEMLIMIT|NODE_CONTRACT_VERSION|XRAY_CORE_VERSION)=' \
  "$TMP_ROOT/openrc-child-env"; then
  echo "OpenRC child inherited an unapproved caller variable" >&2
  exit 1
fi

eerror() { :; }
# shellcheck disable=SC1090
source <(sed -n '/^cgroup_limit_error()/,/^}/p' "$ROOT_DIR/deploy/remnawave-node.openrc")
# shellcheck disable=SC1090
source <(sed -n '/^verify_resource_cgroup()/,/^}/p' "$ROOT_DIR/deploy/remnawave-node.openrc")
cgroup_root="$TMP_ROOT/cgroup"
group="$cgroup_root/openrc.test-service"
mkdir -p "$group"
: >"$cgroup_root/cgroup.controllers"
printf 469762048 >"$group/memory.max"
printf 0 >"$group/memory.swap.max"
printf '100000 100000\n' >"$group/cpu.max"
printf 256 >"$group/pids.max"
printf '%s\n' "$$" >"$group/cgroup.procs"
export RC_SVCNAME=test-service
verify_resource_cgroup "$cgroup_root"
printf '%s\n999998\n' "$$" >"$group/cgroup.procs"
if verify_resource_cgroup "$cgroup_root"; then
  echo "cgroup verification accepted a stale process beside the service shell" >&2
  exit 1
fi
printf '999999\n' >"$group/cgroup.procs"
if verify_resource_cgroup "$cgroup_root"; then
  echo "cgroup verification accepted a service shell outside the target group" >&2
  exit 1
fi
printf '%s\n' "$$" >"$group/cgroup.procs"
printf max >"$group/memory.max"
if verify_resource_cgroup "$cgroup_root"; then
  echo "invalid cgroup limit unexpectedly passed" >&2
  exit 1
fi

# shellcheck disable=SC1090
source <(sed -n '/^read_cgroup_populated()/,/^}/p' "$ROOT_DIR/deploy/remnawave-node.openrc")
# shellcheck disable=SC1090
source <(sed -n '/^cleanup_resource_cgroup()/,/^}/p' "$ROOT_DIR/deploy/remnawave-node.openrc")
cleanup_root="$TMP_ROOT/cleanup-cgroup"
cleanup_group="$cleanup_root/openrc.cleanup-service"
cleanup_marker="$TMP_ROOT/cgroup-kill-value"
mkdir -p "$cleanup_group"
: >"$cleanup_root/cgroup.controllers"
: >"$cleanup_root/cgroup.procs"
printf '%s\n999999\n' "$$" >"$cleanup_group/cgroup.procs"
printf 'populated 1\n' >"$cleanup_group/cgroup.events"
: >"$cleanup_group/cgroup.kill"
(
  # shellcheck disable=SC2030
  export RC_SVCNAME=cleanup-service
  # shellcheck disable=SC2329
  sleep() { printf 'populated 0\n' >"$cleanup_group/cgroup.events"; }
  # shellcheck disable=SC2329
  rmdir() {
    cat "$1/cgroup.kill" >"$cleanup_marker"
    rm -rf "$1"
  }
  cleanup_resource_cgroup "$cleanup_root"
)
[ ! -d "$cleanup_group" ]
[ "$(cat "$cleanup_root/cgroup.procs")" = 0 ]
[ "$(cat "$cleanup_marker")" = 1 ]

empty_cleanup_root="$TMP_ROOT/empty-cleanup-cgroup"
empty_cleanup_group="$empty_cleanup_root/openrc.empty-cleanup-service"
empty_cleanup_marker="$TMP_ROOT/empty-cgroup-kill-value"
mkdir -p "$empty_cleanup_group"
: >"$empty_cleanup_root/cgroup.controllers"
: >"$empty_cleanup_root/cgroup.procs"
: >"$empty_cleanup_group/cgroup.procs"
printf 'populated 0\n' >"$empty_cleanup_group/cgroup.events"
: >"$empty_cleanup_group/cgroup.kill"
(
  # shellcheck disable=SC2031
  export RC_SVCNAME=empty-cleanup-service
  # shellcheck disable=SC2329
  rmdir() {
    cat "$1/cgroup.kill" >"$empty_cleanup_marker"
    rm -rf "$1"
  }
  cleanup_resource_cgroup "$empty_cleanup_root"
)
[ ! -d "$empty_cleanup_group" ]
[ ! -s "$empty_cleanup_marker" ]

symlink_cleanup_root="$TMP_ROOT/symlink-cleanup-cgroup"
symlink_cleanup_target="$TMP_ROOT/symlink-cleanup-target"
mkdir -p "$symlink_cleanup_root" "$symlink_cleanup_target"
: >"$symlink_cleanup_root/cgroup.controllers"
ln -s "$symlink_cleanup_target" "$symlink_cleanup_root/openrc.symlink-cleanup-service"
if RC_SVCNAME=symlink-cleanup-service \
  cleanup_resource_cgroup "$symlink_cleanup_root" >/dev/null 2>&1; then
  echo "cgroup cleanup followed a symlink service path" >&2
  exit 1
fi
[ -d "$symlink_cleanup_target" ]

if (
  manager_state=active
  systemctl() {
    case "$1" in
      show) printf 'LoadState=loaded\nActiveState=%s\n' "$manager_state" ;;
      stop) manager_state=inactive; return 1 ;;
      *) return 1 ;;
    esac
  }
  sleep() { :; }
  stop_remnanode_and_wait /missing/node /missing/core 1 systemd 2>/dev/null
); then
  echo "stop confirmation ignored a failed stop command" >&2
  exit 1
fi

shared_service_state_test_query_errors() {
  local stop_marker="$TMP_ROOT/shared-state-stop-called"
  local sleep_marker="$TMP_ROOT/shared-state-sleep-called"
  # shellcheck disable=SC2329
  (
    systemctl() {
      case "$1" in
        show) return 71 ;;
        stop) : >"$stop_marker" ;;
      esac
    }
    sleep() { : >"$sleep_marker"; }
    if stop_remnanode_and_wait /missing/node /missing/core 1 systemd \
      >/dev/null 2>&1; then
      echo "shared stop accepted a systemd query error" >&2
      exit 1
    fi
    [ ! -e "$stop_marker" ]
    if wait_for_service_stable 2222 2 /missing/node systemd \
      >/dev/null 2>&1; then
      echo "service stability check accepted a systemd query error" >&2
      exit 1
    fi
    [ ! -e "$sleep_marker" ]
  )
}
shared_service_state_test_query_errors

fresh_reinstall_test_failed_stop_recovery() {
  local platform="$1"
  local fixture="$TMP_ROOT/fresh-reinstall-${platform}"
  local probe_marker="$fixture/probed" action_log="$fixture/actions"
  mkdir -p "$fixture"

  # shellcheck disable=SC2329
  (
    probe_remnanode_service_state() {
      if [ ! -e "$probe_marker" ]; then
        : >"$probe_marker"
        printf active
      else
        printf inactive
      fi
    }
    stop_remnanode_and_wait() { return 73; }
    wait_for_owned_processes_stopped() { :; }
    wait_for_service_stable() { printf 'verify:%s:%s\n' "$1" "$4" >>"$action_log"; }
    systemctl() { printf 'systemd:%s\n' "$*" >>"$action_log"; }
    rc-service() { printf 'openrc:%s\n' "$*" >>"$action_log"; }

    if stop_for_fresh_reinstall "$platform" /old/node /old/core 2222 \
      >/dev/null 2>&1; then
      echo "fresh reinstall continued after a failed stop" >&2
      exit 1
    fi
    grep -Fxq "verify:2222:${platform}" "$action_log"
    if [ "$platform" = systemd ]; then
      grep -Fxq 'systemd:start remnawave-node.service' "$action_log"
    else
      grep -Fxq 'openrc:remnawave-node start' "$action_log"
    fi
  )

  rm -f "$probe_marker" "$action_log"
  # shellcheck disable=SC2329
  (
    probe_remnanode_service_state() { printf error; }
    stop_remnanode_and_wait() { : >"$fixture/stop-called"; }
    if stop_for_fresh_reinstall "$platform" /old/node /old/core 2222 \
      >/dev/null 2>&1; then
      echo "fresh reinstall accepted an initial manager query error" >&2
      exit 1
    fi
    [ ! -e "$fixture/stop-called" ]
  )

  rm -f "$probe_marker" "$action_log"
  # shellcheck disable=SC2329
  (
    probe_remnanode_service_state() {
      if [ ! -e "$probe_marker" ]; then
        : >"$probe_marker"
        printf active
      else
        printf inactive
      fi
    }
    stop_remnanode_and_wait() { return 73; }
    wait_for_owned_processes_stopped() { return 1; }
    systemctl() { : >"$fixture/restart-called"; }
    rc-service() { : >"$fixture/restart-called"; }
    if stop_for_fresh_reinstall "$platform" /old/node /old/core 2222 \
      >/dev/null 2>&1; then
      echo "fresh reinstall accepted a failed stop with remaining processes" >&2
      exit 1
    fi
    [ ! -e "$fixture/restart-called" ]
  )
}
fresh_reinstall_test_failed_stop_recovery systemd
fresh_reinstall_test_failed_stop_recovery openrc

fresh_reinstall_test_callsites_fail_closed() {
  local installer_script="$1" function_name="$2" platform="$3"
  local fixture="$TMP_ROOT/fresh-callsite-${platform}"
  mkdir -p "$fixture/bin" "$fixture/etc" "$fixture/log" "$fixture/data"
  : >"$fixture/bin/remnanode-lite"
  chmod 0755 "$fixture/bin/remnanode-lite"
  printf 'NODE_PORT=2222\n' >"$fixture/etc/node.env"
  : >"$fixture/service"

  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n "/^${function_name}()/,/^}/p" \
      "$ROOT_DIR/scripts/$installer_script")
    PREFIX="$fixture/bin"
    BIN_NAME=remnanode-lite
    NODE_ENV="$fixture/etc/node.env"
    UNIT="$fixture/service"
    OPENRC_SVC="$fixture/service"
    ETC_DIR="$fixture/etc"
    LOG_DIR="$fixture/log"
    DATA_DIR="$fixture/data"
    PORT_EXPLICIT=0
    SECRET_FILE_ARG=''
    YES=0
    DRY_RUN=0
    DELEGATE_TO_UPGRADE=0
    read_tty() { printf -v "$1" 2; }
    read_env_value() { :; }
    configured_node_port() { printf 2222; }
    stop_for_fresh_reinstall() {
      printf '%s\n' "$1" >"$fixture/stop-platform"
      return 1
    }
    cleanup_runtime() { : >"$fixture/delete-called"; }
    rm() { : >"$fixture/delete-called"; }

    if "$function_name" >/dev/null 2>&1; then
      echo "$installer_script continued after fresh reinstall stop failed" >&2
      exit 1
    fi
    grep -Fxq "$platform" "$fixture/stop-platform"
    [ ! -e "$fixture/delete-called" ]
  )
}
fresh_reinstall_test_callsites_fail_closed \
  install-node.sh confirm_install systemd
fresh_reinstall_test_callsites_fail_closed \
  install-node-alpine.sh confirm_install openrc

uninstall_test_manager_error_preserves_files() {
  local fixture="$TMP_ROOT/uninstall-manager-error"
  local delete_marker="$fixture/delete-called" stop_marker="$fixture/stop-called"
  mkdir -p "$fixture"
  : >"$fixture/remnawave-node.service"

  set +e
  # shellcheck disable=SC2034,SC2329
  (
    set -Eeuo pipefail
    # shellcheck disable=SC1090
    source <(sed -n '/^probe_uninstall_service_state()/,/^}/p' \
      "$ROOT_DIR/scripts/uninstall.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^uninstall_service_manager_state()/,/^}/p' \
      "$ROOT_DIR/scripts/uninstall.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^stop_service()/,/^}/p' "$ROOT_DIR/scripts/uninstall.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^main() {$/,/^}$/p' "$ROOT_DIR/scripts/uninstall.sh")
    PREFIX="$fixture"
    BIN_NAME=remnanode-lite
    UNIT="$fixture/remnawave-node.service"
    OPENRC_SVC="$fixture/missing-openrc"
    ETC_DIR="$fixture/etc"
    XRAY_BIN="$fixture/rw-core"
    CONFIGURED_XRAY_BIN="$XRAY_BIN"
    DRY_RUN=0
    is_alpine() { return 1; }
    require_root() { :; }
    installer_acquire_lock() { :; }
    installed() { :; }
    interactive_options() { :; }
    confirm_uninstall() { :; }
    print_plan() { :; }
    read_env_value() { :; }
    step() { :; }
    systemctl() {
      case "$1" in
        show) return 71 ;;
        stop) : >"$stop_marker" ;;
      esac
    }
    cleanup_runtime() { : >"$delete_marker"; }
    cleanup_firewall() { : >"$delete_marker"; }
    remove_service_files() { : >"$delete_marker"; }
    remove_binaries() { : >"$delete_marker"; }
    remove_optional_dirs() { : >"$delete_marker"; }
    remove_xray() { : >"$delete_marker"; }
    main
  ) >/dev/null 2>&1
  local status=$?
  set -e
  [ "$status" -ne 0 ]
  [ ! -e "$stop_marker" ]
  [ ! -e "$delete_marker" ]

  # Confirmation itself must also reject a manager query error with no PID.
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^probe_uninstall_service_state()/,/^}/p' \
      "$ROOT_DIR/scripts/uninstall.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^uninstall_service_manager_state()/,/^}/p' \
      "$ROOT_DIR/scripts/uninstall.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^wait_for_stop_confirmation()/,/^}/p' \
      "$ROOT_DIR/scripts/uninstall.sh")
    UNIT="$fixture/remnawave-node.service"
    OPENRC_SVC="$fixture/missing-openrc"
    PREFIX="$fixture"
    BIN_NAME=remnanode-lite
    CONFIGURED_XRAY_BIN="$fixture/rw-core"
    is_alpine() { return 1; }
    systemctl() { return 71; }
    running_pids_for_binary() { :; }
    sleep() { : >"$fixture/uninstall-sleep-called"; }
    if wait_for_stop_confirmation >/dev/null 2>&1; then
      echo "uninstall stop confirmation accepted a manager query error" >&2
      exit 1
    fi
    [ ! -e "$fixture/uninstall-sleep-called" ]
  )
}
uninstall_test_manager_error_preserves_files

upgrade_transaction_test_unarmed_backup_cleanup() {
  local fixture="$TMP_ROOT/upgrade-unarmed-cleanup"
  mkdir -p "$fixture"

  set +e
  # shellcheck disable=SC2034,SC2329
  (
    set -Eeuo pipefail
    # shellcheck disable=SC1090
    source <(sed -n '/^cleanup_unarmed_upgrade_backup()/,/^}/p' \
      "$ROOT_DIR/scripts/upgrade.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^on_error()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^begin_upgrade_transaction()/,/^}/p' \
      "$ROOT_DIR/scripts/upgrade.sh")
    ROLLBACK_ARMED=0
    BACKUP_DIR=""
    STAGE="test"
    DRY_RUN=0
    PREFIX="$fixture"
    BIN_NAME=remnanode-lite
    NODE_ENV="$fixture/node.env"
    SECRET_FILE="$fixture/secret.key"
    UNIT="$fixture/unit"
    OPENRC_SVC="$fixture/openrc"
    SUPPORT_LINK="$fixture/support-current"
    UPGRADE_XRAY=0
    installer_temp_root() { printf '%s' "$fixture"; }
    validate_installer_temp_root_path() { :; }
    validate_installer_temp_root_marker() { :; }
    validate_existing_owned_directory() { :; }
    preflight_upgrade_space() { :; }
    make_installer_temp_dir() { mktemp -d "$fixture/upgrade.XXXXXX"; }
    backup_path() { return 71; }
    step() { :; }
    trap 'on_error $? "$BASH_COMMAND"' ERR
    begin_upgrade_transaction
  ) >/dev/null 2>&1
  local status=$?
  set -e
  [ "$status" -eq 71 ]
  if find "$fixture" -mindepth 1 -maxdepth 1 -name 'upgrade.*' -print | grep -q .; then
    echo "upgrade left an unarmed partial backup directory" >&2
    return 1
  fi

  mkdir "$fixture/not-an-upgrade"
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^cleanup_unarmed_upgrade_backup()/,/^}/p' \
      "$ROOT_DIR/scripts/upgrade.sh")
    ROLLBACK_ARMED=0
    BACKUP_DIR="$fixture/not-an-upgrade"
    installer_temp_root() { printf '%s' "$fixture"; }
    validate_installer_temp_root_path() { :; }
    validate_installer_temp_root_marker() { :; }
    validate_existing_owned_directory() { :; }
    if cleanup_unarmed_upgrade_backup >/dev/null 2>&1; then
      echo "upgrade cleanup accepted a non-generated backup name" >&2
      exit 1
    fi
    [ -d "$BACKUP_DIR" ]
  )
}
upgrade_transaction_test_unarmed_backup_cleanup

replacement_marker="$TMP_ROOT/replacement-started"
stop_attempt_marker="$TMP_ROOT/stop-attempted"
rollback_attempt_marker="$TMP_ROOT/rollback-attempted"
rollback_active_marker="$TMP_ROOT/rollback-active-restored"
installed_prefix="$TMP_ROOT/installed/bin"
mkdir -p "$installed_prefix"
: >"$installed_prefix/remnanode-lite"
# shellcheck disable=SC2034,SC2329
(
  # shellcheck disable=SC1090
  source <(sed -n '/^main() {$/,/^}$/p' "$ROOT_DIR/scripts/upgrade.sh")
  DRY_RUN=0
  SERVICE_WAS_ACTIVE=0
  SERVICE_SHOULD_BE_ACTIVE=0
  ENSURE_SERVICE_STARTED=0
  ENSURE_SERVICE_ENABLED=0
  ROLLBACK_ARMED=0
  PREFIX="$installed_prefix"
  BIN_NAME=remnanode-lite
  NODE_ENV=/etc/remnanode/node.env
  require_root() { :; }
  installer_acquire_lock() { :; }
  require_command() { :; }
  is_alpine() { return 1; }
  service_is_active() { return 0; }
  confirm_upgrade() { :; }
  detect_arch() { printf amd64; }
  current_version() { printf test; }
  ensure_service_account() { :; }
  setup_service_directories() { :; }
  begin_upgrade_transaction() { :; }
  step() { :; }
  stop_service_for_maintenance() { printf 'attempt\n' >>"$stop_attempt_marker"; return 1; }
  rollback_upgrade() {
    : >"$rollback_attempt_marker"
    : >"$rollback_active_marker"
    ROLLBACK_ARMED=0
  }
  download_binary() { : >"$replacement_marker"; }
  if main; then
    echo "upgrade unexpectedly continued after stop confirmation failed" >&2
    exit 1
  fi
  if [ "$ROLLBACK_ARMED" -eq 1 ]; then
    rollback_upgrade
  fi
  [ "$(wc -l <"$stop_attempt_marker" | tr -d '[:space:]')" -eq 1 ]
  [ -e "$rollback_attempt_marker" ]
  [ -e "$rollback_active_marker" ]
)
[ -e "$stop_attempt_marker" ]
[ ! -e "$replacement_marker" ]

upgrade_transaction_test_rollback_stop_gate() {
  local fixture="$TMP_ROOT/upgrade-rollback-stop-gate"
  local restore_marker="$fixture/restore-called" service_log="$fixture/service-log"
  mkdir -p "$fixture/backup"
  : >"$fixture/backup/support-link.absent"

  # A still-running process makes rollback refuse every file replacement.
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^rollback_upgrade()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    BACKUP_DIR="$fixture/backup"
    PREFIX="$fixture"
    BIN_NAME=remnanode-lite
    NODE_ENV="$fixture/node.env"
    SECRET_FILE="$fixture/secret.key"
    UNIT="$fixture/unit"
    OPENRC_SVC="$fixture/openrc"
    SUPPORT_LINK="$fixture/support-current"
    TAG=v0.1.0
    UPGRADE_XRAY=0
    SERVICE_WAS_ACTIVE=1
    ROLLBACK_ARMED=1
    stop_service_for_maintenance() { return 1; }
    restore_path() { : >"$restore_marker"; }
    if rollback_upgrade >/dev/null 2>&1; then
      echo "upgrade rollback replaced files while processes remained" >&2
      exit 1
    fi
    [ ! -e "$restore_marker" ]
  )

  # Once the failed first stop has actually left the service inactive, the
  # rollback stop gate succeeds, restores files, and returns the old active state.
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^rollback_upgrade()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    BACKUP_DIR="$fixture/backup"
    PREFIX="$fixture"
    BIN_NAME=remnanode-lite
    NODE_ENV="$fixture/node.env"
    SECRET_FILE="$fixture/secret.key"
    UNIT="$fixture/unit"
    OPENRC_SVC="$fixture/openrc"
    SUPPORT_LINK="$fixture/support-current"
    TAG=v0.1.0
    UPGRADE_XRAY=0
    SERVICE_WAS_ACTIVE=1
    ROLLBACK_ARMED=1
    stop_service_for_maintenance() { :; }
    restore_path() { printf '%s\n' "$1" >>"$restore_marker"; }
    rm() { :; }
    is_alpine() { return 1; }
    systemctl() { printf '%s\n' "$*" >>"$service_log"; }
    restore_service_enabled_state() { :; }
    read_env_value() { printf 2222; }
    wait_for_service_stable() { :; }
    rollback_upgrade >/dev/null
    grep -Fxq binary "$restore_marker"
    grep -Fxq 'start remnawave-node.service' "$service_log"
    [ "$ROLLBACK_ARMED" -eq 0 ]
  )
}
upgrade_transaction_test_rollback_stop_gate

restart_log="$TMP_ROOT/restart-service.log"
# shellcheck disable=SC2034,SC2329
(
  # shellcheck disable=SC1090
  source <(sed -n '/^restart_service() {$/,/^}$/p' "$ROOT_DIR/scripts/upgrade.sh")
  DRY_RUN=0
  NODE_ENV="$TMP_ROOT/restart-node.env"
  : >"$NODE_ENV"
  SERVICE_SHOULD_BE_ACTIVE=0
  step() { :; }
  is_alpine() { return 1; }
  sleep() { :; }
  systemctl() { printf '%s\n' "$*" >>"$restart_log"; }
  restart_service >/dev/null
  [ ! -e "$restart_log" ]
  SERVICE_SHOULD_BE_ACTIVE=1
  restart_service >/dev/null
  grep -Fxq 'restart remnawave-node.service' "$restart_log"
  NODE_ENV="$TMP_ROOT/missing-restart-node.env"
  set +e
  restart_service >/dev/null 2>&1
  restart_status=$?
  set -e
  [ "$restart_status" -eq 1 ]
)

# shellcheck disable=SC2329
(
  fake_binary="$TMP_ROOT/remnanode-lite"
  fake_proc="$TMP_ROOT/proc"
  printf binary >"$fake_binary"
  mkdir -p "$fake_proc/123"
  ln -s "$fake_binary" "$fake_proc/123/exe"
  RNL_PROC_ROOT="$fake_proc"
  systemctl() {
    [ "$1" = show ] || return 1
    printf 'LoadState=loaded\nActiveState=active\n'
  }
  ss() { printf '%s\n' 'LISTEN 0 128 0.0.0.0:2222 0.0.0.0:* users:(("remnanode-lite",pid=123,fd=3))'; }
  if require_binary_not_running "$fake_binary" 2>/dev/null; then
    echo "release replacement accepted a running target" >&2
    exit 1
  fi
  [ "$(single_service_pid "$fake_binary")" = 123 ]
  listener_owned_by_pid 2222 123
  if listener_owned_by_pid 2222 999; then
    echo "listener ownership accepted the wrong PID" >&2
    exit 1
  fi
  wait_for_service_stable 2222 1 "$fake_binary" systemd
)

dry_run_output="$(bash "$ROOT_DIR/scripts/upgrade.sh" --yes --dry-run --low-memory)"
grep -Fq '[dry-run] 停止服务并确认 remnanode-lite/rw-core 全部退出' <<<"$dry_run_output"
grep -Fq '[dry-run] 设置 /etc/remnanode/node.env LOW_MEMORY=1' <<<"$dry_run_output"
if RNL_ENSURE_SERVICE_STARTED=invalid \
  bash "$ROOT_DIR/scripts/upgrade.sh" --yes --dry-run >/dev/null 2>&1; then
  echo "upgrade accepted invalid ensure-start state" >&2
  exit 1
fi

managed_service_file_test_atomic_install() {
  local fixture="$TMP_ROOT/managed-service-file"
  local source="$fixture/source.service" target="$fixture/target.service"
  local outside="$fixture/outside.service" atomic_log="$fixture/atomic-mv"
  local support_link="$fixture/support-current"
  local release_source="$fixture/support/v0.1.0/deploy/source.service"
  mkdir -p "$fixture/support/v0.1.0/deploy"
  printf 'new-service\n' >"$source"
  printf 'new-service\n' >"$release_source"
  printf 'outside\n' >"$outside"
  chmod 0644 "$source" "$release_source" "$outside"
  ln -s support/v0.1.0 "$support_link"

  # The production helper requires root-owned paths. Tests substitute only the
  # ownership probe/chown while retaining real inode, mode, and link checks.
  # shellcheck disable=SC2329
  (
    local resolved_source
    validate_managed_parent_path() { :; }
    installer_path_owner_ids() {
      case "$1" in
        *foreign-owner*) printf '1000:1000\n' ;;
        *) printf '0:0\n' ;;
      esac
    }
    chown() { :; }
    resolved_source="$(resolve_installed_support_file \
      "$support_link" deploy/source.service)"
    [ "$resolved_source" = "$release_source" ]

    ln -s "$outside" "$target"
    if install_managed_file "$source" "$target" 0644 >/dev/null 2>&1; then
      echo "managed service install followed a target symlink" >&2
      exit 1
    fi
    grep -Fxq outside "$outside"
    rm -f "$target"

    printf 'linked-target\n' >"$target"
    chmod 0644 "$target"
    ln "$target" "$fixture/target-alias"
    if install_managed_file "$source" "$target" 0644 >/dev/null 2>&1; then
      echo "managed service install replaced a hardlinked target" >&2
      exit 1
    fi
    grep -Fxq linked-target "$fixture/target-alias"
    rm -f "$target" "$fixture/target-alias"

    printf 'unsafe-target\n' >"$target"
    chmod 0666 "$target"
    if install_managed_file "$source" "$target" 0644 >/dev/null 2>&1; then
      echo "managed service install replaced a writable target" >&2
      exit 1
    fi
    grep -Fxq unsafe-target "$target"
    rm -f "$target"

    printf 'foreign-target\n' >"$fixture/foreign-owner-target"
    chmod 0644 "$fixture/foreign-owner-target"
    if install_managed_file "$source" "$fixture/foreign-owner-target" 0644 \
      >/dev/null 2>&1; then
      echo "managed service install replaced a foreign-owned target" >&2
      exit 1
    fi
    grep -Fxq foreign-target "$fixture/foreign-owner-target"
    rm -f "$fixture/foreign-owner-target"

    ln -s "$source" "$fixture/source-symlink"
    if install_managed_file "$fixture/source-symlink" "$target" 0644 \
      >/dev/null 2>&1; then
      echo "managed service install followed a source symlink" >&2
      exit 1
    fi
    rm -f "$fixture/source-symlink"

    cp "$source" "$fixture/source-hardlink"
    ln "$fixture/source-hardlink" "$fixture/source-hardlink-alias"
    if install_managed_file "$fixture/source-hardlink" "$target" 0644 \
      >/dev/null 2>&1; then
      echo "managed service install accepted a hardlinked source" >&2
      exit 1
    fi
    rm -f "$fixture/source-hardlink" "$fixture/source-hardlink-alias"

    cp "$source" "$fixture/foreign-owner-source"
    chmod 0644 "$fixture/foreign-owner-source"
    if install_managed_file "$fixture/foreign-owner-source" "$target" 0644 \
      >/dev/null 2>&1; then
      echo "managed service install accepted a foreign-owned source" >&2
      exit 1
    fi
    rm -f "$fixture/foreign-owner-source"

    printf 'preserved-service\n' >"$target"
    chmod 0644 "$target"
    chown() { return 73; }
    if install_managed_file "$source" "$target" 0644 >/dev/null 2>&1; then
      echo "managed service install ignored a staging ownership failure" >&2
      exit 1
    fi
    grep -Fxq preserved-service "$target"
    if find "$fixture" -maxdepth 1 -name '.target.service.*' -print | grep -q .; then
      echo "managed service install left a failed staging file" >&2
      exit 1
    fi
    chown() { :; }

    mv() {
      [ "$#" -eq 4 ] && [ "$1" = -f ] && [ "$2" = -- ] || return 74
      [ "$(dirname "$3")" = "$(dirname "$4")" ] || return 75
      printf '%s -> %s\n' "$3" "$4" >"$atomic_log"
      command mv "$@"
    }
    install_managed_file "$resolved_source" "$target" 0644
    grep -Fq " -> $target" "$atomic_log"
    grep -Fxq new-service "$target"
    [ "$((8#$(installer_path_mode "$target")))" -eq "$((8#0644))" ]
    if find "$fixture" -maxdepth 1 -name '.target.service.*' -print | grep -q .; then
      echo "managed service install left a successful staging file" >&2
      exit 1
    fi
  )
}
managed_service_file_test_atomic_install

managed_service_file_test_callsites() {
  local script="$1" function_name="$2" expected="$3" body
  body="$(sed -n "/^${function_name}()/,/^}/p" "$ROOT_DIR/scripts/$script")"
  grep -Fq "$expected" <<<"$body" || {
    echo "$script:$function_name does not use install_managed_file" >&2
    return 1
  }
  if grep -Eq '^[[:space:]]*install[[:space:]]+-' <<<"$body"; then
    echo "$script:$function_name still installs the service file directly" >&2
    return 1
  fi
}
managed_service_file_test_callsites install-node.sh install_systemd \
  "install_managed_file \"\$support\" \"\$UNIT\" 0644"
managed_service_file_test_callsites install-node-alpine.sh install_openrc \
  "install_managed_file \"\$support\" \"\$OPENRC_SVC\" 0755"
managed_service_file_test_callsites upgrade.sh refresh_systemd \
  "install_managed_file \"\$support\" \"\$UNIT\" 0644"
managed_service_file_test_callsites upgrade.sh refresh_openrc \
  "install_managed_file \"\$support\" \"\$OPENRC_SVC\" 0755"

install_entrypoints_test_helper_safety() {
  local installer_script="$1"
  local helper_dir="$TMP_ROOT/install-entrypoints-${installer_script}"
  local outside="$helper_dir-outside"
  mkdir -p "$helper_dir"
  printf 'outside\n' >"$outside"

  # shellcheck disable=SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^install_log_helper_command() (/,/^)$/{p;}' \
      "$ROOT_DIR/scripts/$installer_script")
    validate_managed_parent_path() { :; }
    installer_path_owner_ids() { printf '0:0\n'; }
    chown() { :; }

    ln -s "$outside" "$helper_dir/remnanode-xlogs"
    if install_log_helper_command "$helper_dir/remnanode-xlogs" /logs/xray.out \
      >/dev/null 2>&1; then
      echo "$installer_script followed an existing helper symlink" >&2
      exit 1
    fi
    grep -Fxq outside "$outside"
    rm -f "$helper_dir/remnanode-xlogs"

    printf 'foreign\n' >"$helper_dir/remnanode-xlogs"
    chmod 0666 "$helper_dir/remnanode-xlogs"
    if install_log_helper_command "$helper_dir/remnanode-xlogs" /logs/xray.out \
      >/dev/null 2>&1; then
      echo "$installer_script overwrote an unsafe regular helper target" >&2
      exit 1
    fi
    grep -Fxq foreign "$helper_dir/remnanode-xlogs"
    rm -f "$helper_dir/remnanode-xlogs"

    printf 'linked\n' >"$helper_dir/remnanode-xlogs"
    ln "$helper_dir/remnanode-xlogs" "$helper_dir/remnanode-xlogs-alias"
    if install_log_helper_command "$helper_dir/remnanode-xlogs" /logs/xray.out \
      >/dev/null 2>&1; then
      echo "$installer_script overwrote a hardlinked helper target" >&2
      exit 1
    fi
    grep -Fxq linked "$helper_dir/remnanode-xlogs-alias"
    rm -f "$helper_dir/remnanode-xlogs" "$helper_dir/remnanode-xlogs-alias"

    printf 'preserved\n' >"$helper_dir/remnanode-xlogs"
    chmod 0644 "$helper_dir/remnanode-xlogs"
    chown() { return 73; }
    if install_log_helper_command "$helper_dir/remnanode-xlogs" /logs/xray.out \
      >/dev/null 2>&1; then
      echo "$installer_script ignored helper staging ownership failure" >&2
      exit 1
    fi
    grep -Fxq preserved "$helper_dir/remnanode-xlogs"
    if find "$helper_dir" -maxdepth 1 -name '.remnanode-xlogs.*' -print | grep -q .; then
      echo "$installer_script left a failed helper staging file" >&2
      exit 1
    fi
    chown() { :; }

    install_log_helper_command "$helper_dir/remnanode-xlogs" /logs/xray.out
    [ -x "$helper_dir/remnanode-xlogs" ]
    grep -Fxq '#!/bin/sh' "$helper_dir/remnanode-xlogs"
    grep -Fxq 'exec tail -n +1 -f /logs/xray.out' "$helper_dir/remnanode-xlogs"
    if find "$helper_dir" -maxdepth 1 -name '.remnanode-xlogs.*' -print | grep -q .; then
      echo "$installer_script left a successful helper staging file" >&2
      exit 1
    fi
  )
}
install_entrypoints_test_helper_safety install-node.sh
install_entrypoints_test_helper_safety install-node-alpine.sh

install_entrypoints_test_registration_failures() {
  local support_fixture="$TMP_ROOT/install-entrypoints-service-support"
  printf 'service\n' >"$support_fixture"

  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^install_systemd()/,/^}/p' "$ROOT_DIR/scripts/install-node.sh")
    DRY_RUN=0
    UNIT="$TMP_ROOT/install-entrypoints-systemd.service"
    step() { :; }
    installed_support_file() { printf '%s\n' "$support_fixture"; }
    install_managed_file() { :; }
    enable_attempted=0
    systemctl() {
      [ "$1" != enable ] || enable_attempted=1
      [ "$1" != enable ]
    }
    if install_systemd >/dev/null 2>&1; then
      echo "install-node.sh ignored systemctl enable failure" >&2
      exit 1
    fi
    [ "$enable_attempted" -eq 1 ]
  )

  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^install_openrc()/,/^}/p' "$ROOT_DIR/scripts/install-node-alpine.sh")
    DRY_RUN=0
    OPENRC_SVC="$TMP_ROOT/install-entrypoints-openrc.service"
    step() { :; }
    installed_support_file() { printf '%s\n' "$support_fixture"; }
    install_managed_file() { :; }
    registration_attempted=0
    rc-update() { registration_attempted=1; return 71; }
    if install_openrc >/dev/null 2>&1; then
      echo "install-node-alpine.sh ignored rc-update add failure" >&2
      exit 1
    fi
    [ "$registration_attempted" -eq 1 ]
  )
}
install_entrypoints_test_registration_failures

install_entrypoints_test_delegated_enable_intent() {
  local installer_script="$1"
  local intent="$TMP_ROOT/install-entrypoints-${installer_script}.intent"
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^run_upgrade_transaction()/,/^}/p' \
      "$ROOT_DIR/scripts/$installer_script")
    REPO=example/repository
    TAG=v0.1.0
    SKIP_XRAY=1
    INSTALL_XRAY=1
    DRY_RUN=0
    LOW_MEMORY=0
    run_sibling_script() {
      printf '%s:%s\n' "$RNL_ENSURE_SERVICE_STARTED" \
        "$RNL_ENSURE_SERVICE_ENABLED" >"$intent"
    }
    run_upgrade_transaction >/dev/null
  )
  grep -Fxq '1:1' "$intent"
}
install_entrypoints_test_delegated_enable_intent install-node.sh
install_entrypoints_test_delegated_enable_intent install-node-alpine.sh

upgrade_transaction_test_restore_failures() {
  local fixture="$TMP_ROOT/upgrade-restore-failures"
  local cp_marker="$fixture/cp-attempted"
  mkdir -p "$fixture/backup"
  printf 'old\n' >"$fixture/backup/binary"
  printf 'new\n' >"$fixture/target"
  : >"$fixture/backup/added.absent"

  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^restore_path()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    BACKUP_DIR="$fixture/backup"
    rm() { return 71; }
    cp() { : >"$cp_marker"; command cp "$@"; }

    if restore_path binary "$fixture/target" >/dev/null 2>&1; then
      echo "upgrade restore reported success after target removal failed" >&2
      exit 1
    fi
    [ ! -e "$cp_marker" ]
    grep -Fxq new "$fixture/target"

    if restore_path added "$fixture/target" >/dev/null 2>&1; then
      echo "upgrade absent-path restore ignored target removal failure" >&2
      exit 1
    fi
    if restore_path missing "$fixture/target" >/dev/null 2>&1; then
      echo "upgrade restore accepted a missing backup record" >&2
      exit 1
    fi
  )
}
upgrade_transaction_test_restore_failures

upgrade_transaction_test_active_probe_states() {
  # shellcheck disable=SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^service_is_active()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    is_alpine() { return 1; }
    systemd_probe=active
    systemctl() {
      case "$systemd_probe" in
        active) printf 'LoadState=loaded\nActiveState=active\n' ;;
        inactive) printf 'LoadState=loaded\nActiveState=inactive\n' ;;
        activating) printf 'LoadState=loaded\nActiveState=activating\n' ;;
        unknown) printf 'LoadState=loaded\nActiveState=mystery\n' ;;
        error) return 70 ;;
      esac
    }
    service_is_active
    systemd_probe=inactive
    if service_is_active; then
      echo "upgrade systemd probe classified inactive as active" >&2
      exit 1
    else
      [ "$?" -eq 1 ]
    fi
    systemd_probe=unknown
    if service_is_active; then
      echo "upgrade systemd probe accepted an unknown ActiveState" >&2
      exit 1
    else
      [ "$?" -eq 2 ]
    fi
    systemd_probe=activating
    if service_is_active; then
      echo "upgrade systemd probe accepted a transitional ActiveState" >&2
      exit 1
    else
      [ "$?" -eq 2 ]
    fi
    systemd_probe=error
    if service_is_active; then
      echo "upgrade systemd probe ignored a manager error" >&2
      exit 1
    else
      [ "$?" -eq 2 ]
    fi

    is_alpine() { return 0; }
    openrc_probe=0
    rc-service() { return "$openrc_probe"; }
    service_is_active
    openrc_probe=3
    if service_is_active; then
      echo "upgrade OpenRC probe classified stopped as active" >&2
      exit 1
    else
      [ "$?" -eq 1 ]
    fi
    openrc_probe=71
    if service_is_active; then
      echo "upgrade OpenRC probe ignored a manager error" >&2
      exit 1
    else
      [ "$?" -eq 2 ]
    fi
  )
}
upgrade_transaction_test_active_probe_states

upgrade_transaction_test_probe_error_fails_closed() {
  local fixture="$TMP_ROOT/upgrade-probe-fail-closed"
  local transaction_marker="$fixture/transaction-started"
  mkdir -p "$fixture/bin"
  : >"$fixture/bin/remnanode-lite"
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^main() {$/,/^}$/p' "$ROOT_DIR/scripts/upgrade.sh")
    DRY_RUN=0
    ENSURE_SERVICE_STARTED=0
    ENSURE_SERVICE_ENABLED=0
    SERVICE_WAS_ACTIVE=0
    SERVICE_SHOULD_BE_ACTIVE=0
    PREFIX="$fixture/bin"
    BIN_NAME=remnanode-lite
    require_root() { :; }
    installer_acquire_lock() { :; }
    require_command() { :; }
    is_alpine() { return 1; }
    confirm_upgrade() { :; }
    service_is_active() { return 2; }
    begin_upgrade_transaction() { : >"$transaction_marker"; }
    if main >/dev/null 2>&1; then
      echo "upgrade continued after service-state probe error" >&2
      exit 1
    fi
    [ ! -e "$transaction_marker" ]
  )
}
upgrade_transaction_test_probe_error_fails_closed

upgrade_transaction_test_enable_and_restore() {
  local action_log="$TMP_ROOT/upgrade-enable-actions"
  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^service_is_enabled()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^ensure_service_enabled()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^restore_service_enabled_state()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    DRY_RUN=0
    ENSURE_SERVICE_ENABLED=1
    SERVICE_ENABLE_MUTATION_ATTEMPTED=0
    is_alpine() { return 1; }
    step() { :; }
    enabled_state=disabled
    enable_result=0
    enable_persists=1
    disable_result=0
    systemctl() {
      case "$1" in
        enable)
          printf 'enable\n' >>"$action_log"
          if [ "$enable_persists" -eq 1 ]; then
            enabled_state=enabled
          fi
          return "$enable_result"
          ;;
        disable)
          printf 'disable\n' >>"$action_log"
          enabled_state=disabled
          return "$disable_result"
          ;;
        is-enabled)
          printf '%s\n' "$enabled_state"
          [ "$enabled_state" = enabled ]
          ;;
      esac
    }

    ensure_service_enabled >/dev/null
    [ "$SERVICE_ENABLE_MUTATION_ATTEMPTED" -eq 1 ]
    [ "$enabled_state" = enabled ]

    enabled_state=disabled
    enable_persists=0
    if ensure_service_enabled >/dev/null 2>&1; then
      echo "upgrade accepted an enable operation that did not persist" >&2
      exit 1
    fi
    enable_persists=1

    SERVICE_ENABLED_STATE_CAPTURED=1
    SERVICE_WAS_ENABLED=0
    restore_service_enabled_state >/dev/null
    [ "$enabled_state" = disabled ]
    grep -Fxq disable "$action_log"

    SERVICE_WAS_ENABLED=1
    restore_service_enabled_state >/dev/null
    [ "$enabled_state" = enabled ]

    SERVICE_WAS_ENABLED=0
    disable_result=73
    if restore_service_enabled_state >/dev/null 2>&1; then
      echo "upgrade rollback ignored a registration mutation failure" >&2
      exit 1
    fi
  )

  # shellcheck disable=SC2034,SC2329
  (
    # shellcheck disable=SC1090
    source <(sed -n '/^service_is_enabled()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    # shellcheck disable=SC1090
    source <(sed -n '/^ensure_service_enabled()/,/^}/p' "$ROOT_DIR/scripts/upgrade.sh")
    DRY_RUN=0
    ENSURE_SERVICE_ENABLED=1
    SERVICE_ENABLE_MUTATION_ATTEMPTED=0
    openrc_enabled=0
    openrc_add_result=0
    is_alpine() { return 0; }
    step() { :; }
    rc-update() {
      if [ "$1" = add ]; then
        openrc_enabled=1
        return "$openrc_add_result"
      fi
      if [ "$1" = show ] && [ "$openrc_enabled" -eq 1 ]; then
        printf 'remnawave-node | default\n'
      fi
    }
    ensure_service_enabled >/dev/null
    [ "$openrc_enabled" -eq 1 ]
    openrc_add_result=74
    if ensure_service_enabled >/dev/null 2>&1; then
      echo "upgrade ignored rc-update add failure" >&2
      exit 1
    fi
  )
}
upgrade_transaction_test_enable_and_restore

if RNL_ENSURE_SERVICE_ENABLED=invalid \
  bash "$ROOT_DIR/scripts/upgrade.sh" --yes --dry-run >/dev/null 2>&1; then
  echo "upgrade accepted invalid ensure-enabled state" >&2
  exit 1
fi

echo "installer operations checks passed"
