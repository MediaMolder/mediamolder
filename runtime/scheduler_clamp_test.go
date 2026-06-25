// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package runtime

import "testing"

// An untrusted per-edge buffer hint must be clamped so it cannot request an
// arbitrarily large channel allocation (memory-exhaustion DoS).
func TestClampEdgeBuffer(t *testing.T) {
	cases := []struct{ in, want int }{
		{0, 0},
		{8, 8},
		{maxEdgeBuffer, maxEdgeBuffer},
		{maxEdgeBuffer + 1, maxEdgeBuffer},
		{1 << 30, maxEdgeBuffer},
	}
	for _, c := range cases {
		if got := clampEdgeBuffer(c.in); got != c.want {
			t.Errorf("clampEdgeBuffer(%d) = %d, want %d", c.in, got, c.want)
		}
	}
}
