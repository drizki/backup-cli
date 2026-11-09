#!/bin/sh
# Fail on unfinished-work markers, non-allowlisted t.Skip, and Go package
# directories that have no test file.
set -eu

# Environment-precondition skips that are allowed, as "path-suffix:reason".
# Add an entry here only for a missing external binary or a root requirement.
SKIP_ALLOWLIST="
internal/manifest/manifest_test.go:sha256sum binary missing
internal/copyx/copy_test.go:sha256sum binary missing
internal/walk/walk_test.go:running as root
internal/lock/lock_test.go:flock binary missing
internal/engine/run_test.go:sha256sum binary missing
internal/engine/openfail_test.go:running as root
"

status=0
files=$(find . -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort)

# Marker words are assembled so this script does not match itself.
markers="TO""DO|FIX""ME|X""XX|HA""CK"
# shellcheck disable=SC2086
if grep -nE "\\b($markers)\\b" $files /dev/null; then
	echo "unfinished-work markers found" >&2
	status=1
fi

# shellcheck disable=SC2086
for hit in $(grep -n 't\.Skip\(f\|Now\)\?(' $files /dev/null | cut -d: -f1,2 | tr ' ' '_'); do
	file=${hit%%:*}
	rel=${file#./}
	allowed=0
	for entry in $SKIP_ALLOWLIST; do
		case "$entry" in
		"$rel":*) allowed=1 ;;
		esac
	done
	if [ "$allowed" -eq 0 ]; then
		echo "$hit: t.Skip is not allowlisted" >&2
		status=1
	fi
done

for dir in $(for f in $files; do dirname "$f"; done | sort -u); do
	case "$dir" in
	*/testdata*) continue ;;
	esac
	if ! ls "$dir"/*_test.go >/dev/null 2>&1; then
		echo "$dir: package has no _test.go" >&2
		status=1
	fi
done
exit $status
