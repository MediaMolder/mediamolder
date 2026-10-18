// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

var videoCtx = ProcessorContext{MediaType: av.MediaTypeVideo}

// columnFrame is a yuv420p frame whose luma column x holds x (mod 256).
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

func TestMovingCropScale_Registered(t *testing.T) {
	p, err := Get("moving_crop_scale")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if _, ok := p.(*MovingCropScale); !ok {
		t.Fatalf("Get = %T, want *MovingCropScale", p)
	}
}

func mcsInit(t *testing.T, params map[string]any) *MovingCropScale {
	t.Helper()
	p := &MovingCropScale{}
	if err := p.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	t.Cleanup(func() {
		if err := p.Close(); err != nil {
			t.Errorf("Close: %v", err)
		}
	})
	return p
}

func mcsFail(t *testing.T, params map[string]any, want string) {
	t.Helper()
	p := &MovingCropScale{}
	err := p.Init(params)
	p.Close()
	if err == nil {
		t.Fatalf("Init succeeded, want error containing %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("Init error %q, want substring %q", err.Error(), want)
	}
}

func mcsParams(w, h int, extra map[string]any, kfs ...map[string]any) map[string]any {
	items := make([]any, len(kfs))
	for i := range kfs {
		items[i] = kfs[i]
	}
	p := map[string]any{
		"output":    map[string]any{"width": w, "height": h},
		"keyframes": items,
	}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

func kf(pts int, x, y, w, h float64) map[string]any {
	return map[string]any{"pts": pts, "x": x, "y": y, "w": w, "h": h}
}

func appliedRect(t *testing.T, md *Metadata) (pts int64, x, y, w, h int, interp bool) {
	t.Helper()
	if md == nil || md.Custom == nil {
		t.Fatal("missing emit_rect metadata")
	}
	switch n := md.Custom["pts"].(type) {
	case int64:
		pts = n
	case int:
		pts = int64(n)
	default:
		t.Fatalf("pts type %T", md.Custom["pts"])
	}
	num := func(k string) int {
		t.Helper()
		switch n := md.Custom[k].(type) {
		case int:
			return n
		case int64:
			return int(n)
		default:
			t.Fatalf("%s type %T (%v)", k, md.Custom[k], md.Custom[k])
			return 0
		}
	}
	b, ok := md.Custom["interpolated"].(bool)
	if !ok {
		t.Fatalf("interpolated type %T", md.Custom["interpolated"])
	}
	return pts, num("x"), num("y"), num("w"), num("h"), b
}

func pixFrame(t *testing.T, format string, w, h int, pts int64) *av.Frame {
	t.Helper()
	pf := av.PixFmtByName(format)
	if pf < 0 {
		t.Fatalf("unknown pixel format %s", format)
	}
	f, err := av.NewVideoFrame(w, h, pf)
	if err != nil {
		t.Fatalf("NewVideoFrame %s: %v", format, err)
	}
	f.SetPTS(pts)
	return f
}

func runOne(t *testing.T, p *MovingCropScale, f *av.Frame) (*av.Frame, *Metadata) {
	t.Helper()
	pts := f.PTS()
	out, md, err := p.Process(f, videoCtx)
	f.Close()
	if err != nil {
		t.Fatalf("pts %d: %v", pts, err)
	}
	return out, md
}

func TestMovingCropScale_InitRejects(t *testing.T) {
	ok := kf(0, 0, 0, 32, 32)
	cases := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"missing output", map[string]any{"keyframes": []any{ok}}, "missing output"},
		{"odd width", mcsParams(63, 64, nil, ok), "even"},
		{"small height", mcsParams(64, 1, nil, ok), "even"},
		{"empty keyframes", mcsParams(64, 64, nil), "empty"},
		{"missing keyframes", map[string]any{"output": map[string]any{"width": 64, "height": 64}}, "missing keyframes"},
		{"mixed zoom", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "zoom": 2}, kf(10, 0, 0, 32, 32)), "mix zoom"},
		{"zoom and w", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "w": 32, "zoom": 2}), "mixes zoom"},
		{"mixed t and pts", mcsParams(64, 64, nil, map[string]any{"t": 0.0, "x": 0, "y": 0, "w": 32, "h": 32}, ok), "mix t and pts"},
		{"both on one", mcsParams(64, 64, nil, map[string]any{"pts": 0, "t": 0.0, "x": 0, "y": 0, "w": 32, "h": 32}), "mixes t and pts"},
		{"xy and centre", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "cx": 16, "y": 0, "w": 32, "h": 32}), "mixes x/y"},
		{"missing y", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "w": 32, "h": 32}), "needs both x and y"},
		{"missing position", mcsParams(64, 64, nil, map[string]any{"pts": 0, "w": 32, "h": 32}), "needs x/y"},
		{"pts too big", mcsParams(64, 64, nil, map[string]any{"pts": int64(1<<53 + 1), "x": 0, "y": 0, "w": 32, "h": 32}), "outside"},
		{"pts maxint", mcsParams(64, 64, nil, map[string]any{"pts": int64(math.MaxInt64 - 10), "x": 0, "y": 0, "w": 32, "h": 32}), "outside"},
		{"pts negative", mcsParams(64, 64, nil, map[string]any{"pts": int(-(1 << 53) - 1), "x": 0, "y": 0, "w": 32, "h": 32}), "outside"},
		{"float pts", mcsParams(64, 64, nil, map[string]any{"pts": float64(1 << 54), "x": 0, "y": 0, "w": 32, "h": 32}), "outside"},
		{"not increasing", mcsParams(64, 64, nil, ok, map[string]any{"pts": 0, "x": 0, "y": 0, "w": 32, "h": 32}), "does not follow"},
		{"bad enum", mcsParams(64, 64, map[string]any{"interpolate": "cubic"}, ok), "interpolate"},
		{"bad flags", mcsParams(64, 64, map[string]any{"flags": "sinc"}, ok), "sinc"},
		{"zoom zero", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "zoom": 0}), "greater than 0"},
		{"zoom negative", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "zoom": -1}), "greater than 0"},
		{"side below 2", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "w": 1, "h": 32}), "below 2"},
		{"missing h", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": 0, "y": 0, "w": 32}), "needs h"},
		{"derived h", mcsParams(100, 4, map[string]any{"aspect": "output"}, map[string]any{"pts": 0, "x": 0, "y": 0, "w": 2}), "below 2"},
		{"nonfinite", mcsParams(64, 64, nil, map[string]any{"pts": 0, "x": math.NaN(), "y": 0, "w": 32, "h": 32}), "not finite"},
		{"negative t", mcsParams(64, 64, nil, map[string]any{"t": -0.1, "x": 0, "y": 0, "w": 32, "h": 32}), "negative"},
		{"bad emit", mcsParams(64, 64, map[string]any{"emit_rect": "yes"}, ok), "bool"},
		{"sample linear", mcsParams(64, 64, map[string]any{"match": "sample", "interpolate": "linear"}, ok), "only allows"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mcsFail(t, tc.params, tc.want)
		})
	}
}

