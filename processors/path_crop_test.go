// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"bytes"
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

var videoCtx = ProcessorContext{MediaType: av.MediaTypeVideo}

// columnFrame is a yuv420p frame whose luma column x holds x (mod 256) and
// whose chroma column c holds c.
func columnFrame(t *testing.T, w, h int, pts int64) *av.Frame {
	t.Helper()
	f, err := av.NewVideoFrame(w, h, av.PixFmtByName("yuv420p"))
	if err != nil {
		t.Fatalf("NewVideoFrame: %v", err)
	}
	for p := 0; p < f.NumPlanes(); p++ {
		data, ls := f.Plane(p), f.Linesize(p)
		for row := 0; row < f.PlaneHeight(p); row++ {
			for c := 0; c < f.PlaneWidth(p); c++ {
				data[row*ls+c] = byte(c)
			}
		}
	}
	f.SetPTS(pts)
	return f
}

func newPathCrop(t *testing.T, jsonParams string) *PathCrop {
	t.Helper()
	var params map[string]any
	if err := json.Unmarshal([]byte(jsonParams), &params); err != nil {
		t.Fatalf("params: %v", err)
	}
	p := &PathCrop{}
	if err := p.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	return p
}

func TestPathCrop_Registered(t *testing.T) {
	p, err := Get("path_crop")
	if err != nil {
		t.Fatalf("Get(path_crop): %v", err)
	}
	if _, ok := p.(*PathCrop); !ok {
		t.Errorf("Get(path_crop) = %T, want *PathCrop", p)
	}
}

// TestPathCrop_X checks the integer left-edge rule (nearest, ties lower,
// clamped, aligned to the chroma step) for yuv420p, and that 4:4:4 skips the
// even step while 4:1:1 aligns to 4.
func TestPathCrop_X(t *testing.T) {
	for _, tc := range []struct {
		cx          float64
		shift, want int
	}{
		{160, 1, 92},   // edge 92
		{160.5, 1, 92}, // edge 92.5 ties to 92
		{160.6, 1, 92}, // edge 92.6 rounds to 93, aligns to 92
		{161.6, 1, 94}, // edge 93.6 rounds to 94
		{0, 1, 0},      // clamped low
		{400, 1, 184},  // clamped high
		{1e300, 1, 184},
		{-1e300, 1, 0},
		{160.6, 0, 93},
		{163, 2, 92}, // edge 95 aligns down to 92
	} {
		if got := pathCropX(tc.cx, 136, 320, tc.shift); got != tc.want {
			t.Errorf("pathCropX(%v, shift %d) = %d, want %d", tc.cx, tc.shift, got, tc.want)
		}
	}
}

