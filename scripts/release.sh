#!/bin/sh
# Builds the release assets into a directory (the web app must be built already, npm run build in
# web/): for each platform a static, stripped binary packed as iridium-<os>-<arch>.tar.gz (the
# binary, README.md, LICENSE), the official QQ plugin as iridium-qq-<os>-<arch>.tar.gz, SHA256SUMS, and notes.md: the commits since the last tag (what the
# settings page shows), then after the marker internal/update.NotesEnd the sizes and how to install.
#
#   scripts/release.sh v1.2.0 dist
#   PLATFORMS="linux/amd64 linux/arm64"   which to build (default both)
set -eu
cd "$(dirname "$0")/.."
version=${1:?usage: scripts/release.sh <version> <dir>}
out=${2:?usage: scripts/release.sh <version> <dir>}
GO=${GO:-go}
command -v "$GO" >/dev/null 2>&1 || GO=$HOME/.local/bin/go
if [ ! -f internal/webui/dist/index.html ]; then
	echo "release: no web build in internal/webui/dist (run npm run build in web/)" >&2
	exit 1
fi
mkdir -p "$out"
out=$(cd "$out" && pwd)
size() { wc -c <"$1" | tr -d ' '; }
mib() { awk -v n="$1" 'BEGIN { printf "%.1f MiB", n / 1048576 }'; }

prev=$(git describe --tags --abbrev=0 "$version^" 2>/dev/null || true)
{
	git log --no-merges --format='- %s' "${prev:+$prev..}HEAD" 2>/dev/null | head -50
	echo
	echo "<!-- iridium:assets -->"
	echo
	echo "| 平台 | 二进制 | 下载（tar.gz） |"
	echo "|---|---|---|"
} >"$out/notes.md"
for p in ${PLATFORMS:-linux/amd64 linux/arm64}; do
	os=${p%/*} arch=${p#*/}
	name=iridium-$os-$arch
	stage=$(mktemp -d)
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$GO" build -trimpath \
		-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
		-o "$stage/iridium" ./cmd/iridium
	cp README.md "$stage/" 2>/dev/null || true
	cp LICENSE "$stage/" 2>/dev/null || true
	tar -C "$stage" -czf "$out/$name.tar.gz" --owner=0 --group=0 --numeric-owner .
	echo "| $os/$arch | $(mib "$(size "$stage/iridium")") | $(mib "$(size "$out/$name.tar.gz")") |" >>"$out/notes.md"
	rm -rf "$stage"
	# The official QQ plugin: not in Iridium's archive; 终端管理 installs it from here when asked.
	stage=$(mktemp -d)
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$GO" build -trimpath \
		-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
		-o "$stage/iridium-qq" ./cmd/iridium-qq
	tar -C "$stage" -czf "$out/iridium-qq-$os-$arch.tar.gz" --owner=0 --group=0 --numeric-owner .
	rm -rf "$stage"
done
# Mac and Windows: the executor (ir join, docs/exec.md) only, no QQ plugin.
for p in ${EXEC_PLATFORMS:-darwin/arm64 darwin/amd64 windows/amd64 windows/arm64}; do
	os=${p%/*} arch=${p#*/}
	name=iridium-$os-$arch
	bin=iridium
	[ "$os" = windows ] && bin=iridium.exe
	stage=$(mktemp -d)
	CGO_ENABLED=0 GOOS=$os GOARCH=$arch "$GO" build -trimpath \
		-ldflags "-s -w -X github.com/kumoSleeping/iridium/internal/version.Version=$version" \
		-o "$stage/$bin" ./cmd/iridium
	tar -C "$stage" -czf "$out/$name.tar.gz" --owner=0 --group=0 --numeric-owner .
	echo "| $os/$arch（执行端） | $(mib "$(size "$stage/$bin")") | $(mib "$(size "$out/$name.tar.gz")") |" >>"$out/notes.md"
	rm -rf "$stage"
done
(cd "$out" && sha256sum iridium-*.tar.gz >SHA256SUMS)
{
	echo
	echo '安装：`tar xzf iridium-linux-amd64.tar.gz && ./iridium install -now`；之后在设置里或 `ir update` 更新。'
	echo 'Mac、Windows 只做执行端：解压后 `iridium join https://<你的 Iridium>`，在网页上核对配对码后允许；`--install` 登录时自动启动。'
} >>"$out/notes.md"
cat "$out/notes.md"
