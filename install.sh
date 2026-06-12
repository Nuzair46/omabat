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

say "Enabling the Omabat user daemon"
if ! "$target" install; then
	fail "the binary was installed, but user daemon setup failed; retry with: $target install"
fi

case ":${PATH:-}:" in
	*":$install_dir:"*) ;;
	*) say "Add $install_dir to PATH to run Omabat as: omabat" ;;
esac

say "Installation complete. Run: $target"
