#!/bin/sh
# Tests for install.sh using a local fake release tree (file:// base URL).
set -eu

here=$(cd "$(dirname "$0")" && pwd)
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

fails=0
pass() { echo "ok   - $1"; }
fail() {
	echo "FAIL - $1"
	fails=$((fails + 1))
}

tag=v1.2.3
rel="$work/rel/$tag"
mkdir -p "$rel" "$work/pkg"
printf '#!/bin/sh\necho fake backup-cli\n' >"$work/pkg/backup-cli"
chmod +x "$work/pkg/backup-cli"
for arch in amd64 arm64; do
	tar -czf "$rel/backup-cli_${tag}_linux_${arch}.tar.gz" -C "$work/pkg" backup-cli
done
(cd "$rel" && sha256sum ./*.tar.gz | sed 's# \./# #' >checksums.txt)
export BACKUP_CLI_RELEASE_BASE_URL="file://$work/rel"

# PATH stubs: fake uname (arch/OS) and fake curl for latest resolution.
stubs="$work/stubs"
mkdir -p "$stubs"
cat >"$stubs/uname" <<'STUB'
#!/bin/sh
case "$1" in
-s) echo "${FAKE_OS:-Linux}" ;;
-m) echo "${FAKE_ARCH:-x86_64}" ;;
esac
STUB
chmod +x "$stubs/uname"
real_curl=$(command -v curl)
cat >"$stubs/curl" <<STUB
#!/bin/sh
for a in "\$@"; do
	if [ "\$a" = "%{url_effective}" ]; then
		echo "https://example.invalid/releases/tag/$tag"
		exit 0
	fi
done
exec "$real_curl" "\$@"
STUB
chmod +x "$stubs/curl"

run() { env PATH="$stubs:$PATH" FAKE_ARCH="${FAKE_ARCH:-x86_64}" FAKE_OS="${FAKE_OS:-Linux}" sh "$here/install.sh" "$@"; }

# 1. good checksum installs
p="$work/p1"
if run --version "$tag" --prefix "$p" >/dev/null 2>&1 && [ -x "$p/bin/backup-cli" ] &&
	[ "$("$p/bin/backup-cli")" = "fake backup-cli" ]; then pass "good checksum installs"; else fail "good checksum installs"; fi

# 2. arm64 detection picks the arm64 archive
p="$work/p2"
if (FAKE_ARCH=aarch64 run --version "$tag" --prefix "$p" >"$work/o2" 2>&1) && grep -q linux_arm64 "$work/o2" && [ -x "$p/bin/backup-cli" ]; then
	pass "aarch64 selects arm64 archive"
else fail "aarch64 selects arm64 archive"; fi

# 3. latest resolves to tag
p="$work/p3"
if run --prefix "$p" >/dev/null 2>&1 && [ -x "$p/bin/backup-cli" ]; then pass "latest resolves tag"; else fail "latest resolves tag"; fi

# 4. tampered tarball aborts, installs nothing
cp -r "$work/rel" "$work/rel-bad"
printf 'tampered' >>"$work/rel-bad/$tag/backup-cli_${tag}_linux_amd64.tar.gz"
p="$work/p4"
if BACKUP_CLI_RELEASE_BASE_URL="file://$work/rel-bad" run --version "$tag" --prefix "$p" 2>&1 | grep -q 'checksum mismatch'; then
	if [ ! -e "$p/bin/backup-cli" ]; then pass "tampered tarball aborts, nothing installed"; else fail "tampered tarball installed something"; fi
else fail "tampered tarball not detected"; fi

# 5. missing checksum entry aborts
cp -r "$work/rel" "$work/rel-miss"
grep -v amd64 "$work/rel/$tag/checksums.txt" >"$work/rel-miss/$tag/checksums.txt"
p="$work/p5"
if BACKUP_CLI_RELEASE_BASE_URL="file://$work/rel-miss" run --version "$tag" --prefix "$p" 2>&1 | grep -q 'no checksum entry'; then
	if [ ! -e "$p/bin/backup-cli" ]; then pass "missing checksum entry aborts"; else fail "missing entry installed something"; fi
else fail "missing checksum entry not detected"; fi

# 6. unsupported arch aborts
p="$work/p6"
if FAKE_ARCH=riscv64 run --version "$tag" --prefix "$p" 2>&1 | grep -q 'unsupported architecture' && [ ! -e "$p/bin/backup-cli" ]; then
	pass "unsupported arch aborts"
else fail "unsupported arch aborts"; fi

# 7. non-Linux aborts
p="$work/p7"
if FAKE_OS=Darwin run --version "$tag" --prefix "$p" 2>&1 | grep -q 'unsupported OS' && [ ! -e "$p/bin/backup-cli" ]; then
	pass "non-Linux aborts"
else fail "non-Linux aborts"; fi

# 8. invalid version rejected
if run --version "1.2.3" --prefix "$work/p8" 2>&1 | grep -q 'invalid version'; then pass "invalid version rejected"; else fail "invalid version rejected"; fi

# 9. uninstall removes
p="$work/p1"
if run --uninstall --prefix "$p" >/dev/null 2>&1 && [ ! -e "$p/bin/backup-cli" ]; then pass "uninstall removes"; else fail "uninstall removes"; fi

# 10. help works
if sh "$here/install.sh" --help | grep -q Usage; then pass "help"; else fail "help"; fi

if [ "$fails" -ne 0 ]; then
	echo "$fails test(s) failed"
	exit 1
fi
echo "all install tests passed"
