//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2025 Brandon Castellano <http://www.bcastell.com>.

package pyscenedetect

import (
	"math"
	"testing"
)

func TestNewFrameTimecode(t *testing.T) {
	ft, err := NewFrameTimecode(30, 30.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 30 {
		t.Errorf("FrameNum: got %d, want 30", ft.FrameNum())
	}
	if ft.FrameRate() != 30.0 {
		t.Errorf("FrameRate: got %g, want 30.0", ft.FrameRate())
	}
}

func TestNewFrameTimecode_NegativeClamped(t *testing.T) {
	ft, err := NewFrameTimecode(-5, 25.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 0 {
		t.Errorf("expected 0 for negative input, got %d", ft.FrameNum())
	}
}

func TestNewFrameTimecode_ZeroFPS(t *testing.T) {
	_, err := NewFrameTimecode(0, 0)
	if err == nil {
		t.Error("expected error for fps=0")
	}
}

func TestFrameTimecodeFromSeconds(t *testing.T) {
	// 1.5 s at 24 fps = frame 36 (round(1.5*24)=36)
	ft, err := FrameTimecodeFromSeconds(1.5, 24.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 36 {
		t.Errorf("got frame %d, want 36", ft.FrameNum())
	}
}

func TestFrameTimecodeFromSeconds_NegativeClamped(t *testing.T) {
	ft, err := FrameTimecodeFromSeconds(-1.0, 30.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 0 {
		t.Errorf("expected 0, got %d", ft.FrameNum())
	}
}

func TestParseFrameTimecode_Timecode(t *testing.T) {
	cases := []struct {
		in       string
		fps      float64
		wantSecs float64
	}{
		{"00:00:01.000", 25.0, 1.0},
		{"00:01:00.000", 25.0, 60.0},
		{"01:00:00.000", 25.0, 3600.0},
		{"00:00:00.500", 25.0, 0.5},
		{"01:02", 30.0, 62.0},
		{"01:02.500", 30.0, 62.5},
	}
	for _, tc := range cases {
		ft, err := ParseFrameTimecode(tc.in, tc.fps)
		if err != nil {
			t.Errorf("ParseFrameTimecode(%q, %g): %v", tc.in, tc.fps, err)
			continue
		}
		gotSecs := ft.Seconds()
		if math.Abs(gotSecs-tc.wantSecs) > 1.0/tc.fps {
			t.Errorf("ParseFrameTimecode(%q, %g): got %.4fs, want %.4fs",
				tc.in, tc.fps, gotSecs, tc.wantSecs)
		}
	}
}

func TestParseFrameTimecode_FrameNumber(t *testing.T) {
	ft, err := ParseFrameTimecode("100", 25.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 100 {
		t.Errorf("got frame %d, want 100", ft.FrameNum())
	}
}

func TestParseFrameTimecode_Seconds(t *testing.T) {
	// Decimal without suffix.
	ft, err := ParseFrameTimecode("2.5", 25.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft.FrameNum() != 63 { // round(2.5*25) = 63
		t.Errorf("ParseFrameTimecode(\"2.5\", 25): got frame %d, want 63", ft.FrameNum())
	}

	// With 's' suffix.
	ft2, err := ParseFrameTimecode("2.5s", 25.0)
	if err != nil {
		t.Fatal(err)
	}
	if ft2.FrameNum() != ft.FrameNum() {
		t.Errorf("'2.5s' and '2.5' should produce same frame, got %d vs %d",
			ft2.FrameNum(), ft.FrameNum())
	}
}

func TestParseFrameTimecode_Invalid(t *testing.T) {
	bad := []string{"-1", "-1.0s", "abc", "01:02:03:04"}
	for _, s := range bad {
		_, err := ParseFrameTimecode(s, 25.0)
		if err == nil {
			t.Errorf("expected error for %q, got nil", s)
		}
	}
}

func TestFrameTimecode_Timecode(t *testing.T) {
	cases := []struct {
		frameNum int64
		fps      float64
		want     string
	}{
		{0, 25.0, "00:00:00.000"},
		{25, 25.0, "00:00:01.000"},
		{1500, 25.0, "00:01:00.000"},
		{90000, 25.0, "01:00:00.000"},
	}
	for _, tc := range cases {
		ft, _ := NewFrameTimecode(tc.frameNum, tc.fps)
		got := ft.Timecode()
		if got != tc.want {
			t.Errorf("frame %d @%gfps: got %q, want %q", tc.frameNum, tc.fps, got, tc.want)
		}
	}
}

func TestFrameTimecode_Seconds(t *testing.T) {
	ft, _ := NewFrameTimecode(30, 30.0)
	got := ft.Seconds()
	if math.Abs(got-1.0) > 1e-9 {
		t.Errorf("Seconds(): got %g, want 1.0", got)
	}
}

func TestFrameTimecode_AddFrames(t *testing.T) {
	ft, _ := NewFrameTimecode(10, 25.0)
	got := ft.AddFrames(5)
	if got.FrameNum() != 15 {
		t.Errorf("AddFrames(5): got %d, want 15", got.FrameNum())
	}
}

func TestFrameTimecode_AddFrames_Clamp(t *testing.T) {
	ft, _ := NewFrameTimecode(3, 25.0)
	got := ft.AddFrames(-10)
	if got.FrameNum() != 0 {
		t.Errorf("AddFrames(-10) on frame 3: got %d, want 0", got.FrameNum())
	}
}

func TestFrameTimecode_SubTimecode(t *testing.T) {
	a, _ := NewFrameTimecode(20, 25.0)
	b, _ := NewFrameTimecode(5, 25.0)
	diff := a.SubTimecode(b)
	if diff.FrameNum() != 15 {
		t.Errorf("SubTimecode: got %d, want 15", diff.FrameNum())
	}
}

func TestFrameTimecode_SubTimecode_Clamp(t *testing.T) {
	a, _ := NewFrameTimecode(5, 25.0)
	b, _ := NewFrameTimecode(20, 25.0)
	diff := a.SubTimecode(b)
	if diff.FrameNum() != 0 {
		t.Errorf("SubTimecode clamped: got %d, want 0", diff.FrameNum())
	}
}

func TestFrameTimecode_ElapsedFrames(t *testing.T) {
	a, _ := NewFrameTimecode(25, 25.0)
	b, _ := NewFrameTimecode(10, 25.0)
	if a.ElapsedFrames(b) != 15 {
		t.Errorf("ElapsedFrames: got %d, want 15", a.ElapsedFrames(b))
	}
	if b.ElapsedFrames(a) != 0 {
		t.Errorf("ElapsedFrames clamped: got %d, want 0", b.ElapsedFrames(a))
	}
}

func TestFrameTimecode_Comparisons(t *testing.T) {
	a, _ := NewFrameTimecode(10, 25.0)
	b, _ := NewFrameTimecode(20, 25.0)
	c, _ := NewFrameTimecode(10, 25.0)

	if !a.Less(b) {
		t.Error("Less: 10 < 20 failed")
	}
	if a.Less(c) {
		t.Error("Less: 10 < 10 should be false")
	}
	if !a.LessEq(c) {
		t.Error("LessEq: 10 <= 10 failed")
	}
	if !b.Greater(a) {
		t.Error("Greater: 20 > 10 failed")
	}
	if !a.Equal(c) {
		t.Error("Equal: 10 == 10 failed")
	}
	if a.Equal(b) {
		t.Error("Equal: 10 == 20 should be false")
	}
}

func TestFrameTimecode_String(t *testing.T) {
	ft, _ := NewFrameTimecode(0, 30.0)
	if ft.String() != ft.Timecode() {
		t.Error("String() should equal Timecode()")
	}
}

func TestComputeDownscaleFactor(t *testing.T) {
	if ComputeDownscaleFactor(0) != 1 {
		t.Error("width=0 should return 1")
	}
	if ComputeDownscaleFactor(128) != 1 {
		t.Error("width < DefaultMinWidth should return 1")
	}
	got := ComputeDownscaleFactor(1920)
	want := float64(1920) / float64(DefaultMinWidth)
	if math.Abs(got-want) > 1e-9 {
		t.Errorf("ComputeDownscaleFactor(1920): got %g, want %g", got, want)
	}
}