func TestMovingCropScale_PTSBoundAccepted(t *testing.T) {
	mcsInit(t, mcsParams(64, 64, nil,
		map[string]any{"pts": int64(1 << 53), "x": 0, "y": 0, "w": 8, "h": 8},
	))
	mcsInit(t, mcsParams(64, 64, nil,
		map[string]any{"pts": int64(-1 << 53), "x": 0, "y": 0, "w": 8, "h": 8},
	))
}

func TestMovingCropScale_Sqrt2Zoom(t *testing.T) {
	p := mcsInit(t, mcsParams(100, 100, map[string]any{"emit_rect": true},
		map[string]any{"pts": 0, "x": 0, "y": 0, "zoom": 1},
		map[string]any{"pts": 100, "x": 0, "y": 0, "zoom": 2},
	))
	f := pixFrame(t, "yuv420p", 128, 128, 50)
	out, md := runOne(t, p, f)
	defer out.Close()
	_, x, y, w, h, interp := appliedRect(t, md)
	if !interp || x != 0 || y != 0 || w != 70 || h != 70 {
		t.Fatalf("rect x,y,w,h=%d,%d,%d,%d interp=%v, want 0,0,70,70 true", x, y, w, h, interp)
	}
}

func TestMovingCropScale_AspectDiscardsHBeforeCentre(t *testing.T) {
	// Supplied h=999 must not centre the window. Derived h = 40 * 50/100 = 20,
	// so y = 40 - 10 = 30.
	p := mcsInit(t, mcsParams(100, 50, map[string]any{"aspect": "output", "emit_rect": true},
		map[string]any{"pts": 0, "cx": 50, "cy": 40, "w": 40, "h": 999},
	))
	f := pixFrame(t, "yuv420p", 128, 128, 0)
	out, md := runOne(t, p, f)
	defer out.Close()
	_, x, y, w, h, interp := appliedRect(t, md)
	if interp || x != 30 || y != 30 || w != 40 || h != 20 {
		t.Fatalf("rect %d,%d %dx%d interp=%v, want 30,30 40x20", x, y, w, h, interp)
	}
}

