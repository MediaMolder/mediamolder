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
    # macOS ships shasum (perl); MSYS2/mingw and Git Bash ship sha256sum (bundle-libraw.sh twin).
    if command -v sha256sum >/dev/null 2>&1; then
        got="$(sha256sum "$f" | awk '{print $1}')"
    else
        got="$(shasum -a 256 "$f" | awk '{print $1}')"
    fi
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

# --- Face-region visibility (OPTIONAL) -------------------------------------------------------
# MediaPipe Multiclass Segmentation (Google, Apache-2.0 per its model card), converted from
# the published TFLite to ONNX (NCHW input) — scripts/convert-visibility-model.sh is the
# reproducible recipe. Hosted as a MediaMolder release asset because Google publishes only
# the TFLite. Verified I/O: input_29[1,3,256,256] → Identity[1,256,256,6]. A bundle without
# this model is still fully detect/embed capable; only AssessFaceVisibility needs it.
fetch \
    "https://github.com/MediaMolder/MediaMolder/releases/download/face-visibility-model-v1/selfie_multiclass_256x256.onnx" \
    "$DEST/selfie_multiclass_256x256.onnx" \
    "5b9a144822d3bf829eca6317084d383864e803bb0cc9dec914ba18bbfcaf4dba"

# --- Facial expression (OPTIONAL) ------------------------------------------------------------
# MediaPipe Face Landmarker cascade (Google, Apache-2.0 per the FaceMesh V2 + Blendshape V2
# model cards): dense landmarks + blendshape coefficients, converted from the published
# TFLite to ONNX — scripts/convert-expression-models.sh is the recipe (and explains why the
# releases, not re-conversion bytes, are the pinned artifacts). Hosted as MediaMolder release
# assets because Google publishes only the bundled .task. Verified I/O:
#   input_12[1,3,256,256] → Identity[1,1,1,1434] + Identity_1[1,1,1,1]
#   serving_default_input_points:0[1,146,2] → StatefulPartitionedCall:0[52]
# A bundle without these is still fully detect/embed capable; only AssessFaceExpression
# needs them.
fetch \
    "https://github.com/MediaMolder/MediaMolder/releases/download/face-expression-model-v1/face_landmarks_detector.onnx" \
    "$DEST/face_landmarks_detector.onnx" \
    "3e824fb93ac4cc39a0cf3b448008311988f6b8dc3e8ecd8665a1daa1303ff635"
fetch \
    "https://github.com/MediaMolder/MediaMolder/releases/download/face-expression-model-v1/face_blendshapes.onnx" \
    "$DEST/face_blendshapes.onnx" \
    "f6bd15b960a55057dbbf05bb2f32d757c67e651b15c9c13d4d5d744ca03dd3aa"
