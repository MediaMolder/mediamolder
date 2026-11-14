// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package detect finds people, sports balls, and faces in one decoded
// picture. It runs a YOLO detect-task model (COCO classes) for people and
// balls and the face package's detector for faces, and returns every box
// in the submitted image's pixels.
//
// The models run only in a build with the with_onnx tag. Without it,
// [Open] and [Available] return [ErrUnsupported].
package detect

import (
	"errors"
	"image"
	"image/draw"
	"sync"

	"github.com/MediaMolder/MediaMolder/face"
	"github.com/MediaMolder/MediaMolder/processors"
)

// ErrUnsupported means this build has no ONNX Runtime (no with_onnx tag).
var ErrUnsupported = errors.New("detect: not available in this build")

// COCO class ids the object model's detections are filtered to.
const (
	ClassPerson     = 0
	ClassSportsBall = 32
)

// Score floors applied when a Config leaves them unset.
const (
	DefaultObjectConf = 0.05
	DefaultFaceConf   = 0.6
)

// Box is one detection in the submitted image's pixels, measured from the
// image's top-left corner. Mouth is the midpoint of the two mouth corners
// and is set only for a face.
type Box struct {
	X     float64     `json:"x"`
	Y     float64     `json:"y"`
	W     float64     `json:"w"`
	H     float64     `json:"h"`
	Score float64     `json:"score"`
	Mouth *[2]float64 `json:"mouth,omitempty"`
}

// Result holds what was asked for. A kind that was not asked for, or that
// found nothing, is an empty non-nil slice.
type Result struct {
	Persons []Box `json:"persons"`
	Balls   []Box `json:"balls"`
	Faces   []Box `json:"faces"`
}

// Want selects what to look for. At least one must be true.
type Want struct {
	Persons bool
	Balls   bool
	Faces   bool
}

// Config names the models. An empty ObjectModel opens no object detector;
// an empty FaceModels opens no face detector. Both empty is an error.
type Config struct {
	ObjectModel string  // path to a YOLO detect-task ONNX file, COCO classes
	FaceModels  string  // directory holding yolov8n-face.onnx and sface.onnx
	ONNXRuntime string  // shared library path; "" uses MediaMolder's discovery
	ObjectConf  float64 // score floor for people and balls; <= 0 means 0.05
	FaceConf    float64 // score floor for faces; <= 0 means 0.6
}

func (c Config) withDefaults() Config {
	if c.ObjectConf <= 0 {
		c.ObjectConf = DefaultObjectConf
	}
	if c.FaceConf <= 0 {
		c.FaceConf = DefaultFaceConf
	}
	return c
}

// Detector runs the opened models on one image at a time. Its methods are
// safe for concurrent use; Detect calls are serialized.
type Detector struct {
	mu        sync.Mutex
	objects   func(image.Image) ([]processors.Detection, error) // nil: not open
	faces     func(image.Image, float64) ([]face.Face, error)   // nil: not open
	faceConf  float64
	inputSize int
	closeFn   func() error
	closed    bool
}

// ObjectBox maps an object detection to a Box. It reports false for a
// class other than person or sports ball, and for a detection whose
// width, height, or confidence is not above zero.
func ObjectBox(d processors.Detection) (Box, bool) {
	if d.ClassID != ClassPerson && d.ClassID != ClassSportsBall {
		return Box{}, false
	}
	w := d.BBox[2] - d.BBox[0]
	h := d.BBox[3] - d.BBox[1]
	if !(w > 0) || !(h > 0) || !(d.Confidence > 0) {
		return Box{}, false
	}
	return Box{X: d.BBox[0], Y: d.BBox[1], W: w, H: h, Score: d.Confidence}, true
}

// FaceBox maps a face to a Box whose Mouth is the midpoint of the two
// mouth-corner landmarks. It reports false for a face whose width or
// height is not above zero.
func FaceBox(f face.Face) (Box, bool) {
	if f.BBox[2] <= 0 || f.BBox[3] <= 0 {
		return Box{}, false
	}
	l, r := f.Landmarks[3], f.Landmarks[4]
	return Box{
		X:     float64(f.BBox[0]),
		Y:     float64(f.BBox[1]),
		W:     float64(f.BBox[2]),
		H:     float64(f.BBox[3]),
		Score: float64(f.Score),
		Mouth: &[2]float64{
			(float64(l[0]) + float64(r[0])) / 2,
			(float64(l[1]) + float64(r[1])) / 2,
		},
	}, true
}

// Detect looks for the wanted kinds in img. Asking for a kind whose model
// is not open is an error, and so is a failure in either model; no partial
// result is returned. The same image and model files give the same result
// on every call.
func (d *Detector) Detect(img image.Image, want Want) (Result, error) {
	if img == nil {
		return Result{}, errors.New("detect: nil image")
	}
	b := img.Bounds()
	if b.Dx() <= 0 || b.Dy() <= 0 {
		return Result{}, errors.New("detect: image has no pixels")
	}
	wantObjects := want.Persons || want.Balls
	if !wantObjects && !want.Faces {
		return Result{}, errors.New("detect: nothing wanted")
	}

	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return Result{}, errors.New("detect: detector is closed")
	}
	if wantObjects && d.objects == nil {
		return Result{}, errors.New("detect: persons and balls need the object model, which is not open")
	}
	if want.Faces && d.faces == nil {
		return Result{}, errors.New("detect: faces need the face models, which are not open")
	}

	if b.Min != (image.Point{}) {
		dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
		draw.Draw(dst, dst.Bounds(), img, b.Min, draw.Src)
		img = dst
	}

	res := Result{Persons: []Box{}, Balls: []Box{}, Faces: []Box{}}
	if wantObjects {
		dets, err := d.objects(img)
		if err != nil {
			return Result{}, err
		}
		for _, det := range dets {
			box, ok := ObjectBox(det)
			if !ok {
				continue
			}
			switch {
			case det.ClassID == ClassPerson && want.Persons:
				res.Persons = append(res.Persons, box)
			case det.ClassID == ClassSportsBall && want.Balls:
				res.Balls = append(res.Balls, box)
			}
		}
	}
	if want.Faces {
		conf := d.faceConf
		if conf <= 0 {
			conf = DefaultFaceConf
		}
		faces, err := d.faces(img, conf)
		if err != nil {
			return Result{}, err
		}
		for _, f := range faces {
			if box, ok := FaceBox(f); ok {
				res.Faces = append(res.Faces, box)
			}
		}
	}
	return res, nil
}

// ObjectInputSize is the object model's square input side, or 0 when no
// object model is open.
func (d *Detector) ObjectInputSize() int {
	return d.inputSize
}

// HasObjects reports whether an object model is open.
func (d *Detector) HasObjects() bool {
	return d.objects != nil
}

// HasFaces reports whether the face models are open.
func (d *Detector) HasFaces() bool {
	return d.faces != nil
}

// Close releases the object model and marks the detector closed. It is
// safe to call twice. The face pipeline is shared by the whole process
// and stays loaded.
func (d *Detector) Close() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.closed {
		return nil
	}
	d.closed = true
	if d.closeFn != nil {
		return d.closeFn()
	}
	return nil
}
