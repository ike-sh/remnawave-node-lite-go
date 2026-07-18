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

if grep -Eq 'remnawave/asn-index/releases/download/(latest|latest/)' .github/workflows/release.yml; then
  echo "release workflow ASN source must use an immutable release tag" >&2
  exit 1
fi
grep -Fq 'ASN_SOURCE_URL: https://github.com/remnawave/asn-index/releases/download/2026-03-30/asn-prefixes.json' \
  .github/workflows/release.yml || {
  echo "release workflow ASN source does not match the audited 2026-03-30 asset" >&2
  exit 1
}
grep -Fq 'ASN_SOURCE_SHA256: 78db1c6b8bc86861a9c4bbca07229fcc4ee8b4fe8fa5b7c6069161f66c365cfc' \
  .github/workflows/release.yml || {
  echo "release workflow ASN source digest does not match the audited asset" >&2
  exit 1
}

for invalid_tag in '../../../../../etc' '..'; do
  if RNL_TAG="$invalid_tag" resolve_install_tag Luxiaba/remnawave-node-lite-go "$tag" >/dev/null 2>&1; then
    echo "release tag $invalid_tag unexpectedly passed" >&2
    exit 1
  fi
done

for bootstrap in scripts/install-node.sh scripts/install-node-alpine.sh scripts/upgrade.sh; do
  if RNL_TAG='../../../../../etc' bash -s -- --help <"$bootstrap" >/dev/null 2>&1; then
    echo "$bootstrap accepted a path-like bootstrap tag" >&2
    exit 1
  fi
done

dry_run_path="$(mktemp -d)"
trap 'rm -rf "$dry_run_path"' EXIT
for command_name in bash curl dirname head tar timeout uname; do
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
