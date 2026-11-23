#!/bin/sh
# Installs the Athenaeum server from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/manzanit0/athenaeum/master/install.sh | sh
#
# It downloads the archive for this machine, checks it against the
# release's checksums and puts the `athenaeum` binary on the PATH.
#
# Environment:
#   ATHENAEUM_VERSION      Release tag to install, e.g. v0.1.0-build.7.
#                          Default: the latest release.
#   ATHENAEUM_INSTALL_DIR  Where the binary goes. Default: the folder of
#                          the `athenaeum` already on the PATH, so that
#                          one is updated; without one, /usr/local/bin
#                          when it is writable, otherwise ~/.local/bin.
#   ATHENAEUM_RELEASE_URL  Folder holding the archives and checksums.txt,
#                          for mirrors. Overrides ATHENAEUM_VERSION.

set -eu

repo="manzanit0/athenaeum"

fail() {
    echo "install.sh: $*" >&2
    exit 1
}

have() {
    command -v "$1" >/dev/null 2>&1
}

# Which archive: the names match the release workflow's build targets.
case "$(uname -s)" in
    Linux) os=linux ;;
    Darwin) os=darwin ;;
    *) fail "no build for $(uname -s); only Linux and macOS are released" ;;
esac
case "$(uname -m)" in
    x86_64 | amd64) arch=amd64 ;;
    arm64 | aarch64) arch=arm64 ;;
    *) fail "no build for $(uname -m); only amd64 and arm64 are released" ;;
esac
archive="athenaeum-$os-$arch.tar.gz"

if [ -n "${ATHENAEUM_RELEASE_URL:-}" ]; then
    base=${ATHENAEUM_RELEASE_URL%/}
elif [ -n "${ATHENAEUM_VERSION:-}" ]; then
    base="https://github.com/$repo/releases/download/$ATHENAEUM_VERSION"
else
    base="https://github.com/$repo/releases/latest/download"
fi

if have curl; then
    fetch() { curl -fsSL -o "$2" "$1"; }
elif have wget; then
    fetch() { wget -q -O "$2" "$1"; }
else
    fail "curl or wget is needed to download the release"
fi

if have sha256sum; then
    sha256() { sha256sum "$1" | cut -d ' ' -f 1; }
elif have shasum; then
    sha256() { shasum -a 256 "$1" | cut -d ' ' -f 1; }
else
    fail "sha256sum or shasum is needed to check the download"
fi

tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT

echo "Downloading $archive from $base"
fetch "$base/$archive" "$tmp/$archive" || fail "could not download $base/$archive"
fetch "$base/checksums.txt" "$tmp/checksums.txt" || fail "could not download $base/checksums.txt"

want=$(awk -v name="$archive" '$2 == name || $2 == "*" name { print $1 }' "$tmp/checksums.txt")
[ -n "$want" ] || fail "checksums.txt has no entry for $archive"
got=$(sha256 "$tmp/$archive")
[ "$want" = "$got" ] || fail "checksum mismatch for $archive: expected $want, got $got"

tar -xzf "$tmp/$archive" -C "$tmp" athenaeum

# An `athenaeum` already on the PATH is updated where it is, so a second
# copy never ends up shadowing it or being shadowed by it.
existing=$(command -v athenaeum 2>/dev/null || true)
case "$existing" in
    /*) ;;
    *) existing="" ;;
esac

if [ -n "${ATHENAEUM_INSTALL_DIR:-}" ]; then
    dir=$ATHENAEUM_INSTALL_DIR
elif [ -n "$existing" ]; then
    dir=$(dirname "$existing")
    echo "Updating $existing"
    [ -w "$dir" ] || fail "$dir is not writable; run the script as a user who can write there, or set ATHENAEUM_INSTALL_DIR"
elif [ -d /usr/local/bin ] && [ -w /usr/local/bin ]; then
    dir=/usr/local/bin
else
    dir="$HOME/.local/bin"
fi
mkdir -p "$dir" || fail "could not create $dir"

# Moved into place within the folder, so a running server keeps its old
# binary until it is restarted.
cp "$tmp/athenaeum" "$dir/.athenaeum.new" || fail "could not write to $dir"
chmod 755 "$dir/.athenaeum.new"
mv -f "$dir/.athenaeum.new" "$dir/athenaeum"

echo "Installed $dir/athenaeum"
case ":$PATH:" in
    *":$dir:"*) ;;
    *) echo "$dir is not on your PATH; add it, or run the binary by its full path." ;;
esac
echo "Run \`athenaeum\` to start the server on http://localhost:8080 with its data in ./data."
