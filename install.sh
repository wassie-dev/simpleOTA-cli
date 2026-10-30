#!/bin/sh
# Install or update the simpleota CLI on Linux or macOS.
#
#   curl -fsSL https://github.com/wassie-dev/simpleOTA-cli/releases/latest/download/install.sh | sh
#
# Run it again to update. It downloads the release archive for this machine,
# checks it against the release's checksums.txt, and installs the binary
# without sudo. Settings (environment):
#
#   SIMPLEOTA_VERSION       version to install, e.g. 0.1.0 (default: latest)
#   SIMPLEOTA_INSTALL_DIR   where to put the binary (default: where simpleota
#                           already is, else /usr/local/bin if writable, else
#                           ~/.local/bin)
#   SIMPLEOTA_DOWNLOAD_URL  base URL of a mirror of the GitHub release
#                           downloads (https, or http on localhost)
#
# Everything runs inside main, so a truncated download of this script does
# nothing.

set -eu

REPO_URL="https://github.com/wassie-dev/simpleOTA-cli"
PROTO="=https"

say() { printf '%s\n' "$*" >&2; }
die() {
	say "simpleota install: $*"
	exit 1
}

has() { command -v "$1" >/dev/null 2>&1; }

# fetch URL FILE downloads URL to FILE.
fetch() {
	if has curl; then
		curl --proto "$PROTO" --tlsv1.2 -fsSL -o "$2" "$1"
	elif has wget; then
		if [ "$PROTO" = "=https" ]; then
			wget --https-only -q -O "$2" "$1"
		else
			wget -q -O "$2" "$1"
		fi
	else
		die "curl or wget is required"
	fi
}

# latest_version prints the newest release version without the leading v.
latest_version() {
	tag=
	if has curl; then
		url=$(curl --proto '=https' --tlsv1.2 -fsSLI -o /dev/null -w '%{url_effective}' "$REPO_URL/releases/latest" 2>/dev/null) &&
			tag=${url##*/}
	elif has wget; then
		json=$(wget --https-only -qO- "https://api.github.com/repos/wassie-dev/simpleOTA-cli/releases/latest" 2>/dev/null) &&
			tag=$(printf '%s\n' "$json" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n 1)
	else
		die "curl or wget is required"
	fi
	case $tag in
	v[0-9]*) printf '%s\n' "${tag#v}" ;;
	*) die "could not find a published release at $REPO_URL/releases; set SIMPLEOTA_VERSION to install a specific one" ;;
	esac
}

detect_platform() {
	case $(uname -s) in
	Linux) os=linux ;;
	Darwin) os=darwin ;;
	*) die "unsupported OS $(uname -s); on Windows use install.ps1" ;;
	esac
	case $(uname -m) in
	x86_64 | amd64) arch=amd64 ;;
	arm64 | aarch64) arch=arm64 ;;
	*) die "unsupported CPU $(uname -m); simpleota is built for amd64 and arm64" ;;
	esac
	# A shell running under Rosetta reports x86_64 on Apple silicon.
	if [ "$os" = darwin ] && [ "$arch" = amd64 ] &&
		[ "$(sysctl -n sysctl.proc_translated 2>/dev/null || echo 0)" = 1 ]; then
		arch=arm64
	fi
}

sha256_of() {
	if has sha256sum; then
		sha256sum "$1" | cut -d ' ' -f 1
	elif has shasum; then
		shasum -a 256 "$1" | cut -d ' ' -f 1
	else
		die "sha256sum or shasum is required to verify the download"
	fi
}

on_path() {
	case ":$PATH:" in
	*":$1:"*) return 0 ;;
	*) return 1 ;;
	esac
}

choose_dir() {
	if [ -n "${SIMPLEOTA_INSTALL_DIR:-}" ]; then
		printf '%s\n' "$SIMPLEOTA_INSTALL_DIR"
		return
	fi
	if existing=$(command -v simpleota 2>/dev/null) && [ -w "$(dirname "$existing")" ]; then
		dirname "$existing"
		return
	fi
	if [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
		printf '%s\n' /usr/local/bin
		return
	fi
	printf '%s\n' "$HOME/.local/bin"
}

installed_version() {
	if [ -x "$1" ]; then
		"$1" --version 2>/dev/null | sed -n 's/^simpleota version v\{0,1\}//p'
	fi
}

main() {
	detect_platform
	base=${SIMPLEOTA_DOWNLOAD_URL:-$REPO_URL/releases/download}
	case $base in
	https://*) ;;
	http://127.0.0.1:* | http://localhost:*) PROTO="=http,https" ;;
	*) die "SIMPLEOTA_DOWNLOAD_URL must be https (or http on localhost)" ;;
	esac
	version=${SIMPLEOTA_VERSION:-}
	version=${version#v}
	[ -n "$version" ] || version=$(latest_version)
	case $version in
	*[!0-9A-Za-z.+-]*) die "invalid SIMPLEOTA_VERSION '$version'" ;;
	esac

	dir=$(choose_dir)
	target="$dir/simpleota"
	current=$(installed_version "$target")
	if [ "$current" = "$version" ]; then
		say "simpleota $version is already installed at $target"
		return
	fi

	archive="simpleota_${version}_${os}_${arch}.tar.gz"
	tmp=$(mktemp -d 2>/dev/null || mktemp -d -t simpleota)
	trap 'rm -rf "$tmp"' EXIT
	trap 'exit 1' INT TERM

	say "Downloading simpleota $version for $os/$arch"
	fetch "$base/v$version/$archive" "$tmp/$archive" ||
		die "download failed: $base/v$version/$archive (check SIMPLEOTA_VERSION)"
	fetch "$base/v$version/checksums.txt" "$tmp/checksums.txt" ||
		die "download failed: $base/v$version/checksums.txt"

	want=$(awk -v f="$archive" '$2 == f { print $1 }' "$tmp/checksums.txt")
	[ -n "$want" ] || die "$archive is not listed in checksums.txt"
	got=$(sha256_of "$tmp/$archive")
	[ "$got" = "$want" ] || die "checksum mismatch for $archive: expected $want, got $got"

	tar -xzf "$tmp/$archive" -C "$tmp" simpleota
	mkdir -p "$dir" || die "cannot create $dir; set SIMPLEOTA_INSTALL_DIR to a writable directory"
	[ -w "$dir" ] || die "$dir is not writable; set SIMPLEOTA_INSTALL_DIR to a writable directory"
	cp "$tmp/simpleota" "$dir/.simpleota.new"
	chmod 0755 "$dir/.simpleota.new"
	mv -f "$dir/.simpleota.new" "$target"

	if [ -n "$current" ]; then
		say "Updated simpleota from $current to $version at $target"
	else
		say "Installed simpleota $version to $target"
	fi
	if ! on_path "$dir"; then
		say "Add $dir to your PATH, for example:"
		say "  echo 'export PATH=\"$dir:\$PATH\"' >> ~/.profile"
	fi
	found=$(command -v simpleota 2>/dev/null || true)
	if [ -n "$found" ] && [ "$found" != "$target" ]; then
		say "Note: $found comes first on your PATH and will run instead; remove it, or update it by running this script with sudo"
	fi
}

main "$@"
