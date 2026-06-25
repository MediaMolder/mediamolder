#!/usr/bin/env bash
# Fetch the face-analysis models used by the `face` package (build tag with_onnx) and verify
# their pinned SHA-256. Models are NOT committed; bundle the fetched files in the host
# application and point MEDIAMOLDER_FACE_MODELS at the directory.
#
# Usage:  scripts/fetch-face-models.sh [DEST_DIR]
# Default DEST_DIR: testdata/face_models  (gitignored)
set -euo pipefail

DEST="${1:-testdata/face_models}"
mkdir -p "$DEST"

# verify <file> <sha256>  — exits non-zero on mismatch.
verify() {
    local f="$1" want="$2"
    local got
    got="$(shasum -a 256 "$f" | awk '{print $1}')"
    if [[ "$got" != "$want" ]]; then
        echo "SHA-256 mismatch for $f:" >&2
        echo "  got  $got" >&2
        echo "  want $want" >&2
        return 1
    fi
    echo "verified $f"
}

fetch() {
    local url="$1" out="$2" sha="$3"
    if [[ -f "$out" ]] && verify "$out" "$sha" 2>/dev/null; then
        echo "already present: $out"
        return 0
    fi
    echo "downloading $(basename "$out") …"
    curl -fL --retry 3 --retry-delay 3 "$url" -o "${out}.tmp"
    verify "${out}.tmp" "$sha"
    mv "${out}.tmp" "$out"
}

# --- SFace embedder (OpenCV Zoo, Apache-2.0) — verified I/O: data[1,3,112,112] → fc1[1,128]
fetch \
    "https://github.com/opencv/opencv_zoo/raw/main/models/face_recognition_sface/face_recognition_sface_2021dec.onnx" \
    "$DEST/sface.onnx" \
    "0ba9fbfa01b5270c96627c4ef784da859931e02f04419c829e83484087c34e79"

# --- YOLOv8-face detector (akanametov/yolo-face, AGPL-3.0) -----------------------------------
# Ultralytics 8.3.241 YOLOv8n-pose trained on WIDERFace, end-to-end-NMS export. Verified I/O:
# task=pose, kpt_shape=[5,3], images[1,3,640,640] → output0[1,300,21]. The face package's
# NMS-embedded parser consumes this directly (DefaultDetectSpec MaxDet=300).
fetch \
    "https://github.com/akanametov/yolo-face/releases/download/1.0.0/yolov8n-face.onnx" \
    "$DEST/yolov8n-face.onnx" \
    "06b941fd5792be624ad18f2df9ede0a021c4df165dd418204d978c20fd555928"

echo "Done. Set MEDIAMOLDER_FACE_MODELS=$DEST to enable the face pipeline."
