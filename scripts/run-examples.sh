#!/usr/bin/env bash
# run-examples.sh — smoke-test every runnable example job.
#
# Usage:
#   scripts/run-examples.sh [INPUT_FILE [OUTPUT_DIR [BINARY]]]
#
# Defaults:
#   INPUT_FILE  /Volumes/SSD/sources/big_buck_bunny_1080p_stereo.avi
#   OUTPUT_DIR  /tmp/mm-example-tests
#   BINARY      ./mediamolder   (must be pre-built; run `make build` first)
#
# Each example's "{{input}}" / "{{output}}" placeholders are filled in via
# `mediamolder run --set KEY=VALUE`.  The job JSON files live in
# testdata/examples/ and use empty-string URLs by default so they work as
# templates in the GUI; the --set mechanism lets this script (or any developer)
# override them without touching the source files.
#
# Skip categories (printed as SKIP, not counted as failures):
#   CUDA / NVENC (21, 30)     — needs NVIDIA GPU
#   VAAPI (22)                — needs /dev/dri/renderD128
#   QSV (28)                  — needs Intel QSV driver
#   YOLOv8 (29-32)            — needs ONNX model files
#   Subtitle burn-in (23, 24) — needs .srt / .ass subtitle files
#   Subtitle tracks (25, 26)  — input file has no subtitle stream
#
# Exit code: 0 if all runnable tests pass, 1 otherwise.

set -euo pipefail

# ---------------------------------------------------------------------------
# Configuration
# ---------------------------------------------------------------------------
INPUT="${1:-/Volumes/SSD/sources/big_buck_bunny_1080p_stereo.avi}"
OUTDIR="${2:-/tmp/mm-example-tests}"
BINARY="${3:-./mediamolder}"
EXAMPLES="testdata/examples"

# ---------------------------------------------------------------------------
# Helpers
# ---------------------------------------------------------------------------
PASS=0
FAIL=0
SKIP=0
FAILED_LIST=""

