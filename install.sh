#!/bin/sh
# Install the latest silo release on Linux or macOS.
#
#   curl -fsSL https://raw.githubusercontent.com/nonex-code/silo/main/install.sh | sh
#
# Env: SILO_INSTALL_DIR overrides the destination directory.
set -eu

repo="nonex-code/silo"
os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  arm64 | aarch64) arch=arm64 ;;
  *)
    echo "silo: unsupported architecture: $arch" >&2
    exit 1
    ;;
esac
case "$os" in
  linux | darwin) ;;
  *)
    echo "silo: unsupported OS: $os (use install.ps1 on Windows)" >&2
    exit 1
    ;;
esac

url="https://github.com/$repo/releases/latest/download/silo_${os}_${arch}.tar.gz"
tmp=$(mktemp -d)
trap 'rm -rf "$tmp"' EXIT INT TERM

echo "downloading $url"
curl -fsSL "$url" | tar -xz -C "$tmp"

dest=${SILO_INSTALL_DIR:-}
if [ -z "$dest" ]; then
  if [ -w /usr/local/bin ] 2>/dev/null; then dest=/usr/local/bin; else dest=$HOME/.local/bin; fi
fi
mkdir -p "$dest"
install -m 0755 "$tmp/silo" "$dest/silo"
echo "installed $dest/silo"

case ":$PATH:" in
  *":$dest:"*) ;;
  *) echo "note: add $dest to your PATH" ;;
esac