#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

fail() {
  echo "version check: $*" >&2
  exit 1
}

extract_go_var() {
  local name="$1"
  sed -n "s/^var ${name} = \"\([^\"]*\)\"$/\1/p" internal/version/version.go
}

extract_script_version() {
  local path="$1"
  sed -n 's/^VERSION="\([^"]*\)"$/\1/p' "$path"
}

version="$(extract_go_var Version)"
contract_version="$(extract_go_var ContractVersion)"
[ -n "$version" ] || fail "internal/version Version is missing"
[ -n "$contract_version" ] || fail "internal/version ContractVersion is missing"

file_contract_version="$(tr -d '[:space:]' < internal/version/contract.version)"
[ "$contract_version" = "$file_contract_version" ] ||
  fail "contract.version=${file_contract_version}, Go ContractVersion=${contract_version}"

for script in \
  scripts/install-node.sh \
  scripts/install-node-alpine.sh \
  scripts/upgrade.sh \
  scripts/uninstall.sh; do
  script_version="$(extract_script_version "$script")"
  [ "$script_version" = "$version" ] ||
    fail "$script VERSION=${script_version:-missing}, want $version"
done

expected_rnl_tag="$(printf "RNL_TAG=\"\${RNL_TAG:-v%s}\"" "$version")"
grep -Fq "$expected_rnl_tag" scripts/install-xray.sh ||
  fail "scripts/install-xray.sh default RNL_TAG is not v$version"
grep -Fq "remnanode-contract-probe/${version}" internal/contract/probe.go ||
  fail "contract probe User-Agent is not $version"
grep -Fq "| 当前版本 | \`${version}\`" README.md ||
  fail "README current version is not $version"
for script in install-node.sh install-node-alpine.sh upgrade.sh uninstall.sh; do
  grep -Fq "remnawave-node-lite-go/v${version}/scripts/${script}" README.md ||
    fail "README ${script} URL is not pinned to v${version}"
done

toolchain="$(sed -n 's/^toolchain[[:space:]][[:space:]]*//p' go.mod)"
[ "$toolchain" = "go1.26.5" ] || fail "go.mod toolchain=${toolchain:-missing}, want go1.26.5"

release_tag="${RELEASE_TAG:-}"
if [ -n "$release_tag" ]; then
  [[ "$release_tag" =~ ^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$ ]] ||
    fail "release tag $release_tag is not canonical semver"
  [ "$release_tag" = "v$version" ] || fail "release tag $release_tag does not match v$version"
fi

echo "version check: $version (contract $contract_version, toolchain $toolchain)"
