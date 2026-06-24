//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/face"
)

// FaceDetect is a go_processor that detects faces (YOLOv8-face), aligns each to the canonical
// 112x112, and optionally embeds it (SFace) for recognition/clustering. It is only compiled
// when the "with_onnx" build tag is set; the detect→embed pipeline and its models live in the
// face package.
//
// Video frames pass through unchanged; results are emitted as metadata: a Detection per face
// (box + score, for generic overlay consumers) plus the richer face.Record slice (landmarks +
// optional embedding) under Custom["faces"].
//
// Optional params:
//
//	"every"         — analyse every Nth video frame, default 1 (every frame).
//	"conf"          — detector confidence threshold, default 0 (face package default, 0.5).
//	"embeddings"    — also compute the 128-d SFace embedding per face, default false.
//	"models_dir"    — directory of face models (overrides MEDIAMOLDER_FACE_MODELS).
//	"output_file"   — write detections to this absolute path (a sidecar), no extra node needed.
//	"output_format" — sidecar format: jsonl (default), csv, timecodes.
type FaceDetect struct {
	hook  fileWriteHook
	every uint64
	conf  float64
	embed bool
}

func (p *FaceDetect) Init(params map[string]any) error {
	// Pull "output_file"/"output_format" out first so face_detect can self-write
	// its detections to a sidecar — no separate metadata_file_writer node needed
	// (mirrors the scene-change detectors).
	params, err := p.hook.initFromParams("face_detect", params)
	if err != nil {
		return err
	}

	p.every = 1
	if v, ok := params["every"].(float64); ok && v >= 1 {
		p.every = uint64(v)
	}
	if v, ok := params["conf"].(float64); ok && v > 0 {
		p.conf = v
	}
	if v, ok := params["embeddings"].(bool); ok {
		p.embed = v
	}
	if d, ok := params["models_dir"].(string); ok && d != "" {
		face.SetModelsDir(d)
	}
	if !face.Capable() {
		p.hook.close() // don't leak the sidecar file opened above
		return fmt.Errorf("face_detect: face models unavailable — set MEDIAMOLDER_FACE_MODELS or the " +
			"\"models_dir\" param to the bundled models (see scripts/fetch-face-models.sh)")
	}
	return nil
}

func (p *FaceDetect) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}
	if p.every > 1 && ctx.FrameIndex%p.every != 0 {
		return frame, nil, nil
	}
	img, err := frame.ToRGBA()
	if err != nil {
		return nil, nil, fmt.Errorf("face_detect: to rgba: %w", err)
	}
	faces, err := face.AnalyzeImageOpts(img, face.Options{ConfThresh: p.conf, Embed: p.embed})
	if err != nil {
		return nil, nil, fmt.Errorf("face_detect: analyze: %w", err)
	}
	if len(faces) == 0 {
		return frame, nil, nil
	}

	dets := make([]Detection, len(faces))
	recs := make([]face.Record, len(faces))
	for i, f := range faces {
		// Detection boxes are x1,y1,x2,y2; Face.BBox is x,y,w,h.
		dets[i] = Detection{
			Label:      "face",
			Confidence: float64(f.Score),
			BBox: [4]float64{
				float64(f.BBox[0]), float64(f.BBox[1]),
				float64(f.BBox[0] + f.BBox[2]), float64(f.BBox[1] + f.BBox[3]),
			},
		}
		recs[i] = f.ToRecord(ctx.FrameIndex, ctx.PTS, 0) // time base unknown here; GUI keys on frame/PTS
	}
	md := &Metadata{
		Detections: dets,
		Custom:     map[string]any{"faces": recs},
	}
	p.hook.write(ctx, md) // optional sidecar; a no-op unless output_file was set
	return frame, md, nil
}

func (p *FaceDetect) Close() error { return p.hook.close() }

func init() {
	Register("face_detect", func() Processor { return &FaceDetect{} })
}
