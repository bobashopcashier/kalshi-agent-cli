#!/bin/sh

set -eu

repository="bobashopcashier/kalshi-cli"
source_ref="${KALSHI_CLI_VERSION:-main}"

fail() {
	printf 'kalshi-cli installer: %s\n' "$1" >&2
	exit 1
}

require_command() {
	command -v "$1" >/dev/null 2>&1 || fail "required command not found: $1"
}

case "$source_ref" in
	main)
		archive_url="https://github.com/${repository}/archive/refs/heads/main.tar.gz"
		build_version="main"
		;;
	v[0-9]*)
		case "$source_ref" in
			*[!A-Za-z0-9._-]*) fail "invalid KALSHI_CLI_VERSION: $source_ref" ;;
		esac
		archive_url="https://github.com/${repository}/archive/refs/tags/${source_ref}.tar.gz"
		build_version="$source_ref"
		;;
	*)
		fail "KALSHI_CLI_VERSION must be main or a tag beginning with v"
		;;
esac

if [ -n "${KALSHI_CLI_INSTALL_DIR:-}" ]; then
	install_dir="$KALSHI_CLI_INSTALL_DIR"
elif [ -n "${HOME:-}" ]; then
	install_dir="$HOME/.local/bin"
else
	fail "set KALSHI_CLI_INSTALL_DIR when HOME is unavailable"
fi

require_command curl
require_command go
require_command install
require_command mktemp
require_command mkdir
require_command mv
require_command rm
require_command tar

go_version="$(go env GOVERSION 2>/dev/null)" || fail "could not determine Go version"
go_numbers="${go_version#go}"
go_major="${go_numbers%%.*}"
go_remainder="${go_numbers#*.}"
go_minor="${go_remainder%%.*}"
case "$go_major:$go_minor" in
	*[!0-9:]* | :* | *:) fail "Go 1.26 or newer is required; found $go_version" ;;
esac
if [ "$go_major" -lt 1 ] || { [ "$go_major" -eq 1 ] && [ "$go_minor" -lt 26 ]; }; then
	fail "Go 1.26 or newer is required; found $go_version"
fi

temporary_dir=""
staged_binary=""
cleanup() {
	if [ -n "$staged_binary" ] && [ -e "$staged_binary" ]; then
		rm -f "$staged_binary"
	fi
	if [ -n "$temporary_dir" ] && [ -d "$temporary_dir" ]; then
		rm -rf "$temporary_dir"
	fi
}
trap cleanup EXIT
trap 'exit 1' HUP INT TERM

temporary_dir="$(mktemp -d "${TMPDIR:-/tmp}/kalshi-cli.XXXXXX")"

printf 'Downloading %s (%s)...\n' "$repository" "$source_ref"
curl --proto '=https' --tlsv1.2 -fsSL --retry 3 \
	"$archive_url" \
	-o "$temporary_dir/source.tar.gz"

tar -xzf "$temporary_dir/source.tar.gz" -C "$temporary_dir"
source_dir=""
for candidate in "$temporary_dir"/kalshi-cli-*; do
	if [ -d "$candidate" ]; then
		source_dir="$candidate"
		break
	fi
done
[ -n "$source_dir" ] || fail "downloaded archive did not contain the source tree"

printf 'Building kalshi with %s...\n' "$(go version)"
(
	cd "$source_dir"
	go build -trimpath -ldflags "-s -w -X main.version=$build_version" \
		-o "$temporary_dir/kalshi" ./cmd/kalshi
)

mkdir -p "$install_dir"
staged_binary="$(mktemp "$install_dir/.kalshi.install.XXXXXX")"
install -m 0755 "$temporary_dir/kalshi" "$staged_binary"
"$staged_binary" --version >/dev/null 2>&1 || fail "built binary failed its version check"
mv -f "$staged_binary" "$install_dir/kalshi"
staged_binary=""

printf 'Installed kalshi to %s/kalshi\n' "$install_dir"
case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*) printf 'Add %s to PATH to run kalshi from any directory.\n' "$install_dir" ;;
esac
