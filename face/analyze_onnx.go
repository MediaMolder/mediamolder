// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"fmt"
	"image"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/onnxrt"
	ort "github.com/yalue/onnxruntime_go"
)

// EnvONNXRuntimeLib points at the onnxruntime shared library (a host application bundles it
// and sets this). Mirrors the existing processors convention; empty ⇒ default search path.
const EnvONNXRuntimeLib = "ONNXRUNTIME_SHARED_LIBRARY_PATH"

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

func newPipeline() (*pipeline, error) {
	if err := initONNX(); err != nil {
		return nil, fmt.Errorf("face: onnxruntime init: %w", err)
	}
	dir := resolveModelsDir()
	p := &pipeline{detectSpec: DefaultDetectSpec, embedSpec: DefaultEmbedSpec}

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
		[]ort.Value{p.detectIn}, []ort.Value{p.detectOut}, nil); err != nil {
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
		[]ort.Value{p.embedIn}, []ort.Value{p.embedOut}, nil); err != nil {
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
