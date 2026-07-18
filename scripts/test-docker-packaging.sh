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

for file in Dockerfile compose.yaml .dockerignore deploy/docker.env.example docs/deployment-docker.md; do
  require_file "$file"
done

if grep -Eqi '(^|[/:_-])latest([[:space:]/:@_-]|$)' Dockerfile; then
  echo "Dockerfile must not use floating latest assets or base images" >&2
  exit 1
fi

require_text Dockerfile 'ARG GO_VERSION=1.26.5'
require_text Dockerfile 'ARG XRAY_CORE_VERSION=v26.6.27'
require_text Dockerfile 'Xray-linux-64.zip'
require_text Dockerfile 'Xray-linux-arm64-v8a.zip'
require_text Dockerfile 'b3e5902d06d6282fe53cfa2fc426058b9aeaa429b2c812e20887cd47f26d08bf'
require_text Dockerfile '13a251379bea366c2cf10363ad71e75734193d401f26f518bf0c25e5c8f8c931'
require_text Dockerfile 'https://github.com/remnawave/asn-index/releases/download/2026-03-30/asn-prefixes.json'
require_text Dockerfile '78db1c6b8bc86861a9c4bbca07229fcc4ee8b4fe8fa5b7c6069161f66c365cfc'
require_text Dockerfile 'ENV XRAY_CORE_VERSION=v26.6.27'
require_text Dockerfile 'ENTRYPOINT ["/usr/local/bin/remnanode-lite"]'

require_text compose.yaml 'network_mode: host'
require_text compose.yaml 'NET_ADMIN'
require_text compose.yaml 'NET_BIND_SERVICE'
require_text compose.yaml 'mem_limit: 448m'
require_text compose.yaml 'memswap_limit: 448m'
require_text compose.yaml 'cpus: 1.0'
require_text compose.yaml 'pids_limit: 256'
require_text compose.yaml 'read_only: true'
require_text compose.yaml 'test -S /run/remnanode/internal.sock && kill -0 1'

if command -v docker >/dev/null 2>&1 && docker compose version >/dev/null 2>&1; then
  SECRET_KEY=packaging-check docker compose -f compose.yaml config --quiet
else
  echo "docker compose is unavailable; skipped Compose schema validation" >&2
fi

echo "Docker packaging checks passed"
