// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
)

// Encoder option enumeration is expensive (cgo + alloc + walk both option
// classes). Cache results in-memory for the process lifetime — the answer
// only depends on the linked libavcodec build.
var (
	encoderOptionsCache   = map[string]av.EncoderInfo{}
	encoderOptionsCacheMu sync.RWMutex
)

// handleEncoderOptions serves GET /api/encoders/{name}/options.
//
// Response body is the av.EncoderInfo JSON: encoder name + long_name +
// media_type + every AVOption (generic AVCodecContext + codec private),
// each with its type, range, default, and named constants. The frontend
// uses this to render typed, validated controls in the Inspector.
func handleEncoderOptions(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("encoder name is required"))
		return
	}

	encoderOptionsCacheMu.RLock()
	cached, ok := encoderOptionsCache[name]
	encoderOptionsCacheMu.RUnlock()
	if ok {
		writeEncoderOptions(w, cached)
		return
	}

	info, err := av.EncoderOptionsByName(name)
	if err != nil {
		writeJSONError(w, http.StatusNotFound, err)
		return
	}

	encoderOptionsCacheMu.Lock()
	encoderOptionsCache[name] = info
	encoderOptionsCacheMu.Unlock()

	writeEncoderOptions(w, info)
}

func writeEncoderOptions(w http.ResponseWriter, info av.EncoderInfo) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(info)
}
