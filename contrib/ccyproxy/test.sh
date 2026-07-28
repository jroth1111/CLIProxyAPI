#!/bin/sh
set -eu

script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
launcher="$script_dir/ccyproxy.zsh"
installer="$script_dir/install.sh"

zsh -n "$launcher"
sh -n "$installer"

workdir=$(mktemp -d "${TMPDIR:-/tmp}/ccyproxy-launcher-test.XXXXXX")
trap 'rm -rf "$workdir"' EXIT HUP INT TERM
HOME="$workdir/home" "$installer" --target "$workdir/installed/ccyproxy.zsh" >/dev/null
cmp "$launcher" "$workdir/installed/ccyproxy.zsh"

zsh -c '
	source "$1"
	[[ "$CCYPROXY_LAUNCHER_VERSION" == ccyfix.4 ]]
	(( $+functions[ccyproxy] ))
	(( $+functions[_ccyproxy_unset_provider_env] ))
' -- "$workdir/installed/ccyproxy.zsh"

if grep -q 'legacy-372k-calibrated' "$launcher"; then
	printf '%s\n' 'launcher test: legacy incomplete-metadata fallback must remain absent' >&2
	exit 1
fi

for required in capacity_complete capacity_blockers credential_availability CLAUDE_CODE_MAX_CONTEXT_TOKENS CLAUDE_CODE_AUTO_COMPACT_WINDOW; do
	if ! grep -q "$required" "$launcher"; then
		printf 'launcher test: missing contract field %s\n' "$required" >&2
		exit 1
	fi
done
