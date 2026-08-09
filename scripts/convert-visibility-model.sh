#!/usr/bin/env bash
# Reproducible conversion recipe for the face-region visibility model
# (face.DefaultVisibilitySpec): MediaPipe Multiclass Segmentation, TFLite → ONNX.
#
# Provenance:
#   source   https://storage.googleapis.com/mediapipe-models/image_segmenter/selfie_multiclass_256x256/float32/latest/selfie_multiclass_256x256.tflite
#   sha256   c6748b1253a99067ef71f7e26ca71096cd449baefa8f101900ea23016507e0e0  (TFLite, fetched 2026-08-09)
#   license  Apache-2.0 (per the model card: "Model Card Multiclass Segmentation", Google);
#            the converted artifact inherits it.
#   tooling  tf2onnx 1.17.0 (tensorflow 2.19.1), opset 13, --inputs-as-nchw
#   output   selfie_multiclass_256x256.onnx
#   sha256   5b9a144822d3bf829eca6317084d383864e803bb0cc9dec914ba18bbfcaf4dba
#   I/O      input_29 [1,3,256,256] float32 RGB in [0,1]  →  Identity [1,256,256,6] logits
#            classes: 0 background · 1 hair · 2 body-skin · 3 face-skin · 4 clothes · 5 other
#
# The converted artifact is hosted as the `face-visibility-model-v1` release asset (Google
# publishes only the TFLite); scripts/fetch-face-models.sh downloads and pin-verifies it.
# Re-running this script must reproduce the output sha; a tooling upgrade that changes the
# graph requires a new release tag AND a new pin in face/models.go.
set -euo pipefail

WORK="${1:-$(mktemp -d)}"
cd "$WORK"

SRC_URL="https://storage.googleapis.com/mediapipe-models/image_segmenter/selfie_multiclass_256x256/float32/latest/selfie_multiclass_256x256.tflite"
SRC_SHA="c6748b1253a99067ef71f7e26ca71096cd449baefa8f101900ea23016507e0e0"
OUT_SHA="5b9a144822d3bf829eca6317084d383864e803bb0cc9dec914ba18bbfcaf4dba"

curl -fL "$SRC_URL" -o selfie_multiclass_256x256.tflite
echo "$SRC_SHA  selfie_multiclass_256x256.tflite" | shasum -a 256 -c -

python3 -m venv venv
./venv/bin/pip install -q "tensorflow>=2.15,<2.20" tf2onnx==1.17.0 onnx
./venv/bin/python -m tf2onnx.convert \
    --tflite selfie_multiclass_256x256.tflite \
    --output selfie_multiclass_256x256.onnx \
    --opset 13 --inputs-as-nchw input_29

./venv/bin/python - <<'PY'
import onnx
m = onnx.load("selfie_multiclass_256x256.onnx")
onnx.checker.check_model(m)
ins = [(i.name, [d.dim_value for d in i.type.tensor_type.shape.dim]) for i in m.graph.input]
outs = [(o.name, [d.dim_value for d in o.type.tensor_type.shape.dim]) for o in m.graph.output]
assert ins == [("input_29", [1, 3, 256, 256])], ins
assert outs == [("Identity", [1, 256, 256, 6])], outs
print("graph verified:", ins, "->", outs)
PY

echo "$OUT_SHA  selfie_multiclass_256x256.onnx" | shasum -a 256 -c -
echo "converted + verified: $WORK/selfie_multiclass_256x256.onnx"
