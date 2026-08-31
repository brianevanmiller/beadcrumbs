#!/usr/bin/env bash
#
# Install bdc from a GitHub release.
#
#   curl -fsSL https://raw.githubusercontent.com/brianevanmiller/beadcrumbs/v1.1.0/scripts/install.sh | bash
#
# Downloads the prebuilt static-ICU archive for this platform, verifies its
# SHA-256 against the release's checksums.txt, and installs one binary.
#
# There is no source-build fallback. bdc needs Go >= 1.26.2, CGO, and ICU4C, and
# a source build links ICU dynamically against a prefix that will not exist on
# another machine. A script that quietly compiles one instead of downloading it
# hands you a binary you cannot move, so this fails with instructions instead.
#
# Environment:
#   BDC_VERSION      release to install (default: latest)
#   BDC_INSTALL_DIR  where to put the binary (default: /usr/local/bin, else ~/.local/bin)
#   BDC_BASE_URL     override the download base; accepts a local directory for testing

# -E is load-bearing, not style: without errtrace an ERR trap is not inherited by
# shell functions, and every failure path below — fetch, die, resolve_version,
# detect_platform, main itself — is inside one. Verified on bash 3.2, the /bin/bash
# every stock macOS ships and the one `curl … | bash` in the header runs.
set -Eeuo pipefail

REPO="brianevanmiller/beadcrumbs"

RED=$'\033[0;31m'; GREEN=$'\033[0;32m'; YELLOW=$'\033[1;33m'; BLUE=$'\033[0;34m'; NC=$'\033[0m'
info()    { printf '%s==>%s %s\n' "$BLUE" "$NC" "$1"; }
ok()      { printf '%s==>%s %s\n' "$GREEN" "$NC" "$1"; }
warn()    { printf '%s==>%s %s\n' "$YELLOW" "$NC" "$1"; }
die()     { printf '%sError:%s %s\n' "$RED" "$NC" "$1" >&2; exit 1; }

manual_instructions() {
  cat >&2 <<EOF

Prebuilt binaries exist for macOS and Linux on arm64 and amd64 only.
Windows is not supported.

To build from source you need Go >= 1.26.2, CGO, and ICU4C:
  macOS:  brew install icu4c
          CGO_CPPFLAGS="-I\$(brew --prefix icu4c)/include" \\
          CGO_LDFLAGS="-L\$(brew --prefix icu4c)/lib" \\
          go install github.com/${REPO}/cmd/bdc@latest
  Debian: sudo apt install libicu-dev
          go install github.com/${REPO}/cmd/bdc@latest

Releases: https://github.com/${REPO}/releases
EOF
}
trap 'manual_instructions' ERR

detect_platform() {
  case "$(uname -s)" in
    Darwin) os=darwin ;;
    Linux)  os=linux ;;
    MINGW*|MSYS*|CYGWIN*|Windows_NT)
      die "Windows is not supported. bdc runs on macOS and Linux only." ;;
    *) die "Unsupported operating system: $(uname -s). bdc runs on macOS and Linux only." ;;
  esac
  case "$(uname -m)" in
    x86_64|amd64)  arch=amd64 ;;
    aarch64|arm64) arch=arm64 ;;
    *) die "Unsupported architecture: $(uname -m). bdc ships arm64 and amd64 only." ;;
  esac
}

resolve_version() {
  if [ -n "${BDC_VERSION:-}" ]; then
    printf '%s' "${BDC_VERSION#v}"
    return
  fi
  local tag
  tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
        | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p' | head -1)
  [ -n "$tag" ] || die "cannot determine the latest release; set BDC_VERSION"
  printf '%s' "${tag#v}"
}

# Fetch from either an https base or a local directory, so this script can be
# exercised against a locally built asset without a network or a publish.
fetch() {
  local base=$1 name=$2 dest=$3
  case "$base" in
    http://*|https://*) curl -fsSL "${base}/${name}" -o "$dest" ;;
    file://*)           cp "${base#file://}/${name}" "$dest" ;;
    *)                  cp "${base}/${name}" "$dest" ;;
  esac
}

sha256_of() {
  if command -v shasum >/dev/null 2>&1; then shasum -a 256 "$1" | awk '{print $1}'
  elif command -v sha256sum >/dev/null 2>&1; then sha256sum "$1" | awk '{print $1}'
  else die "neither shasum nor sha256sum is available; cannot verify the download"
  fi
}

main() {
  echo
  info "Installing bdc"

  detect_platform
  local version base archive tmp
  version=$(resolve_version)
  base="${BDC_BASE_URL:-https://github.com/${REPO}/releases/download/v${version}}"
  archive="beadcrumbs_${version}_${os}_${arch}.tar.gz"

  info "Platform: ${os}/${arch}, version ${version}"

  tmp=$(mktemp -d)
  trap 'rm -rf "$tmp"; manual_instructions' ERR
  # shellcheck disable=SC2064
  trap "rm -rf '$tmp'" EXIT

  info "Downloading ${archive}"
  fetch "$base" "$archive" "$tmp/$archive"
  fetch "$base" "checksums.txt" "$tmp/checksums.txt"

  local expected actual
  expected=$(grep -F "$archive" "$tmp/checksums.txt" | awk '{print $1}' | head -1)
  [ -n "$expected" ] || die "$archive is not listed in checksums.txt"
  actual=$(sha256_of "$tmp/$archive")
  [ "$expected" = "$actual" ] || die "checksum mismatch: expected $expected, got $actual"
  ok "Checksum verified"

  tar -xzf "$tmp/$archive" -C "$tmp" bdc

  local install_dir="${BDC_INSTALL_DIR:-}"
  if [ -z "$install_dir" ]; then
    if [ -w /usr/local/bin ]; then install_dir=/usr/local/bin
    else install_dir="$HOME/.local/bin"; fi
  fi
  mkdir -p "$install_dir"
  install -m 0755 "$tmp/bdc" "$install_dir/bdc"

  # An unsigned binary copied out of a tarball trips Gatekeeper's slow path on
  # every run; an ad-hoc signature makes it a one-time cost.
  if [ "$os" = darwin ] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$install_dir/bdc" >/dev/null 2>&1 || true
  fi

  ok "Installed $install_dir/bdc"
  "$install_dir/bdc" version

  case ":$PATH:" in
    *":$install_dir:"*) ;;
    *) warn "$install_dir is not on your PATH"
       echo "  export PATH=\"\$PATH:$install_dir\"" ;;
  esac

  trap - ERR
  echo
  echo "Get started:"
  echo "  cd your-repo && bdc init && bdc capture \"your first fragment\""
  echo
}

main "$@"
