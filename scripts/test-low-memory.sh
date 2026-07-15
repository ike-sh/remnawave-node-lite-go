#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
RW_CORE_BIN="${RW_CORE_BIN:-}"
RESOURCE_USERS="${REMNANODE_RESOURCE_USERS:-50000}"
MEMORY_MIB="${REMNANODE_RESOURCE_MEMORY_MIB:-448}"
RESOURCE_IMAGE="${REMNANODE_RESOURCE_IMAGE:-alpine:3.22}"

usage() {
  cat <<'EOF'
Usage: scripts/test-low-memory.sh --rw-core /path/to/linux/rw-core [options]

Options:
  --rw-core PATH   Linux rw-core v26.6.27 binary (or set RW_CORE_BIN)
  --users COUNT    Large-config user count (default: 50000)
  --memory MIB     Container hard limit and peak gate (default: 448)
  --image IMAGE    Container image (default: alpine:3.22)
EOF
}

while [ "$#" -gt 0 ]; do
  case "$1" in
    --rw-core) RW_CORE_BIN="$2"; shift 2 ;;
    --users) RESOURCE_USERS="$2"; shift 2 ;;
    --memory) MEMORY_MIB="$2"; shift 2 ;;
    --image) RESOURCE_IMAGE="$2"; shift 2 ;;
    --help|-h) usage; exit 0 ;;
    *) echo "unknown argument: $1" >&2; usage >&2; exit 2 ;;
  esac
done

if [ -z "$RW_CORE_BIN" ] || [ ! -x "$RW_CORE_BIN" ]; then
  echo "--rw-core must point to an executable Linux rw-core binary" >&2
  exit 2
fi
if ! command -v docker >/dev/null 2>&1 || ! docker info >/dev/null 2>&1; then
  echo "Docker is required and its daemon must be running" >&2
  exit 1
fi

case "$(docker info --format '{{.Architecture}}')" in
  x86_64|amd64) GOARCH=amd64 ;;
  aarch64|arm64) GOARCH=arm64 ;;
  *) echo "unsupported Docker architecture" >&2; exit 1 ;;
esac

if [ -n "${REMNANODE_RESOURCE_TMPDIR:-}" ]; then
  TMP_ROOT="$REMNANODE_RESOURCE_TMPDIR"
elif [ "$(uname -s)" = "Darwin" ]; then
  TMP_ROOT="$HOME/Library/Caches/remnanode-resource-test"
else
  TMP_ROOT="${TMPDIR:-/tmp}/remnanode-resource-test"
fi
mkdir -p "$TMP_ROOT"
TMP_DIR="$(mktemp -d "$TMP_ROOT/run.XXXXXX")"
trap 'rm -rf "$TMP_DIR"' EXIT

cp "$RW_CORE_BIN" "$TMP_DIR/rw-core"

echo "Building Linux ${GOARCH} resource test binary..."
(
  cd "$ROOT_DIR"
  CGO_ENABLED=0 GOOS=linux GOARCH="$GOARCH" \
    go test -c -o "$TMP_DIR/resource-probe" ./internal/xray
)

echo "Running 1 CPU / ${MEMORY_MIB} MiB / ${RESOURCE_USERS} users..."
docker run --rm \
  --cpus 1 \
  --memory "${MEMORY_MIB}m" \
  --memory-swap "${MEMORY_MIB}m" \
  --pids-limit 256 \
  --network none \
  --cap-drop ALL \
  --read-only \
  --tmpfs /tmp:rw,size=64m \
  -e REMNANODE_LOW_MEMORY_INTEGRATION=1 \
  -e REMNANODE_RESOURCE_USERS="$RESOURCE_USERS" \
  -e REMNANODE_RESOURCE_MAX_PEAK_MIB="$MEMORY_MIB" \
  -e RW_CORE_BIN=/rw-core \
  -v "$TMP_DIR/resource-probe:/resource-probe:ro" \
  -v "$TMP_DIR/rw-core:/rw-core:ro" \
  "$RESOURCE_IMAGE" \
  /resource-probe -test.run '^TestLowMemoryRealCore$' -test.v -test.timeout 5m
