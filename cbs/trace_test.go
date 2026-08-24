// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import "testing"

func TestDisplayNameSubstitution(t *testing.T) {
	cases := []struct {
		name string
		subs []int
		want string
	}{
		{"profile_idc", nil, "profile_idc"},
		{"offset_for_ref_frame[i]", []int{3}, "offset_for_ref_frame[3]"},
		{"initial_cpb_removal_delay[SchedSelIdx]", []int{7}, "initial_cpb_removal_delay[7]"},
		{"comp_model_value[c][i][j]", []int{1, 2, 3}, "comp_model_value[1][2][3]"},
		// More bracket groups than subscripts: the rest stay literal.
		{"a[i][j]", []int{5}, "a[5][j]"},
		{"payload_byte[i]", []int{0}, "payload_byte[0]"},
	}
	for _, c := range cases {
		e := Element{Name: c.name, Subscripts: c.subs}
		if got := e.DisplayName(); got != c.want {
			t.Errorf("DisplayName(%q, %v) = %q, want %q", c.name, c.subs, got, c.want)
		}
	}
}

func TestFFmpegLinePadding(t *testing.T) {
	// Short name: '=' column at 61-name_len.
	e := Element{Position: 0, Length: 1, Name: "forbidden_zero_bit", Bits: "0", Value: 0}
	if got, want := e.FFmpegLine(),
		"0           forbidden_zero_bit                                          0 = 0"; got != want {
		t.Errorf("short:\ngot  %q\nwant %q", got, want)
	}

	// 32-bit value: still the 61-column rule (17+32 = 49 <= 60).
	e = Element{Position: 81, Length: 32, Name: "num_units_in_tick",
		Bits: "00000000000000000000000000000001", Value: 1}
	if got, want := e.FFmpegLine(),
		"81          num_units_in_tick            00000000000000000000000000000001 = 1"; got != want {
		t.Errorf("32-bit:\ngot  %q\nwant %q", got, want)
	}

	// Overflow branch: name_len+bits_len > 60 → pad = bits_len + 2.
	long := "a_very_long_element_name_that_overflows_the_column_rule_x[0]"
	e = Element{Position: 5, Length: 8, Name: long, Bits: "10101010", Value: 170}
	want := "5           " + long + "  10101010 = 170"
	if got := e.FFmpegLine(); got != want {
		t.Errorf("overflow:\ngot  %q\nwant %q", got, want)
	}
}
