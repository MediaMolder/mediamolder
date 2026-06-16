// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

/*
#include <libavcodec/avcodec.h>
#include <libavutil/opt.h>
*/
import "C"

import (
	"fmt"
	"unsafe"
)

// PresetCapability describes how (if at all) an encoder supports changing
// its preset on a live, opened codec context.
type PresetCapability int

const (
	// PresetCapNone — preset cannot be changed without rebuilding the
	// entire encoder. Applies to hardware encoders and codecs without a
	// preset concept.
	PresetCapNone PresetCapability = iota

	// PresetCapRestartIDR — preset can only change by closing and
	// reopening the codec; the next frame must be an IDR. Applies to
	// libx264 and libx265.
	PresetCapRestartIDR

	// PresetCapHotReconfig — preset can be changed mid-stream by writing
	// to the encoder's private AVOption (no close/reopen needed).
	// Applies to libsvtav1 (the underlying SVT-AV1 encoder accepts an
	// updated `preset` / `enc_mode` value at frame boundaries).
	PresetCapHotReconfig
)

// CodecName returns the encoder codec name (e.g. "libx264") that the
// EncoderContext was opened with.
func (e *EncoderContext) CodecName() string {
	if e == nil {
		return ""
	}
	return e.codecName
}

// PresetCapability reports how this encoder supports adaptive preset
// changes. Used by the pipeline to decide between close+reopen at the
// next IDR (x264/x265) and a hot-reconfig path (SVT-AV1).
func (e *EncoderContext) PresetCapability() PresetCapability {
	if e == nil {
		return PresetCapNone
	}
	switch e.codecName {
	case "libx264", "libx265":
		return PresetCapRestartIDR
	case "libsvtav1":
		return PresetCapHotReconfig
	}
	return PresetCapNone
}

// RequestPresetChange attempts to change the encoder preset in-place.
// This is only valid when PresetCapability() == PresetCapHotReconfig
// (currently SVT-AV1 only); for restart-required codecs the caller must
// close+reopen the EncoderContext with the new preset in
// EncoderOptions.ExtraOpts.
//
// The change is forwarded to the codec's private context via
// av_opt_set("preset", name, AV_OPT_SEARCH_CHILDREN). Returns an error
// when the codec does not support hot reconfig or when av_opt_set
// rejects the value.
func (e *EncoderContext) RequestPresetChange(name string) error {
	if e == nil || e.p == nil {
		return fmt.Errorf("RequestPresetChange: nil encoder")
	}
	if e.PresetCapability() != PresetCapHotReconfig {
		return fmt.Errorf("RequestPresetChange: codec %q requires close+reopen", e.codecName)
	}
	ck := C.CString("preset")
	defer C.free(unsafe.Pointer(ck))
	cv := C.CString(name)
	defer C.free(unsafe.Pointer(cv))
	// AV_OPT_SEARCH_CHILDREN = 1: descend into priv_data where the
	// SVT-AV1 wrapper exposes its preset option (libavcodec/libsvtav1.c).
	if ret := C.av_opt_set(unsafe.Pointer(e.p), ck, cv, 1); ret < 0 {
		return fmt.Errorf("av_opt_set(preset=%s): %w", name, newErr(ret))
	}
	return nil
}
