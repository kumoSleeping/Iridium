#!/bin/sh
# What CI and release.yml check, on a fresh clone of HEAD, before a push: a file left out of a commit
# (untracked, or caught by .gitignore) fails here, not on GitHub. Then a release is one push:
#
#   scripts/preflight.sh && git push origin main vX.Y.Z
#
# release.yml runs the same checks before it publishes, so there is no CI run to wait for first.
set -eu
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go

# Not in HEAD, so neither checked nor shipped.
dirty=$(git status --porcelain)
[ -z "$dirty" ] || { echo "preflight: not committed (not checked, not shipped):"; echo "$dirty"; }
# Test data .gitignore keeps out (a fake package's node_modules/): the clone below would miss it.
ignored=$(git status --porcelain --ignored=matching | sed -n 's/^!! //p' | grep '/testdata/' || true)
[ -z "$ignored" ] || { echo "preflight: test data ignored by .gitignore:"; echo "$ignored"; }

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
git clone -q . "$tmp/src"
cd "$tmp/src"
# The web app and the Go side at once, as CI's two jobs.
(cd web && npm ci --no-audit --no-fund --prefer-offline --silent && npm run check --silent &&
	npm run build --silent >/dev/null) >"$tmp/web.log" 2>&1 &
web=$!
go_ok=0
{ GO="$GO" scripts/check-types.sh && "$GO" vet ./... && GOOS=darwin GOARCH=arm64 "$GO" vet ./... &&
	"$GO" test ./...; } >"$tmp/go.log" 2>&1 || go_ok=1
web_ok=0
wait $web || web_ok=1
[ $web_ok = 0 ] || { echo "preflight: web:"; cat "$tmp/web.log"; }
[ $go_ok = 0 ] || { echo "preflight: go:"; grep -v '^ok\|no test files' "$tmp/go.log"; }
[ $web_ok = 0 ] && [ $go_ok = 0 ] || exit 1
echo "preflight: ok at $(git rev-parse --short HEAD)"
