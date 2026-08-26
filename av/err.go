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
// static int averror_exit(void)   { return AVERROR_EXIT; }
import "C"

import (
	"errors"
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

// Is reports whether target is an *Err with the same code, so errors.Is
// matches sentinel errors like ErrPoisonedPacket by code rather than only by
// pointer identity — a wrapped or re-constructed equal-coded error still
// compares equal.
func (e *Err) Is(target error) bool {
	t, ok := target.(*Err)
	return ok && t.Code == e.Code
}

// newErr converts a negative AVERROR int from C into an *Err.
// Returns nil if code >= 0 (success).
func newErr(code C.int) error {
	return newErrCode(int(code))
}

// newErrCode is newErr for a Go int (tests cannot name C types).
func newErrCode(code int) error {
	if code >= 0 {
		return nil
	}
	const bufSize = 256
	buf := make([]C.char, bufSize)
	C.get_error_string(C.int(code), &buf[0], C.size_t(bufSize))
	msg := C.GoString((*C.char)(unsafe.Pointer(&buf[0])))
	if name, ok := codecParseErrorName(code); ok {
		msg = name
	}
	return &Err{Code: code, Message: msg}
}

// libavcodec's AAC/AC-3 frame-header parser reports failures with private
// codes (libavcodec/aac_ac3_parser.h) that are neither AVERROR(errno) nor
// FFERRTAG values, and the AC-3 decoder returns them raw from
// avcodec_send_packet / avcodec_receive_frame. av_strerror knows nothing about
// them, so without this table a damaged AC-3 frame surfaces as "Error number
// -16976906 occurred". The values have been stable since 2008: the parser's
// own tag (0x030c0a) under a one-byte reason.
const (
	aacAC3ParseErrorTag = 0x030c0a

	ErrCodeAACAC3ParseSync       = -0x1030c0a // AAC_AC3_PARSE_ERROR_SYNC
	ErrCodeAACAC3ParseBSID       = -0x2030c0a // AAC_AC3_PARSE_ERROR_BSID
	ErrCodeAACAC3ParseSampleRate = -0x3030c0a // AAC_AC3_PARSE_ERROR_SAMPLE_RATE
	ErrCodeAACAC3ParseFrameSize  = -0x4030c0a // AAC_AC3_PARSE_ERROR_FRAME_SIZE
	ErrCodeAACAC3ParseFrameType  = -0x5030c0a // AAC_AC3_PARSE_ERROR_FRAME_TYPE
	ErrCodeAACAC3ParseCRC        = -0x6030c0a // AAC_AC3_PARSE_ERROR_CRC
	ErrCodeAACAC3ParseChannelCfg = -0x7030c0a // AAC_AC3_PARSE_ERROR_CHANNEL_CFG
)

var aacAC3ParseReasons = [...]struct{ desc, name string }{
	1: {"frame sync", "SYNC"},
	2: {"bitstream id", "BSID"},
	3: {"sample rate", "SAMPLE_RATE"},
	4: {"frame size", "FRAME_SIZE"},
	5: {"frame type", "FRAME_TYPE"},
	6: {"CRC", "CRC"},
	7: {"channel configuration", "CHANNEL_CFG"},
}

// codecParseErrorName names a libavcodec-private AAC/AC-3 parser code, or
// reports false for anything else.
func codecParseErrorName(code int) (string, bool) {
	if code >= 0 {
		return "", false
	}
	n := -code
	if n&0xffffff != aacAC3ParseErrorTag {
		return "", false
	}
	reason := n >> 24
	if reason <= 0 || reason >= len(aacAC3ParseReasons) {
		return "", false
	}
	r := aacAC3ParseReasons[reason]
	return fmt.Sprintf("AC-3/AAC parser: %s error (AAC_AC3_PARSE_ERROR_%s)", r.desc, r.name), true
}

// IsCodecParseError reports whether err is one of libavcodec's private
// AAC/AC-3 frame-header parser failures — a damaged audio frame, which a
// tolerant decode loop skips rather than fails on.
func IsCodecParseError(err error) bool {
	var e *Err
	if !errors.As(err, &e) {
		return false
	}
	_, ok := codecParseErrorName(e.Code)
	return ok
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

// IsInterrupted reports whether err is AVERROR_EXIT — a blocking libav call
// aborted by the interrupt callback, e.g. a resilient input's watchdog
// deadline expiring (OpenInputResilient / SetReadDeadline).
func IsInterrupted(err error) bool {
	var e *Err
	if errors.As(err, &e) {
		return e.Code == int(C.averror_exit())
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
