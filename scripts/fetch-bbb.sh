#!/usr/bin/env bash
# Provision the Big Buck Bunny 1080p source used by the test suite.
#
# The Blender original is MS-MPEG-4 v2 video / MP3 audio in an AVI container,
# which cannot be stream-copied into MP4 or fed to H.264 bitstream filters. So we
# download it once and transcode to H.264 / AAC MP4 — the form the example/community
# tests expect. Tests seek into this file directly (ss=450 for 10-second clips,
# ss=400 for 30-second clips); the transcode preserves duration, so the offsets are
# unchanged. No trimmed copies are stored in git.
#
# Usage:
#   scripts/fetch-bbb.sh [DEST]
# Default DEST: testdata/BBB_1080p.mp4
#
# Requires ffmpeg on PATH. Subsequent calls are no-ops when DEST already exists.
set -euo pipefail

DEST="${1:-testdata/BBB_1080p.mp4}"

# PRIMARY: the pre-transcoded artifact this script would produce, hosted as the
# bbb-testdata-v1 release asset and pin-verified — the upstream sources rotted out from
# under CI in 2026-08 (download.blender.org answers 403/404; web.archive.org rate-limits
# large transfers to a crawl). Big Buck Bunny is (c) 2008 Blender Foundation /
# www.bigbuckbunny.org, CC-BY 3.0 — redistribution of the transcode with attribution is
# permitted (see the release notes). FALLBACK: download the original and transcode
# locally, exactly as before; the tests seek fixed offsets, so only this exact cut is
# acceptable (the 2013 "sunflower" remaster is a different edit).
MP4_URL="https://github.com/MediaMolder/MediaMolder/releases/download/bbb-testdata-v1/BBB_1080p.mp4"
MP4_SHA="b7bea3d0be5ea34deb8f0c3e3cf3a26e7758fb7ac7e9f62d7f0cdf73a8737717"
URLS=(
    "https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_1080p_stereo.avi"
    "https://web.archive.org/web/20230708170654id_/https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_1080p_stereo.avi"
)

if [[ -f "$DEST" ]]; then
    echo "Already present: $DEST"
    exit 0
fi

# sha256 <file> — prints the digest (shasum on macOS, sha256sum elsewhere).
sha256() {
    if command -v sha256sum >/dev/null 2>&1; then
        sha256sum "$1" | awk '{print $1}'
    else
        shasum -a 256 "$1" | awk '{print $1}'
    fi
}

# The pre-transcoded release asset: one fast, pin-verified download; done.
mkdir -p "$(dirname "$DEST")"
echo "Downloading pre-transcoded BBB test source (~367 MB) ← $MP4_URL"
if curl -fL --retry 3 --retry-delay 5 "$MP4_URL" -o "${DEST}.tmp"; then
    got="$(sha256 "${DEST}.tmp")"
    if [[ "$got" == "$MP4_SHA" ]]; then
        mv "${DEST}.tmp" "$DEST"
        echo "Done: $DEST (release asset, sha verified)"
        exit 0
    fi
    echo "release asset sha mismatch (got $got); falling back to source transcode" >&2
fi
rm -f "${DEST}.tmp"

command -v ffmpeg >/dev/null 2>&1 || { echo "error: ffmpeg not found on PATH" >&2; exit 1; }

mkdir -p "$(dirname "$DEST")"
SRC="${DEST%.*}.src.avi"   # the downloaded original; removed after a successful transcode

if [[ ! -f "$SRC" ]]; then
    # fetch_resumable: curl with resume (-C -), looping while the file is still GROWING.
    # web.archive.org serves this file but cuts each HTTP/1.1 connection after ~100 MB
    # (h2 aborts even earlier with curl error 92), so a single-shot fetch can never
    # finish — resumed connections stitch the whole file. Three consecutive attempts
    # with no growth ⇒ the source is genuinely dead (e.g. the 404ing primary), give up
    # and try the next mirror. Success = curl exit 0, i.e. the server completed the
    # final range; the transcode below is the content validator.
    fetch_resumable() {
        local url="$1" out="$2" stall=0 size=0 newsize
        while :; do
            if curl -fL --http1.1 -C - "$url" -o "$out"; then
                return 0
            fi
            newsize=$(stat -f%z "$out" 2>/dev/null || stat -c%s "$out" 2>/dev/null || echo 0)
            if [[ "$newsize" -gt "$size" ]]; then
                stall=0
                size="$newsize"
            else
                stall=$((stall + 1))
            fi
            [[ "$stall" -ge 3 ]] && return 1
            echo "retrying (resume at ${newsize} bytes)" >&2
            sleep 3
        done
    }

    ok=""
    for url in "${URLS[@]}"; do
        echo "Downloading Big Buck Bunny 1080p stereo (~733 MB) ← $url"
        if fetch_resumable "$url" "${SRC}.tmp"; then
            ok=1
            break
        fi
        echo "source failed, trying the next mirror" >&2
        rm -f "${SRC}.tmp"
    done
    [[ -n "$ok" ]] || { echo "error: every BBB source failed" >&2; exit 1; }
    mv "${SRC}.tmp" "$SRC"
fi

echo "Transcoding to H.264 / AAC MP4 → $DEST"
ffmpeg -hide_banner -nostdin -y -i "$SRC" \
    -map 0:v:0 -map 0:a:0 \
    -c:v libx264 -preset ultrafast -crf 28 -pix_fmt yuv420p \
    -c:a aac -b:a 128k \
    -f mp4 "${DEST}.tmp"   # explicit muxer: the .tmp suffix hides the .mp4 extension
mv "${DEST}.tmp" "$DEST"
rm -f "$SRC"
echo "Done: $DEST"