// TestPathCrop_LeftEdge is the lossless left-edge test: the first output
// luma column equals the integer x of the sample's cx, and the first chroma
// column equals x/2. A held cx keeps its x; a later cx may jump.
func TestPathCrop_LeftEdge(t *testing.T) {
	p := newPathCrop(t, `{"width":136,"frames":[
		{"pts":0,"cx":100},{"pts":10,"cx":100},{"pts":20,"cx":160.6},{"pts":30,"cx":250.2}]}`)
	for i, want := range []int{32, 32, 92, 182} {
		src := columnFrame(t, 320, 240, int64(i*10))
		src.AddSEIUnregisteredSideData(append(bytes.Repeat([]byte{1}, 16), 'x'))
		out, md, err := p.Process(src, videoCtx)
		if err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
		if md != nil || out == nil || out == src {
			t.Fatalf("sample %d: out=%v md=%v, want a new frame and no metadata", i, out, md)
		}
		if out.Width() != 136 || out.Height() != 240 || out.PTS() != int64(i*10) {
			t.Errorf("sample %d: out %dx%d pts %d", i, out.Width(), out.Height(), out.PTS())
		}
		for _, row := range []int{0, 239} {
			if got := int(out.Plane(0)[row*out.Linesize(0)]); got != want {
				t.Errorf("sample %d row %d: luma column 0 = %d, want %d", i, row, got, want)
			}
		}
		if got := int(out.Plane(1)[0]); got != want/2 {
			t.Errorf("sample %d: chroma column 0 = %d, want %d", i, got, want/2)
		}
		if len(out.SEIUnregisteredSideData()) != 1 {
			t.Errorf("sample %d: side data not copied", i)
		}
		src.Close()
		out.Close()
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestPathCrop_Matching(t *testing.T) {
	p := newPathCrop(t, `{"width":64,"frames":[{"pts":3000,"cx":160},{"pts":6003,"cx":160},{"pts":9006,"cx":160}]}`)
	for _, tc := range []struct {
		pts  int64
		keep bool
	}{
		{1400, false}, // 1600 before sample 0
		{3001, true},  // one tick late takes sample 0
		{3000, false}, // sample 0 already used
		{6002, true},  // one tick early takes sample 1
		{7500, false}, // 1506 from sample 2; sample 1 is used
		{9006, true},
		{12009, false}, // past the out point
	} {
		src := columnFrame(t, 320, 16, tc.pts)
		out, _, err := p.Process(src, videoCtx)
		if err != nil {
			t.Fatalf("pts %d: %v", tc.pts, err)
		}
		if (out != nil) != tc.keep {
			t.Errorf("pts %d: kept=%v, want %v", tc.pts, out != nil, tc.keep)
		}
		src.Close()
		if out != nil {
			out.Close()
		}
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close after every sample matched: %v", err)
	}
}

func TestPathCrop_TieGoesToEarlierSample(t *testing.T) {
	p := newPathCrop(t, `{"width":64,"frames":[{"pts":0,"cx":100},{"pts":10,"cx":200}]}`)
	src := columnFrame(t, 320, 16, 5)
	defer src.Close()
	out, _, err := p.Process(src, videoCtx)
	if err != nil || out == nil {
		t.Fatalf("Process: out=%v err=%v", out, err)
	}
	defer out.Close()
	if got := out.Plane(0)[0]; got != 68 {
		t.Errorf("luma column 0 = %d, want 68 (sample 0)", got)
	}
}

func TestPathCrop_CloseReportsUnusedSamples(t *testing.T) {
	p := newPathCrop(t, `{"width":64,"frames":[{"pts":0,"cx":100},{"pts":10,"cx":100}]}`)
	err := p.Close()
	if err == nil || !strings.Contains(err.Error(), "2 of 2") || !strings.Contains(err.Error(), "first unused pts 0") {
		t.Fatalf("Close = %v, want 2 of 2 unused", err)
	}
}

func TestPathCrop_PassesNonVideo(t *testing.T) {
	p := newPathCrop(t, `{"width":64,"frames":[{"pts":0,"cx":100}]}`)
	src := columnFrame(t, 320, 16, 0)
	defer src.Close()
	out, _, err := p.Process(src, ProcessorContext{MediaType: av.MediaTypeAudio})
	if err != nil || out != src {
		t.Fatalf("non-video: out=%v err=%v, want passthrough", out, err)
	}
	if out, _, err := p.Process(nil, videoCtx); out != nil || err != nil {
		t.Fatalf("nil frame: out=%v err=%v", out, err)
	}
}

func TestPathCrop_NarrowFrameIsError(t *testing.T) {
	p := newPathCrop(t, `{"width":400,"frames":[{"pts":0,"cx":100}]}`)
	src := columnFrame(t, 320, 16, 0)
	defer src.Close()
	if out, _, err := p.Process(src, videoCtx); err == nil {
		out.Close()
		t.Fatal("narrow frame: want error")
	}
}

func TestPathCrop_FramesParse(t *testing.T) {
	p := newPathCrop(t, `{"width":"608","frames":[{"pts":18399380,"cx":1285.2289979757086},{"pts":18402383,"cx":1285.8241402692095}]}`)
	if p.width != 608 || len(p.pts) != 2 || p.half != 1501 {
		t.Fatalf("parsed %+v, want width 608, 2 samples, half 1501", p)
	}
	if p.pts[1] != 18402383 || p.cx[0] != 1285.2289979757086 {
		t.Errorf("samples (%d, %v), (%d, %v)", p.pts[0], p.cx[0], p.pts[1], p.cx[1])
	}
}

// TestPathCrop_RejectsNonFiniteCX covers Go callers; JSON cannot carry NaN.
func TestPathCrop_RejectsNonFiniteCX(t *testing.T) {
	for _, cx := range []float64{math.NaN(), math.Inf(1)} {
		err := (&PathCrop{}).Init(map[string]any{"width": 8, "frames": []map[string]any{{"pts": 0, "cx": cx}}})
		if err == nil {
			t.Errorf("cx %v: Init succeeded, want error", cx)
		}
	}
}

func TestPathCrop_InitErrors(t *testing.T) {
	for name, params := range map[string]string{
		"missing width":    `{"frames":[{"pts":0,"cx":1}]}`,
		"odd width":        `{"width":7,"frames":[{"pts":0,"cx":1}]}`,
		"width below 2":    `{"width":0,"frames":[{"pts":0,"cx":1}]}`,
		"fractional width": `{"width":8.5,"frames":[{"pts":0,"cx":1}]}`,
		"no path":          `{"width":8}`,
		"empty frames":     `{"width":8,"frames":[]}`,
		"frames not array": `{"width":8,"frames":"0:1"}`,
		"element not obj":  `{"width":8,"frames":[3]}`,
		"string pts":       `{"width":8,"frames":[{"pts":"0","cx":1}]}`,
		"string cx":        `{"width":8,"frames":[{"pts":0,"cx":"1"}]}`,
		"missing cx":       `{"width":8,"frames":[{"pts":0}]}`,
		"fractional pts":   `{"width":8,"frames":[{"pts":0.5,"cx":1}]}`,
		"pts beyond 2^53":  `{"width":8,"frames":[{"pts":1e17,"cx":1}]}`,
		"repeated pts":     `{"width":8,"frames":[{"pts":0,"cx":1},{"pts":0,"cx":2}]}`,
		"decreasing pts":   `{"width":8,"frames":[{"pts":10,"cx":1},{"pts":0,"cx":2}]}`,
		"pts/cx strings":   `{"width":8,"pts":"0,10","cx":"1,2"}`,
	} {
		var m map[string]any
		if err := json.Unmarshal([]byte(params), &m); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if err := (&PathCrop{}).Init(m); err == nil {
			t.Errorf("%s: Init succeeded, want error", name)
		}
	}
}

func TestPathCrop_IgnoresInjectedFrameRate(t *testing.T) {
	newPathCrop(t, `{"width":8,"frame_rate":29.97,"frames":[{"pts":0,"cx":1}]}`)
}
