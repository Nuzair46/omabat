#!/bin/sh

set -eu

repo_url="https://github.com/nuzair46/omabat"
install_dir="${OMABAT_INSTALL_DIR:-$HOME/.local/bin}"
version="${OMABAT_VERSION:-}"

say() {
	printf 'omabat: %s\n' "$*"
}

fail() {
	say "$*" >&2
	exit 1
}

confirm_integration() {
	printf '%s\n' 'Integrate Omabat with your desktop panel?' >&3
	printf '%s\n' 'This will modify panel settings and may restart the panel.' >&3
	while :; do
		printf '[Y/n] ' >&3
		# Read the controlling terminal, never the piped installer source.
		if ! IFS= read -r answer <&3; then
			printf '\n' >&3
			return 1
		fi
		case "$answer" in
			"" | y | Y | [Yy][Ee][Ss]) return 0 ;;
			n | N | [Nn][Oo]) return 1 ;;
			*) printf '%s\n' 'Please answer yes or no.' >&3 ;;
		esac
	done
}

for command in curl tar sha256sum install mktemp uname awk; do
	command -v "$command" >/dev/null 2>&1 || fail "required command not found: $command"
done

[ "$(uname -s)" = "Linux" ] || fail "Omabat supports Linux only"

case "$(uname -m)" in
	x86_64 | amd64)
		arch="amd64"
		;;
	aarch64 | arm64)
		arch="arm64"
		;;
	*)
		fail "unsupported architecture: $(uname -m)"
		;;
esac

if [ -z "$version" ]; then
	say "Finding the latest release"
	latest_url="$(curl -fsSLI --retry 3 -o /dev/null -w '%{url_effective}' "$repo_url/releases/latest")"
	tag="${latest_url##*/}"
	case "$tag" in
		v*) ;;
		*) fail "could not determine the latest release version" ;;
	esac
	version="${tag#v}"
else
	version="${version#v}"
	tag="v$version"
fi

case "$version" in
	"" | *[!0-9A-Za-z.+-]*)
		fail "invalid release version: $version"
		;;
esac
case "$version" in
	[0-9]*.[0-9]*.[0-9]*) ;;
	*) fail "release version is not SemVer: $version" ;;
esac

archive="omabat_${version}_linux_${arch}.tar.gz"
release_url="$repo_url/releases/download/$tag"
temp_dir="$(mktemp -d)"

cleanup() {
	rm -rf "$temp_dir"
}
trap cleanup 0
trap 'exit 1' HUP INT TERM

say "Downloading Omabat $tag for linux/$arch"
curl -fsSL --retry 3 -o "$temp_dir/$archive" "$release_url/$archive"
curl -fsSL --retry 3 -o "$temp_dir/checksums.txt" "$release_url/checksums.txt"

checksum_line="$(awk -v archive="$archive" '$2 == archive { print; found = 1; exit } END { if (!found) exit 1 }' "$temp_dir/checksums.txt")" ||
	fail "release checksum does not include $archive"
printf '%s\n' "$checksum_line" | (cd "$temp_dir" && sha256sum -c - >/dev/null) ||
	fail "checksum verification failed"

tar -xzf "$temp_dir/$archive" -C "$temp_dir"
binary="$temp_dir/omabat_${version}_linux_${arch}/omabat"
[ -f "$binary" ] || fail "release archive does not contain the Omabat binary"

mkdir -p "$install_dir"
target="$install_dir/omabat"
install -m 0755 "$binary" "$target"
say "Installed $("$target" version) to $target"

# The script from main can be newer than the latest release. Older binaries
# integrate as part of "install", so never call that command on such a release.
help_output="$("$target" help)" || fail "could not check release installation capabilities"
case "$help_output" in
	*"omabat integrate"*) ;;
	*) fail "this release does not support daemon-only installation; select a release with 'omabat integrate' using OMABAT_VERSION" ;;
esac

say "Enabling the Omabat user daemon"
if ! "$target" install; then
	fail "the binary was installed, but user daemon setup failed; retry with: $target install"
fi

# Probe in a subshell: opening an unavailable /dev/tty must not abort a
# non-interactive installation under set -e (or a shell's exec semantics).
if (exec 3<>/dev/tty) 2>/dev/null; then
	if confirm_integration 3<>/dev/tty; then
		if ! "$target" integrate; then
			say "Desktop integration failed; the binary and user daemon remain installed. Retry with: $target integrate" >&2
		fi
	else
		say "Desktop integration skipped; existing panel settings were left unchanged. You can run: $target integrate"
	fi
else
	say "No interactive terminal available; desktop integration skipped. You can run: $target integrate"
fi

case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*) say "Add $install_dir to PATH to run Omabat as: omabat" ;;
esac

say "Installation complete. Run: $target"
