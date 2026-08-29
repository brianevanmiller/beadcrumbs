#!/usr/bin/env bash
#
# Packaging release gate.
#
# Runs the real npm postinstall.js and the real scripts/install.sh against a
# release asset, each into its own isolated prefix, and then uses the installed
# binary. It never installs bdc globally and never publishes anything: every
# prefix is a mktemp -d that this script asserts is absent from the default PATH.
#
# `bdc version` exits before the engine opens, so it passes on a binary whose ICU
# linkage is broken. That is why the run continues into init + capture + doctor.
#
# Usage:
#   test/packaging/smoke.sh                 build the asset locally, then verify it
#   BDC_DIST=/path/to/dist test/packaging/smoke.sh    verify an already-built asset
#
# Requires: node, tar, git, and (unless BDC_DIST is set) a working Go toolchain
# with ICU4C, because cross-compilation is unavailable and the asset must be
# native.

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"
cd "$repo_root"

pass() { printf '  ok  %s\n' "$1"; }
fail() { printf '  FAIL  %s\n' "$1" >&2; exit 1; }
step() { printf '\n== %s\n' "$1"; }

case "$(uname -s)" in
  Darwin) os=darwin ;;
  Linux)  os=linux ;;
  *) fail "packaging smoke runs on macOS and Linux only; Windows is not supported" ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  arch=amd64 ;;
  aarch64|arm64) arch=arm64 ;;
  *) fail "unsupported architecture $(uname -m)" ;;
esac

version=$(sed -n 's/^const version = "\(.*\)"$/\1/p' cmd/bdc/root.go)
pkg_version=$(node -p 'require("./npm-package/package.json").version')
[ "$version" = "$pkg_version" ] ||
  fail "cmd/bdc/root.go says $version, npm-package/package.json says $pkg_version"

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

step "asset"
if [ -n "${BDC_DIST:-}" ]; then
  dist="$BDC_DIST"
else
  dist="$work/dist"
  BDC_DIST="$dist" scripts/build-release.sh "$version" >/dev/null