func TestMovingCropScale_SmoothMidpoint(t *testing.T) {
	// x = 0, 100, 0. Catmull-Rom at the midpoint of the first segment is 56.25,
	// which rounds ties-down to 56 and stays on the chroma grid.
	p := mcsInit(t, mcsParams(64, 64, map[string]any{"interpolate": "smooth", "emit_rect": true},
		kf(0, 0, 0, 64, 64),
		kf(100, 100, 0, 64, 64),
		kf(200, 0, 0, 64, 64),
	))
	f := columnFrame(t, 320, 64, 50)
	out, md, err := p.Process(f, videoCtx)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	_, x, _, w, h, interp := appliedRect(t, md)
	if !interp || x != 56 || w != 64 || h != 64 {
		t.Fatalf("x=%d w=%d h=%d interp=%v, want 56, 64x64, true", x, w, h, interp)
	}
	if got := int(out.Plane(0)[0]); got != 56 {
		t.Fatalf("luma column 0 = %d, want 56", got)
	}
}

func TestMovingCropScale_SmoothZoomStaysPositive(t *testing.T) {
	// Catmull-Rom in log size at t=0.5 of zoom 1, 2, 1 is about 67.71, which
	// rounds even to 68. The linear midpoint would be 70.
	p := mcsInit(t, mcsParams(100, 100, map[string]any{"interpolate": "smooth", "emit_rect": true},
		map[string]any{"pts": 0, "x": 0, "y": 0, "zoom": 1},
		map[string]any{"pts": 100, "x": 0, "y": 0, "zoom": 2},
		map[string]any{"pts": 200, "x": 0, "y": 0, "zoom": 1},
	))
	f := pixFrame(t, "yuv420p", 128, 128, 50)
	out, md := runOne(t, p, f)
	defer out.Close()
	_, _, _, w, h, interp := appliedRect(t, md)
	if !interp || w != 68 || h != 68 {
		t.Fatalf("w,h=%d,%d interp=%v, want 68,68 true", w, h, interp)
	}
}

func TestMovingCropScale_SmoothNegativeWidthCollapses(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, map[string]any{"interpolate": "smooth"},
		kf(0, 0, 0, 2, 64),
		kf(100, 0, 0, 2, 64),
		kf(200, 0, 0, 200, 64),
	))
	f := pixFrame(t, "yuv420p", 320, 64, 50)
	_, _, err := p.Process(f, videoCtx)
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "collapsed") {
		t.Fatalf("err = %v, want collapsed", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMovingCropScale_LinearMidpointAndHold(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, map[string]any{"emit_rect": true},
		kf(0, 0, 0, 32, 32),
		kf(100, 10, 0, 32, 32),
	))
	f := pixFrame(t, "yuv420p", 64, 64, 50)
	out, md := runOne(t, p, f)
	defer out.Close()
	// 5 aligns down to 4 on 4:2:0.
	_, x, _, _, _, interp := appliedRect(t, md)
	if !interp || x != 4 {
		t.Fatalf("linear x=%d interp=%v, want 4 true", x, interp)
	}

	hold := mcsInit(t, mcsParams(32, 32, map[string]any{"interpolate": "hold", "emit_rect": true},
		kf(0, 0, 0, 32, 32),
		kf(100, 10, 0, 32, 32),
	))
	f = pixFrame(t, "yuv420p", 64, 64, 50)
	out, md = runOne(t, hold, f)
	defer out.Close()
	_, x, _, _, _, interp = appliedRect(t, md)
	if interp || x != 0 {
		t.Fatalf("hold x=%d interp=%v, want 0 false", x, interp)
	}
}

