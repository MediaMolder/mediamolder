// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"fmt"
	"image"
	"os"
	"strings"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/onnxrt"
	ort "github.com/yalue/onnxruntime_go"
)

// EnvONNXRuntimeLib points at the onnxruntime shared library (a host application bundles it
// and sets this). Mirrors the existing processors convention; empty ⇒ default search path.
const EnvONNXRuntimeLib = "ONNXRUNTIME_SHARED_LIBRARY_PATH"

// EnvFaceEP selects the ONNX execution provider for face inference:
//
//	""/"auto"   best available — try CUDA, then DirectML, then CPU
//	"cuda"      NVIDIA CUDA only, else CPU
//	"directml"  DirectML (any Direct3D 12 GPU on Windows) only, else CPU
//	"coreml"    Apple CoreML (Neural Engine / GPU on Apple silicon) only, else CPU
//	"cpu"       force the CPU provider (deterministic; the right choice with a CPU-only runtime)
//
// A GPU provider only actually engages when the onnxruntime build at ONNXRUNTIME_SHARED_LIBRARY_PATH
// was compiled with it (the CUDA "…-gpu" build for CUDA, a DirectML build for DirectML, a CoreML
// build for CoreML). With the plain CPU build the provider append fails and inference cleanly stays
// on CPU — so a host chooses its GPU by which onnxruntime it bundles, and this never breaks a
// CPU-only setup. A single onnxruntime build carries at most one GPU provider, so the auto order
// simply picks whichever the bundled runtime offers.
//
// CoreML is opt-in only — it is deliberately NOT in the auto order. Its Neural-Engine/GPU path runs
// in reduced precision, so embeddings can differ slightly from the CPU path; a host enables it
// explicitly once it has confirmed downstream use (clustering, dedup) tolerates that shift. With
// EnvFaceEP unset, an Apple-silicon host therefore stays on the deterministic CPU provider.
const EnvFaceEP = "MEDIAMOLDER_FACE_EP"

const (
	detectAttrs    = 5 + numLandmarks*3 // YOLOv8-face: cx,cy,w,h,score + 5×(x,y,vis) = 20
	embedDim       = 128                // SFace embedding length
	faceConfThresh = 0.5
	faceIoUThresh  = 0.45
)

// pipeline holds the two ONNX sessions and their pre-allocated tensors. ONNX sessions with
// bound tensors are not safe for concurrent Run(), so all inference is serialized on mu.
type pipeline struct {
	mu sync.Mutex

	detectSpec     ModelSpec
	detectNumPreds int
	detect         *ort.AdvancedSession
	detectIn       *ort.Tensor[float32]
	detectOut      *ort.Tensor[float32]

	embedSpec ModelSpec
	embed     *ort.AdvancedSession
	embedIn   *ort.Tensor[float32]
	embedOut  *ort.Tensor[float32]

	provider string // the active execution provider both sessions run on: "cuda"/"directml"/"coreml" or "cpu"
}

var (
	initMu sync.Mutex
	pipe   *pipeline
)

// Capable reports whether the models load and the pipeline initialises (ONNX runtime present,
// models found, SHA-256 verified). Safe to call repeatedly; see [Available] for the reason
// when it returns false.
func Capable() bool { return Available() == nil }

// Available returns nil when face analysis is ready, or the specific reason it is not — no
// models directory configured, a model missing or SHA-256 mismatch, the ONNX runtime absent,
// etc. Hosts can surface this to the user instead of a generic "unavailable".
func Available() error {
	_, err := ensurePipeline()
	return err
}

// Analyze decodes path with MediaMolder's deterministic software decoder, detects faces with
// YOLOv8-face, aligns each to the canonical 112×112, and embeds it with SFace. The returned
// embeddings are L2-normalised and reproducible across machines for the same input. It is a
// convenience wrapper over [AnalyzeImage] for a single still image or the first video frame.
func Analyze(path string) ([]Face, error) {
	p, err := ensurePipeline()
	if err != nil {
		return nil, err
	}
	img, err := decodeRGBA(path)
	if err != nil {
		return nil, err
	}
	return p.analyzeImage(img, Options{Embed: true})
}

// AnalyzeImage runs the full detect → align → embed pipeline on an already-decoded frame.
// It is the frame-level seam the CLI (per video frame) and the face_detect processor build
// on, avoiding a re-decode. Embeddings are L2-normalised and reproducible.
func AnalyzeImage(img image.Image) ([]Face, error) {
	return AnalyzeImageOpts(img, Options{Embed: true})
}

