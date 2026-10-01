#!/bin/sh
# install.sh: Install a published yz release binary. Asset names match scripts/release.sh.
set -eu

repo="${YZ_REPO:-baires/yz}"
github_base="${YZ_GITHUB_BASE:-https://github.com}"
bin_dir="${YZ_INSTALL_DIR:-}"
version="${YZ_VERSION:-}"
dry_run=0
work=""
stage=""

info() {
  printf '%s\n' "$1" >&2
}

die() {
  printf 'error: %s\n' "$1" >&2
  exit 1
}

usage() {
  cat <<'EOF'
Usage: install.sh [--version TAG] [--bin-dir DIR] [--dry-run]

Download a static yz release for this machine, verify its SHA-256, and
install it. Release assets are named yz_<tag>_<os>_<arch> and are listed
in checksums.txt, the same files a Homebrew formula can install later.

  curl -fsSL https://raw.githubusercontent.com/baires/yz/main/install.sh | sh

Options:
  -v, --version TAG   Release tag to install (default: latest)
  -b, --bin-dir DIR   Install directory (default: a writable directory on PATH,
                      otherwise ~/.local/bin)
      --dry-run       Print the selected asset and directory, then exit
  -h, --help          Show this help

Environment:
  YZ_VERSION          Same as --version
  YZ_INSTALL_DIR      Same as --bin-dir
  YZ_REPO             GitHub owner/name (default: baires/yz)
  YZ_GITHUB_BASE      GitHub base URL (default: https://github.com)
EOF
}

need() {
  command -v "$1" >/dev/null 2>&1 || die "required command not found: $1"
}

normalize_dir() {
  local norm=$1
  while [ "$norm" != "/" ] && [ "${norm%/}" != "$norm" ]; do
    norm=${norm%/}
  done
  printf '%s' "$norm"
}

on_path() {
  local target rest entry
  target=$(normalize_dir "$1")
  rest=$PATH
  while [ -n "$rest" ]; do
    entry=${rest%%:*}
    case "$rest" in
      *:*) rest=${rest#*:} ;;
      *) rest="" ;;
    esac
    [ -n "$entry" ] || continue
    entry=$(normalize_dir "$entry")
    if [ "$entry" = "$target" ]; then
      return 0
    fi
  done
  return 1
}

choose_bindir() {
  local candidate
  if [ -n "$bin_dir" ]; then
    normalize_dir "$bin_dir"
    return
  fi
  for candidate in /usr/local/bin /opt/homebrew/bin "${HOME}/.local/bin" "${HOME}/bin"; do
    if [ -d "$candidate" ] && [ -w "$candidate" ] && on_path "$candidate"; then
      printf '%s' "$candidate"
      return
    fi
  done
  printf '%s' "${HOME}/.local/bin"
}

detect_target() {
  local raw_os raw_arch
  raw_os=$(uname -s)
  raw_arch=$(uname -m)
  case "$raw_os" in
    Darwin) os=darwin ;;
    Linux) os=linux ;;
    *) die "unsupported OS: ${raw_os} (published binaries are darwin and linux)" ;;
  esac
  case "$raw_arch" in
    x86_64|amd64) arch=amd64 ;;
    arm64|aarch64) arch=arm64 ;;
    *) die "unsupported architecture: ${raw_arch} (published binaries are amd64 and arm64)" ;;
  esac
}

validate_repo() {
  case "$repo" in
    [A-Za-z0-9_.-]*/[A-Za-z0-9_.-]*) ;;
    *) die "invalid YZ_REPO: ${repo}" ;;
  esac
  case "$repo" in
    */*/*|../*|*/..|*/.|./*) die "invalid YZ_REPO: ${repo}" ;;
  esac
}

validate_version() {
  case "$version" in
    ""|*[!A-Za-z0-9._+-]*|-* ) die "invalid version: ${version}" ;;
  esac
}

curl_get() {
  local url=$1
  local out=${2:-}
  if [ -n "$out" ]; then
    case "$url" in
      https://*)
        curl --fail --silent --show-error --location --retry 3 --retry-delay 1 \
          --proto '=https' --tlsv1.2 \
          "$url" -o "$out"
        ;;
      *)
        curl --fail --silent --show-error --location --retry 3 --retry-delay 1 \
          "$url" -o "$out"
        ;;
    esac
    return
  fi
  case "$url" in
    https://*)
      curl --fail --silent --show-error --location --retry 3 --retry-delay 1 \
        --proto '=https' --tlsv1.2 \
        -o /dev/null -w '%{url_effective}' \
        "$url"
      ;;
    *)
      curl --fail --silent --show-error --location --retry 3 --retry-delay 1 \
        -o /dev/null -w '%{url_effective}' \
        "$url"
      ;;
  esac
}

resolve_version() {
  local latest
  if [ -n "$version" ]; then
    validate_version
    return
  fi
  latest=$(curl_get "${github_base}/${repo}/releases/latest") || die "could not find releases for ${repo}"
  case "$latest" in
    */releases/tag/*) version=${latest##*/} ;;
    *) die "no published release for ${repo}. Build from source, or pass --version TAG." ;;
  esac
  validate_version
}

