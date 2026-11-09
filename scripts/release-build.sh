#!/usr/bin/env bash
# Build and package release archives for backup-cli.
#
# Usage: scripts/release-build.sh vMAJOR.MINOR.PATCH [OUTDIR]
#
# Produces, in OUTDIR (default: dist):
#   backup-cli_<tag>_linux_<arch>.tar.gz   (binary, README.md, LICENSE)
#   checksums.txt                          (sha256, sha256sum -c compatible)
#
# Env: ARCHES (default "amd64 arm64"). Runs from any directory; needs go,
# tar, gzip, sha256sum and git (git only for commit and date metadata).
set -euo pipefail

usage() {
	echo "usage: $0 vMAJOR.MINOR.PATCH [OUTDIR]" >&2
	exit 2
}

[ $# -ge 1 ] && [ $# -le 2 ] || usage
tag=$1
semver='^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$'
if ! [[ $tag =~ $semver ]]; then
	echo "error: '$tag' is not a strict SemVer tag (vMAJOR.MINOR.PATCH)" >&2
	exit 2
fi

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
out=${2:-dist}
mkdir -p "$out"
out=$(cd "$out" && pwd)
arches=${ARCHES:-"amd64 arm64"}
pkg=github.com/drizki/backup-cli/internal/version

cd "$root"
commit=$(git rev-parse --short=12 HEAD)
epoch=$(git log -1 --format=%ct)
date=$(date -u -d "@$epoch" +%Y-%m-%dT%H:%M:%SZ)
ldflags="-s -w -X $pkg.Version=$tag -X $pkg.Commit=$commit -X $pkg.Date=$date"

stage=$(mktemp -d)
trap 'rm -rf "$stage"' EXIT

for arch in $arches; do
	name="backup-cli_${tag}_linux_${arch}"
	dir="$stage/$name"
	mkdir -p "$dir"
	CGO_ENABLED=0 GOOS=linux GOARCH="$arch" \
		go build -trimpath -ldflags "$ldflags" -o "$dir/backup-cli" ./cmd/backup-cli
	cp README.md LICENSE "$dir/"
	tar --sort=name --owner=0 --group=0 --numeric-owner --mtime="@$epoch" \
		-C "$dir" -cf - backup-cli README.md LICENSE | gzip -n -9 >"$out/$name.tar.gz"
	echo "built $name.tar.gz"
done

(cd "$out" && sha256sum -- backup-cli_"${tag}"_linux_*.tar.gz >checksums.txt)
echo "wrote $out/checksums.txt"
