#!/usr/bin/env bash
set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "$1" ]; then
  echo "usage: build-release-binaries.sh OUTPUT_DIR" >&2
  exit 2
fi

case "$1" in
  /*) output_dir="$1" ;;
  *) output_dir="$(pwd)/$1" ;;
esac

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"
mkdir -p "$output_dir"

version="$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/version/version.go)"
contract_version="$(tr -d ' \n\r' < internal/version/contract.version)"
toolchain="$(sed -n 's/^toolchain[[:space:]][[:space:]]*//p' go.mod)"
[ -n "$version" ] && [ -n "$contract_version" ] && [ -n "$toolchain" ] || {
  echo "release version metadata is incomplete" >&2
  exit 1
}
actual_toolchain="$(GOTOOLCHAIN=local go env GOVERSION)"
[ "$actual_toolchain" = "$toolchain" ] || {
  echo "release build requires $toolchain, found $actual_toolchain" >&2
  exit 1
}

for arch in amd64 arm64; do
  GOTOOLCHAIN=local GOWORK=off GOFLAGS='' GOEXPERIMENT='' GOFIPS140=off \
    CGO_ENABLED=0 GOOS=linux GOARCH="$arch" GOAMD64=v1 GOARM64=v8.0 \
    go build -mod=readonly -buildvcs=false -trimpath \
      -ldflags="-s -w -X github.com/Luxiaba/remnawave-node-lite-go/internal/version.Version=${version} -X github.com/Luxiaba/remnawave-node-lite-go/internal/version.ContractVersion=${contract_version}" \
      -o "$output_dir/remnanode-lite_linux_${arch}" \
      ./cmd/remnanode-lite
done
