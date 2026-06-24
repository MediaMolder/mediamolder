// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_onnx

package onnxrt

import (
	"sync"

	ort "github.com/yalue/onnxruntime_go"
)

var (
	mu   sync.Mutex
	done bool
)

// Init initialises the process-global ONNX Runtime environment, locating the shared library via
// [LibPath] (override → env → auto-discovery). It is idempotent and safe to call from multiple
// packages (face, processors): the underlying ort.InitializeEnvironment runs at most once per
// process, avoiding the "already initialised" error when, e.g., a graph uses both face_detect
// and yolo_v8. On failure it does NOT latch, so a corrected library path (override/env) is
// retried on the next call rather than failing for the process lifetime.
//
// override is the highest-priority library location (e.g. a node's "ort_lib" param); pass "" to
// use the env var / auto-discovery.
func Init(override string) error {
	mu.Lock()
	defer mu.Unlock()
	if done {
		return nil
	}
	if lib := LibPath(override); lib != "" {
		ort.SetSharedLibraryPath(lib)
	}
	if err := ort.InitializeEnvironment(); err != nil {
		return err
	}
	done = true
	return nil
}
