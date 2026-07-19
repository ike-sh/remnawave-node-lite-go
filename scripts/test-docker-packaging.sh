#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

require_file() {
  [ -f "$1" ] || {
    echo "required Docker packaging file is missing: $1" >&2
    exit 1
  }
}

require_text() {
  local file="$1" text="$2"
  grep -Fq -- "$text" "$file" || {
    echo "$file is missing required Docker packaging text: $text" >&2
    exit 1
  }
}

for file in \
  Dockerfile \
  compose.yaml \
  compose.build.yaml \
  .dockerignore \
  .env.example \
  .github/workflows/container.yml \
  .github/workflows/release.yml \
  docs/deployment-docker.md; do
  require_file "$file"
done

if grep -Eqi '(^|[/:_-])latest([[:space:]/:@_-]|$)' Dockerfile; then
  echo "Dockerfile must not use floating latest assets or base images" >&2
  exit 1
fi

require_text Dockerfile '# syntax=docker/dockerfile:1.7.0@sha256:dbbd5e059e8a07ff7ea6233b213b36aa516b4c53c645f1817a4dd18b83cbea56'
require_text Dockerfile 'ARG GO_IMAGE=golang:1.26.5-bookworm@sha256:1ecb7edf62a0408027bd5729dfd6b1b8766e578e8df93995b225dfd0944eb651'
require_text Dockerfile 'ARG DEBIAN_IMAGE=debian:bookworm-slim@sha256:7b140f374b289a7c2befc338f42ebe6441b7ea838a042bbd5acbfca6ec875818'
require_text Dockerfile 'ARG XRAY_CORE_VERSION=v26.6.27'
require_text Dockerfile 'Xray-linux-64.zip'
require_text Dockerfile 'Xray-linux-arm64-v8a.zip'
require_text Dockerfile 'b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf'
require_text Dockerfile '13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931'
require_text Dockerfile 'https://github.com/ipverse/as-ip-blocks/archive/56d021c7536afb15317155e45b57e7b5c87a4700.tar.gz'
require_text Dockerfile 'fc8be15bfbef3134f603276a26364935dbd2543d099dbaafa978a33b674a58ec'
require_text Dockerfile 'asn-builder -format ipverse-tar-gz'
require_text Dockerfile 'ENV XRAY_CORE_VERSION=v26.6.27'
require_text Dockerfile 'ENTRYPOINT ["/usr/local/bin/remnanode-lite"]'

version="$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/version/version.go)"
[ -n "$version" ] || {
  echo "application version is missing" >&2
  exit 1
}
production_image="ghcr.io/luxiaba/remnawave-node-lite-go:${version}"
require_text compose.yaml "$production_image"
require_text .env.example "REMNANODE_IMAGE=${production_image}"
require_text compose.build.yaml "image: remnanode-lite-go:${version}"
require_text compose.build.yaml 'build:'

if grep -Eq '^[[:space:]]+build:' compose.yaml; then
  echo "production compose.yaml must not require a source build" >&2
  exit 1
fi

require_text compose.yaml 'network_mode: host'
require_text compose.yaml 'NET_ADMIN'
require_text compose.yaml 'NET_BIND_SERVICE'
require_text compose.yaml 'mem_limit: 448m'
require_text compose.yaml 'memswap_limit: 448m'
require_text compose.yaml 'cpus: 1.0'
require_text compose.yaml 'pids_limit: 256'
require_text compose.yaml 'read_only: true'
require_text compose.yaml 'test -S /run/remnanode/internal.sock && kill -0 1'

