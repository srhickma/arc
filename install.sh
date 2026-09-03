#!/usr/bin/env bash
set -euo pipefail

# Builds the arc binary from the current checkout and installs it globally.

SRC_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DEST="/usr/local/bin"

command -v go >/dev/null || {
	echo "error: go toolchain not found on PATH" >&2
	exit 1
}

BUILD_DIR="$(mktemp -d)"
trap 'rm -rf "$BUILD_DIR"' EXIT

echo "building arc from ${SRC_DIR} ..."
(cd "$SRC_DIR" && go build -o "${BUILD_DIR}/arc" .)

if [ -w "$DEST" ]; then
	install -m 0755 "${BUILD_DIR}/arc" "$DEST/arc"
else
	echo "installing to ${DEST} (requires sudo) ..."
	sudo install -d -m 0755 "$DEST"
	sudo install -m 0755 "${BUILD_DIR}/arc" "$DEST/arc"
fi

echo "installed: ${DEST}/arc"
