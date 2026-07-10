#!/usr/bin/env bash
# Build the bundled, version-pinned LibRaw that the `raw` package (build tag with_libraw) links
# statically. LibRaw implements patented decode paths, so we ship NO binary — this script builds
# it from the pinned, SHA-256-verified source into third_party/libraw/ (gitignored), exactly the
# way `make build-gui-libraw` expects. Mirrors scripts/fetch-face-models.sh.
#
# It is built static, with jpeg/jasper/lcms/openmp OFF (a faithful sRGB develop needs none of
# them) and zlib ON (deflate-compressed DNG), so the link is self-contained (see the per-platform
# LDFLAGS in raw/cgo_flags_libraw.go). On macOS it builds arm64 + x86_64 and lipos them into a
# universal archive. On Windows, run from the SAME MSYS2 environment (MINGW64/UCRT64) the daemon
# builds with, so the archive's CRT matches the cgo toolchain.
#
# Usage:  scripts/bundle-libraw.sh
# Keep the version + SHA below in sync with raw/pins.go (LibRawVersion / LibRawSourceSHA256).
set -euo pipefail

VERSION="0.21.3"
SHA256="dba34b7fc1143503942fa32ad9db43e94f714e62a4a856e91617f8f3e1e0aa5c"
URL="https://www.libraw.org/data/LibRaw-${VERSION}.tar.gz"

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
DEST="$ROOT/third_party/libraw"          # install prefix: include/ + lib/
WORK="$ROOT/third_party/.libraw-build"   # scratch (download + per-arch build trees)

# Already built? (idempotent — skip the multi-minute build.)
if [[ -f "$DEST/lib/libraw.a" && -f "$DEST/include/libraw/libraw.h" ]]; then
    echo "LibRaw already bundled at $DEST (delete it to rebuild)."
    exit 0
fi

# On Windows the archive must be compiled by the same CRT/toolchain flavor cgo links with — a
# plain-MSYS gcc produces a cygwin-runtime archive that fails the later go build with cryptic
# undefined __imp_* references. Warn early instead.
case "$(uname -s)" in
    MINGW*|MSYS*)
        if [[ "${MSYSTEM:-MSYS}" == "MSYS" ]]; then
            echo "warning: run this from the MSYS2 MinGW64 (or UCRT64) shell — the plain MSYS shell's gcc" >&2
            echo "         builds a cygwin-runtime archive that the daemon's cgo link cannot use." >&2
        fi
        ;;
esac

mkdir -p "$WORK"
TARBALL="$WORK/LibRaw-${VERSION}.tar.gz"

verify() { # <file> <sha256>
    # macOS ships shasum (perl); MSYS2/mingw (the Windows daemon toolchain) ships sha256sum.
    local got
    if command -v sha256sum >/dev/null 2>&1; then
        got="$(sha256sum "$1" | awk '{print $1}')"
    else
        got="$(shasum -a 256 "$1" | awk '{print $1}')"
    fi
    [[ "$got" == "$2" ]] || { echo "SHA-256 mismatch for $1:" >&2; echo "  got  $got" >&2; echo "  want $2" >&2; return 1; }
    echo "verified $(basename "$1")"
}

if [[ ! -f "$TARBALL" ]] || ! verify "$TARBALL" "$SHA256" 2>/dev/null; then
    echo "downloading LibRaw ${VERSION} …"
    curl -fL --retry 3 --retry-delay 3 "$URL" -o "${TARBALL}.tmp"
    verify "${TARBALL}.tmp" "$SHA256"
    mv "${TARBALL}.tmp" "$TARBALL"
fi

CONFIGURE_FLAGS=(
    --disable-shared --enable-static
    --disable-jpeg --disable-jasper --disable-lcms --enable-zlib
    --disable-openmp --disable-examples --disable-dependency-tracking
)

# build_arch <arch>  — configure+make+install LibRaw for one arch into $WORK/stage-<arch>.
# Written for macOS's stock bash 3.2: split `local` lines (one `local` per var that
# references a previously-declared one) and guard empty-array expansion.
build_arch() {
    local arch="$1"
    local src="$WORK/src-${arch}"
    local stage="$WORK/stage-${arch}"
    rm -rf "$src" "$stage"; mkdir -p "$src"
    tar xzf "$TARBALL" -C "$src" --strip-components=1
    local extra=()
    if [[ "$(uname -s)" == "Darwin" ]]; then
        extra=(CC="clang -arch ${arch}" CXX="clang++ -arch ${arch}")
        [[ "$arch" == "x86_64" ]] && extra+=(--host=x86_64-apple-darwin)
        [[ "$arch" == "arm64"  ]] && extra+=(--host=aarch64-apple-darwin)
    fi
    ( cd "$src" && ./configure --prefix="$stage" "${CONFIGURE_FLAGS[@]}" ${extra[@]+"${extra[@]}"} \
        && make -j"$(getconf _NPROCESSORS_ONLN 2>/dev/null || echo 4)" && make install ) >/dev/null
    echo "built libraw ($arch)"
}

rm -rf "$DEST"; mkdir -p "$DEST/lib" "$DEST/include"

if [[ "$(uname -s)" == "Darwin" ]]; then
    build_arch arm64
    build_arch x86_64
    cp -R "$WORK/stage-arm64/include/." "$DEST/include/"
    for lib in libraw.a libraw_r.a; do
        lipo -create "$WORK/stage-arm64/lib/$lib" "$WORK/stage-x86_64/lib/$lib" -output "$DEST/lib/$lib"
    done
    echo "lipo'd universal (arm64 + x86_64)"
else
    arch="$(uname -m)"
    build_arch "$arch"
    cp -R "$WORK/stage-${arch}/include/." "$DEST/include/"
    cp "$WORK/stage-${arch}/lib/"libraw*.a "$DEST/lib/"
fi

# Bundle the chosen license text (LibRaw is dual LGPL-2.1 / CDDL-1.0; we record CDDL-1.0).
for s in "$WORK"/src-*; do
    if [[ -f "$s/LICENSE.CDDL" ]]; then cp "$s/LICENSE.CDDL" "$DEST/LICENSE.CDDL"; break; fi
done

echo "Done. Bundled LibRaw ${VERSION} → $DEST"
echo "Now build with LibRaw, e.g.:  make build-gui-libraw   (or: go build -tags with_libraw ./...)"