checksum_file() {
  local file=$1
  if command -v sha256sum >/dev/null 2>&1; then
    sha256sum "$file" | awk '{print $1}'
  elif command -v shasum >/dev/null 2>&1; then
    shasum -a 256 "$file" | awk '{print $1}'
  else
    die "need sha256sum or shasum to verify the download"
  fi
}

expected_checksum() {
  local asset=$1
  local sums=$2
  local expected
  expected=$(awk -v asset="$asset" '
    {
      name = $2
      sub(/^\*/, "", name)
      if (name == asset) {
        print $1
        found = 1
        exit
      }
    }
    END { if (!found) exit 1 }
  ' "$sums") || die "checksums.txt has no entry for ${asset}"
  printf '%s' "$expected" | grep -Eq '^[0-9a-fA-F]{64}$' || die "invalid checksum for ${asset}"
  printf '%s' "$expected" | tr 'A-F' 'a-f'
}

cleanup() {
  if [ -n "$work" ]; then
    rm -rf "$work"
  fi
  if [ -n "$stage" ]; then
    rm -f "$stage"
  fi
}

while [ $# -gt 0 ]; do
  case "$1" in
    -h|--help)
      usage
      exit 0
      ;;
    -v|--version)
      [ $# -ge 2 ] || die "--version requires a release tag"
      version=$2
      shift 2
      ;;
    -b|--bin-dir)
      [ $# -ge 2 ] || die "--bin-dir requires a directory"
      bin_dir=$2
      shift 2
      ;;
    --dry-run)
      dry_run=1
      shift
      ;;
    --)
      shift
      break
      ;;
    -*)
      die "unknown option: $1 (see --help)"
      ;;
    *)
      die "unexpected argument: $1 (see --help)"
      ;;
  esac
done

[ $# -eq 0 ] || die "unexpected argument: $1 (see --help)"

need curl
need awk
need uname
validate_repo
github_base=${github_base%/}
case "$github_base" in
  https://*) ;;
  http://127.0.0.1|http://127.0.0.1:*|http://localhost|http://localhost:*) ;;
  *) die "YZ_GITHUB_BASE must be https, or a local http://127.0.0.1 or http://localhost URL" ;;
esac

if [ -z "$bin_dir" ] && [ -z "${HOME:-}" ]; then
  die "HOME is not set. Pass --bin-dir."
fi

detect_target
resolve_version

asset="yz_${version}_${os}_${arch}"
base="${github_base}/${repo}/releases/download/${version}"
dest=$(choose_bindir)

if [ "$dry_run" -eq 1 ]; then
  printf 'repo: %s\n' "$repo"
  printf 'version: %s\n' "$version"
  printf 'asset: %s\n' "$asset"
  printf 'url: %s/%s\n' "$base" "$asset"
  printf 'bin_dir: %s\n' "$dest"
  exit 0
fi

info "Downloading ${asset}"
work=$(mktemp -d)
trap cleanup EXIT
curl_get "${base}/${asset}" "${work}/${asset}"
curl_get "${base}/checksums.txt" "${work}/checksums.txt"

expected=$(expected_checksum "$asset" "${work}/checksums.txt")
actual=$(checksum_file "${work}/${asset}")
actual=$(printf '%s' "$actual" | tr 'A-F' 'a-f')
if [ "$actual" != "$expected" ]; then
  die "checksum mismatch for ${asset}"
fi

if [ ! -d "$dest" ]; then
  mkdir -p "$dest" || die "cannot create ${dest}"
fi
if [ ! -w "$dest" ]; then
  die "cannot write to ${dest}. Re-run with --bin-dir \"\$HOME/.local/bin\", or install there with sudo."
fi

stage="${dest}/.yz.install.$$"
cp "${work}/${asset}" "$stage"
chmod 0755 "$stage"
mv -f "$stage" "${dest}/yz"
stage=""

info "Installed ${dest}/yz"
if ! on_path "$dest"; then
  info "Add it to your PATH:"
  case "${SHELL:-}" in
    */fish) info "  fish_add_path \"${dest}\"" ;;
    *) info "  export PATH=\"${dest}:\$PATH\"" ;;
  esac
else
  existing=$(command -v yz 2>/dev/null || true)
  if [ -n "$existing" ] && [ "$existing" != "${dest}/yz" ]; then
    info "Note: another yz is earlier on PATH: ${existing}"
  fi
fi
"${dest}/yz" version >&2 || die "installed binary failed to run"
info "Next: yz setup"
