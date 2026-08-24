#!/bin/sh
# Copyright (C) 2026 Thomas Vaughan
# SPDX-License-Identifier: LGPL-2.1-or-later
#
# Regenerates the trace_headers golden files the cbs package diffs against.
# Run from the repository root after gen-cbs-fixtures.sh. FFMPEG should be
# a build matching the libavcodec/cbs* sources the Go port follows.
set -eu

FFMPEG=${FFMPEG:-ffmpeg}
OUT=cbs/testdata/golden
mkdir -p "$OUT"

gen() {
    in=$1
    out=$2
    "$FFMPEG" -hide_banner -loglevel info -i "$in" -c copy -bsf:v trace_headers -f null - 2>&1 |
        grep '^\[trace_headers @ 0x' |
        sed -E 's/^\[trace_headers @ 0x[0-9a-f]+\] //' >"$out"
    wc -l "$out"
}

gen cbs/testdata/tiny.h264 "$OUT/tiny.h264.txt"
gen cbs/testdata/tiny.hevc "$OUT/tiny.hevc.txt"
gen cbs/testdata/tiny.obu "$OUT/tiny.obu.txt"
gen cbs/testdata/tiny.ivf "$OUT/tiny.ivf.txt"
gen av/testdata/tiny.mp4 "$OUT/tiny.mp4.txt"
gen cbs/testdata/tiny_hevc.mp4 "$OUT/tiny_hevc.mp4.txt"
gen cbs/testdata/tiny_av1.mp4 "$OUT/tiny_av1.mp4.txt"
