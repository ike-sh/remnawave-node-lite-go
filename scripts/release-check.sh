#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

status="$(git status --porcelain --untracked-files=all)"
if [ -n "$status" ]; then
  echo "release requires a clean worktree:" >&2
  echo "$status" >&2
  exit 1
fi
if git ls-files --error-unmatch cmd/m8-runtime-probe >/dev/null 2>&1; then
  echo "temporary M8 runtime probe must not be tracked" >&2
  exit 1
fi

official_source="${REMNANODE_OFFICIAL_SOURCE:-}"
[ -n "$official_source" ] && [ -d "$official_source/.git" ] || {
  echo "REMNANODE_OFFICIAL_SOURCE must point to the pinned official Git checkout" >&2
  exit 1
}

version="$(sed -n 's/^var Version = "\([^"]*\)"$/\1/p' internal/version/version.go)"
release_tag="${RELEASE_TAG:-v${version}}"
RELEASE_TAG="$release_tag" bash scripts/check-version.sh

grep -Fq 'Unreleased' docs/CHANGELOG.md && {
  echo "CHANGELOG still contains Unreleased" >&2
  exit 1
}
grep -Fq '开发中' README.md && {
  echo "README still marks the release as under development" >&2
  exit 1
}
grep -Fq "| M8 发布验收 | 已完成 | \`checkpoint-m08-release-acceptance\` |" docs/development/roadmap.md || {
  echo "roadmap does not mark M8 release acceptance complete" >&2
  exit 1
}
grep -Eq "^## \[${version//./\\.}\] - [0-9]{4}-[0-9]{2}-[0-9]{2}$" docs/CHANGELOG.md || {
  echo "CHANGELOG does not contain a dated ${version} release heading" >&2
  exit 1
}

release_note="docs/releases/v${version}.md"
[ -s "$release_note" ] || {
  echo "missing or empty $release_note" >&2
  exit 1
}
git ls-files --error-unmatch "$release_note" >/dev/null 2>&1 || {
  echo "$release_note must be tracked" >&2
  exit 1
}
[ "$(head -n 1 "$release_note")" = "# v${version}" ] || {
  echo "$release_note must start with '# v${version}'" >&2
  exit 1
}
for heading in '## 兼容范围' '## 验收结果' '## 已知风险' '## 安装与升级'; do
  grep -Fxq "$heading" "$release_note" || {
    echo "$release_note is missing heading: $heading" >&2
    exit 1
  }
done
manifest="docs/development/acceptance/v${version}/manifest.json"
grep -Fq "../development/acceptance/v${version}/manifest.json" "$release_note" || {
  echo "$release_note does not link $manifest" >&2
  exit 1
}
if grep -Eiq 'TODO|TBD|待补|Unreleased|开发中' "$release_note"; then
  echo "$release_note still contains placeholder text" >&2
  exit 1
fi

go run ./cmd/release-evidence-check -manifest "$manifest" -tag "$release_tag"

artifact_dir="$(mktemp -d)"
trap 'rm -rf "$artifact_dir"' EXIT
CHECK_ARTIFACT_DIR="$artifact_dir" bash scripts/check.sh
go run ./cmd/release-evidence-check \
  -manifest "$manifest" \
  -tag "$release_tag" \
  -artifacts "$artifact_dir"

if [ "${REQUIRE_TAG_AT_HEAD:-0}" = "1" ]; then
  git tag --points-at HEAD | grep -Fxq "$release_tag" || {
    echo "$release_tag does not point at HEAD" >&2
    exit 1
  }
fi
echo "release gate passed for $release_tag"
