#!/bin/sh
# Tries scripts/deploy.sh for real without touching the live setup: a temp prefix, state, home and
# Claude directory, the fake claude (internal/testagent, through a test binary), a user unit named
# iridium-test-deploy-<n> in $XDG_RUNTIME_DIR/systemd/user (not ~/.config) on a random port. The
# holders' units are iridium-test-hold-<n>-*. Nothing of the real iridium.service is touched.
#
#   scripts/test-deploy.sh up [dir]    build, install, start; prints the address and the env to use
#   scripts/test-deploy.sh again [dir] the same unit onto a new build (NO_WEB=1 keeps the web build)
#   scripts/test-deploy.sh down [dir]  stop and remove the units, delete the dir
#
# dir defaults to $TMPDIR/iridium-test-deploy. web/scripts/seed.mjs fills it with sessions.
set -eu
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
cmd=${1:-up}
ROOT=${2:-${TMPDIR:-/tmp}/iridium-test-deploy}
RUNTIME=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}

down() {
	[ -f "$ROOT/env" ] || { echo "nothing at $ROOT"; return 0; }
	. "$ROOT/env"
	systemctl --user stop "$IRIDIUM_TEST_UNIT.service" 2>/dev/null || true
	for u in $(systemctl --user list-units --all --plain --no-legend "$IRIDIUM_TEST_HOLD*" | awk '{print $1}'); do
		systemctl --user stop "$u" 2>/dev/null || true
	done
	rm -f "$RUNTIME/systemd/user/$IRIDIUM_TEST_UNIT.service"
	systemctl --user daemon-reload
	systemctl --user reset-failed "$IRIDIUM_TEST_UNIT.service" 2>/dev/null || true
	rm -rf "$ROOT"
	echo "removed $IRIDIUM_TEST_UNIT and $ROOT"
}

if [ "$cmd" = down ]; then
	down
	exit 0
fi
if [ "$cmd" = again ]; then
	# The same test unit onto a new build: deploy.sh restarts it.
	. "$ROOT/env"
	PREFIX="$ROOT/opt" BIN="$ROOT/bin" CONFIG="$IRIDIUM_CONFIG" UNIT="$IRIDIUM_TEST_UNIT" XDG_CONFIG_HOME="$RUNTIME" \
		UNIT_ENV="HOME=$ROOT/home CODEX_HOME=$ROOT/home/.codex" sh scripts/deploy.sh
	exit 0
fi

[ ! -e "$ROOT" ] || down
n=$(od -An -N2 -tu2 /dev/urandom | tr -d ' ')
port=$((20000 + n % 20000))
unit=iridium-test-deploy-$n
mkdir -p "$ROOT/home/kumo" "$ROOT/claude/projects" "$ROOT/state" "$ROOT/run" "$ROOT/fake" "$ROOT/config"
chmod 700 "$ROOT"

# The fake claude: a test binary of package server runs as it when called claude.
"$GO" test -c -o "$ROOT/fake/agents.test" ./internal/server
ln -sfn "$ROOT/fake/agents.test" "$ROOT/fake/claude"

cat >"$ROOT/config/config.json" <<EOF
{
  "listen": "127.0.0.1:$port",
  "socket": "$ROOT/run/ir.sock",
  "stateDir": "$ROOT/state",
  "claudeHome": "$ROOT/claude",
  "unitPrefix": "iridium-test-hold-$n-",
  "update": {"url": "off"},
  "programs": {"claude": "$ROOT/fake/claude"}
}
EOF

PREFIX="$ROOT/opt" BIN="$ROOT/bin" CONFIG="$ROOT/config/config.json" UNIT="$unit" XDG_CONFIG_HOME="$RUNTIME" \
	UNIT_ENV="HOME=$ROOT/home CODEX_HOME=$ROOT/home/.codex" sh scripts/deploy.sh

cat >"$ROOT/env" <<EOF
IRIDIUM_TEST_UNIT=$unit
IRIDIUM_TEST_HOLD=iridium-test-hold-$n-
IRIDIUM_TEST_BASE=http://127.0.0.1:$port/
IRIDIUM_CONFIG=$ROOT/config/config.json
IRIDIUM_SOCKET=$ROOT/run/ir.sock
IRIDIUM_TEST_HOME=$ROOT/home
IRIDIUM_TEST_CLAUDE=$ROOT/claude
IR=$ROOT/bin/ir
EOF
systemctl --user start "$unit.service"
for i in $(seq 50); do
	curl -sf "http://127.0.0.1:$port/api/health" >/dev/null && break
	sleep 0.2
done
curl -sf "http://127.0.0.1:$port/api/health" >/dev/null || { systemctl --user status "$unit.service" --no-pager || true; exit 1; }
echo "up: $unit on http://127.0.0.1:$port/ (env in $ROOT/env)"
