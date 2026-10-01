#!/usr/bin/env bash
set -euo pipefail

# scripts/release.sh: Build static release binaries and verify binary-size gate (<10 MB).

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${ROOT_DIR}"

VERSION="${VERSION:-$(git describe --tags --always 2>/dev/null || echo "v0.1.0")}"
DIST_DIR="${ROOT_DIR}/dist"
MAX_BYTES=$((10 * 1024 * 1024)) # 10 MB limit

echo "==> Building yz release ${VERSION}"
rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

PLATFORMS=(
  "darwin/amd64"
  "darwin/arm64"
  "linux/amd64"
  "linux/arm64"
)

for PLATFORM in "${PLATFORMS[@]}"; do
  GOOS="${PLATFORM%/*}"
  GOARCH="${PLATFORM#*/}"
  OUT_NAME="yz_${VERSION}_${GOOS}_${GOARCH}"
  OUT_PATH="${DIST_DIR}/${OUT_NAME}"

  echo "  -> Building ${GOOS}/${GOARCH} -> ${OUT_NAME}"
  CGO_ENABLED=0 GOOS="${GOOS}" GOARCH="${GOARCH}" go build \
    -trimpath \
    -ldflags="-s -w -X main.version=${VERSION}" \
    -o "${OUT_PATH}" \
    ./cmd/yz

  # Size gate (<10 MB)
  if [[ "$OSTYPE" == "darwin"* ]]; then
    SIZE=$(stat -f%z "${OUT_PATH}")
  else
    SIZE=$(stat -c%s "${OUT_PATH}")
  fi

  SIZE_MB=$(awk "BEGIN {printf \"%.2f\", ${SIZE}/1048576}")
  echo "     Size: ${SIZE_MB} MB (${SIZE} bytes)"

  if (( SIZE >= MAX_BYTES )); then
    echo "ERROR: ${OUT_NAME} exceeded binary-size gate (<10 MB): ${SIZE} bytes" >&2
    exit 1
  fi
done

# Verification on host platform
HOST_OS=$(go env GOOS)
HOST_ARCH=$(go env GOARCH)
HOST_BIN="${DIST_DIR}/yz_${VERSION}_${HOST_OS}_${HOST_ARCH}"
if [[ -x "${HOST_BIN}" ]]; then
  echo "==> Verifying host binary version output"
  VER_OUT=$("${HOST_BIN}" version)
  echo "    ${VER_OUT}"
  if [[ "${VER_OUT}" != *"${VERSION}"* ]]; then
    echo "ERROR: version string ${VER_OUT} does not contain ${VERSION}" >&2
    exit 1
  fi
fi

# Checksums
echo "==> Generating checksums.txt"
cd "${DIST_DIR}"
if command -v sha256sum >/dev/null 2>&1; then
  sha256sum yz_* > checksums.txt
elif command -v shasum >/dev/null 2>&1; then
  shasum -a 256 yz_* > checksums.txt
fi
cat checksums.txt

echo "==> Release build succeeded!"
