// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"fmt"
	"image"

	ort "github.com/yalue/onnxruntime_go"
)

// visNumClasses is the model's output channel count ([1,S,S,visNumClasses] logits).
const visNumClasses = 6

// The visibility model is a LAZY, OPTIONAL third session: hosts whose model bundles
// predate it keep full detect/embed capability ([Capable] is unchanged), and the session
// is only built — and its 16 MB only loaded — on the first [AssessFaceVisibility] call.
// Failed initialisation is not cached (the ensurePipeline convention), so a host that
// fetches the model later recovers without a restart.

// VisibilityAvailable returns nil when face-visibility assessment is ready, or the reason
// it is not (detect/embed pipeline unavailable, visibility model missing or mismatched).
func VisibilityAvailable() error {
	p, err := ensurePipeline()
	if err != nil {
		return err
	}
	return p.ensureVis()
}

// AssessFaceVisibility measures how much of a detected face is covered by something in
// front of it. bbox is the face box in img's pixel space ([4]int x,y,w,h — the same
// contract as [Face].BBox). Returns an occlusion fraction in [0,1]: 0 = the face region
// reads fully as face content, 1 = none of it does. Deterministic for the same input on
// every machine (CPU provider; nearest-neighbour geometry; argmax).
func AssessFaceVisibility(img image.Image, bbox [4]int) (float32, error) {
	p, err := ensurePipeline()
	if err != nil {
		return 0, err
	}
	if err := p.ensureVis(); err != nil {
		return 0, err
	}

	ctx := headContextRect(bbox, img.Bounds())
	if ctx.Empty() {
		return 0, fmt.Errorf("face: empty face box %v", bbox)
	}
	crop := cropRGBA(img, ctx)

	p.mu.Lock()
	defer p.mu.Unlock()
	size := p.visSpec.InputSize
	lb := letterbox(crop, size, size)
	copy(p.visIn.GetData(), inputTensor(lb, p.visSpec))
	if err := p.vis.Run(); err != nil {
		return 0, fmt.Errorf("face: visibility: %w", err)
	}
	mask := argmaxMask(p.visOut.GetData(), size)

	// Map the measurement region (bbox shrunk, in source pixels) into mask coordinates
	// through the crop offset + letterbox geometry.
	scale, offX, offY := letterboxGeom(ctx.Dx(), ctx.Dy(), size, size)
	m := measureRect(bbox)
	inMask := image.Rect(
		offX+int(float64(m.Min.X-ctx.Min.X)*scale),
		offY+int(float64(m.Min.Y-ctx.Min.Y)*scale),
		offX+int(float64(m.Max.X-ctx.Min.X)*scale),
		offY+int(float64(m.Max.Y-ctx.Min.Y)*scale),
	)
	return float32(occlusionFromMask(mask, size, size, inMask)), nil
}

// ensureVis lazily builds the visibility session on the shared pipeline. Guarded by initMu
// (session creation), like the pipeline itself; inference serialises on p.mu.
func (p *pipeline) ensureVis() error {
	initMu.Lock()
	defer initMu.Unlock()
	if p.vis != nil {
		return nil
	}
	data, err := loadVerified(resolveModelsDir(), p.visSpec)
	if err != nil {
		return err
	}
	s := p.visSpec.InputSize
	if p.visIn, err = ort.NewTensor(ort.NewShape(1, 3, int64(s), int64(s)), make([]float32, 3*s*s)); err != nil {
		return err
	}
	// Output is [1,S,S,classes] (spatial-last), logits; argmax needs no activation.
	if p.visOut, err = ort.NewEmptyTensor[float32](ort.NewShape(1, int64(s), int64(s), visNumClasses)); err != nil {
		p.visIn.Destroy()
		p.visIn = nil
		return err
	}
	// Same execution-provider choice as the other two sessions would be ideal, but the
	// options were destroyed after pipeline construction; the CPU provider keeps this
	// deterministic everywhere, and a 256² segmentation is a few ms there.
	if p.vis, err = ort.NewAdvancedSessionWithONNXData(data,
		[]string{p.visSpec.InputName}, []string{p.visSpec.OutputName},
		[]ort.Value{p.visIn}, []ort.Value{p.visOut}, nil); err != nil {
		p.visIn.Destroy()
		p.visOut.Destroy()
		p.visIn, p.visOut = nil, nil
		return fmt.Errorf("face: visibility session: %w", err)
	}
	return nil
}

// argmaxMask reduces a [1,S,S,classes] logit tensor to a row-major S×S class-id plane.
func argmaxMask(out []float32, size int) []uint8 {
	mask := make([]uint8, size*size)
	for px := range mask {
		base := px * visNumClasses
		best, bestC := out[base], 0
		for c := 1; c < visNumClasses; c++ {
			if v := out[base+c]; v > best {
				best, bestC = v, c
			}
		}
		mask[px] = uint8(bestC)
	}
	return mask
}

// cropRGBA copies the region r of img into a fresh RGBA (r must be within bounds).
func cropRGBA(img image.Image, r image.Rectangle) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, r.Dx(), r.Dy()))
	for y := 0; y < r.Dy(); y++ {
		for x := 0; x < r.Dx(); x++ {
			dst.Set(x, y, img.At(r.Min.X+x, r.Min.Y+y))
		}
	}
	return dst
}
