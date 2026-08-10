#!/usr/bin/env bash
# Reproducible conversion recipe for the face-expression models
# (face.DefaultExpressionMeshSpec / DefaultExpressionBlendshapesSpec): MediaPipe Face
# Landmarker's dense-landmark + blendshape cascade, TFLite → ONNX.
#
# Provenance:
#   source   https://storage.googleapis.com/mediapipe-models/face_landmarker/face_landmarker/float16/1/face_landmarker.task
#   sha256   64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff  (.task bundle, fetched 2026-08-10;
#            float16 is the only precision Google publishes — it is what MediaPipe itself runs)
#   members  face_landmarks_detector.tflite  c7d54204ce0448474c7f3fa9af494787c0965cbdd6f20fc72867e43046bd43d5
#            face_blendshapes.tflite         4f36dded049db18d76048567439b2a7f58f1daabc00d78bfe8f3ad396a2d2082
#   license  Apache-2.0 (per the Face Landmarker model card, Google); the converted artifacts
#            inherit it.
#   tooling  tf2onnx 1.17.0 (tensorflow 2.19.1), opset 13; --inputs-as-nchw for the image model
#   outputs  face_landmarks_detector.onnx  input_12[1,3,256,256] RGB [0,1]
#              → Identity[1,1,1,1434] (478×(x,y,z), input-crop px) · Identity_1[1,1,1,1] (presence LOGIT)
#            face_blendshapes.onnx  serving_default_input_points:0[1,146,2] (crop-px landmarks)
#              → StatefulPartitionedCall:0[52] (blendshape coefficients, [0,1])
#
# ⚠ Byte-reproducibility: tf2onnx's transpose optimizer is not run-to-run deterministic (its
# rewrite order follows Python object identity), so re-running this script produces a
# semantically identical graph with DIFFERENT bytes — a byte-compare against the release is
# meaningless. What this script verifies instead is stronger: graph I/O contracts by
# inspection, and NUMERIC EQUIVALENCE of each converted model against its TFLite source on
# seeded probe inputs. The released `face-expression-model-v1` assets are the pinned artifacts
# of record (SHA-256 in face/models.go + scripts/fetch-face-models.sh); this script exists to
# audit them and to rebuild candidates if the models are ever re-released.
set -euo pipefail

WORK="${1:-$(mktemp -d)}"
cd "$WORK"

TASK_URL="https://storage.googleapis.com/mediapipe-models/face_landmarker/face_landmarker/float16/1/face_landmarker.task"
TASK_SHA="64184e229b263107bc2b804c6625db1341ff2bb731874b0bcc2fe6544e0bc9ff"
MESH_TFLITE_SHA="c7d54204ce0448474c7f3fa9af494787c0965cbdd6f20fc72867e43046bd43d5"
BS_TFLITE_SHA="4f36dded049db18d76048567439b2a7f58f1daabc00d78bfe8f3ad396a2d2082"

curl -fL "$TASK_URL" -o face_landmarker.task
echo "$TASK_SHA  face_landmarker.task" | shasum -a 256 -c -

python3 -m venv venv
./venv/bin/pip install -q "tensorflow>=2.15,<2.20" tf2onnx==1.17.0 onnx onnxruntime

./venv/bin/python - <<'PY'
import zipfile
zipfile.ZipFile("face_landmarker.task").extractall(".")
PY
echo "$MESH_TFLITE_SHA  face_landmarks_detector.tflite" | shasum -a 256 -c -
echo "$BS_TFLITE_SHA  face_blendshapes.tflite" | shasum -a 256 -c -

./venv/bin/python -m tf2onnx.convert \
    --tflite face_landmarks_detector.tflite \
    --output face_landmarks_detector.onnx \
    --opset 13 --inputs-as-nchw input_12
./venv/bin/python -m tf2onnx.convert \
    --tflite face_blendshapes.tflite \
    --output face_blendshapes.onnx \
    --opset 13

./venv/bin/python - <<'PY'
import numpy as np, onnx, onnxruntime as ort, tensorflow as tf

# Graph contracts.
m = onnx.load("face_landmarks_detector.onnx")
onnx.checker.check_model(m)
ins = [(i.name, [d.dim_value for d in i.type.tensor_type.shape.dim]) for i in m.graph.input]
outs = {o.name: [d.dim_value for d in o.type.tensor_type.shape.dim] for o in m.graph.output}
assert ins == [("input_12", [0, 3, 256, 256])], ins
assert outs["Identity"] == [0, 1, 1, 1434] and outs["Identity_1"] == [0, 1, 1, 1], outs

b = onnx.load("face_blendshapes.onnx")
onnx.checker.check_model(b)
ins = [(i.name, [d.dim_value for d in i.type.tensor_type.shape.dim]) for i in b.graph.input]
outs = [(o.name, [d.dim_value for d in o.type.tensor_type.shape.dim]) for o in b.graph.output]
assert ins == [("serving_default_input_points:0", [1, 146, 2])], ins
assert outs == [("StatefulPartitionedCall:0", [52])], outs

# Numeric equivalence vs the TFLite source on seeded probes (the reproducibility contract —
# see the header for why byte-compare is not it). Tolerances cover fp16-constant rounding
# differences between the XNNPACK and onnxruntime kernels.
def tfl(path, feed):
    it = tf.lite.Interpreter(model_path=path)
    it.allocate_tensors()
    for d, v in zip(it.get_input_details(), feed):
        it.set_tensor(d["index"], v)
    it.invoke()
    return {d["name"]: it.get_tensor(d["index"]).copy() for d in it.get_output_details()}

rng = np.random.default_rng(20260810)
img = rng.random((1, 256, 256, 3), dtype=np.float32)
t = tfl("face_landmarks_detector.tflite", [img])
s = ort.InferenceSession("face_landmarks_detector.onnx", providers=["CPUExecutionProvider"])
lm, pres = s.run(["Identity", "Identity_1"], {"input_12": img.transpose(0, 3, 1, 2)})
d_lm = float(np.abs(t["Identity"].reshape(-1) - lm.reshape(-1)).max())
d_pr = float(np.abs(t["Identity_1"].reshape(-1) - pres.reshape(-1)).max())
assert d_lm < 2e-3 and d_pr < 1e-3, (d_lm, d_pr)

pts = (rng.random((1, 146, 2), dtype=np.float32) * 256).astype(np.float32)
t = tfl("face_blendshapes.tflite", [pts])
s = ort.InferenceSession("face_blendshapes.onnx", providers=["CPUExecutionProvider"])
bs = s.run(None, {"serving_default_input_points:0": pts})[0]
d_bs = float(np.abs(list(t.values())[0].reshape(-1) - bs.reshape(-1)).max())
assert d_bs < 1e-4, d_bs
print(f"equivalence verified: mesh maxdiff {d_lm:.2e}, presence {d_pr:.2e}, blendshapes {d_bs:.2e}")
PY

shasum -a 256 face_landmarks_detector.onnx face_blendshapes.onnx
echo "converted + verified in $WORK (shas above are release CANDIDATES; the pinned artifacts"
echo "of record live on the face-expression-model-v1 release — see the header)"
