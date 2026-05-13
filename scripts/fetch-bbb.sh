#!/usr/bin/env bash
# Download the Big Buck Bunny 1080p stereo source file used by the test suite.
# Tests seek into this file directly (ss=450 for 10-second clips, ss=400 for
# 30-second clips) — no trimmed copies are stored in git.
#
# Usage:
#   scripts/fetch-bbb.sh [DEST]
# Default DEST: testdata/BBB_1080p.avi
#
# The file is ~733 MB. Subsequent calls are no-ops when DEST already exists.
set -euo pipefail

DEST="${1:-testdata/BBB_1080p.avi}"
URL="https://download.blender.org/peach/bigbuckbunny_movies/big_buck_bunny_1080p_stereo.avi"

if [[ -f "$DEST" ]]; then
    echo "Already present: $DEST"
    exit 0
fi

mkdir -p "$(dirname "$DEST")"
echo "Downloading Big Buck Bunny 1080p stereo (~733 MB) → $DEST"
curl -fL --retry 3 --retry-delay 5 "$URL" -o "${DEST}.tmp"
mv "${DEST}.tmp" "$DEST"
echo "Done: $DEST"
