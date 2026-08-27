#!/usr/bin/env bash
# Install the NodeRings CLI (`nr`) from GitHub Releases.
set -euo pipefail

REPO="${NR_INSTALL_REPO:-noderings/cli}"
INSTALL_DIR="${NR_INSTALL_DIR:-/usr/local/bin}"

need() {
  command -v "$1" >/dev/null 2>&1 || {
    echo "error: $1 is required" >&2
    exit 1
  }
}

need curl
need tar

os=$(uname -s | tr '[:upper:]' '[:lower:]')
arch=$(uname -m)
case "$arch" in
  x86_64 | amd64) arch=amd64 ;;
  aarch64 | arm64) arch=arm64 ;;
  *)
    echo "error: unsupported architecture: $arch" >&2
    exit 1
    ;;
esac

case "$os" in
  linux | darwin) ;;
  *)
    echo "error: unsupported OS: $os (linux or macOS required)" >&2
    exit 1
    ;;
esac

api="https://api.github.com/repos/${REPO}/releases/latest"
tag=$(curl -fsSL "$api" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -n1)
if [ -z "$tag" ]; then
  echo "error: could not resolve the latest release from ${api}" >&2
  echo "Make sure https://github.com/${REPO} is public and has a published release." >&2
  exit 1
fi

ver="${tag#v}"
asset="nr_${ver}_${os}_${arch}.tar.gz"
base="https://github.com/${REPO}/releases/download/${tag}"

tmpdir=$(mktemp -d)
trap 'rm -rf "$tmpdir"' EXIT

curl -fsSL "${base}/${asset}" -o "${tmpdir}/${asset}"
if curl -fsSL "${base}/checksums.txt" -o "${tmpdir}/checksums.txt"; then
  (
    cd "$tmpdir"
    line=$(grep -E " ${asset}$" checksums.txt || true)
    if [ -n "$line" ]; then
      if command -v sha256sum >/dev/null 2>&1; then
        echo "$line" | sha256sum -c -
      elif command -v shasum >/dev/null 2>&1; then
        echo "$line" | shasum -a 256 -c -
      fi
    fi
  )
fi

tar -xzf "${tmpdir}/${asset}" -C "$tmpdir"
if [ ! -f "${tmpdir}/nr" ]; then
  echo "error: release archive did not contain the nr binary" >&2
  exit 1
fi

mkdir -p "$INSTALL_DIR"
if [ -w "$INSTALL_DIR" ]; then
  install -m 0755 "${tmpdir}/nr" "${INSTALL_DIR}/nr"
else
  need sudo
  sudo install -m 0755 "${tmpdir}/nr" "${INSTALL_DIR}/nr"
fi

"${INSTALL_DIR}/nr" version
echo "Installed ${INSTALL_DIR}/nr"
