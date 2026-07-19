#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

# shellcheck source=scripts/install-env-helpers.sh
source scripts/install-env-helpers.sh

version="$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/version/version.go)"
tag="v${version}"
version_output="$(go run ./cmd/remnanode-lite version)"
release_binary_version_matches_tag "$version_output" "$tag"
if release_binary_version_matches_tag "$version_output" "v${version}.invalid"; then
  echo "release version matcher accepted the wrong tag" >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+ASN_SOURCE_URL:.*latest' .github/workflows/release.yml; then
  echo "release workflow ASN source must not use a floating latest reference" >&2
  exit 1
fi
grep -Fq 'ASN_SOURCE_URL: https://github.com/ipverse/as-ip-blocks/archive/56d021c7536afb15317155e45b57e7b5c87a4700.tar.gz' \
  .github/workflows/release.yml || {
  echo "release workflow ASN source is not pinned to the audited ipverse commit" >&2
  exit 1
}
grep -Fq 'ASN_SOURCE_SHA256: fc8be15bfbef3134f603276a26364935dbd2543d099dbaafa978a33b674a58ec' \
  .github/workflows/release.yml || {
  echo "release workflow ASN source digest does not match the audited commit archive" >&2
  exit 1
}

for invalid_tag in '../../../../../etc' '..'; do
  if RNL_TAG="$invalid_tag" resolve_install_tag Luxiaba/remnawave-node-lite-go "$tag" >/dev/null 2>&1; then
    echo "release tag $invalid_tag unexpectedly passed" >&2
    exit 1
  fi
done

for bootstrap in \
  scripts/install-node.sh \
  scripts/install-node-alpine.sh \
  scripts/install-xray.sh \
  scripts/upgrade.sh \
  scripts/uninstall.sh; do
  case "$bootstrap" in
    scripts/install-node.sh|scripts/install-node-alpine.sh)
      bootstrap_args=(--upgrade --yes --dry-run)
      ;;
    scripts/install-xray.sh)
      bootstrap_args=(--dry-run)
      ;;
    scripts/upgrade.sh)
      bootstrap_args=(--yes --dry-run)
      ;;
    scripts/uninstall.sh)
      bootstrap_args=(--yes --dry-run)
      ;;
  esac
  if bootstrap_output="$(RNL_TAG='../../../../../etc' \
    bash -s -- "${bootstrap_args[@]}" <"$bootstrap" 2>&1)"; then
    bootstrap_status=0
  else
    bootstrap_status=$?
  fi
  if [ "$bootstrap_status" -ne 2 ] \
    || ! grep -Fq 'RNL_REPO' <<<"$bootstrap_output" \
    || ! grep -Fq 'RNL_TAG' <<<"$bootstrap_output"; then
    echo "$bootstrap did not reject a path-like bootstrap tag at coordinate validation" >&2
    exit 1
  fi
done

dry_run_path="$(mktemp -d)"
trap 'rm -rf "$dry_run_path"' EXIT
for command_name in bash curl dirname grep head stat tar timeout tr uname wc; do
  command_path="$(command -v "$command_name")"
  [ -n "$command_path" ] || {
    echo "missing command required by portable dry-run test: $command_name" >&2
    exit 1
  }
  ln -s "$command_path" "$dry_run_path/$command_name"
done

dry_run_output="$(PATH="$dry_run_path" RNL_UPGRADE_XRAY=1 bash scripts/upgrade.sh --yes --dry-run)"
grep -Fq '目标 Release 中已校验的 install-xray.sh' <<<"$dry_run_output"
wrapper_dry_run_output="$(PATH="$dry_run_path" bash scripts/install-node.sh --upgrade --yes --dry-run)"
grep -Fq '[dry-run] 更新 /etc/systemd/system/remnawave-node.service' <<<"$wrapper_dry_run_output"

if CUSTOM_CORE_URL=https://example.invalid/rw-core bash scripts/install-xray.sh --dry-run; then
  echo "CUSTOM_CORE_URL without SHA-256 unexpectedly passed" >&2
  exit 1
fi
if ASN_DB_URL=https://example.invalid/asn-prefixes.bin bash scripts/install-xray.sh --dry-run; then
  echo "ASN_DB_URL without SHA-256 unexpectedly passed" >&2
  exit 1
fi
if XRAY_CORE_SHA256=0000000000000000000000000000000000000000000000000000000000000000 \
  bash scripts/install-xray.sh --dry-run; then
  echo "pinned rw-core SHA-256 override unexpectedly passed" >&2
  exit 1
fi

echo "supply-chain checks passed"
