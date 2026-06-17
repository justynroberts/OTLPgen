#!/bin/sh
# otlpgen installer — downloads the right prebuilt binary for this machine.
#
#   curl -fsSL https://raw.githubusercontent.com/justynroberts/OTLPgen/main/install.sh | sh
#
# Env overrides:
#   BINDIR=/usr/local/bin   where to install (default: current directory)
#   REF=main                git ref to pull binaries from (branch/tag)
set -eu

REPO="justynroberts/OTLPgen"
REF="${REF:-main}"
BINDIR="${BINDIR:-$(pwd)}"

OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "otlpgen: unsupported arch: $ARCH" >&2; exit 1 ;;
esac

EXT=""; BIN="otlpgen"
case "$OS" in
  darwin) [ "$ARCH" = amd64 ] || [ "$ARCH" = arm64 ] || { echo "otlpgen: unsupported macOS arch" >&2; exit 1; } ;;
  linux)  ;;
  msys*|mingw*|cygwin*) OS=windows; EXT=".exe"; BIN="otlpgen.exe" ;;
  *) echo "otlpgen: unsupported OS: $OS" >&2; exit 1 ;;
esac

ASSET="otlpgen-${OS}-${ARCH}${EXT}"
URL="https://raw.githubusercontent.com/${REPO}/${REF}/dist/${ASSET}"
DEST="${BINDIR%/}/${BIN}"

echo "otlpgen: downloading ${ASSET} -> ${DEST}"
curl -fsSL -o "$DEST" "$URL"
chmod +x "$DEST"

# macOS Gatekeeper: clear the quarantine flag so it runs without a prompt.
if [ "$OS" = darwin ] && command -v xattr >/dev/null 2>&1; then
  xattr -d com.apple.quarantine "$DEST" 2>/dev/null || true
fi

echo "otlpgen: installed $("$DEST" --version 2>/dev/null || echo "$DEST")"
cat <<EOF

Next:
  export OTEL_EXPORTER_OTLP_ENDPOINT="https://your-otlp-endpoint:443"
  export OTEL_API_KEY="your-token"
  ${DEST} --one-shot
EOF