func TestMovingCropScale_UnmatchedAndNoPTS(t *testing.T) {
	for _, mode := range []string{"hold", "drop"} {
		p := mcsInit(t, mcsParams(32, 32, map[string]any{"unmatched": mode, "emit_rect": true},
			kf(100, 8, 0, 32, 32),
		))
		for _, pts := range []int64{50, 100, 150} {
			f := pixFrame(t, "yuv420p", 64, 32, pts)
			out, md, err := p.Process(f, videoCtx)
			f.Close()
			if err != nil {
				t.Fatalf("%s pts %d: %v", mode, pts, err)
			}
			want := mode == "hold" || pts == 100
			if (out != nil) != want {
				t.Fatalf("%s pts %d: kept=%v, want %v", mode, pts, out != nil, want)
			}
			if out != nil {
				_, x, _, _, _, interp := appliedRect(t, md)
				if interp || x != 8 {
					t.Errorf("%s pts %d: x=%d interp=%v", mode, pts, x, interp)
				}
				out.Close()
			}
		}
		f := pixFrame(t, "yuv420p", 64, 32, 0)
		f.SetPTS(av.NoPTSValue)
		out, _, err := p.Process(f, videoCtx)
		f.Close()
		if err != nil || out != nil {
			t.Fatalf("%s no PTS: out=%v err=%v, want drop", mode, out, err)
		}
	}

	p := mcsInit(t, mcsParams(32, 32, map[string]any{"unmatched": "error"}, kf(100, 0, 0, 32, 32)))
	f := pixFrame(t, "yuv420p", 64, 32, 50)
	_, _, err := p.Process(f, videoCtx)
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("err = %v, want outside", err)
	}
	f = pixFrame(t, "yuv420p", 64, 32, 0)
	f.SetPTS(av.NoPTSValue)
	_, _, err = p.Process(f, videoCtx)
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "no PTS") {
		t.Fatalf("err = %v, want no PTS", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMovingCropScale_ChromaAlign(t *testing.T) {
	cases := []struct {
		format     string
		align      string
		x, y, w, h int
	}{
		{"yuv420p", "chroma", 4, 4, 18, 18},
		{"yuv422p", "chroma", 4, 5, 18, 18},
		{"yuv444p", "chroma", 5, 5, 18, 18},
		{"yuv420p", "exact", 5, 5, 18, 18},
	}
	for _, tc := range cases {
		t.Run(tc.format+"_"+tc.align, func(t *testing.T) {
			p := mcsInit(t, mcsParams(8, 8, map[string]any{"align": tc.align, "emit_rect": true},
				map[string]any{"pts": 0, "x": 5.2, "y": 5.2, "w": 17.2, "h": 17.2},
			))
			f := pixFrame(t, tc.format, 64, 64, 0)
			out, md := runOne(t, p, f)
			defer out.Close()
			_, x, y, w, h, interp := appliedRect(t, md)
			if interp || x != tc.x || y != tc.y || w != tc.w || h != tc.h {
				t.Fatalf("got %d,%d %dx%d, want %d,%d %dx%d", x, y, w, h, tc.x, tc.y, tc.w, tc.h)
			}
		})
	}
}

func TestMovingCropScale_OddVisibleWidth(t *testing.T) {
	p := mcsInit(t, mcsParams(1918, 16, map[string]any{"emit_rect": true},
		map[string]any{"pts": 0, "x": 0, "y": 0, "w": 1919, "h": 16},
	))
	f := pixFrame(t, "yuv420p", 1920, 16, 0)
	f.SetCrop(0, 0, 0, 1)
	if f.VisibleWidth() != 1919 {
		t.Fatalf("visible width %d, want 1919", f.VisibleWidth())
	}
	out, md, err := p.Process(f, videoCtx)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	_, x, _, w, h, _ := appliedRect(t, md)
	if x != 0 || w != 1918 || h != 16 || out.Width() != 1918 || out.Height() != 16 {
		t.Fatalf("rect %d %dx%d frame %dx%d, want x=0 1918x16", x, w, h, out.Width(), out.Height())
	}
}

func TestMovingCropScale_FastPathLeftEdge(t *testing.T) {
	const width, height = 136, 240
	samples := []struct {
		pts  int64
		cx   float64
		want int
	}{
		{0, 100, 32},
		{10, 100, 32},
		{20, 160.6, 92},
		{30, 250.2, 182},
		{40, 160.5, 92},
	}
	kfs := make([]any, len(samples))
	for i, s := range samples {
		kfs[i] = map[string]any{"pts": s.pts, "cx": s.cx, "cy": height / 2, "w": width, "h": height}
	}
	mov := mcsInit(t, map[string]any{
		"output":      map[string]any{"width": width, "height": height},
		"keyframes":   kfs,
		"match":       "sample",
		"interpolate": "hold",
		"unmatched":   "drop",
		"align":       "chroma",
	})
	for _, s := range samples {
		src := columnFrame(t, 320, height, s.pts)
		out, _, err := mov.Process(src, videoCtx)
		src.Close()
		if err != nil || out == nil {
			t.Fatalf("pts %d: out=%v err=%v", s.pts, out != nil, err)
		}
		if out.Width() != width || out.Height() != height {
			t.Fatalf("pts %d: %dx%d", s.pts, out.Width(), out.Height())
		}
		for _, row := range []int{0, height - 1} {
			if got := int(out.Plane(0)[row*out.Linesize(0)]); got != s.want {
				t.Errorf("pts %d row %d: luma column 0 = %d, want %d", s.pts, row, got, s.want)
			}
		}
		out.Close()
	}
	if mov.ScaleReinits() != 0 {
		t.Fatalf("fast path rebuilt swscale %d times", mov.ScaleReinits())
	}
}

func TestMovingCropScale_ScaleChangesSize(t *testing.T) {
	p := mcsInit(t, mcsParams(16, 16, nil,
		kf(0, 0, 0, 32, 32),
		kf(10, 0, 0, 48, 48),
		kf(20, 0, 0, 32, 32),
	))
	var want int64 = 1
	for _, pts := range []int64{0, 10, 20} {
		f := pixFrame(t, "yuv420p", 80, 80, pts)
		out, _, err := p.Process(f, videoCtx)
		f.Close()
		if err != nil {
			t.Fatal(err)
		}
		if out.Width() != 16 || out.Height() != 16 || out.PTS() != pts {
			t.Fatalf("out %dx%d pts %d", out.Width(), out.Height(), out.PTS())
		}
		out.Close()
		if got := p.ScaleReinits(); got != want {
			t.Fatalf("after pts %d reinits %d, want %d", pts, got, want)
		}
		want++
	}
}

func TestMovingCropScale_RejectsInterlacedAndHardware(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, nil, kf(0, 0, 0, 32, 32)))
	f := pixFrame(t, "yuv420p", 64, 64, 0)
	f.SetFieldFlags(true, true)
	_, _, err := p.Process(f, videoCtx)
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "interlaced") {
		t.Fatalf("err = %v, want interlaced", err)
	}

	f = pixFrame(t, "yuv420p", 64, 64, 0)
	pix := av.PixFmtByName("cuda")
	if pix < 0 {
		pix = av.PixFmtByName("videotoolbox")
	}
	if pix < 0 {
		t.Skip("no hardware pixel format")
	}
	f.SetTestPixFmt(pix)
	_, _, err = p.Process(f, videoCtx)
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "not supported") {
		t.Fatalf("err = %v, want not supported", err)
	}
}

