// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import "testing"

func TestLadderFor_KnownCodecs(t *testing.T) {
	for _, c := range []string{"libx264", "libx265", "libsvtav1"} {
		l, ok := LadderFor(c)
		if !ok {
			t.Errorf("LadderFor(%q) returned ok=false", c)
			continue
		}
		if len(l.Names) == 0 {
			t.Errorf("LadderFor(%q) returned empty ladder", c)
		}
		if l.Codec != c {
			t.Errorf("LadderFor(%q).Codec = %q", c, l.Codec)
		}
	}
}

func TestLadderFor_UnknownCodec(t *testing.T) {
	if _, ok := LadderFor("aac"); ok {
		t.Errorf("LadderFor(aac) should not be supported")
	}
	if _, ok := LadderFor("h264_nvenc"); ok {
		t.Errorf("LadderFor(h264_nvenc) should not be supported (HW encoder)")
	}
}

func TestLadder_Step_x264(t *testing.T) {
	l, _ := LadderFor("libx264")
	cases := []struct {
		current     string
		n           int
		wantNext    string
		wantClamped bool
	}{
		{"slower", 1, "slow", false},
		{"slow", 1, "medium", false},
		{"medium", 1, "fast", false},
		{"fast", -1, "medium", false},
		{"ultrafast", 1, "ultrafast", true}, // clamped at fastest
		{"placebo", -1, "placebo", true},    // clamped at slowest
		{"unknown", 0, "medium", true},      // unknown → default
		{"SLOWER", 1, "slow", false},        // case-insensitive
		{"slower", 100, "ultrafast", true},  // clamped
	}
	for _, c := range cases {
		got, clamped := l.Step(c.current, c.n)
		if got != c.wantNext || clamped != c.wantClamped {
			t.Errorf("Step(%q,%d) = (%q,%v); want (%q,%v)",
				c.current, c.n, got, clamped, c.wantNext, c.wantClamped)
		}
	}
}

func TestLadder_Step_svtav1(t *testing.T) {
	l, _ := LadderFor("libsvtav1")
	got, clamped := l.Step("4", 1)
	if got != "5" || clamped {
		t.Errorf("svt-av1 Step(4,+1) = (%q,%v); want (5,false)", got, clamped)
	}
	got, clamped = l.Step("13", 1)
	if got != "13" || !clamped {
		t.Errorf("svt-av1 Step(13,+1) = (%q,%v); want (13,true)", got, clamped)
	}
	got, clamped = l.Step("0", -1)
	if got != "0" || !clamped {
		t.Errorf("svt-av1 Step(0,-1) = (%q,%v); want (0,true)", got, clamped)
	}
}

func TestLadder_IsFasterThan(t *testing.T) {
	l, _ := LadderFor("libx264")
	if !l.IsFasterThan("fast", "slower") {
		t.Errorf("fast should be faster than slower")
	}
	if l.IsFasterThan("slower", "fast") {
		t.Errorf("slower should not be faster than fast")
	}
	if l.IsFasterThan("unknown", "fast") {
		t.Errorf("unknown should not be faster than anything")
	}
}

func TestLadder_IndexOf(t *testing.T) {
	l, _ := LadderFor("libx264")
	if l.IndexOf("medium") != 4 {
		t.Errorf("IndexOf(medium) = %d; want 4", l.IndexOf("medium"))
	}
	if l.IndexOf("nonexistent") != -1 {
		t.Errorf("IndexOf(nonexistent) should be -1")
	}
}
