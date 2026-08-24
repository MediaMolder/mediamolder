// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import "testing"

func TestGoldenAV1RawStream(t *testing.T) {
	goldenRawStream(t, CodecAV1, "testdata/tiny.obu", "testdata/golden/tiny.obu.txt")
}
