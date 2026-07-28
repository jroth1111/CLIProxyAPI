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

launcher_version=$(zsh -c 'source "$1"; printf "%s" "$CCYPROXY_LAUNCHER_VERSION"' -- "$workdir/installed/ccyproxy.zsh")
installer_version=$(sed -n "s/^version='\\([^']*\\)'$/\\1/p" "$installer")
if [ -z "$launcher_version" ] || [ "$launcher_version" != "$installer_version" ]; then
	printf 'launcher test: version mismatch: installer=%s launcher=%s\n' "${installer_version:-missing}" "${launcher_version:-missing}" >&2
	exit 1
fi

zsh -c '
	source "$1"
	(( $+functions[ccyproxy] ))
	(( $+functions[_ccyproxy_unset_provider_env] ))
' -- "$workdir/installed/ccyproxy.zsh"

mkdir -p "$workdir/mismatch"
cp "$installer" "$workdir/mismatch/install.sh"
sed "s/CCYPROXY_LAUNCHER_VERSION='[^']*'/CCYPROXY_LAUNCHER_VERSION='mismatch'/" "$launcher" >"$workdir/mismatch/ccyproxy.zsh"
if HOME="$workdir/home" "$workdir/mismatch/install.sh" --target "$workdir/mismatch-installed.zsh" >"$workdir/mismatch.out" 2>"$workdir/mismatch.err"; then
	printf '%s\n' 'launcher test: installer accepted mismatched launcher version' >&2
	exit 1
fi
grep -q 'launcher version mismatch' "$workdir/mismatch.err"

if grep -q 'legacy-372k-calibrated' "$launcher"; then
	printf '%s\n' 'launcher test: legacy incomplete-metadata fallback must remain absent' >&2
	exit 1
fi

for required in capacity_complete capacity_blockers credential_availability availability_complete CLAUDE_CODE_MAX_CONTEXT_TOKENS CLAUDE_CODE_AUTO_COMPACT_WINDOW; do
	if ! grep -q "$required" "$launcher"; then
		printf 'launcher test: missing contract field %s\n' "$required" >&2
		exit 1
	fi
done

mkdir -p "$workdir/bin"
cat >"$workdir/bin/curl" <<'EOF'
#!/bin/sh
set -eu
url=''
for arg do
	url=$arg
done
case "$url" in
	*client_version=ccyproxy*) cat "$MOCK_CATALOG" ;;
	*/v1/models) cat "$MOCK_MODELS" ;;
	*) printf 'unexpected curl URL: %s\n' "$url" >&2; exit 22 ;;
esac
EOF
chmod +x "$workdir/bin/curl"

cat >"$workdir/config.yaml" <<'EOF'
host: 127.0.0.1
port: 8317
api-keys:
  - test-key
smart-aliases:
  worker:
    candidates:
      - route-primary
      - route-fallback
EOF

cat >"$workdir/models-direct.json" <<'EOF'
{"data":[{"id":"gpt-5.6-sol"}]}
EOF
cat >"$workdir/catalog-complete.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"translation_margin_tokens":52000,"auto_compact_token_limit":287000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"available","availability_complete":true,"total_credentials":1,"eligible_credentials":1,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":[]}}]}
EOF

run_launcher() {
	models=$1
	catalog=$2
	shift 2
	PATH="$workdir/bin:$PATH" \
		CCYPROXY_CONFIG="$workdir/config.yaml" \
		MOCK_MODELS="$models" \
		MOCK_CATALOG="$catalog" \
		zsh -c 'source "$1"; shift; ccyproxy "$@"' -- "$launcher" "$@"
}

complete_output=$(run_launcher "$workdir/models-direct.json" "$workdir/catalog-complete.json" --show-config)
printf '%s\n' "$complete_output" | grep -q '^main_model=gpt-5.6-sol$'
printf '%s\n' "$complete_output" | grep -q '^compact_trigger=287000$'
printf '%s\n' "$complete_output" | grep -q '^auto_compact_window=320000$'

cat >"$workdir/catalog-invalid-margin.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"available","availability_complete":true,"total_credentials":1,"eligible_credentials":1,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":[]}}]}
EOF
if run_launcher "$workdir/models-direct.json" "$workdir/catalog-invalid-margin.json" --show-config >"$workdir/invalid-margin.out" 2>"$workdir/invalid-margin.err"; then
	printf '%s\n' 'launcher test: complete capacity without a positive translation margin was accepted' >&2
	exit 1
fi
grep -q 'incomplete capacity metadata' "$workdir/invalid-margin.err"

