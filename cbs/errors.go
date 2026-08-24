// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import (
	"errors"
	"fmt"
)

// ErrInvalidData mirrors AVERROR_INVALIDDATA: the bitstream violates the
// specification (out-of-range value, truncation, bad start code, ...).
var ErrInvalidData = errors.New("invalid data found when processing input")

// ErrUnsupported mirrors AVERROR(ENOSYS): the unit type is valid but its
// decomposition is not implemented (matches FFmpeg's CBS coverage).
var ErrUnsupported = errors.New("decomposition unimplemented")

// ErrPatchWelcome mirrors AVERROR_PATCHWELCOME: a valid feature FFmpeg's
// CBS (and hence this port) does not implement (SVC/MVC/3DAVC).
var ErrPatchWelcome = errors.New("not yet implemented in FFmpeg, patches welcome")

// ErrSkipped mirrors AVERROR(EAGAIN): the unit was deliberately not
// decomposed (e.g. an AV1 OBU dropped by operating-point selection).
var ErrSkipped = errors.New("unit skipped")

// InternalError reports a bug in the parser itself: a runtime panic (index
// out of range, ...) recovered at the unit boundary. It is never expected
// for any input; fuzz targets assert it does not occur.
type InternalError struct {
	Panic any
	Stack []byte
}

func (e *InternalError) Error() string {
	return fmt.Sprintf("cbs: internal parser error: %v", e.Panic)
}
