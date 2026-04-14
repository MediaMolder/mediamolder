// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/error.h"
// #include <errno.h>
// #include <string.h>
//
// // av_strerror writes into a caller-supplied buffer; wrap it so Go can call it.
// static void get_error_string(int errnum, char *buf, size_t size) {
//     av_strerror(errnum, buf, size);
// }
//
// static int averror_eagain(void) { return AVERROR(EAGAIN); }
// static int averror_eof(void)    { return AVERROR_EOF; }
import "C"

import (
	"fmt"
	"unsafe"
)

// Err wraps an AVERROR code with a human-readable message.
type Err struct {
	Code    int
	Message string
}

func (e *Err) Error() string {
	return fmt.Sprintf("averror(%d): %s", e.Code, e.Message)
}

// newErr converts a negative AVERROR int from C into an *Err.
// Returns nil if code >= 0 (success).
func newErr(code C.int) error {
	if code >= 0 {
		return nil
	}
	const bufSize = 256
	buf := make([]C.char, bufSize)
	C.get_error_string(code, &buf[0], C.size_t(bufSize))
	return &Err{
		Code:    int(code),
		Message: C.GoString((*C.char)(unsafe.Pointer(&buf[0]))),
	}
}

// ErrEOF is returned when an operation reaches end-of-stream.
var ErrEOF = &Err{Code: int(C.averror_eof()), Message: "end of file"}

// IsEOF reports whether err represents an end-of-stream condition.
func IsEOF(err error) bool {
	if e, ok := err.(*Err); ok {
		return e.Code == int(C.averror_eof())
	}
	return false
}

// IsEAgain reports whether err is AVERROR(EAGAIN) -- try again / output not ready.
func IsEAgain(err error) bool {
	if e, ok := err.(*Err); ok {
		return e.Code == int(C.averror_eagain())
	}
	return false
}
