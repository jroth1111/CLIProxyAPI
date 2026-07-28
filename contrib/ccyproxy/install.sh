#!/bin/sh
set -eu

version='ccyfix.4'
script_dir=$(CDPATH= cd -- "$(dirname -- "$0")" && pwd)
source_file="$script_dir/ccyproxy.zsh"
target=${CCYPROXY_INSTALL_PATH:-"$HOME/.config/ccyproxy/ccyproxy.zsh"}

usage() {
	cat <<EOF
Usage: $0 [--target PATH] [--print-source-line]

Install the ccyproxy $version launcher without modifying shell startup files.
EOF
}

print_source_line=0
while [ "$#" -gt 0 ]; do
	case "$1" in
		--target)
			[ "$#" -ge 2 ] || { printf '%s\n' 'install.sh: --target requires a path' >&2; exit 2; }
			target=$2
			shift 2
			;;
		--print-source-line)
			print_source_line=1
			shift
			;;
		--help|-h)
			usage
			exit 0
			;;
		*)
			printf 'install.sh: unknown argument: %s\n' "$1" >&2
			usage >&2
			exit 2
			;;
	esac
done

if [ ! -r "$source_file" ]; then
	printf 'install.sh: launcher source is not readable: %s\n' "$source_file" >&2
	exit 1
fi

launcher_version=$(sed -n "s/^typeset -g CCYPROXY_LAUNCHER_VERSION='\\([^']*\\)'$/\\1/p" "$source_file")
if [ "$launcher_version" != "$version" ]; then
	printf 'install.sh: launcher version mismatch: installer=%s launcher=%s\n' "$version" "${launcher_version:-missing}" >&2
	exit 1
fi

install_dir=$(dirname -- "$target")
mkdir -p "$install_dir"
tmp="$target.tmp.$$"
trap 'rm -f "$tmp"' EXIT HUP INT TERM
cp "$source_file" "$tmp"
chmod 0644 "$tmp"
mv "$tmp" "$target"
trap - EXIT HUP INT TERM

source_line="source \"$target\""
if [ "$print_source_line" -eq 1 ]; then
	printf '%s\n' "$source_line"
else
	printf 'Installed ccyproxy %s at %s\n' "$version" "$target"
	printf '%s\n' 'Shell startup files were not modified.'
	printf 'Activate explicitly by adding this line to ~/.zshrc:\n%s\n' "$source_line"
fi
