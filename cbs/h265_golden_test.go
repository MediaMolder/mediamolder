// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import "testing"

func TestGoldenH265RawStream(t *testing.T) {
	goldenRawStream(t, CodecH265, "testdata/tiny.hevc", "testdata/golden/tiny.hevc.txt")
}
