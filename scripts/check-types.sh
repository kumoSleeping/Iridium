#!/bin/sh
# web/src/api/types.gen.ts is generated from internal/api (go generate ./internal/api) and
# committed: regenerate it and fail when it differs from the tree's, i.e. a change to the Go
# types went in without the TypeScript ones. CI, release.yml and preflight.sh run it.
set -eu
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
gen=web/src/api/types.gen.ts
tmp=$(mktemp)
trap 'cp "$tmp" "$gen"; rm -f "$tmp"' EXIT
cp "$gen" "$tmp"
"$GO" generate ./internal/api
if ! cmp -s "$tmp" "$gen"; then
	echo "check-types: $gen is stale; run go generate ./internal/api and commit it:"
	diff -u --label "$gen (committed)" --label "$gen (generated)" "$tmp" "$gen" || true
	exit 1
fi