// DetectImage runs detection + alignment only, skipping the SFace embedding step — the faster
// path when callers want boxes and landmarks but not recognition vectors.
func DetectImage(img image.Image) ([]Face, error) {
	return AnalyzeImageOpts(img, Options{Embed: false})
}

// AnalyzeImageOpts is [AnalyzeImage] with explicit [Options] (thresholds and the embed toggle).
func AnalyzeImageOpts(img image.Image, o Options) ([]Face, error) {
	p, err := ensurePipeline()
	if err != nil {
		return nil, err
	}
	return p.analyzeImage(img, o)
}

// analyzeImage is the shared body behind Analyze / AnalyzeImage*. Inference is serialised on
// p.mu (ONNX sessions with bound tensors are not safe for concurrent Run()).
func (p *pipeline) analyzeImage(img image.Image, o Options) ([]Face, error) {
	conf := o.ConfThresh
	if conf <= 0 {
		conf = faceConfThresh
	}
	iou := o.IoUThresh
	if iou <= 0 {
		iou = faceIoUThresh
	}

	p.mu.Lock()
	defer p.mu.Unlock()

	dets, err := p.runDetect(img, conf, iou)
	if err != nil {
		return nil, err
	}
	faces := make([]Face, 0, len(dets))
	for _, d := range dets {
		f := toFace(d)
		if o.Embed {
			aligned := alignTo112(img, d.landmarks)
			emb, err := p.runEmbed(aligned)
			if err != nil {
				return nil, err
			}
			f.Embedding = l2Normalize(emb)
		}
		faces = append(faces, f)
	}
	return faces, nil
}

// ensurePipeline lazily builds the detect→embed pipeline, caching only success. A failed
// attempt is NOT cached, so a corrected models directory (e.g. set via [SetModelsDir] after a
// first failure in a long-running host such as the GUI) is retried on the next call instead of
// failing permanently for the process lifetime.
func ensurePipeline() (*pipeline, error) {
	initMu.Lock()
	defer initMu.Unlock()
	if pipe != nil {
		return pipe, nil
	}
	p, err := newPipeline()
	if err != nil {
		return nil, err
	}
	pipe = p
	return pipe, nil
}

// initONNX initialises the process-global ONNX Runtime via the shared onnxrt package, passing
// the SetONNXLib override; onnxrt handles env/auto-discovery, the single-init guard (shared with
// yolo_v8), and retry-on-failure. Caller holds initMu (via ensurePipeline → newPipeline).
func initONNX() error {
	return onnxrt.Init(onnxLibOverridePath())
}

// ActiveExecutionProvider reports which ONNX execution provider face inference is running on
// ("cuda"/"directml"/"coreml" for GPU, "cpu" for the software provider), or "" when the pipeline
// has not been initialised yet. Hosts can surface this so a user knows whether the GPU is in use.
func ActiveExecutionProvider() string {
	initMu.Lock()
	defer initMu.Unlock()
	if pipe == nil {
		return ""
	}
	return pipe.provider
}

// faceSessionOptions builds the session options shared by the detector and embedder, selecting the
// execution provider per EnvFaceEP (default: best available). It returns options with the first
// working GPU provider appended, or nil options — i.e. the default CPU provider — when the request
// is "cpu" or every GPU attempt fails (a CPU-only onnxruntime build, no compatible GPU), so face
// analysis always works. The caller owns the returned options and must Destroy them (nil-guarded)
// once both sessions are created; onnxruntime copies the options into each session.
func faceSessionOptions() (opts *ort.SessionOptions, provider string) {
	names := providersFor(os.Getenv(EnvFaceEP))
	if len(names) == 0 {
		return nil, "cpu" // "cpu" (or the deterministic opt-out): default options, runtime untouched
	}
	return tryProviders(names...)
}

// providersFor maps an EnvFaceEP value to the execution providers to try, in order: a single named
// GPU provider, or the auto order (CUDA then DirectML) when unset/unrecognised. An empty slice means
// "force the CPU provider". CoreML is returned ONLY when explicitly requested — never in the auto
// order — so an unset Apple-silicon host stays on the deterministic CPU path (see EnvFaceEP). Pure,
// so the routing (and that opt-in invariant) is unit-tested without touching the ONNX runtime.
func providersFor(env string) []string {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "cpu":
		return nil
	case "cuda":
		return []string{"cuda"}
	case "directml":
		return []string{"directml"}
	case "coreml":
		return []string{"coreml"}
	default:
		return []string{"cuda", "directml"}
	}
}

