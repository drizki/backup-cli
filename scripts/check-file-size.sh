#!/bin/sh
# Fail when a Go file exceeds its line limit: 400 for non-test files, 700 for tests.
set -eu

max_src=${MAX_SRC_LINES:-400}
max_test=${MAX_TEST_LINES:-700}
status=0

for f in $(find . -name '*.go' -not -path './.git/*' -not -path './vendor/*' | sort); do
	lines=$(wc -l <"$f")
	case "$f" in
	*_test.go) limit=$max_test ;;
	*) limit=$max_src ;;
	esac
	if [ "$lines" -gt "$limit" ]; then
		echo "$f: $lines lines (limit $limit)" >&2
		status=1
	fi
done
exit $status
