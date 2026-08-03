#!/usr/bin/env sh
set -e

REPO="seer-monitoring/seer_binaries"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  linux*) OS="linux" ;;
  darwin*) OS="darwin" ;;
  mingw*|msys*|cygwin*) OS="windows" ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

BINARY="seer-${OS}-${ARCH}"
[ "$OS" = "windows" ] && BINARY="${BINARY}.exe"

URL="https://github.com/${REPO}/releases/latest/download/${BINARY}"

echo "Downloading SEER ($OS/$ARCH)"
curl -fsSL "$URL" -o seer

chmod +x seer
mv seer /usr/local/bin/seer 2>/dev/null || {
  echo ""
  echo "Permission denied installing SEER to /usr/local/bin."
  echo ""
  echo "Try one of the following:"
  echo "  sudo mv seer /usr/local/bin/seer"
  echo ""
  echo "Or install without sudo:"
  echo "  mkdir -p \$HOME/.local/bin"
  echo "  mv seer \$HOME/.local/bin/seer"
  echo "  export PATH=\$HOME/.local/bin:\$PATH"
  echo ""
  exit 1
}

echo "✓ SEER installed"
seer --help