func TestMovingCropScale_PassThrough(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, nil, kf(0, 0, 0, 32, 32)))
	if out, md, err := p.Process(nil, videoCtx); out != nil || md != nil || err != nil {
		t.Fatalf("nil frame: %v %v %v", out, md, err)
	}
	f := pixFrame(t, "yuv420p", 64, 64, 7)
	out, md, err := p.Process(f, ProcessorContext{MediaType: av.MediaTypeAudio})
	if err != nil || md != nil || out != f {
		t.Fatalf("audio out=%v md=%v err=%v", out, md, err)
	}
	f.Close()
}

func TestMovingCropScale_TimeKeyframes(t *testing.T) {
	raw := []byte(`{"output":{"width":64,"height":64},"emit_rect":true,"keyframes":[
		{"t":0,"x":0,"y":0,"w":64,"h":64},
		{"t":0.5,"x":20,"y":0,"w":64,"h":64}
	]}`)
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	p := mcsInit(t, params)
	ctx := ProcessorContext{MediaType: av.MediaTypeVideo, TimeBase: [2]int{1, 25}}
	// 0.5s in 1/25 rounds halfway away from zero to tick 13.
	f := columnFrame(t, 320, 64, 12)
	out, md, err := p.Process(f, ctx)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	_, x, _, _, _, interp := appliedRect(t, md)
	if !interp || x != 18 {
		t.Fatalf("tick 12 x=%d interp=%v, want 18 true", x, interp)
	}
	if got := int(out.Plane(0)[0]); got != 18 {
		t.Fatalf("luma column 0 = %d, want 18", got)
	}

	f = columnFrame(t, 320, 64, 13)
	out, md, err = p.Process(f, ctx)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	pts, x, _, _, _, interp := appliedRect(t, md)
	if interp || x != 20 || pts != 13 {
		t.Fatalf("tick 13 pts=%d x=%d interp=%v, want exact 20", pts, x, interp)
	}
}