require_text .github/workflows/release.yml 'packages: write'
require_text .github/workflows/release.yml 'attestations: write'
require_text .github/workflows/release.yml 'needs: release'
require_text .github/workflows/release.yml 'attest-container:'
require_text .github/workflows/release.yml 'needs: publish-container'
github_expression_prefix='$'
require_text .github/workflows/release.yml 'docker buildx imagetools inspect'
require_text .github/workflows/release.yml "sha-${github_expression_prefix}{{ github.sha }}"
require_text .github/workflows/release.yml "subject-digest: ${github_expression_prefix}{{ steps.digest.outputs.digest }}"
require_text .github/workflows/release.yml 'platforms: linux/amd64,linux/arm64'
require_text .github/workflows/release.yml 'provenance: mode=max'
sbom_generator='generator=docker.io/docker/buildkit-syft-scanner:stable-1@sha256:79e7b013cbec16bbb436f312819a49a4a57752b2270c1a9332ae1a10fcc82a68'
require_text .github/workflows/release.yml "sbom: ${sbom_generator}"
require_text .github/workflows/release.yml 'flavor: latest=false'
require_text .github/workflows/release.yml 'image: docker.io/tonistiigi/binfmt:qemu-v10.2.3@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0'
require_text .github/workflows/release.yml 'image=moby/buildkit:v0.31.1@sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a'
require_text .github/workflows/release.yml 'dist/compose.yaml'
require_text .github/workflows/release.yml 'dist/remnanode.env.example'
require_text .github/workflows/container.yml 'outputs: type=cacheonly'
require_text .github/workflows/container.yml 'push: false'
require_text .github/workflows/container.yml 'workflow_dispatch:'
require_text .github/workflows/container.yml "cancel-in-progress: \${{ github.event_name != 'workflow_dispatch' }}"
require_text .github/workflows/container.yml "if: github.event_name != 'workflow_dispatch'"
require_text .github/workflows/container.yml "if: github.event_name == 'workflow_dispatch' && github.ref == 'refs/heads/main'"
require_text .github/workflows/container.yml "if: github.event_name == 'workflow_dispatch' && github.ref != 'refs/heads/main'"
require_text .github/workflows/container.yml 'Reject non-main candidate dispatch'
require_text .github/workflows/container.yml 'candidate publishing is only allowed from refs/heads/main'
require_text .github/workflows/container.yml 'candidate-sha-'
require_text .github/workflows/container.yml "sbom: ${sbom_generator}"
require_text .github/workflows/container.yml 'packages: write'
require_text .github/workflows/container.yml 'attestations: write'
require_text .github/workflows/container.yml 'provenance: mode=max'
require_text .github/workflows/container.yml 'push: true'
require_text .github/workflows/container.yml 'push-to-registry: true'
require_text .github/workflows/container.yml 'attest-candidate:'
require_text .github/workflows/container.yml 'needs: publish-candidate'
require_text .github/workflows/container.yml 'docker buildx imagetools inspect'
require_text .github/workflows/container.yml "candidate-sha-${github_expression_prefix}{{ github.sha }}"
require_text .github/workflows/container.yml "subject-digest: ${github_expression_prefix}{{ steps.digest.outputs.digest }}"
require_text .github/workflows/container.yml 'image: docker.io/tonistiigi/binfmt:qemu-v10.2.3@sha256:400a4873b838d1b89194d982c45e5fb3cda4593fbfd7e08a02e76b03b21166f0'
require_text .github/workflows/container.yml 'image=moby/buildkit:v0.31.1@sha256:6b59b7df63a8cb9902736f9ddf7fcff8261613d3e7449b8ea8b7537fc399c03a'

if grep -Eq 'ghcr\.io/[^[:space:]]+:latest|type=raw[^[:space:]]*latest' \
  compose.yaml .env.example .github/workflows/release.yml .github/workflows/container.yml; then
  echo "production container configuration must not publish or consume latest" >&2
  exit 1
fi

if grep -Eq '^[[:space:]]+needs:[[:space:]]+build[[:space:]]*$' .github/workflows/container.yml; then
  echo "manual candidate publishing must not repeat the CI build job" >&2
  exit 1
fi

while IFS= read -r action_ref; do
  case "$action_ref" in
    ./*) continue ;;
  esac
  revision="${action_ref##*@}"
  if ! [[ "$revision" =~ ^[0-9a-f]{40}$ ]]; then
    echo "GitHub Action is not pinned to a full commit: $action_ref" >&2
    exit 1
  fi
done < <(
  sed -En \
    's/^[[:space:]]*(-[[:space:]]*)?uses:[[:space:]]*([^[:space:]#]+).*$/\2/p' \
    .github/workflows/*.yml .github/workflows/*.yaml 2>/dev/null
)

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  SECRET_KEY=packaging-check REMNANODE_IMAGE="$production_image" \
    docker compose -f compose.yaml config --quiet
  SECRET_KEY=packaging-check REMNANODE_IMAGE="$production_image" \
    docker compose -f compose.yaml -f compose.build.yaml config --quiet
else
  echo "docker compose is unavailable; skipped Compose schema validation" >&2
fi

echo "Docker packaging checks passed"
