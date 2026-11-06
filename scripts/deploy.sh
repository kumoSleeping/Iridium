#!/bin/sh
# Builds Iridium and installs it for this user: the web app (web/, npm) built into the binary, the
# binary in ~/.local/opt/iridium, ir linked in ~/.local/bin, and the user unit iridium.service
# through `iridium install` (config ~/.config/iridium/config.json, state ~/.local/state/iridium).
#
# It starts nothing: a service that already runs is restarted onto the new binary, one that does
# not is left for you (systemctl --user enable --now iridium.service).
#
#   PREFIX, BIN             where the binary and the ir link go
#   CONFIG                  the config file the unit reads (default ~/.config/iridium/config.json)
#   UNIT                    the unit's name (default iridium; tests use iridium-test-*)
#   UNIT_ENV                more NAME=VALUE words the service runs with, space-separated
#   IRIDIUM_NO_SYSTEMCTL=1  touch no systemd at all (no daemon-reload, no restart)
#   NO_WEB=1                keep the web build already in internal/webui/dist
set -eu
cd "$(dirname "$0")/.."

GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
PREFIX=${PREFIX:-$HOME/.local/opt/iridium}
BIN=${BIN:-$HOME/.local/bin}
UNIT=${UNIT:-iridium}
version=$(sh scripts/version.sh)

if [ "${NO_WEB:-}" != 1 ] && [ -f web/package.json ]; then
	(cd web && npm ci --no-audit --no-fund && npm run build)
fi
if [ ! -f internal/webui/dist/index.html ]; then
	echo "deploy: no web build in internal/webui/dist (run npm run build in web/)" >&2
	exit 1
fi

mkdir -p "$PREFIX" "$BIN"
CGO_ENABLED=0 "$GO" build -trimpath \
	-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
	-o "$PREFIX/iridium.new" ./cmd/iridium
mv -f "$PREFIX/iridium.new" "$PREFIX/iridium"
ln -sfn "$PREFIX/iridium" "$BIN/ir"
echo "installed $("$PREFIX/iridium" version) at $PREFIX/iridium; $BIN/ir -> it"

set -- -unit "$UNIT" -prefix "$PREFIX" -bin "$BIN"
if [ -n "${CONFIG:-}" ]; then
	set -- "$@" -config "$CONFIG"
fi
for e in ${UNIT_ENV:-}; do
	set -- "$@" -env "$e"
done
if [ "${IRIDIUM_NO_SYSTEMCTL:-}" = 1 ]; then
	"$PREFIX/iridium" install -reload=false "$@"
	exit 0
fi
# Keep the user's services running without a login and start them at boot. Enabling it for
# yourself needs no root on some systems (polkit); otherwise say what to run.
if [ "$(loginctl show-user "$(id -un)" -p Linger --value 2>/dev/null)" != yes ]; then
	if loginctl enable-linger 2>/dev/null; then
		echo "linger: on"
	else
		echo "linger: off; needs root once: sudo loginctl enable-linger $(id -un)" >&2
	fi
fi
"$PREFIX/iridium" install "$@"
if systemctl --user is-active --quiet "$UNIT.service" 2>/dev/null; then
	systemctl --user restart "$UNIT.service"
	echo "restarted $UNIT.service"
fi
