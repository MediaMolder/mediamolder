// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package face

import (
	"fmt"
	"image"

	ort "github.com/yalue/onnxruntime_go"
)

// exprMeshPresenceOutput is the landmark model's second output: the face-presence score as
// a RAW LOGIT ([1,1,1,1]); sigmoid() maps it to a confidence. (The model's third output, a
// tongue-out score, is deliberately not bound — the upstream pipeline drops it too.)
const exprMeshPresenceOutput = "Identity_1"

// The expression models are LAZY, OPTIONAL sessions four and five: hosts whose model
// bundles predate them keep full detect/embed(/visibility) capability, and the sessions are
// only built — and their ~7 MB only loaded — on the first [AssessFaceExpression] call.
// Failed initialisation is not cached (the ensurePipeline convention), so a host that
// fetches the models later recovers without a restart.

// ExpressionAvailable returns nil when facial-expression assessment is ready, or the reason
// it is not (detect/embed pipeline unavailable, expression models missing or mismatched).
func ExpressionAvailable() error {
	p, err := ensurePipeline()
	if err != nil {
		return err
	}
	return p.ensureExpr()
}

// AssessFaceExpression measures the facial expression of a detected face: 52 blendshape
// coefficients plus the derived Smile and EyesOpen scores. bbox is the face box in img's
// pixel space and landmarks its 5 detector keypoints (the same contracts as [Face].BBox and
// [Face].Landmarks; zero-value landmarks are tolerated — the crop is then not eye-aligned).
// Returns [ErrNoFace] (wrapped) when the landmark model is not confident the region holds a
// face — heavy occlusion, a stale bbox, a false positive — so callers record nothing instead
// of hallucinated coefficients. Deterministic for the same input on every machine (CPU
// provider; nearest-neighbour geometry).
func AssessFaceExpression(img image.Image, bbox [4]int, landmarks [numLandmarks][2]int) (Expression, error) {
	p, err := ensurePipeline()
	if err != nil {
		return Expression{}, err
	}
	if err := p.ensureExpr(); err != nil {
		return Expression{}, err
	}

	cx, cy, side, cosA, sinA, ok := exprCropGeom(bbox, landmarks)
	if !ok {
		return Expression{}, fmt.Errorf("face: empty face box %v", bbox)
	}
	crop := exprSampleCrop(img, cx, cy, side, cosA, sinA, p.exprMeshSpec.InputSize)

	p.mu.Lock()
	defer p.mu.Unlock()
	copy(p.exprMeshIn.GetData(), inputTensor(crop, p.exprMeshSpec))
	if err := p.exprMesh.Run(); err != nil {
		return Expression{}, fmt.Errorf("face: expression landmarks: %w", err)
	}
	presence := sigmoid(p.exprMeshPres.GetData()[0])
	if presence < exprPresenceThresh {
		return Expression{}, fmt.Errorf("%w (presence %.2f)", ErrNoFace, presence)
	}

	meshSubsetPoints(p.exprMeshOut.GetData(), p.exprBSIn.GetData())
	if err := p.exprBS.Run(); err != nil {
		return Expression{}, fmt.Errorf("face: blendshapes: %w", err)
	}
	return expressionFromCoeffs(p.exprBSOut.GetData(), presence), nil
}

// ensureExpr lazily builds the two expression sessions on the shared pipeline — both or
// neither. Guarded by initMu (session creation), like the pipeline itself; inference
// serialises on p.mu. CPU-pinned like the visibility session (the shared session options
// were destroyed after pipeline construction), which also keeps it deterministic; a 256²
// landmark pass + a 146-point MLP are a few ms there.
func (p *pipeline) ensureExpr() error {
	initMu.Lock()
	defer initMu.Unlock()
	if p.exprBS != nil {
		return nil
	}
	dir := resolveModelsDir()
	meshData, err := loadVerified(dir, p.exprMeshSpec)
	if err != nil {
		return err
	}
	bsData, err := loadVerified(dir, p.exprBSSpec)
	if err != nil {
		return err
	}

	fail := func(err error) error {
		for _, t := range []*ort.Tensor[float32]{p.exprMeshIn, p.exprMeshOut, p.exprMeshPres, p.exprBSIn, p.exprBSOut} {
			if t != nil {
				t.Destroy()
			}
		}
		if p.exprMesh != nil {
			p.exprMesh.Destroy()
		}
		p.exprMeshIn, p.exprMeshOut, p.exprMeshPres, p.exprBSIn, p.exprBSOut = nil, nil, nil, nil, nil
		p.exprMesh = nil
		return err
	}

	s := p.exprMeshSpec.InputSize
	if p.exprMeshIn, err = ort.NewTensor(ort.NewShape(1, 3, int64(s), int64(s)), make([]float32, 3*s*s)); err != nil {
		return fail(err)
	}
	if p.exprMeshOut, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 1, 1, exprMeshValues)); err != nil {
		return fail(err)
	}
	if p.exprMeshPres, err = ort.NewEmptyTensor[float32](ort.NewShape(1, 1, 1, 1)); err != nil {
		return fail(err)
	}
	if p.exprMesh, err = ort.NewAdvancedSessionWithONNXData(meshData,
		[]string{p.exprMeshSpec.InputName}, []string{p.exprMeshSpec.OutputName, exprMeshPresenceOutput},
		[]ort.Value{p.exprMeshIn}, []ort.Value{p.exprMeshOut, p.exprMeshPres}, nil); err != nil {
		return fail(fmt.Errorf("face: expression landmark session: %w", err))
	}

	if p.exprBSIn, err = ort.NewTensor(ort.NewShape(1, exprSubsetLen, 2), make([]float32, exprSubsetLen*2)); err != nil {
		return fail(err)
	}
	if p.exprBSOut, err = ort.NewEmptyTensor[float32](ort.NewShape(BlendshapeCount)); err != nil {
		return fail(err)
	}
	if p.exprBS, err = ort.NewAdvancedSessionWithONNXData(bsData,
		[]string{p.exprBSSpec.InputName}, []string{p.exprBSSpec.OutputName},
		[]ort.Value{p.exprBSIn}, []ort.Value{p.exprBSOut}, nil); err != nil {
		return fail(fmt.Errorf("face: blendshape session: %w", err))
	}
	return nil
}
