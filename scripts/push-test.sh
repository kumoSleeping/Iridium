#!/bin/sh
# Builds this tree and puts it on the test node (by default the account ir on this machine,
# https://ir-test.kumo.ltd, 127.0.0.1:7811). The building is all this script does; the binary
# does the rest: iridium node new makes the account a node the first time (sudo asks for your
# password once), iridium push sends the binary there and restarts it after. The main node runs
# releases only and is never pushed to.
#
#   NODE     the test account (default ir)
#   PORT     its port on the first push (default 7811)
#   NO_WEB=1 keep the web build already in internal/webui/dist
set -eu
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
NODE=${NODE:-ir}
PORT=${PORT:-7811}
KEY=$HOME/.ssh/iridium_${NODE}_ed25519
version=$(sh scripts/version.sh)

if [ "${NO_WEB:-}" != 1 ]; then
	(cd web && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
fi
[ -f internal/webui/dist/index.html ] || { echo "push-test: no web build in internal/webui/dist" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
CGO_ENABLED=0 "$GO" build -trimpath \
	-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
	-o "$tmp/iridium" ./cmd/iridium
echo "push-test: $version ($(du -h "$tmp/iridium" | cut -f1))"

if id "$NODE" >/dev/null 2>&1 && [ -f "$KEY" ] &&
	ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "$KEY" -o IdentitiesOnly=yes "$NODE@localhost" true 2>/dev/null; then
	"$tmp/iridium" push -i "$KEY" "$NODE@localhost"
else
	"$tmp/iridium" node new "$NODE" -listen "127.0.0.1:$PORT"
fi
