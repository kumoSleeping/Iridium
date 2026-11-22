#!/bin/sh
# Installs ir, Iridium's program, on this computer (macOS, Linux; docs/exec.md "安装"). It only
# installs (or updates); what follows `sh -s --` is then run as ir:
#   curl -fsSL <this script> | sh                                installs ir
#   curl -fsSL <this script> | sh -s -- start                    then: this computer's Iridium, as a service
#   curl -fsSL <this script> | sh -s -- start --no-server --join <address>   then: an executor of that one
# Iridium {{RELEASE}}, from {{BASE}}, each file checked against that release's SHA256SUMS. In
# ~/.local/opt/iridium, ir in ~/.local/bin; on macOS with a screen, Iridium.app in /Applications
# (that one program for both); on a Linux desktop, an entry in its menu.
set -eu
base='{{BASE}}'
release='{{RELEASE}}'
origin='{{ORIGIN}}'
case "$(uname -s)" in
Darwin) os=darwin ;;
Linux) os=linux ;;
*) echo "不支持的系统：$(uname -s)" >&2; exit 1 ;;
esac
case "$(uname -m)" in
x86_64 | amd64) arch=amd64 ;;
arm64 | aarch64) arch=arm64 ;;
*) echo "不支持的架构：$(uname -m)" >&2; exit 1 ;;
esac
prefix="$HOME/.local/opt/iridium"
bindir="$HOME/.local/bin"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

sha() {
	if command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | cut -d' ' -f1; else shasum -a 256 "$1" | cut -d' ' -f1; fi
}
# want is file's sum in the release's SHA256SUMS; "" when it is not there.
want() { awk -v f="$1" '$2 == f || $2 == "*" f { print $1; exit }' "$tmp/SHA256SUMS"; }
# get fetches file of the release into $tmp, its progress shown, and checks it.
get() {
	curl -fL --progress-bar -o "$tmp/$1" "$base$1"
	if [ "$(sha "$tmp/$1")" != "$(want "$1")" ]; then
		echo "$1 与 SHA256SUMS 不符，未安装" >&2
		exit 1
	fi
}

curl -fsSL -o "$tmp/SHA256SUMS" "${base}SHA256SUMS"
echo "安装 Iridium ${release}（$os/${arch}）"
mkdir -p "$prefix"
app=
zip="iridium-app-darwin-$arch.zip"
if [ "$os" = darwin ] && [ -z "${SSH_CONNECTION:-}" ] && [ -n "$(want "$zip")" ]; then
	# The app, one program for the window and the rest: ir is the one in it.
	get "$zip"
	apps=/Applications
	[ -w "$apps" ] || { apps="$HOME/Applications"; mkdir -p "$apps"; }
	mkdir "$tmp/app"
	ditto -x -k "$tmp/$zip" "$tmp/app"
	rm -rf "$apps/Iridium.app"
	mv "$tmp/app/Iridium.app" "$apps/Iridium.app"
	app="$apps/Iridium.app"
	ln -sfn "$app/Contents/MacOS/iridium" "$prefix/iridium"
else
	archive="iridium-$os-$arch.tar.gz"
	get "$archive"
	mkdir "$tmp/x"
	tar -xzf "$tmp/$archive" -C "$tmp/x"
	chmod 755 "$tmp/x/iridium"
	# A symlink (to an app installed before) or the running file: replaced, not written into.
	mv -f "$tmp/x/iridium" "$prefix/iridium.new"
	rm -f "$prefix/iridium"
	mv -f "$prefix/iridium.new" "$prefix/iridium"
	[ ! -f "$tmp/x/icon.png" ] || cp "$tmp/x/icon.png" "$prefix/icon.png"
fi

# ir: a link to it, unless an ir of another kind is there.
mkdir -p "$bindir"
if [ -e "$bindir/ir" ] && [ ! -L "$bindir/ir" ]; then
	echo "$bindir/ir 已存在且不是链接，未替换" >&2
else
	ln -sfn "$prefix/iridium" "$bindir/ir"
fi

# A Linux desktop: Iridium in its menu.
if [ "$os" = linux ] && { [ -n "${DISPLAY:-}" ] || [ -n "${WAYLAND_DISPLAY:-}" ]; }; then
	d="${XDG_DATA_HOME:-$HOME/.local/share}/applications"
	mkdir -p "$d"
	cat >"$d/iridium.desktop" <<DESKTOP
[Desktop Entry]
Type=Application
Name=Iridium
Exec=$prefix/iridium
Icon=$prefix/icon.png
Terminal=false
Categories=Development;
DESKTOP
fi

case ":$PATH:" in
*":$bindir:"*) ;;
*)
	if [ "$os" = darwin ]; then
		# macOS's zsh has no ~/.local/bin on its PATH.
		grep -qs '\.local/bin' "$HOME/.zprofile" || printf '\nexport PATH="$HOME/.local/bin:$PATH"\n' >>"$HOME/.zprofile"
		echo "ir 在新开的终端里可用（已写入 ~/.zprofile）"
	else
		echo "把 $bindir 加入 PATH 后可直接运行 ir"
	fi
	;;
esac

if [ $# -gt 0 ]; then
	exec "$prefix/iridium" ir "$@"
fi
echo "已安装：$prefix/iridium${app:+（${app}）}"
echo "接下来："
echo "  ir start                      本机运行 Iridium（服务，开机自启）"
if [ -n "$origin" ]; then
	echo "  ir start --no-server --join $origin   只当 $origin 的执行器"
else
	echo "  ir start --no-server --join <地址>     只当那台的执行器"
fi
echo "  ir status                     查看状态"
