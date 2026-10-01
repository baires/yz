#!/usr/bin/env bash
set -eu
if [ "${GITHUB_EVENT_NAME}" != "push" ]; then
  exit 0
fi
make test
VERSION="${RELEASE_TAG}" bash scripts/release.sh
test -s dist/checksums.txt
test -s "dist/yz_${RELEASE_TAG}_darwin_amd64"
test -s "dist/yz_${RELEASE_TAG}_darwin_arm64"
test -s "dist/yz_${RELEASE_TAG}_linux_amd64"
test -s "dist/yz_${RELEASE_TAG}_linux_arm64"
cp dist/checksums.txt dist/yz_* "${ASSETS_DIR}/"
if [ "${FIRST_RELEASE}" = "true" ]; then
  printf 'Initial release.\n' > "${RELEASE_NOTES_FILE}"
fi
printf '%s\n' "${RELEASE_TAG}" > version.txt
git add version.txt
git commit -m "bump version.txt to ${RELEASE_TAG}"
git tag "${RELEASE_TAG}"
