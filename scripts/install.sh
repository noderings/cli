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

# Resolve latest tag via the HTML redirect, not api.github.com.
# Unauthenticated REST is 60 req/hour per public IP; shared lab NAT often gets HTTP 403.
latest_url=$(curl -fsSLI -o /dev/null -w '%{url_effective}' "https://github.com/${REPO}/releases/latest")
tag=$(basename "${latest_url}")
if [ -z "$tag" ] || [ "$tag" = "latest" ]; then
  echo "error: could not resolve the latest release from https://github.com/${REPO}/releases/latest" >&2
  echo "Download a tarball from https://github.com/${REPO}/releases and install nr from it." >&2
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
