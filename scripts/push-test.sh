#!/bin/sh
# Builds this tree and puts it on the test node (another account, by default ir on this machine,
# https://ir-test.kumo.ltd) over ssh: the binary in ~/.local/opt/iridium there, run once, then the
# service restarted (holders keep running). The first push installs it: a config on its own port,
# the unit, ir linked. The main node runs releases only and is never pushed to.
#
# A local test account that is not there yet is made first (sudo asks for your password once):
# the user, linger, this account's key let in over ssh, Claude Code installed in it. Logging in to
# the agents (Claude Code against DeepSeek, Codex) and ir public are yours to do there.
#
#   TARGET   ssh destination (default ir@localhost)
#   KEY      ssh identity (default ~/.ssh/ir_local_ed25519 when there)
#   PORT     the test node's port on its first push (default 7811)
#   NO_WEB=1 keep the web build already in internal/webui/dist
set -eu
cd "$(dirname "$0")/.."
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
TARGET=${TARGET:-ir@localhost}
PORT=${PORT:-7811}
KEY=${KEY:-$HOME/.ssh/ir_local_ed25519}
set -- -o BatchMode=yes -o StrictHostKeyChecking=accept-new
[ -f "$KEY" ] && set -- "$@" -i "$KEY" -o IdentitiesOnly=yes
version=$(sh scripts/version.sh)

# The test account on this machine, made once.
user=${TARGET%%@*}
host=${TARGET#*@}
if [ "$host" = localhost ] || [ "$host" = 127.0.0.1 ]; then
	if ! id "$user" >/dev/null 2>&1 || ! ssh "$@" "$TARGET" true 2>/dev/null; then
		echo "push-test: setting up the account $user (sudo asks for your password)"
		[ -f "$KEY" ] || ssh-keygen -q -t ed25519 -N "" -C "$(id -un)@$(hostname) -> $TARGET" -f "$KEY"
		id "$user" >/dev/null 2>&1 || sudo useradd -m -s /bin/bash "$user"
		sudo loginctl enable-linger "$user"
		home=$(getent passwd "$user" | cut -d: -f6)
		sudo install -d -m 700 -o "$user" -g "$user" "$home/.ssh"
		if ! sudo grep -qF "$(cut -d' ' -f2 "$KEY.pub")" "$home/.ssh/authorized_keys" 2>/dev/null; then
			sudo sh -c "cat '$KEY.pub' >> '$home/.ssh/authorized_keys'"
		fi
		sudo chown "$user:$user" "$home/.ssh/authorized_keys"
		sudo chmod 600 "$home/.ssh/authorized_keys"
		set -- -o BatchMode=yes -o StrictHostKeyChecking=accept-new -i "$KEY" -o IdentitiesOnly=yes
		ssh "$@" "$TARGET" 'test -x ~/.local/bin/claude || curl -fsSL https://claude.ai/install.sh | bash'
	fi
fi

if [ "${NO_WEB:-}" != 1 ]; then
	(cd web && npm ci --no-audit --no-fund >/dev/null && npm run build >/dev/null)
fi
[ -f internal/webui/dist/index.html ] || { echo "push-test: no web build in internal/webui/dist" >&2; exit 1; }
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT
CGO_ENABLED=0 "$GO" build -trimpath \
	-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
	-o "$tmp/iridium" ./cmd/iridium
echo "push-test: $version ($(du -h "$tmp/iridium" | cut -f1)) to $TARGET"

ssh "$@" "$TARGET" 'mkdir -p ~/.local/opt/iridium && cat > ~/.local/opt/iridium/.iridium-push && chmod 755 ~/.local/opt/iridium/.iridium-push' <"$tmp/iridium"
ssh "$@" "$TARGET" PORT="$PORT" sh -s <<'REMOTE'
set -eu
opt=$HOME/.local/opt/iridium
"$opt/.iridium-push" version
mv -f "$opt/.iridium-push" "$opt/iridium"
export XDG_RUNTIME_DIR=${XDG_RUNTIME_DIR:-/run/user/$(id -u)}
# The agents' PATH, which the unit keeps: Claude Code installs itself in ~/.local/bin.
export PATH=$HOME/.local/bin:$PATH
cfg=${XDG_CONFIG_HOME:-$HOME/.config}/iridium/config.json
if [ ! -f "$cfg" ]; then
	mkdir -p "$(dirname "$cfg")"
	printf '{\n  "listen": "127.0.0.1:%s"\n}\n' "$PORT" >"$cfg"
	chmod 600 "$cfg"
fi
if systemctl --user is-active -q iridium.service; then
	systemctl --user restart iridium.service
else
	"$opt/iridium" install -now
fi
sleep 1
systemctl --user is-active iridium.service
REMOTE