// tryProviders returns options with the first named provider that initialises against the loaded
// runtime, in order; if all fail (or none is named) it yields nil options = the CPU provider.
func tryProviders(names ...string) (*ort.SessionOptions, string) {
	for _, name := range names {
		o, err := ort.NewSessionOptions()
		if err != nil {
			return nil, "cpu"
		}
		if appendProvider(o, name) == nil {
			return o, name
		}
		o.Destroy() // this provider isn't in the loaded build/machine — try the next
	}
	return nil, "cpu"
}

// appendProvider appends the named GPU execution provider to o, returning the onnxruntime error
// when the loaded build lacks it (so the caller falls back). CUDA uses device 0's default options;
// DirectML first applies its documented constraints (sequential execution, memory-pattern off).
func appendProvider(o *ort.SessionOptions, name string) error {
	switch name {
	case "cuda":
		cuda, err := ort.NewCUDAProviderOptions()
		if err != nil {
			return err
		}
		defer cuda.Destroy()
		return o.AppendExecutionProviderCUDA(cuda)
	case "directml":
		if err := o.SetExecutionMode(ort.ExecutionModeSequential); err != nil {
			return err
		}
		if err := o.SetMemPattern(false); err != nil {
			return err
		}
		return o.AppendExecutionProviderDirectML(0)
	case "coreml":
		// MLComputeUnits=ALL lets CoreML place each op on the CPU, GPU, or Neural Engine. The
		// append fails (→ CPU fallback) on any runtime without the CoreML provider — non-Apple
		// platforms, or a CPU-only build — so this is safe to request anywhere.
		return o.AppendExecutionProviderCoreMLV2(map[string]string{"MLComputeUnits": "ALL"})
	default:
		return fmt.Errorf("face: unknown execution provider %q", name)
	}
}

func newPipeline() (*pipeline, error) {
	if err := initONNX(); err != nil {
		return nil, fmt.Errorf("face: onnxruntime init: %w", err)
	}
	dir := resolveModelsDir()
	p := &pipeline{detectSpec: DefaultDetectSpec, embedSpec: DefaultEmbedSpec}

	// Choose the execution provider once and share the options across both sessions; DirectML by
	// default (GPU), CPU otherwise. onnxruntime copies the options into each session, so they are
	// safe to destroy after both are created.
	opts, provider := faceSessionOptions()
	if opts != nil {
		defer opts.Destroy()
	}
	p.provider = provider
	fmt.Fprintf(os.Stderr, "face: onnx execution provider: %s\n", provider)

	// Detector: input [1,3,S,S], output [1,detectAttrs,numPreds].
	dData, err := loadVerified(dir, p.detectSpec)
	if err != nil {
		return nil, err
	}
	s := p.detectSpec.InputSize
	if p.detectIn, err = ort.NewTensor(ort.NewShape(1, 3, int64(s), int64(s)), make([]float32, 3*s*s)); err != nil {
		return nil, err
	}
	var detOutShape ort.Shape
	if p.detectSpec.MaxDet > 0 {
		detOutShape = ort.NewShape(1, int64(p.detectSpec.MaxDet), int64(nmsDetAttrs)) // end-to-end NMS
	} else {
		p.detectNumPreds = (s/8)*(s/8) + (s/16)*(s/16) + (s/32)*(s/32)
		detOutShape = ort.NewShape(1, int64(detectAttrs), int64(p.detectNumPreds)) // raw transposed
	}
	if p.detectOut, err = ort.NewEmptyTensor[float32](detOutShape); err != nil {
		p.destroy()
		return nil, err
	}
	if p.detect, err = ort.NewAdvancedSessionWithONNXData(dData,
		[]string{p.detectSpec.InputName}, []string{p.detectSpec.OutputName},
		[]ort.Value{p.detectIn}, []ort.Value{p.detectOut}, opts); err != nil {
		p.destroy()
		return nil, fmt.Errorf("face: detector session: %w", err)
	}

	// Embedder: input [1,3,112,112], output [1,embedDim].
	eData, err := loadVerified(dir, p.embedSpec)
	if err != nil {
		p.destroy()
		return nil, err
	}
	es := p.embedSpec.InputSize
	if p.embedIn, err = ort.NewTensor(ort.NewShape(1, 3, int64(es), int64(es)), make([]float32, 3*es*es)); err != nil {
		p.destroy()
		return nil, err
	}
	if p.embedOut, err = ort.NewEmptyTensor[float32](ort.NewShape(1, embedDim)); err != nil {
		p.destroy()
		return nil, err
	}
	if p.embed, err = ort.NewAdvancedSessionWithONNXData(eData,
		[]string{p.embedSpec.InputName}, []string{p.embedSpec.OutputName},
		[]ort.Value{p.embedIn}, []ort.Value{p.embedOut}, opts); err != nil {
		p.destroy()
		return nil, fmt.Errorf("face: embedder session: %w", err)
	}
	return p, nil
}

