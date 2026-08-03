#!/usr/bin/env sh
# Cross-compile seer-cli for all install.sh targets.
# Output: cli/builds/seer-<os>-<arch>[.exe]
set -e

ROOT="$(CDPATH= cd -- "$(dirname "$0")" && pwd)"
OUT="$ROOT/builds"
mkdir -p "$OUT"

VERSION="${SEER_CLI_VERSION:-0.2.4}"
echo "Building seer-cli $VERSION -> $OUT"

build() {
  os="$1"
  arch="$2"
  ext=""
  [ "$os" = "windows" ] && ext=".exe"
  name="seer-${os}-${arch}${ext}"
  echo "  -> $name"
  GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 go build -ldflags "-s -w" -o "$OUT/$name" .
}

cd "$ROOT"
build darwin amd64
build darwin arm64
build linux amd64
build linux arm64
build windows amd64

# Convenience copies for local Windows use
cp "$OUT/seer-windows-amd64.exe" "$OUT/seer.exe"
cp "$OUT/seer-windows-amd64.exe" "$ROOT/seer.exe"

echo "✓ All binaries built:"
ls -la "$OUT"
