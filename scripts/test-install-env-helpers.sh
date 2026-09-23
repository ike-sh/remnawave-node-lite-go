#!/usr/bin/env bash
set -euo pipefail

# shellcheck source=install-env-helpers.sh
source "$(dirname "${BASH_SOURCE[0]}")/install-env-helpers.sh"

tmp="$(mktemp -d)"
trap 'rm -f "$tmp/archive/asn-builder" "$tmp/archive/asn-prefixes.bin" "$tmp/bin/asn-builder" "$tmp/data/asn-prefixes.bin"; rmdir "$tmp/archive" "$tmp/bin" "$tmp/data" "$tmp"' EXIT
mkdir -p "$tmp/archive" "$tmp/bin" "$tmp/data"
printf 'builder\n' > "$tmp/archive/asn-builder"
printf 'new-db\n' > "$tmp/archive/asn-prefixes.bin"
printf 'old-db\n' > "$tmp/data/asn-prefixes.bin"

install_release_asn_assets "$tmp/archive" "$tmp/bin" "$tmp/data/asn-prefixes.bin"
test "$(cat "$tmp/data/asn-prefixes.bin")" = old-db
test -f "$tmp/bin/asn-builder"

rm -f "$tmp/data/asn-prefixes.bin"
install_release_asn_assets "$tmp/archive" "$tmp/bin" "$tmp/data/asn-prefixes.bin"
test "$(cat "$tmp/data/asn-prefixes.bin")" = new-db

SECRET_FILE="$tmp/secret.key"
LOG_DIR="$tmp/log"
export SECRET_FILE LOG_DIR
template="$(render_env_template 2222 0 test)"
test "$(printf '%s\n' "$template" | grep -c '^SNI_VERIFICATION=false$')" = 1
test "$(printf '%s\n' "$template" | grep -c '^NFTABLES_LOGGING=true$')" = 1
test "$(printf '%s\n' "$template" | grep -c '^NFTABLES_ACCEPT_REPLY_TRAFFIC=false$')" = 1