// runDetect letterboxes the frame, runs YOLOv8-face, and returns NMS-filtered detections in
// original-frame pixel coordinates, gated by confThresh (and iouThresh for raw exports).
// Caller holds p.mu.
func (p *pipeline) runDetect(img image.Image, confThresh, iouThresh float64) ([]faceDetection, error) {
	lb := letterbox(img, p.detectSpec.InputSize, p.detectSpec.InputSize)
	copy(p.detectIn.GetData(), inputTensor(lb, p.detectSpec))
	if err := p.detect.Run(); err != nil {
		return nil, fmt.Errorf("face: detect: %w", err)
	}
	b := img.Bounds()
	if p.detectSpec.MaxDet > 0 {
		// End-to-end-NMS export: boxes already filtered, no faceNMS needed.
		return parseYOLOv8FaceNMSOutput(p.detectOut.GetData(), p.detectSpec.MaxDet, p.detectSpec.InputSize,
			confThresh, b.Dx(), b.Dy()), nil
	}
	dets := parseYOLOv8FaceOutput(p.detectOut.GetData(), p.detectNumPreds, p.detectSpec.InputSize,
		confThresh, b.Dx(), b.Dy())
	return faceNMS(dets, iouThresh), nil
}

// runEmbed runs SFace on an aligned 112×112 crop, returning a copy of the embedding (the
// output tensor is reused across calls). Caller holds p.mu.
func (p *pipeline) runEmbed(aligned *image.RGBA) ([]float32, error) {
	copy(p.embedIn.GetData(), inputTensor(aligned, p.embedSpec))
	if err := p.embed.Run(); err != nil {
		return nil, fmt.Errorf("face: embed: %w", err)
	}
	out := p.embedOut.GetData()
	emb := make([]float32, len(out))
	copy(emb, out)
	return emb, nil
}

func (p *pipeline) destroy() {
	for _, s := range []*ort.AdvancedSession{p.detect, p.embed} {
		if s != nil {
			s.Destroy()
		}
	}
	for _, t := range []*ort.Tensor[float32]{p.detectIn, p.detectOut, p.embedIn, p.embedOut} {
		if t != nil {
			t.Destroy()
		}
	}
}

// decodeRGBA decodes the first image/video frame of path to *image.RGBA using MediaMolder's
// deterministic software decoder (the same path behind the pixel hash) + the zero-init
// ToRGBA, so detections and embeddings are reproducible across machines. Any decoder panic
// becomes an error so one bad file never crashes the host.
func decodeRGBA(path string) (img *image.RGBA, err error) {
	defer func() {
		if r := recover(); r != nil {
			img, err = nil, fmt.Errorf("face: decode panicked: %v", r)
		}
	}()

	input, err := av.OpenInput(path, nil)
	if err != nil {
		return nil, err
	}
	defer input.Close()

	vid := -1
	for i := 0; i < input.NumStreams(); i++ {
		si, e := input.StreamInfo(i)
		if e != nil || si.Type != av.MediaTypeVideo {
			continue
		}
		vid = i
		break
	}
	if vid < 0 {
		return nil, fmt.Errorf("face: no decodable image stream in %s", path)
	}

	dec, err := av.OpenDecoder(input, vid)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	pkt, err := av.AllocPacket()
	if err != nil {
		return nil, err
	}
	defer pkt.Close()

	drained := false
	for {
		fr, e := av.AllocFrame()
		if e != nil {
			return nil, e
		}
		if dec.ReceiveFrame(fr) == nil {
			rgba, e := fr.ToRGBA()
			fr.Close()
			return rgba, e
		}
		fr.Close()

		pkt.Unref()
		if e := input.ReadPacket(pkt); e != nil {
			if av.IsEOF(e) && !drained {
				_ = dec.Flush()
				drained = true
				continue
			}
			return nil, fmt.Errorf("face: no frame decoded: %w", e)
		}
		if pkt.StreamIndex() == vid {
			_ = dec.SendPacket(pkt)
		}
	}
}
