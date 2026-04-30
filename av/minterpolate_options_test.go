// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// TestMinterpolateExposesEnumOptions locks in Wave 7 #43: the AVOption
// miner must expose minterpolate's mi_mode / mc_mode / me_mode / me /
// vsbmc as typed `int` options carrying their named constants, so the
// GUI Inspector can render them as enum dropdowns instead of free-text.
func TestMinterpolateExposesEnumOptions(t *testing.T) {
	if !FindFilter("minterpolate") {
		t.Skip("minterpolate filter not built into this libavfilter")
	}
	info, err := FilterOptionsByName("minterpolate")
	if err != nil {
		t.Fatalf("FilterOptionsByName: %v", err)
	}
	want := map[string]int{
		"mi_mode": 3, // dup, blend, mci
		"mc_mode": 2, // obmc, aobmc
		"me_mode": 2, // bidir, bilat
		"me":      9, // esa..umh
	}
	got := map[string]int{}
	saw := map[string]bool{}
	for _, o := range info.Options {
		if _, expected := want[o.Name]; expected {
			got[o.Name] = len(o.Constants)
			if o.Type != "int" {
				t.Errorf("%s: type=%q want int", o.Name, o.Type)
			}
		}
		if o.Name == "vsbmc" {
			saw["vsbmc"] = true
			if o.Type != "int" {
				t.Errorf("vsbmc: type=%q want int", o.Type)
			}
		}
	}
	for name, n := range want {
		if got[name] != n {
			t.Errorf("%s: %d constants, want %d", name, got[name], n)
		}
	}
	if !saw["vsbmc"] {
		t.Errorf("vsbmc option not surfaced")
	}
}
