#!/usr/bin/env bash
set -euo pipefail

version="${1:?version required}"
host_os="${2:?host OS required}"
host_arch="${3:?host architecture required}"
archive="golangci-lint-${version}-${host_os}-${host_arch}.tar.gz"
base_url="https://github.com/golangci/golangci-lint/releases/download/v${version}"
destination=".tools/golangci-lint-${version}-${host_os}-${host_arch}"
work_dir="$(mktemp -d)"
trap 'rm -rf "$work_dir"' EXIT

curl --fail --silent --show-error --location --retry 3 \
  "${base_url}/${archive}" -o "${work_dir}/${archive}"
curl --fail --silent --show-error --location --retry 3 \
  "${base_url}/golangci-lint-${version}-checksums.txt" -o "${work_dir}/checksums.txt"

awk -v archive="$archive" '$2 == archive { print; found = 1 } END { if (!found) exit 1 }' \
  "${work_dir}/checksums.txt" > "${work_dir}/selected-checksum.txt"
(
  cd "$work_dir"
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum --check selected-checksum.txt
  else
    shasum -a 256 --check selected-checksum.txt
  fi
  tar -xzf "$archive"
)

mkdir -p "$destination"
install -m 0755 "${work_dir}/golangci-lint-${version}-${host_os}-${host_arch}/golangci-lint" \
  "${destination}/golangci-lint"
