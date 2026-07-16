#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

for command in go git gofmt shellcheck actionlint; do
  command -v "$command" >/dev/null 2>&1 || {
    echo "required check command is missing: $command" >&2
    exit 1
  }
done

git diff --check
git diff --cached --check
unformatted="$(
  while IFS= read -r -d '' file; do
    gofmt -l -- "$file"
  done < <(git ls-files -co --exclude-standard -z -- '*.go')
)"
if [ -n "$unformatted" ]; then
  echo "gofmt is required for:" >&2
  echo "$unformatted" >&2
  exit 1
fi

bash scripts/check-version.sh
go mod verify
go mod tidy -diff
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...

shellcheck -x scripts/*.sh deploy/remnawave-node.openrc
for script in scripts/*.sh; do
  bash -n "$script"
done
sh -n deploy/remnawave-node.openrc
actionlint
bash scripts/check-supply-chain.sh

if command -v govulncheck >/dev/null 2>&1; then
  govulncheck ./...
elif [ "${REQUIRE_GOVULNCHECK:-0}" = "1" ]; then
  echo "govulncheck is required but not installed" >&2
  exit 1
fi

if [ -n "${CHECK_ARTIFACT_DIR:-}" ]; then
  artifact_dir="$CHECK_ARTIFACT_DIR"
  mkdir -p "$artifact_dir"
else
  artifact_dir="$(mktemp -d)"
  trap 'rm -rf "$artifact_dir"' EXIT
fi
bash scripts/build-release-binaries.sh "$artifact_dir"

echo "portable repository checks passed"
