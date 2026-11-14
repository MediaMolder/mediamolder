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

// faceMu serializes openFaces, which changes process-wide face settings.
var faceMu sync.Mutex

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

// openFaces loads the process-wide face pipeline from cfg.FaceModels, or
// confirms that the one already loaded came from there. The face
// package's settings are changed only by a call that loads the pipeline;
// a failed call leaves them as it found them.
func openFaces(cfg Config) error {
	dir, err := filepath.Abs(cfg.FaceModels)
	if err != nil {
		return err
	}
	faceMu.Lock()
	defer faceMu.Unlock()
	if loaded := face.LoadedModelsDir(); loaded != "" {
		return sameFaceDir(loaded, dir)
	}
	if err := face.CheckModels(dir); err != nil {
		return err
	}
	prevDir, prevLib := face.ModelsDir(), face.ONNXLib()
	face.SetModelsDir(dir)
	if cfg.ONNXRuntime != "" {
		face.SetONNXLib(cfg.ONNXRuntime)
	}
	err = face.Available()
	if err == nil {
		// Another caller of the face package may have loaded the
		// pipeline from its own directory in the meantime.
		err = sameFaceDir(face.LoadedModelsDir(), dir)
	}
	if err != nil {
		face.SetModelsDir(prevDir)
		face.SetONNXLib(prevLib)
		return err
	}
	return nil
}

func sameFaceDir(loaded, dir string) error {
	if abs, err := filepath.Abs(loaded); err == nil {
		loaded = abs
	}
	if loaded != dir {
		return fmt.Errorf("the face pipeline is already loaded from %s and cannot be reloaded from %s in this process", loaded, dir)
	}
	return nil
}