run_example() {
    local id="$1"      # e.g. "01_simple_transcode"
    local file="$2"    # path to the job JSON
    local output="$3"  # output filename (relative to OUTDIR)
    shift 3
    # remaining args are optional extra --set KEY=VALUE pairs

    printf "  %-40s " "$id"

    local logfile="$OUTDIR/logs/${id}.log"
    local args=(run
        --set "input=$INPUT"
        --set "output=$OUTDIR/$output"
    )
    while [[ $# -gt 0 ]]; do
        args+=(--set "$1")
        shift
    done
    args+=("$file")

    if "$BINARY" "${args[@]}" >"$logfile" 2>&1; then
        echo "PASS"
        PASS=$((PASS + 1))
    else
        echo "FAIL  (see $logfile)"
        FAIL=$((FAIL + 1))
        FAILED_LIST="$FAILED_LIST $id"
    fi
}

skip_example() {
    local id="$1"
    local reason="$2"
    printf "  %-40s SKIP  (%s)\n" "$id" "$reason"
    SKIP=$((SKIP + 1))
}

# ---------------------------------------------------------------------------
# Pre-flight
# ---------------------------------------------------------------------------
if [[ ! -f "$INPUT" ]]; then
    echo "ERROR: input file not found: $INPUT" >&2
    exit 1
fi
if [[ ! -x "$BINARY" ]]; then
    echo "ERROR: binary not found or not executable: $BINARY" >&2
    echo "       Run 'make build' first." >&2
    exit 1
fi

mkdir -p "$OUTDIR/logs"
echo "Input:   $INPUT"
echo "Output:  $OUTDIR"
echo "Binary:  $BINARY"
echo "----------------------------------------"

# ---------------------------------------------------------------------------
# Tests
# ---------------------------------------------------------------------------
echo "Running examples..."

run_example "01_simple_transcode"     "$EXAMPLES/01_simple_transcode.json"     "01.mp4"
run_example "02_stream_copy"          "$EXAMPLES/02_stream_copy.json"          "02.mkv"
run_example "03_scale_filter"         "$EXAMPLES/03_scale_filter.json"         "03.mp4"
run_example "04_filter_chain"         "$EXAMPLES/04_filter_chain.json"         "04.mp4"
run_example "05_audio_filter"         "$EXAMPLES/05_audio_filter.json"         "05.mp4"
run_example "06_strip_audio"          "$EXAMPLES/06_strip_audio.json"          "06.mp4"
run_example "07_audio_only"           "$EXAMPLES/07_audio_only.json"           "07.mp3"
run_example "08_set_format"           "$EXAMPLES/08_set_format.json"           "08.mkv"
run_example "09_set_bitrate"          "$EXAMPLES/09_set_bitrate.json"          "09.mp4"
run_example "10_set_framerate"        "$EXAMPLES/10_set_framerate.json"        "10.mp4"
run_example "11_hevc"                 "$EXAMPLES/11_hevc.json"                 "11.mp4"
run_example "12_vp9_opus"             "$EXAMPLES/12_vp9_opus.json"             "12.webm"
run_example "13_text_overlay"         "$EXAMPLES/13_text_overlay.json"         "13.mp4"
run_example "14_crop_scale"           "$EXAMPLES/14_crop_scale.json"           "14.mp4"
run_example "15_audio_normalize"      "$EXAMPLES/15_audio_normalize.json"      "15.mp4"
run_example "16_video_audio_filters"  "$EXAMPLES/16_video_audio_filters.json"  "16.mp4"
run_example "17_overwrite"            "$EXAMPLES/17_overwrite.json"            "17.mp4"
run_example "18_letterbox"            "$EXAMPLES/18_letterbox.json"            "18.mp4"
run_example "19_three_filter_chain"   "$EXAMPLES/19_three_filter_chain.json"   "19.mp4"
run_example "20_quoted_paths"         "$EXAMPLES/20_quoted_paths.json"         "20.mp4"

skip_example "21_cuda_hwaccel"        "requires NVIDIA GPU (h264_nvenc)"
skip_example "22_vaapi_hwaccel"       "requires VAAPI (/dev/dri/renderD128)"

skip_example "23_burn_in_srt"         "requires a .srt subtitle file"
skip_example "24_burn_in_ass"         "requires a .ass subtitle file"
skip_example "25_subtitle_passthrough" "requires input with subtitle track"
skip_example "26_strip_subtitles"     "requires input with subtitle track"

run_example "27_bsf_remux"            "$EXAMPLES/27_bsf_remux.json"           "27.ts"

skip_example "28_qsv_bsf"            "requires Intel QSV driver"
skip_example "29_yolov8_basic"        "requires YOLOv8 ONNX model"
skip_example "30_yolov8_cuda_nth_frame" "requires NVIDIA GPU + YOLOv8 model"
skip_example "31_yolov8_custom_model" "requires custom YOLOv8 ONNX model"
skip_example "32_yolov8_metadata_to_file" "requires YOLOv8 ONNX model"

run_example "33_frame_info_to_file"   "$EXAMPLES/33_frame_info_to_file.json"   "33.mp4"
run_example "34_scene_change_to_file" "$EXAMPLES/34_scene_change_to_file.json" "34.mp4"

# ABR ladder: 4 outputs with named vars
printf "  %-40s " "35_abr_ladder"
logfile="$OUTDIR/logs/35_abr_ladder.log"
if "$BINARY" run \
    --set "input=$INPUT" \
    --set "out_1080=$OUTDIR/35_1080.mp4" \
    --set "out_720=$OUTDIR/35_720.mp4"  \
    --set "out_540=$OUTDIR/35_540.mp4"  \
    --set "out_360=$OUTDIR/35_360.mp4"  \
    "$EXAMPLES/35_abr_ladder.json" >"$logfile" 2>&1; then
    echo "PASS"
    PASS=$((PASS + 1))
else
    echo "FAIL  (see $logfile)"
    FAIL=$((FAIL + 1))
    FAILED_LIST="$FAILED_LIST 35_abr_ladder"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo "----------------------------------------"
echo "Results: PASS=$PASS  FAIL=$FAIL  SKIP=$SKIP"
if [[ -n "$FAILED_LIST" ]]; then
    echo "Failed: $FAILED_LIST"
    exit 1
fi
