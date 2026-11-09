#!/usr/bin/env bash
# Check that README.md and docs/*.md only mention backup-cli commands and flags
# that the real binary knows, and that docs/commands.md covers every flag.
#
# Usage: scripts/check-docs.sh [path/to/backup-cli]
# Without an argument the binary is built into a temporary directory.
set -euo pipefail

root=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$root"

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

bin=${1:-}
if [ -z "$bin" ]; then
	bin=$tmp/backup-cli
	go build -o "$bin" ./cmd/backup-cli
fi

# Flags that belong to other tools and may appear in the docs.
external=" exclude link-dest no-block prefix repo uninstall version ignore-missing "
# Global flags (accepted before the command).
globals="state-dir unit-dir verbose"

# flag_names: flag names printed in a help page, one per line.
flag_names() {
	"$bin" help "$@" | sed -nE 's/^[[:space:]]+-{1,2}([a-z][a-z-]*).*/\1/p'
}

# block_names: first words of the indented lines under a heading of a help page.
block_names() {
	local heading=$1
	shift
	"$bin" help "$@" | awk -v h="$heading" '
		$0 == h { on = 1; next }
		on && /^$/ { exit }
		on { print $1 }'
}

commands=$(block_names "Commands:")
lists=$(block_names "Subcommands:" list)
jobs=$(block_names "Subcommands:" job)

# valid_flags CMD: flags accepted by a command, space separated.
valid_flags() {
	local out="$globals"
	case $1 in
	list) out="$out $(flag_names list jobs) $(flag_names list runs) $(flag_names list backups)" ;;
	job) out="$out $(flag_names job rm)" ;;
	*) out="$out $(flag_names "$1")" ;;
	esac
	echo "$out" | tr '\n' ' '
}

all_flags=$globals
for c in $commands; do
	[ "$c" != help ] || continue
	all_flags="$all_flags $(valid_flags "$c")"
done

fail=0
span_re=$(printf '\140[^\140]+\140')
report() {
	echo "$1:$2: $3" >&2
	fail=1
}

has_word() { # has_word "list of words" word
	case " $1 " in *" $2 "*) return 0 ;; esac
	return 1
}

is_command() { has_word "$(echo "$commands" | tr '\n' ' ')" "$1"; }

# find_command UNIT: set found to the backup-cli command used in UNIT (empty
# when UNIT has no invocation) and bad to a message when a command or
# subcommand is unknown.
found=""
bad=""
find_command() {
	local unit=$1 tok
	found=""
	bad=""
	local -a toks
	read -ra toks <<<"$unit"
	local i=0 n=${#toks[@]}
	while [ "$i" -lt "$n" ]; do
		tok=${toks[$i]}
		[ "${tok##*/}" = backup-cli ] && break
		i=$((i + 1))
	done
	[ "$i" -lt "$n" ] || return 0
	i=$((i + 1))
	while [ "$i" -lt "$n" ]; do
		case ${toks[$i]} in
		--state-dir | --unit-dir) i=$((i + 2)) ;;
		--verbose | -v) i=$((i + 1)) ;;
		*) break ;;
		esac
	done
	[ "$i" -lt "$n" ] || return 0
	tok=${toks[$i]}
	tok=${tok%%[\`.,;\)]*}
	[[ $tok =~ ^[a-z]+$ ]] || return 0
	if [ "$tok" = help ]; then
		found=help
		return 0
	fi
	if ! is_command "$tok"; then
		bad="unknown command 'backup-cli $tok'"
		return 0
	fi
	found=$tok
	check_sub "$tok" "${toks[$((i + 1))]:-}"
}

check_sub() { # check_sub command nextword
	local cmd=$1 next=$2 s
	case $cmd in list | job) ;; *) return 0 ;; esac
	next=${next%%[\`.,;\)]*}
	[[ $next =~ ^[a-z|]+$ ]] || return 0
	local valid=$lists
	[ "$cmd" = job ] && valid=$jobs
	for s in ${next//|/ }; do
		if ! has_word "$(echo "$valid" | tr '\n' ' ')" "$s"; then
			bad="unknown subcommand 'backup-cli $cmd $s'"
		fi
	done
}

# check_unit FILE LINENO UNIT SECTION_CMD CONT_CMD: verify one code unit and
# set unit_cmd to the command that applies to a continuation line.
unit_cmd=""
check_unit() {
	local file=$1 line=$2 unit=$3 section=$4 cont=$5
	local cmd first flag valid
	find_command "$unit"
	cmd=$found
	if [ -n "$bad" ]; then report "$file" "$line" "$bad"; fi
	if [ -z "$cmd" ]; then
		first=${unit%% *}
		if is_command "$first"; then cmd=$first; fi
	fi
	[ -n "$cmd" ] || cmd=$cont
	[ -n "$cmd" ] || cmd=$section
	[ "$cmd" != help ] || cmd=""
	for flag in $(grep -oE -- '(^|[^[:alnum:]-])--[a-z][a-z-]*' <<<"$unit" | grep -oE -- '--[a-z-]+' || true); do
		flag=${flag#--}
		if [ -n "$cmd" ]; then
			valid=$(valid_flags "$cmd")
		else
			valid="$all_flags$external"
		fi
		has_word "$valid" "$flag" || report "$file" "$line" "flag --$flag is not accepted by 'backup-cli ${cmd:-any command}'"
	done
	unit_cmd=$cmd
}

check_file() {
	local file=$1 line n=0 in_block=0 section="" cont="" span
	local -a lines
	mapfile -t lines <"$file"
	for line in "${lines[@]}"; do
		n=$((n + 1))
		case $line in
		'```'*)
			in_block=$((1 - in_block))
			cont=""
			continue
			;;
		'## '*)
			section=${line#\#\# }
			section=${section%% *}
			is_command "$section" || section=""
			[ "$section" != help ] || section=""
			continue
			;;
		esac
		if [ "$in_block" = 1 ]; then
			check_unit "$file" "$n" "$line" "$section" "$cont"
			if [[ $line == *\\ ]]; then cont=$unit_cmd; else cont=""; fi
			continue
		fi
		while IFS= read -r span; do
			[ -n "$span" ] || continue
			check_unit "$file" "$n" "${span//\`/}" "$section" ""
		done < <(grep -oE "$span_re" <<<"$line" || true)
	done
}

for f in README.md docs/*.md CONTRIBUTING.md; do
	check_file "$f"
done

# Reverse check: docs/commands.md documents every command, and every flag of a
# command in that command's own section.
section_text() { # section_text command
	local heading=$1
	[ "$heading" != job ] || heading="job rm"
	awk -v h="## $heading" '
		/^## / { on = (index($0, h) == 1) }
		on { print }' docs/commands.md
}
for c in $commands; do
	[ "$c" != help ] || continue
	text=$(section_text "$c")
	if [ -z "$text" ]; then
		report docs/commands.md 0 "command '$c' has no section"
		continue
	fi
	for f in $(valid_flags "$c"); do
		has_word "$globals" "$f" && continue
		grep -q -- "--$f" <<<"$text" || report docs/commands.md 0 "flag --$f of '$c' is not documented in its section"
	done
done
for f in $globals; do
	grep -q -- "--$f" docs/commands.md || report docs/commands.md 0 "global flag --$f is not documented"
done

if [ "$fail" -ne 0 ]; then
	echo "docs check failed" >&2
	exit 1
fi
echo "docs ok"
