#!/bin/sh
# Builds Iridium and installs it for this user: the binary in
# ~/.local/opt/iridium, ir linked in ~/.local/bin, and the user unit iridium.service through
# `iridium install` (config ~/.config/iridium, state ~/.local/state/iridium).
#
# It starts nothing: a service that already runs is restarted onto the new binary, one that does
# not is left for you (systemctl --user enable --now iridium.service).
#
#   PREFIX, BIN             where the binary and the ir link go
#   IRIDIUM_NO_SYSTEMCTL=1  touch no systemd at all (no daemon-reload, no restart)
set -eu
cd "$(dirname "$0")/.."

GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
PREFIX=${PREFIX:-$HOME/.local/opt/iridium}
BIN=${BIN:-$HOME/.local/bin}
version=$(git describe --tags --always --dirty 2>/dev/null || echo dev)

if [ -f web/package.json ]; then
	(cd web && npm ci && npm run build)
fi

mkdir -p "$PREFIX" "$BIN"
CGO_ENABLED=0 "$GO" build -trimpath \
	-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
	-o "$PREFIX/iridium.new" ./cmd/iridium
mv -f "$PREFIX/iridium.new" "$PREFIX/iridium"
ln -sfn "$PREFIX/iridium" "$BIN/ir"
echo "installed $("$PREFIX/iridium" version) at $PREFIX/iridium; $BIN/ir -> it"

if [ "${IRIDIUM_NO_SYSTEMCTL:-}" = 1 ]; then
	"$PREFIX/iridium" install -reload=false
	exit 0
fi
"$PREFIX/iridium" install
if systemctl --user is-active --quiet iridium.service 2>/dev/null; then
	systemctl --user restart iridium.service
	echo "restarted iridium.service"
fi
