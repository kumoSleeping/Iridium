#!/bin/sh
# Prints the version of the tree: the tag itself on a tagged, clean commit (v0.1.0), else one past
# the last tag as a pre-release (v0.1.1-dev.3+g1db5968, .dirty when changed), so a build from source
# is newer than the release it came after and older than the next one (internal/update.Newer).
# "dev" with no tag or no git. scripts/next-version, if there and newer than the last tag, names the
# release being worked toward instead (0.2.0 → v0.2.0-dev.3+g1db5968).
cd "$(dirname "$0")/.."
desc=$(git describe --tags --match 'v[0-9]*' --long --dirty 2>/dev/null) || { echo dev; exit 0; }
dirty=
case "$desc" in *-dirty) dirty=.dirty desc=${desc%-dirty} ;; esac
hash=${desc##*-}
rest=${desc%-*}
n=${rest##*-}
tag=${rest%-*}
if [ "$n" = 0 ] && [ -z "$dirty" ]; then
	echo "$tag"
	exit 0
fi
core=${tag#v}
core=${core%%-*}
major=${core%%.*}
minor=${core#*.}
minor=${minor%%.*}
patch=${core##*.}
next=$(cat scripts/next-version 2>/dev/null || true)
if [ -n "$next" ] && [ "$(printf '%s\n%s\n' "$core" "$next" | sort -V | tail -n1)" = "$next" ] && [ "$next" != "$core" ]; then
	echo "v$next-dev.$n+$hash$dirty"
	exit 0
fi
echo "v$major.$minor.$((patch + 1))-dev.$n+$hash$dirty"