func TestMovingCropScale_TimeBaseErrors(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, nil,
		map[string]any{"t": 0.0, "x": 0, "y": 0, "w": 32, "h": 32},
		map[string]any{"t": 0.4, "x": 0, "y": 0, "w": 32, "h": 32},
	))
	f := pixFrame(t, "yuv420p", 64, 32, 0)
	_, _, err := p.Process(f, ProcessorContext{MediaType: av.MediaTypeVideo, TimeBase: [2]int{1, 1}})
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "same tick") {
		t.Fatalf("err = %v, want same tick", err)
	}

	p = mcsInit(t, mcsParams(32, 32, nil, map[string]any{"t": 0.0, "x": 0, "y": 0, "w": 32, "h": 32}))
	f = pixFrame(t, "yuv420p", 64, 32, 0)
	_, _, err = p.Process(f, ProcessorContext{MediaType: av.MediaTypeVideo, TimeBase: [2]int{0, 1}})
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "cannot convert") {
		t.Fatalf("err = %v, want cannot convert", err)
	}

	// 1e7 seconds in time base 1/2000000000 is 2e16 ticks, past ±2^53.
	// Both the seconds and the denominator fit in the int32 rational av_d2q uses.
	p = mcsInit(t, mcsParams(32, 32, nil, map[string]any{"t": 1e7, "x": 0, "y": 0, "w": 32, "h": 32}))
	f = pixFrame(t, "yuv420p", 64, 32, 0)
	_, _, err = p.Process(f, ProcessorContext{MediaType: av.MediaTypeVideo, TimeBase: [2]int{1, 2000000000}})
	f.Close()
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("err = %v, want outside ±2^53", err)
	}
}

func TestMovingCropScale_JSONConfig(t *testing.T) {
	raw := []byte(`{
		"output": {"width": 32, "height": 32},
		"interpolate": "linear",
		"unmatched": "hold",
		"keyframes": [
			{"pts": 0, "cx": 40, "cy": 16, "w": 32, "h": 32},
			{"pts": 1000, "cx": 80, "cy": 16, "w": 32, "h": 32}
		]
	}`)
	var params map[string]any
	if err := json.Unmarshal(raw, &params); err != nil {
		t.Fatal(err)
	}
	p := mcsInit(t, params)
	f := columnFrame(t, 320, 32, 0)
	out, _, err := p.Process(f, videoCtx)
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	defer out.Close()
	// cx 40, w 32 → x 24. Column 24.
	if out.Width() != 32 || out.Height() != 32 {
		t.Fatalf("out %dx%d", out.Width(), out.Height())
	}
	if got := int(out.Plane(0)[0]); got != 24 {
		t.Fatalf("luma column 0 = %d, want 24", got)
	}
}

