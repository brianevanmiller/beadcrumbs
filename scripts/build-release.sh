#!/usr/bin/env bash
#
# Build the release artifact for the host platform into dist/.
#
# Cross-compilation is not available: CGO_ENABLED=1 with a foreign GOOS fails in
# runtime/cgo without a cross C toolchain. Run this on a native runner per
# platform and architecture, then collect the dist/ directories.
#
# Usage: scripts/build-release.sh [version]   (default: read from cmd/bdc/root.go)
# Output:  $BDC_DIST, default dist/

set -euo pipefail

repo_root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$repo_root"

version="${1:-$(sed -n 's/^const version = "\(.*\)"$/\1/p' cmd/bdc/root.go)}"
[ -n "$version" ] || { echo "cannot determine version" >&2; exit 1; }

case "$(uname -s)" in
  Darwin) goos=darwin ;;
  Linux)  goos=linux ;;
  *) echo "unsupported platform: $(uname -s). macOS and Linux only." >&2; exit 1 ;;
esac
case "$(uname -m)" in
  x86_64|amd64)  goarch=amd64 ;;
  aarch64|arm64) goarch=arm64 ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

staging="$(mktemp -d)"
trap 'rm -rf "$staging"' EXIT

# `-tags icu_static` only changes the linker line -- it still passes -licuuc and
# friends, and a linker that can see both libicuuc.a and libicuuc.dylib in the
# same directory takes the shared one. Both Homebrew and libicu-dev ship them
# side by side, so the static build needs a link directory holding nothing but
# the archives. That is what this staging directory is.
if [ "$goos" = darwin ]; then
  icu_prefix="$(brew --prefix icu4c)"
  icu_include="${icu_prefix}/include"
  icu_lib="${icu_prefix}/lib"
else
  icu_lib="$(dirname "$(find /usr/lib -name libicuuc.a -print -quit)")"
  [ -d "$icu_lib" ] || { echo "libicuuc.a not found; install libicu-dev" >&2; exit 1; }
  icu_include=/usr/include
fi
for lib in libicui18n.a libicuuc.a libicudata.a; do
  [ -f "${icu_lib}/${lib}" ] || { echo "missing ${icu_lib}/${lib}" >&2; exit 1; }
  cp "${icu_lib}/${lib}" "$staging/"
done

export CGO_ENABLED=1
export CGO_CPPFLAGS="-I${icu_include}"
export CGO_LDFLAGS="-L${staging}"

out="$(mktemp -d)"
echo "building bdc ${version} for ${goos}/${goarch} with static ICU"
go build -tags icu_static -trimpath -ldflags "-s -w" -o "$out/bdc" ./cmd/bdc

# A binary that links a Homebrew or distro ICU path is not portable, and that
# failure is invisible until someone runs it on a machine without that prefix.
echo "checking dynamic linkage"
if [ "$goos" = darwin ]; then
  if otool -L "$out/bdc" | tail -n +2 | grep -vqE '^[[:space:]]+/(usr/lib|System/Library)/'; then
    otool -L "$out/bdc" >&2
    echo "release binary links a non-system dylib" >&2; exit 1
  fi
else
  if ldd "$out/bdc" | grep -iq icu; then
    ldd "$out/bdc" >&2
    echo "release binary links ICU dynamically" >&2; exit 1
  fi
fi

dist="${BDC_DIST:-dist}"
mkdir -p "$dist"
archive="${dist}/beadcrumbs_${version}_${goos}_${goarch}.tar.gz"
tar -czf "$archive" -C "$out" bdc
rm -rf "$out"

# One checksum file per archive. CI concatenates them into the release's
# checksums.txt, which is what the installers verify against.
( cd "$dist" && shasum -a 256 "$(basename "$archive")" > "$(basename "$archive").sha256" )

echo "wrote $archive"
cat "${archive}.sha256"
