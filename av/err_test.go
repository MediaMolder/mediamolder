// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"errors"
	"fmt"
	"strings"
	"testing"
)

func TestErrError(t *testing.T) {
	e := &Err{Code: -1, Message: "test error"}
	if e.Error() == "" {
		t.Error("Err.Error() returned empty string")
	}
}

// The AC-3 decoder leaks libavcodec's private parser codes, which av_strerror
// renders as "Error number N occurred". They must come out named, and be
// classifiable, so a damaged audio frame is recognisable as such.
func TestAACAC3ParseErrorsAreNamed(t *testing.T) {
	err := newErrCode(-16976906) // AAC_AC3_PARSE_ERROR_SYNC as seen in the wild
	if err == nil {
		t.Fatal("negative code must be an error")
	}
	if !strings.Contains(err.Error(), "frame sync") || !strings.Contains(err.Error(), "AAC_AC3_PARSE_ERROR_SYNC") {
		t.Fatalf("message = %q", err)
	}
	if !IsCodecParseError(err) {
		t.Fatal("IsCodecParseError(sync) = false")
	}
	var e *Err
	if !errors.As(err, &e) || e.Code != ErrCodeAACAC3ParseSync {
		t.Fatalf("code = %v", err)
	}
	if !IsCodecParseError(fmt.Errorf("wrapped: %w", newErrCode(ErrCodeAACAC3ParseCRC))) {
		t.Fatal("a wrapped CRC parse error must still classify")
	}

	// Ordinary codes keep av_strerror's text and do not classify.
	inval := newErrCode(-22) // AVERROR(EINVAL)
	if IsCodecParseError(inval) {
		t.Fatalf("EINVAL classified as a parse error: %v", inval)
	}
	if strings.Contains(inval.Error(), "parser") {
		t.Fatalf("EINVAL renamed: %v", inval)
	}
	if IsCodecParseError(ErrEOF) || IsCodecParseError(errors.New("x")) {
		t.Fatal("EOF / non-av errors must not classify")
	}
	// An unknown reason byte under the parser tag is left alone.
	if _, ok := codecParseErrorName(-0x9030c0a); ok {
		t.Fatal("reason 9 is not a known parser error")
	}
	if newErrCode(0) != nil {
		t.Fatal("success must be nil")
	}
}

func TestIsEOF(t *testing.T) {
	if !IsEOF(ErrEOF) {
		t.Error("IsEOF(ErrEOF) = false; want true")
	}
	if IsEOF(errors.New("not eof")) {
		t.Error("IsEOF(other) = true; want false")
	}
}