func sampleParams(w, h int, kfs ...map[string]any) map[string]any {
	return mcsParams(w, h, map[string]any{"match": "sample", "interpolate": "hold", "unmatched": "drop"}, kfs...)
}

func centreKF(pts int, cx float64) map[string]any {
	return map[string]any{"pts": pts, "cx": cx, "cy": 8, "w": 64, "h": 16}
}

func TestMovingCropScale_SampleMatch(t *testing.T) {
	p := &MovingCropScale{}
	if err := p.Init(sampleParams(64, 16, centreKF(3000, 160), centreKF(6003, 160), centreKF(9006, 160))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pts  int64
		keep bool
	}{
		{1400, false},
		{3001, true},
		{3000, false},
		{6002, true},
		{7500, false},
		{9006, true},
		{12009, false},
	} {
		src := columnFrame(t, 320, 16, tc.pts)
		out, _, err := p.Process(src, videoCtx)
		src.Close()
		if err != nil {
			t.Fatalf("pts %d: %v", tc.pts, err)
		}
		if (out != nil) != tc.keep {
			t.Errorf("pts %d: kept=%v, want %v", tc.pts, out != nil, tc.keep)
		}
		if out != nil {
			out.Close()
		}
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close after every keyframe matched: %v", err)
	}
}

func TestMovingCropScale_SampleHalfStepMatchesNeither(t *testing.T) {
	p := &MovingCropScale{}
	if err := p.Init(sampleParams(64, 16, centreKF(0, 100), centreKF(20, 200), centreKF(40, 300))); err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		pts  int64
		keep bool
		col0 byte
	}{
		{0, true, 68},
		{10, false, 0},
		{20, true, 168},
		{30, false, 0},
		{40, true, 0},
	} {
		src := columnFrame(t, 320, 16, tc.pts)
		out, _, err := p.Process(src, videoCtx)
		src.Close()
		if err != nil {
			t.Fatalf("pts %d: %v", tc.pts, err)
		}
		if (out != nil) != tc.keep {
			t.Fatalf("pts %d: kept=%v, want %v", tc.pts, out != nil, tc.keep)
		}
		if out != nil {
			if got := out.Plane(0)[0]; got != tc.col0 {
				t.Errorf("pts %d: luma column 0 = %d, want %d", tc.pts, got, tc.col0)
			}
			out.Close()
		}
	}
	if err := p.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
}

func TestMovingCropScale_SampleDropsFarPTS(t *testing.T) {
	p := &MovingCropScale{}
	if err := p.Init(sampleParams(64, 16,
		map[string]any{"pts": 0, "cx": 100, "cy": 8, "w": 64, "h": 16},
		map[string]any{"pts": int64(1 << 53), "cx": 100, "cy": 8, "w": 64, "h": 16},
	)); err != nil {
		t.Fatal(err)
	}
	for _, pts := range []int64{av.NoPTSValue, math.MinInt64 + 1, math.MaxInt64, -(1 << 55), 1 << 55, -(1 << 62), 1 << 62} {
		src := columnFrame(t, 320, 16, pts)
		out, _, err := p.Process(src, videoCtx)
		src.Close()
		if err != nil {
			t.Fatalf("pts %d: %v", pts, err)
		}
		if out != nil {
			out.Close()
			t.Errorf("pts %d matched a keyframe", pts)
		}
	}
	src := columnFrame(t, 320, 16, 0)
	out, _, err := p.Process(src, videoCtx)
	src.Close()
	if err != nil || out == nil {
		t.Fatalf("pts 0 after far PTS: out=%v err=%v", out != nil, err)
	}
	out.Close()
	if err := p.Close(); err == nil || !strings.Contains(err.Error(), "1 of 2") {
		t.Fatalf("Close = %v, want the unused far keyframe", err)
	}
}

