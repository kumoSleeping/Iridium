#!/bin/sh
# Prints the version of the tree: the tag itself on a tagged, clean commit (v0.1.0), else one past
# the last tag as a pre-release (v0.1.1-dev.3+g1db5968, .dirty when changed), so a build from source
# is newer than the release it came after and older than the next one (internal/update.Newer).
# "dev" with no tag or no git.
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
echo "v$major.$minor.$((patch + 1))-dev.$n+$hash$dirty"
