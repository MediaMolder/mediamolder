//go:build with_onnx

// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package detect

import (
	"errors"
	"fmt"
	"image"
	"path/filepath"
	"sync"

	"github.com/MediaMolder/MediaMolder/face"
	"github.com/MediaMolder/MediaMolder/processors"
)

var (
	faceMu  sync.Mutex
	faceDir string // directory of the first face pipeline this package loaded
)

// Available returns nil: this build can run the models. Whether a given
// model file loads is reported by Open.
func Available() error { return nil }

// Open loads the models cfg names. An object-model failure returns an
// error that starts with "object model:", and a face-model failure one
// that starts with "face models:".
//
// The face pipeline is one per process and keeps the first directory that
// loaded. Open with a different FaceModels directory than an earlier
// successful Open in the same process returns an error. Both face files
// must be present and match their pinned SHA-256, although embeddings are
// never computed here.
func Open(cfg Config) (*Detector, error) {
	if cfg.ObjectModel == "" && cfg.FaceModels == "" {
		return nil, errors.New("detect: neither an object model nor face models are configured")
	}
	cfg = cfg.withDefaults()
	d := &Detector{faceConf: cfg.FaceConf}

	if cfg.ObjectModel != "" {
		yolo, err := openObjects(cfg)
		if err != nil {
			return nil, fmt.Errorf("object model: %w", err)
		}
		d.objects = yolo.DetectImage
		d.inputSize = yolo.InputSize()
		d.closeFn = yolo.Close
	}

	if cfg.FaceModels != "" {
		if err := openFaces(cfg); err != nil {
			if d.closeFn != nil {
				d.closeFn()
			}
			return nil, fmt.Errorf("face models: %w", err)
		}
		d.faces = func(img image.Image, conf float64) ([]face.Face, error) {
			return face.AnalyzeImageOpts(img, face.Options{ConfThresh: conf, Embed: false})
		}
	}
	return d, nil
}

func openObjects(cfg Config) (*processors.YOLODetector, error) {
	p, err := processors.Get("yolo")
	if err != nil {
		return nil, err
	}
	yolo, ok := p.(*processors.YOLODetector)
	if !ok {
		return nil, fmt.Errorf("yolo processor is %T, not *processors.YOLODetector", p)
	}
	params := map[string]any{"model": cfg.ObjectModel, "conf": cfg.ObjectConf}
	if cfg.ONNXRuntime != "" {
		params["ort_lib"] = cfg.ONNXRuntime
	}
	if err := yolo.Init(params); err != nil {
		return nil, err
	}
	return yolo, nil
}

func openFaces(cfg Config) error {
	dir, err := filepath.Abs(cfg.FaceModels)
	if err != nil {
		return err
	}
	faceMu.Lock()
	defer faceMu.Unlock()
	if faceDir != "" {
		if dir != faceDir {
			return fmt.Errorf("the face pipeline is already loaded from %s and cannot be reloaded from %s in this process", faceDir, dir)
		}
		return nil
	}
	// The pipeline may already be loaded by another caller, in which case
	// Available cannot report on dir. CheckModels reads dir itself.
	if err := face.CheckModels(dir); err != nil {
		return err
	}
	face.SetModelsDir(dir)
	if cfg.ONNXRuntime != "" {
		face.SetONNXLib(cfg.ONNXRuntime)
	}
	if err := face.Available(); err != nil {
		return err
	}
	faceDir = dir
	return nil
}