func TestMovingCropScale_SampleCloseReportsUnused(t *testing.T) {
	p := &MovingCropScale{}
	if err := p.Init(sampleParams(64, 16, centreKF(0, 100), centreKF(10, 100))); err != nil {
		t.Fatal(err)
	}
	err := p.Close()
	if err == nil || !strings.Contains(err.Error(), "2 of 2") || !strings.Contains(err.Error(), "first unused pts 0") {
		t.Fatalf("Close = %v, want 2 of 2 unused", err)
	}
}

func TestMovingCropScale_SampleOneTick(t *testing.T) {
	for _, tc := range []struct {
		pts  int64
		keep bool
	}{
		{10, true},
		{11, true},
		{9, true},
		{12, false},
		{8, false},
	} {
		p := &MovingCropScale{}
		if err := p.Init(sampleParams(32, 32, kf(10, 0, 0, 32, 32))); err != nil {
			t.Fatal(err)
		}
		f := pixFrame(t, "yuv420p", 32, 32, tc.pts)
		out, _, err := p.Process(f, videoCtx)
		f.Close()
		if err != nil {
			t.Fatalf("pts %d: %v", tc.pts, err)
		}
		if (out != nil) != tc.keep {
			t.Errorf("pts %d: kept=%v, want %v", tc.pts, out != nil, tc.keep)
		}
		if out != nil {
			out.Close()
		}
		cerr := p.Close()
		if tc.keep && cerr != nil {
			t.Errorf("pts %d: Close = %v", tc.pts, cerr)
		}
		if !tc.keep && cerr == nil {
			t.Errorf("pts %d: Close succeeded, want the unused keyframe", tc.pts)
		}
	}
}

func TestMovingCropScale_SampleTimeGap(t *testing.T) {
	p := mcsInit(t, mcsParams(32, 32, map[string]any{
		"match": "sample", "interpolate": "hold", "unmatched": "drop", "emit_rect": true,
	},
		map[string]any{"t": 0.0, "x": 0, "y": 0, "w": 32, "h": 32},
		map[string]any{"t": 1.0, "x": 8, "y": 0, "w": 32, "h": 32},
	))
	ctx := ProcessorContext{MediaType: av.MediaTypeVideo, TimeBase: [2]int{1, 25}}
	f := pixFrame(t, "yuv420p", 64, 32, 1)
	out, md, err := p.Process(f, ctx)
	f.Close()
	if err != nil || out == nil {
		t.Fatalf("tick 1: out=%v err=%v", out != nil, err)
	}
	out.Close()
	_, x, _, _, _, _ := appliedRect(t, md)
	if x != 0 {
		t.Fatalf("tick 1 x=%d, want the first keyframe", x)
	}
	f = pixFrame(t, "yuv420p", 64, 32, 25)
	out, md, err = p.Process(f, ctx)
	f.Close()
	if err != nil || out == nil {
		t.Fatalf("tick 25: out=%v err=%v", out != nil, err)
	}
	out.Close()
	_, x, _, _, _, _ = appliedRect(t, md)
	if x != 8 {
		t.Fatalf("tick 25 x=%d, want 8", x)
	}
}

func TestMovingCropScale_SampleHoldDoesNotConsume(t *testing.T) {
	p := mcsInit(t, mcsParams(64, 16, map[string]any{
		"match": "sample", "interpolate": "hold", "unmatched": "hold", "emit_rect": true,
	}, centreKF(0, 100), centreKF(20, 200)))
	mid := columnFrame(t, 320, 16, 10)
	out, md, err := p.Process(mid, videoCtx)
	mid.Close()
	if err != nil || out == nil {
		t.Fatalf("halfway hold: out=%v err=%v", out != nil, err)
	}
	out.Close()
	_, x, _, _, _, interp := appliedRect(t, md)
	if x != 68 || interp {
		t.Fatalf("halfway hold x=%d interp=%v, want 68 false", x, interp)
	}
	for _, pts := range []int64{0, 20} {
		f := columnFrame(t, 320, 16, pts)
		got, _, err := p.Process(f, videoCtx)
		f.Close()
		if err != nil || got == nil {
			t.Fatalf("pts %d: out=%v err=%v", pts, got != nil, err)
		}
		got.Close()
	}
}
