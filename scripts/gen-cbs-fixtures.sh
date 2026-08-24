#!/bin/sh
# Copyright (C) 2026 Thomas Vaughan
# SPDX-License-Identifier: LGPL-2.1-or-later
#
# Regenerates the tiny elementary-stream / container fixtures used by the
# cbs package tests. Run from the repository root. Requires an ffmpeg with
# libx264, libx265 and libsvtav1. The outputs are checked in; rerun only
# when a fixture needs to change, then regenerate the goldens too
# (scripts/gen-cbs-golden.sh).
set -eu

FFMPEG=${FFMPEG:-ffmpeg}
OUT=cbs/testdata
mkdir -p "$OUT"

SRC="-f lavfi -i testsrc2=size=64x64:rate=10 -frames:v 6 -pix_fmt yuv420p"

$FFMPEG -hide_banner -loglevel error -y $SRC \
    -c:v libx264 -preset ultrafast -x264-params keyint=3:bframes=1:aud=1 \
    -f h264 "$OUT/tiny.h264"

$FFMPEG -hide_banner -loglevel error -y $SRC \
    -c:v libx265 -preset ultrafast -x265-params keyint=3:bframes=1:aud=1:info=1 \
    -f hevc "$OUT/tiny.hevc"

$FFMPEG -hide_banner -loglevel error -y $SRC \
    -c:v libsvtav1 -preset 12 -svtav1-params keyint=3 \
    -f obu "$OUT/tiny.obu"

$FFMPEG -hide_banner -loglevel error -y $SRC \
    -c:v libsvtav1 -preset 12 -svtav1-params keyint=3 \
    -f ivf "$OUT/tiny.ivf"

$FFMPEG -hide_banner -loglevel error -y -i "$OUT/tiny.hevc" -c copy "$OUT/tiny_hevc.mp4"
$FFMPEG -hide_banner -loglevel error -y -i "$OUT/tiny.obu" -c copy "$OUT/tiny_av1.mp4"

ls -la "$OUT"
