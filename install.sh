#!/bin/sh
# backup-cli installer. Linux only (amd64, arm64). Verifies SHA-256 before install.
set -eu

REPO=drizki/backup-cli
# Downloads live at $BASE/<tag>/<file>. Override for tests or mirrors.
BASE=${BACKUP_CLI_RELEASE_BASE_URL:-https://github.com/$REPO/releases/download}
LATEST_URL=${BACKUP_CLI_LATEST_URL:-https://github.com/$REPO/releases/latest}

usage() {
	cat <<USAGE
Usage: install.sh [--version vX.Y.Z] [--prefix DIR] [--uninstall] [--help]

  --version vX.Y.Z  install this release tag (default: latest)
  --prefix DIR      install to DIR/bin (default: /usr/local)
  --uninstall       remove DIR/bin/backup-cli
  --help            show this help

Environment:
  BACKUP_CLI_RELEASE_BASE_URL  download base (default: GitHub releases)
USAGE
}

die() {
	echo "install.sh: $*" >&2
	exit 1
}

sha256_of() {
	if command -v sha256sum >/dev/null 2>&1; then
		sha256sum "$1" | cut -d' ' -f1
		return
	fi
	if command -v shasum >/dev/null 2>&1; then
		shasum -a 256 "$1" | cut -d' ' -f1
		return
	fi
	die "need sha256sum or shasum"
}

detect_arch() {
	[ "$(uname -s)" = Linux ] || die "unsupported OS: $(uname -s) (Linux only)"
	case "$(uname -m)" in
	x86_64 | amd64) echo amd64 ;;
	aarch64 | arm64) echo arm64 ;;
	*) die "unsupported architecture: $(uname -m)" ;;
	esac
}

latest_tag() {
	url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "$LATEST_URL") ||
		die "could not resolve latest release"
	tag=${url##*/}
	echo "$tag"
}

check_tag() {
	case "$1" in
	v[0-9]*.[0-9]*.[0-9]*) ;;
	*) die "invalid version '$1' (want vMAJOR.MINOR.PATCH)" ;;
	esac
	case "$1" in
	*[!0-9v.]*) die "invalid version '$1' (want vMAJOR.MINOR.PATCH)" ;;
	esac
}

install_release() {
	command -v curl >/dev/null 2>&1 || die "curl is required"
	arch=$(detect_arch)
	[ -n "$version" ] || version=$(latest_tag)
	check_tag "$version"
	file="backup-cli_${version}_linux_${arch}.tar.gz"

	tmp=$(mktemp -d)
	trap 'rm -rf "$tmp"' EXIT
	echo "downloading $file"
	curl -fsSL -o "$tmp/$file" "$BASE/$version/$file" || die "download failed: $BASE/$version/$file"
	curl -fsSL -o "$tmp/checksums.txt" "$BASE/$version/checksums.txt" || die "download failed: checksums.txt"

	want=$(awk -v f="$file" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "no checksum entry for $file; aborting"
	got=$(sha256_of "$tmp/$file")
	[ "$want" = "$got" ] || die "checksum mismatch for $file; aborting"
	echo "checksum ok"

	mkdir "$tmp/x"
	tar -xzf "$tmp/$file" -C "$tmp/x" backup-cli || die "archive has no backup-cli binary"
	install -d "$prefix/bin"
	install -m 0755 "$tmp/x/backup-cli" "$prefix/bin/backup-cli"
	echo "installed $prefix/bin/backup-cli ($version)"
}

uninstall_release() {
	if [ ! -e "$prefix/bin/backup-cli" ]; then
		echo "nothing to remove at $prefix/bin/backup-cli"
		return
	fi
	rm -f "$prefix/bin/backup-cli"
	echo "removed $prefix/bin/backup-cli"
	echo "state dir, jobs and timers are untouched; use 'backup-cli job rm' first to remove them"
}

version=
prefix=/usr/local
uninstall=0
while [ $# -gt 0 ]; do
	case "$1" in
	--version)
		[ $# -ge 2 ] || die "--version needs a value"
		version=$2
		shift 2
		;;
	--prefix)
		[ $# -ge 2 ] || die "--prefix needs a value"
		prefix=$2
		shift 2
		;;
	--uninstall)
		uninstall=1
		shift
		;;
	--help | -h)
		usage
		exit 0
		;;
	*)
		usage >&2
		die "unknown option: $1"
		;;
	esac
done

if [ "$uninstall" = 1 ]; then
	uninstall_release
else
	install_release
fi
