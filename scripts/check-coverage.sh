#!/bin/sh
# Enforce coverage gates: overall >= 85%, plus per-package gates for the
# packages that exist. Usage: scripts/check-coverage.sh [coverage.out]
set -eu

profile=${1:-coverage.out}
overall_min=${OVERALL_MIN:-85}
pkg_min=${PKG_MIN:-90}
# Packages that must reach pkg_min once they exist.
gated="pathguard engine copyx manifest journal safedelete retention"
module=github.com/drizki/backup-cli

if [ ! -f "$profile" ]; then
	go test -race -coverprofile="$profile" ./... >/dev/null
fi

status=0
total=$(go tool cover -func="$profile" | awk '/^total:/ {gsub("%","",$3); print $3}')
echo "overall coverage: ${total}% (min ${overall_min}%)"
if ! awk -v t="$total" -v m="$overall_min" 'BEGIN { exit !(t+0 >= m+0) }'; then
	echo "overall coverage below ${overall_min}%" >&2
	status=1
fi

for p in $gated; do
	[ -d "internal/$p" ] || continue
	pct=$(awk -v prefix="$module/internal/$p/" '
		NR > 1 && index($1, prefix) == 1 {
			n = split($1, a, " "); stmts = $2; cnt = $3
			total += stmts; if (cnt > 0) covered += stmts
		}
		END { if (total == 0) print "0"; else printf "%.1f", covered * 100 / total }' "$profile")
	echo "internal/$p coverage: ${pct}% (min ${pkg_min}%)"
	if ! awk -v t="$pct" -v m="$pkg_min" 'BEGIN { exit !(t+0 >= m+0) }'; then
		echo "internal/$p coverage below ${pkg_min}%" >&2
		status=1
	fi
done
exit $status
