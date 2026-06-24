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
URL="https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_1080p_stereo.avi"

if [[ -f "$DEST" ]]; then
    echo "Already present: $DEST"
    exit 0
fi

command -v ffmpeg >/dev/null 2>&1 || { echo "error: ffmpeg not found on PATH" >&2; exit 1; }

mkdir -p "$(dirname "$DEST")"
SRC="${DEST%.*}.src.avi"   # the downloaded original; removed after a successful transcode

if [[ ! -f "$SRC" ]]; then
    echo "Downloading Big Buck Bunny 1080p stereo (~733 MB) → $SRC"
    curl -fL --retry 3 --retry-delay 5 "$URL" -o "${SRC}.tmp"
    mv "${SRC}.tmp" "$SRC"
fi

echo "Transcoding to H.264 / AAC MP4 → $DEST"
ffmpeg -hide_banner -nostdin -y -i "$SRC" \
    -map 0:v:0 -map 0:a:0 \
    -c:v libx264 -preset ultrafast -crf 28 -pix_fmt yuv420p \
    -c:a aac -b:a 128k \
    "${DEST}.tmp"
mv "${DEST}.tmp" "$DEST"
rm -f "$SRC"
echo "Done: $DEST"
