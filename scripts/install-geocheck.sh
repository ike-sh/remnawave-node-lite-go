#!/usr/bin/env bash
# Install the geocheck helper required by @remnawave/node >= 3.3.0.
set -euo pipefail

GEOCHECK_VERSION="${GEOCHECK_VERSION:-0.3.0}"
GEOCHECK_RELEASE_URL="${GEOCHECK_RELEASE_URL:-https://github.com/remnawave/geocheck/releases/download}"
GEOCHECK_BIN="${GEOCHECK_BIN:-/usr/local/bin/geocheck}"
DRY_RUN=0

usage() {
  cat <<'EOF'
用法：install-geocheck.sh [--version 0.3.0] [--dry-run]

环境变量：
  GEOCHECK_VERSION       版本，默认 0.3.0
  GEOCHECK_RELEASE_URL   Release 下载根地址
  GEOCHECK_BIN           安装路径，默认 /usr/local/bin/geocheck
EOF
}

while [ $# -gt 0 ]; do
  case "$1" in
    --version)
      GEOCHECK_VERSION="${2:-}"
      [ -n "$GEOCHECK_VERSION" ] || { echo "--version 需要版本号" >&2; exit 2; }
      shift 2
      ;;
    --dry-run) DRY_RUN=1; shift ;;
    --help|-h) usage; exit 0 ;;
    *) echo "未知参数：$1" >&2; usage; exit 2 ;;
  esac
done

if [ "$DRY_RUN" -ne 1 ] && [ "$(id -u)" -ne 0 ]; then
  echo "请使用 root 运行：sudo bash install-geocheck.sh" >&2
  exit 1
fi

case "$(uname -m)" in
  x86_64|amd64) arch="amd64" ;;
  aarch64|arm64) arch="arm64" ;;
  *) echo "不支持的架构：$(uname -m)" >&2; exit 1 ;;
esac

version="${GEOCHECK_VERSION#v}"
tag="v${version}"
archive="geocheck_linux_${arch}.tar.gz"
base="${GEOCHECK_RELEASE_URL}/${tag}"

if [ "$DRY_RUN" -eq 1 ]; then
  echo "[dry-run] curl -fsSL ${base}/${archive}"
  echo "[dry-run] curl -fsSL ${base}/checksums.txt"
  echo "[dry-run] sha256sum -c ${archive}"
  echo "[dry-run] install -m 0755 geocheck ${GEOCHECK_BIN}"
  exit 0
fi

for command in curl tar sha256sum install mktemp; do
  command -v "$command" >/dev/null 2>&1 || { echo "缺少命令：${command}" >&2; exit 1; }
done

tmp="$(mktemp -d)"
cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT INT TERM

curl --proto '=https' --tlsv1.2 -fsSL "${base}/${archive}" -o "${tmp}/${archive}"
curl --proto '=https' --tlsv1.2 -fsSL "${base}/checksums.txt" -o "${tmp}/checksums.txt"

checksum_line="$(grep -E "^[[:xdigit:]]{64}  ${archive}$" "${tmp}/checksums.txt" || true)"
if [ -z "$checksum_line" ] || [ "$(printf '%s\n' "$checksum_line" | wc -l | tr -d ' ')" != "1" ]; then
  echo "checksums.txt 中未找到唯一的 ${archive} 校验记录" >&2
  exit 1
fi
(cd "$tmp" && printf '%s\n' "$checksum_line" | sha256sum -c -)

archive_members="$(tar -tzf "${tmp}/${archive}")"
if ! printf '%s\n' "$archive_members" | grep -Fxq 'geocheck'; then
  echo "归档中缺少 geocheck 文件" >&2
  exit 1
fi
tar -xzf "${tmp}/${archive}" -C "$tmp" geocheck
if [ ! -f "${tmp}/geocheck" ] || [ -L "${tmp}/geocheck" ]; then
  echo "归档中的 geocheck 不是普通文件" >&2
  exit 1
fi

install -d -m 0755 "$(dirname "$GEOCHECK_BIN")"
staged="$(mktemp "${GEOCHECK_BIN}.staged.XXXXXX")"
trap 'rm -f "$staged"; cleanup' EXIT INT TERM
install -m 0755 "${tmp}/geocheck" "$staged"
"$staged" --help >/dev/null 2>&1
mv -f "$staged" "$GEOCHECK_BIN"
trap cleanup EXIT INT TERM

echo "geocheck ${tag} 已安装到 ${GEOCHECK_BIN}"
