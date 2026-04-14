// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"errors"
	"testing"
)

func TestErrError(t *testing.T) {
	e := &Err{Code: -1, Message: "test error"}
	if e.Error() == "" {
		t.Error("Err.Error() returned empty string")
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
