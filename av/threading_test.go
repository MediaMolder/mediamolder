// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

func TestParseThreadType(t *testing.T) {
	tests := []struct {
		input string
		want  int
	}{
		{"", 0},
		{"frame", 1},
		{"slice", 2},
		{"frame+slice", 3},
		{"unknown", 0},
	}
	for _, tt := range tests {
		got := parseThreadType(tt.input)
		if got != tt.want {
			t.Errorf("parseThreadType(%q) = %d, want %d", tt.input, got, tt.want)
		}
	}
}
