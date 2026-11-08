#!/bin/sh
# Builds this tree and puts it on the test node (by default the account ir on this machine,
# https://ir-test.kumo.ltd, 127.0.0.1:7811). The building is all this script does; the binary
# does the rest: iridium node new makes the account a node the first time (root once: sudo, or su
# with the root password), iridium push sends the binary there and restarts it after. The main node runs
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
CGO_ENABLED=0 "$GO" build -trimpath \
	-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
	-o "$tmp/iridium-qq" ./cmd/iridium-qq
echo "push-test: $version ($(du -h "$tmp/iridium" | cut -f1))"

if id "$NODE" >/dev/null 2>&1 && [ -f "$KEY" ] &&
	ssh -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "$KEY" -o IdentitiesOnly=yes "$NODE@localhost" true 2>/dev/null; then
	"$tmp/iridium" push -i "$KEY" "$NODE@localhost"
else
	"$tmp/iridium" node new "$NODE" -listen "127.0.0.1:$PORT"
fi

# The QQ plugin goes beside it as iridium-qq.dist: a development build has no release to install it
# from, so 终端管理's 安装 takes this one. One installed already is replaced, its service restarted.
ssh -o BatchMode=yes -i "$KEY" -o IdentitiesOnly=yes "$NODE@localhost" '
	set -eu
	opt=$HOME/.local/opt/iridium
	cat > "$opt/.iridium-qq.push" && chmod 755 "$opt/.iridium-qq.push" && mv -f "$opt/.iridium-qq.push" "$opt/iridium-qq.dist"
	if [ -e "$opt/iridium-qq" ]; then
		cp -f "$opt/iridium-qq.dist" "$opt/.iridium-qq.new" && mv -f "$opt/.iridium-qq.new" "$opt/iridium-qq"
		export XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}
		if systemctl --user is-active -q iridium-qq.service 2>/dev/null; then systemctl --user restart iridium-qq.service; fi
	fi
	echo "push-test: iridium-qq staged"' <"$tmp/iridium-qq"