fi
archive="beadcrumbs_${version}_${os}_${arch}.tar.gz"
[ -f "$dist/$archive" ] || fail "no asset at $dist/$archive"
# The installers verify against checksums.txt; the build writes one .sha256 per
# archive and the release job concatenates them. Do the same here.
cat "$dist"/*.sha256 > "$dist/checksums.txt"
pass "$archive present with a checksum"

# --- npm path -----------------------------------------------------------------

step "npm postinstall into an isolated prefix"
npm_prefix="$work/npm-prefix"
mkdir -p "$npm_prefix"
cp -R npm-package "$npm_prefix/pkg"
case ":$PATH:" in *":$npm_prefix/pkg/bin:"*) fail "the isolated prefix is on PATH" ;; esac
pass "prefix $npm_prefix is not on the default PATH"

BDC_BASE_URL="file://$dist" node "$npm_prefix/pkg/scripts/postinstall.js" >/dev/null
npm_bdc="$npm_prefix/pkg/bin/bdc"
[ -x "$npm_bdc" ] || fail "postinstall.js did not produce an executable"
pass "postinstall.js installed and checksum-verified the asset"

step "postinstall rejects a corrupted archive"
bad="$work/bad-dist"
mkdir -p "$bad"
cp "$dist/checksums.txt" "$bad/"
printf 'not a tarball' > "$bad/$archive"
rm -rf "$npm_prefix/pkg/bin"
if BDC_BASE_URL="file://$bad" node "$npm_prefix/pkg/scripts/postinstall.js" >/dev/null 2>&1; then
  fail "postinstall.js accepted an archive whose checksum does not match"
fi
[ ! -e "$npm_prefix/pkg/bin/bdc" ] || fail "a failed install left a binary behind"
pass "checksum mismatch aborts the install with nothing written"

BDC_BASE_URL="file://$dist" node "$npm_prefix/pkg/scripts/postinstall.js" >/dev/null

# --- shell installer path -----------------------------------------------------

step "scripts/install.sh into an isolated prefix"
sh_prefix="$work/sh-prefix/bin"
case ":$PATH:" in *":$sh_prefix:"*) fail "the isolated prefix is on PATH" ;; esac
BDC_VERSION="$version" BDC_BASE_URL="file://$dist" BDC_INSTALL_DIR="$sh_prefix" \
  bash scripts/install.sh >/dev/null
sh_bdc="$sh_prefix/bdc"
[ -x "$sh_bdc" ] || fail "install.sh did not produce an executable"
pass "install.sh installed and checksum-verified the asset"

step "install.sh refuses an unlisted asset"
if BDC_VERSION="9.9.9" BDC_BASE_URL="file://$dist" BDC_INSTALL_DIR="$work/never" \
   bash scripts/install.sh >/dev/null 2>"$work/install-fail.err"; then
  fail "install.sh installed a version that is not in the release"
fi
[ ! -e "$work/never/bdc" ] || fail "a failed install left a binary behind"
# The script's whole safety-net UX is the ERR trap printing manual_instructions.
# Exiting nonzero says nothing about whether it fired: without `set -E` the trap
# is not inherited by the functions every failure happens inside, and the user
# gets a bare curl error with no idea what to do next.
if ! grep -q "Prebuilt binaries exist" "$work/install-fail.err"; then
  cat "$work/install-fail.err" >&2
  fail "a failed install printed no manual instructions"
fi
pass "a missing asset fails loudly with build-from-source instructions"

# --- the installed binary -----------------------------------------------------

step "linkage"
if [ "$os" = darwin ]; then
  if otool -L "$npm_bdc" | tail -n +2 | grep -vqE '^[[:space:]]+/(usr/lib|System/Library)/'; then
    otool -L "$npm_bdc" >&2
    fail "the released binary links a non-system dylib"
  fi
else
  if ldd "$npm_bdc" | grep -iq icu; then
    ldd "$npm_bdc" >&2
    fail "the released binary links ICU dynamically"
  fi
fi
pass "no non-system dynamic libraries"

step "the installed binary works"
"$npm_bdc" version --json | node -e '
  let s = "";
  process.stdin.on("data", (d) => (s += d)).on("end", () => {
    const e = JSON.parse(s);
    if (!e.ok || e.data.version !== process.argv[1]) {
      console.error("unexpected version envelope: " + s);
      process.exit(1);
    }
  });
' "$version"
pass "bdc version --json reports $version"

fixture="$work/fixture"
mkdir -p "$fixture"
git -C "$fixture" init -q
git -C "$fixture" config user.email smoke@example.com
git -C "$fixture" config user.name smoke

"$npm_bdc" -C "$fixture" init --json >/dev/null
pass "bdc init"

"$npm_bdc" -C "$fixture" capture "Packaging smoke: the released binary opens its own ledger." \
  --json >/dev/null
pass "bdc capture"

"$npm_bdc" -C "$fixture" doctor --json | node -e '
  let s = "";
  process.stdin.on("data", (d) => (s += d)).on("end", () => {
    const e = JSON.parse(s);
    if (!e.ok || e.data.ok !== true) {
      console.error("doctor reports an unhealthy ledger: " + s);
      process.exit(1);
    }
  });
'
pass "bdc doctor reports a healthy ledger"

[ -z "$(git -C "$fixture" status --porcelain)" ] || fail "the ledger showed up in git status"
pass "git status is clean"

step "nothing was installed globally"
for prefix in "$npm_prefix/pkg/bin" "$sh_prefix"; do
  case ":$PATH:" in *":$prefix:"*) fail "$prefix ended up on PATH" ;; esac
done
if command -v bdc >/dev/null 2>&1; then
  resolved=$(command -v bdc)
  case "$resolved" in
    "$npm_bdc"|"$sh_bdc") fail "a smoke-test prefix is resolving as the global bdc" ;;
    *) printf '  note  a pre-existing bdc is on PATH at %s; untouched\n' "$resolved" ;;
  esac
fi
pass "no global install, no publish"

printf '\npackaging smoke passed\n'