cat >"$workdir/catalog-incomplete.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"capacity_complete":false,"capacity_source":"test","capacity_blockers":["missing_translation_margin"],"credential_availability":{"status":"available","availability_complete":true,"total_credentials":1,"eligible_credentials":1,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":[]}}]}
EOF
if run_launcher "$workdir/models-direct.json" "$workdir/catalog-incomplete.json" --show-config >"$workdir/incomplete.out" 2>"$workdir/incomplete.err"; then
	printf '%s\n' 'launcher test: incomplete capacity was accepted' >&2
	exit 1
fi
grep -q 'incomplete capacity metadata' "$workdir/incomplete.err"

cat >"$workdir/catalog-missing-availability.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"translation_margin_tokens":52000,"auto_compact_token_limit":287000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[]}]}
EOF
if run_launcher "$workdir/models-direct.json" "$workdir/catalog-missing-availability.json" --show-config >"$workdir/missing.out" 2>"$workdir/missing.err"; then
	printf '%s\n' 'launcher test: absent availability metadata was accepted' >&2
	exit 1
fi
grep -q 'availability metadata missing' "$workdir/missing.err"

cat >"$workdir/catalog-incomplete-availability.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"translation_margin_tokens":52000,"auto_compact_token_limit":287000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"incomplete","availability_complete":false,"total_credentials":0,"eligible_credentials":0,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":["auth_manager_unavailable"]}}]}
EOF
if run_launcher "$workdir/models-direct.json" "$workdir/catalog-incomplete-availability.json" --show-config >"$workdir/incomplete-availability.out" 2>"$workdir/incomplete-availability.err"; then
	printf '%s\n' 'launcher test: incomplete availability metadata was accepted' >&2
	exit 1
fi
grep -q 'availability metadata incomplete (auth_manager_unavailable)' "$workdir/incomplete-availability.err"

cat >"$workdir/models-haiku.json" <<'EOF'
{"data":[{"id":"gpt-5.6-sol"},{"id":"custom-haiku"}]}
EOF
cat >"$workdir/catalog-haiku-missing.json" <<'EOF'
{"models":[{"slug":"gpt-5.6-sol","context_window":372000,"max_output_tokens":128000,"translation_margin_tokens":52000,"auto_compact_token_limit":287000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"available","availability_complete":true,"total_credentials":1,"eligible_credentials":1,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":[]}},{"slug":"custom-haiku","context_window":200000,"max_output_tokens":64000,"translation_margin_tokens":13000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[]}]}
EOF
if run_launcher "$workdir/models-haiku.json" "$workdir/catalog-haiku-missing.json" --proxy-haiku-model custom-haiku --show-config >"$workdir/haiku.out" 2>"$workdir/haiku.err"; then
	printf '%s\n' 'launcher test: haiku without availability metadata was accepted' >&2
	exit 1
fi
grep -q 'custom-haiku: availability metadata missing' "$workdir/haiku.err"

cat >"$workdir/models-route.json" <<'EOF'
{"data":[{"id":"route-primary"},{"id":"route-fallback"}]}
EOF
cat >"$workdir/catalog-route.json" <<'EOF'
{"models":[{"slug":"route-primary","context_window":200000,"max_output_tokens":64000,"translation_margin_tokens":13000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"unavailable","availability_complete":true,"total_credentials":1,"eligible_credentials":0,"cooling_credentials":0,"blocked_credentials":1,"availability_blockers":[]}},{"slug":"route-fallback","context_window":180000,"max_output_tokens":64000,"translation_margin_tokens":13000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"available","availability_complete":true,"total_credentials":1,"eligible_credentials":1,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":[]}}]}
EOF
route_output=$(run_launcher "$workdir/models-route.json" "$workdir/catalog-route.json" --proxy-route worker --show-config)
printf '%s\n' "$route_output" | grep -q '^requested_route=worker$'
printf '%s\n' "$route_output" | grep -q '^resolved_candidate=route-fallback$'
printf '%s\n' "$route_output" | grep -q '^main_model=route-fallback$'

cat >"$workdir/catalog-route-incomplete.json" <<'EOF'
{"models":[{"slug":"route-primary","context_window":200000,"max_output_tokens":64000,"translation_margin_tokens":13000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[]},{"slug":"route-fallback","context_window":180000,"max_output_tokens":64000,"translation_margin_tokens":13000,"capacity_complete":true,"capacity_source":"test","capacity_blockers":[],"credential_availability":{"status":"incomplete","availability_complete":false,"total_credentials":0,"eligible_credentials":0,"cooling_credentials":0,"blocked_credentials":0,"availability_blockers":["auth_manager_unavailable"]}}]}
EOF
if run_launcher "$workdir/models-route.json" "$workdir/catalog-route-incomplete.json" --proxy-route worker --show-config >"$workdir/route-incomplete.out" 2>"$workdir/route-incomplete.err"; then
	printf '%s\n' 'launcher test: route without complete availability was accepted' >&2
	exit 1
fi
grep -q "route 'worker' has no currently eligible candidate" "$workdir/route-incomplete.err"
