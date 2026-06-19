// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package av

// This file holds the //export'd C callback trampolines for whisper.go. cgo
// requires that a file using //export contain ONLY declarations in its preamble
// (no function definitions) — hence the split from whisper.go, whose preamble
// defines the C bridge functions that call these.

import "C"

import (
	"runtime/cgo"
	"unsafe"
)

//export mmWhisperProgressGo
func mmWhisperProgressGo(progress C.int, ud unsafe.Pointer) {
	cb, ok := cgo.Handle(uintptr(ud)).Value().(*whisperCallbacks)
	if ok && cb.progress != nil {
		cb.progress(int(progress))
	}
}

//export mmWhisperAbortGo
func mmWhisperAbortGo(ud unsafe.Pointer) C.int {
	cb, ok := cgo.Handle(uintptr(ud)).Value().(*whisperCallbacks)
	if ok && cb.ctx != nil && cb.ctx.Err() != nil {
		return 1
	}
	return 0
}
