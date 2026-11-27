// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package lookahead

import (
	"testing"
)

// shortFixture builds a lag-1-only CostMatrix with luma thumbnails.
type shortFixture struct {
	m *CostMatrix
}

func newShortFixture(n int, floor float32) *shortFixture {
	m := &CostMatrix{N: n, L: 1, Lags: []int{1}, ThumbC: 1}
	for j := 0; j < n; j++ {
		m.Ratio = append(m.Ratio, []float32{floor})
		m.InterCost = append(m.InterCost, []float32{0})
		m.IntraCost = append(m.IntraCost, 1000)
		m.AvgLuma = append(m.AvgLuma, 100)
		m.Thumb = append(m.Thumb, make([]float32, ThumbW*ThumbH))
	}
	return &shortFixture{m}
}

// scene returns a deterministic pseudo-random thumbnail.
func scene(seed uint32) []float32 {
	t := make([]float32, ThumbW*ThumbH)
	s := seed*2654435761 + 12345
	for i := range t {
		s = s*1664525 + 1013904223
		t[i] = float32(s>>24) * 0.8
	}
	return t
}

// wide returns a pseudo-random field wider than a thumbnail, for panning.
func wide(seed uint32, w int) []float32 {
	t := make([]float32, w*ThumbH)
	s := seed*2654435761 + 999
	for i := range t {
		s = s*1664525 + 1013904223
		t[i] = float32(s>>24) * 0.8
	}
	return t
}

func window(field []float32, w, x0 int) []float32 {
	t := make([]float32, ThumbW*ThumbH)
	for y := 0; y < ThumbH; y++ {
		copy(t[y*ThumbW:(y+1)*ThumbW], field[y*w+x0:y*w+x0+ThumbW])
	}
	return t
}

func mix(a, b []float32, alpha float32) []float32 {
	t := make([]float32, len(a))
	for i := range t {
		t[i] = (1-alpha)*a[i] + alpha*b[i]
	}
	return t
}

func (f *shortFixture) fill(lo, hi int, t []float32) {
	for j := lo; j < hi; j++ {
		f.m.Thumb[j] = t
	}
}

func (f *shortFixture) set(j int, r float32) { f.m.Ratio[j][0] = r }

func (f *shortFixture) detect() []SceneTransition {
	cfg := (&LookaheadAnalyzer{}).withDefaults(f.m.L)
	return cfg.detectShortTransitions(f.m, 0)
}

func wantOne(t *testing.T, got []SceneTransition, typ TransitionType, start, end int) {
	t.Helper()
	if len(got) != 1 {
		t.Fatalf("want 1 transition, got %d: %+v", len(got), got)
	}
	if got[0].Type != typ || got[0].StartFrame != start || got[0].EndFrame != end {
		t.Fatalf("want type %d [%d,%d], got %+v", typ, start, end, got[0])
	}
}

func TestShort_HardCut(t *testing.T) {
	f := newShortFixture(100, 0.25)
	f.fill(0, 50, scene(1))
	f.fill(50, 100, scene(2))
	f.set(50, 0.98)
	wantOne(t, f.detect(), TransitionHardCut, 50, 50)
}

// A hard cut on repeated-frame content (30 fps in a 60 fps stream): every
// other lag-1 ratio is near zero, and the cut is still a single isolated
// spike.
func TestShort_HardCut_RepeatedFrames(t *testing.T) {
	f := newShortFixture(100, 0.05)
	for j := 1; j < 100; j += 2 {
		f.set(j, 0.27)
	}
	f.fill(0, 51, scene(1))
	f.fill(51, 100, scene(2))
	f.set(51, 0.99)
	wantOne(t, f.detect(), TransitionHardCut, 51, 51)
}

// Periodic moderate spikes (e.g. keyframe quality pulses) are isolated but
// far from prediction failure, and are not cuts.
func TestShort_PeriodicPulsesNotCuts(t *testing.T) {
	f := newShortFixture(200, 0.30)
	f.fill(0, 200, scene(1))
	for j := 15; j < 200; j += 30 {
		f.set(j, 0.62)
	}
	if got := f.detect(); len(got) != 0 {
		t.Fatalf("want no transitions, got %+v", got)
	}
}

// One mixed frame between two scenes: reported as a 3-frame span whose
// StartFrame is the last clean old-scene frame.
func TestShort_TwoFrameCut(t *testing.T) {
	f := newShortFixture(100, 0.2)
	a, b := scene(1), scene(2)
	f.fill(0, 50, a)
	f.m.Thumb[50] = mix(a, b, 0.5)
	f.fill(51, 100, b)
	f.set(50, 0.80)
	f.set(51, 0.97)
	wantOne(t, f.detect(), TransitionDissolve, 49, 51)
}

// A cut into a shot in fast motion: the frames after the cut are not calm,
// so the spike is isolated on the old-scene side only; the isolated
// thumbnail jump confirms it.
func TestShort_CutIntoMotion(t *testing.T) {
	const w = 200
	f := newShortFixture(100, 0.3)
	f.fill(0, 50, scene(1))
	field := wide(2, w)
	for j := 50; j < 100; j++ {
		f.m.Thumb[j] = window(field, w, (j-50)/8) // slow drift
		f.set(j, 0.85)
	}
	f.set(50, 0.99)
	wantOne(t, f.detect(), TransitionHardCut, 50, 50)
}

// A fast pan: prediction fails for many consecutive frames, and nothing is
// reported.
func TestShort_PanNotATransition(t *testing.T) {
	const w = 200
	f := newShortFixture(120, 0.25)
	field := wide(3, w)
	f.fill(0, 50, window(field, w, 0))
	for j := 50; j < 62; j++ {
		f.m.Thumb[j] = window(field, w, 2*(j-49))
		f.set(j, 0.95)
	}
	f.fill(62, 120, window(field, w, 24))
	if got := f.detect(); len(got) != 0 {
		t.Fatalf("want no transitions for a pan, got %+v", got)
	}
}

// A stepped dissolve on repeated-frame content: each mix level is held for
// two frames. Bounds are the last clean old-scene frame and the first clean
// new-scene frame.
func TestShort_SteppedDissolve(t *testing.T) {
	f := newShortFixture(120, 0.25)
	a, b := scene(1), scene(2)
	f.fill(0, 40, a)
	for j := 40; j < 48; j++ {
		f.m.Thumb[j] = mix(a, b, float32((j-40)/2+1)/5)
	}
	f.fill(48, 120, b)
	for j := 40; j <= 48; j += 2 {
		f.set(j, 0.62)
	}
	wantOne(t, f.detect(), TransitionDissolve, 39, 48)
}

// A linear dissolve with one mix level per frame.
func TestShort_LinearDissolve(t *testing.T) {
	f := newShortFixture(160, 0.25)
	a, b := scene(4), scene(5)
	f.fill(0, 60, a)
	for j := 60; j < 72; j++ {
		f.m.Thumb[j] = mix(a, b, float32(j-59)/13)
		f.set(j, 0.55)
	}
	f.fill(72, 160, b)
	f.set(72, 0.55)
	wantOne(t, f.detect(), TransitionDissolve, 59, 72)
}

// A long dissolve: the mix advances by less than the short-blend margin per
// frame, and the bounds must still be the last clean old-scene frame and the
// first clean new-scene frame.
func TestShort_LongDissolveBounds(t *testing.T) {
	for _, d := range []int{25, 40, 60} {
		f := newShortFixture(300, 0.25)
		a, b := scene(6), scene(7)
		f.fill(0, 100, a)
		for j := 100; j < 100+d; j++ {
			f.m.Thumb[j] = mix(a, b, float32(j-99)/float32(d+1))
			f.set(j, 0.55)
		}
		f.fill(100+d, 300, b)
		f.set(100+d, 0.55)
		got := f.detect()
		if len(got) != 1 || got[0].Type != TransitionDissolve || got[0].StartFrame != 99 || got[0].EndFrame != 100+d {
			t.Fatalf("D=%d: want dissolve [99,%d], got %+v", d, 100+d, got)
		}
	}
}

// An explicit maximum blend length below the default is honoured.
func TestShort_MaxBlendHonoured(t *testing.T) {
	f := newShortFixture(160, 0.25)
	a, b := scene(4), scene(5)
	f.fill(0, 60, a)
	for j := 60; j < 72; j++ {
		f.m.Thumb[j] = mix(a, b, float32(j-59)/13)
		f.set(j, 0.55)
	}
	f.fill(72, 160, b)
	f.set(72, 0.55)
	cfg := (&LookaheadAnalyzer{}).withDefaults(f.m.L)
	if got := cfg.detectShortTransitions(f.m, 12); len(got) != 1 || got[0].EndFrame != 72 {
		t.Fatalf("max 12 must admit a 12-frame blend, got %+v", got)
	}
	for _, tr := range cfg.detectShortTransitions(f.m, 8) {
		if tr.EndFrame-tr.StartFrame-1 > 8 {
			t.Fatalf("max 8 reported a longer blend: %+v", tr)
		}
	}
}

// The cut rule applies to every frame with a full neighbourhood, including
// frame 3 and frame n−4.
func TestShort_HardCutNearClipEnds(t *testing.T) {
	f := newShortFixture(60, 0.25)
	f.fill(0, 3, scene(1))
	f.fill(3, 56, scene(2))
	f.fill(56, 60, scene(3))
	f.set(3, 0.98)
	f.set(56, 0.98)
	got := f.detect()
	if len(got) != 2 || got[0].StartFrame != 3 || got[1].StartFrame != 56 {
		t.Fatalf("want cuts at 3 and 56, got %+v", got)
	}
}

// Hard cuts do not depend on the staged analysis's score threshold.
func TestShort_HardCutIgnoresThreshold(t *testing.T) {
	f := newShortFixture(100, 0.25)
	f.fill(0, 50, scene(1))
	f.fill(50, 100, scene(2))
	f.set(50, 0.98)
	for _, th := range []float64{0.05, 0.5, 0.99} {
		cfg := (&LookaheadAnalyzer{HardCutThreshold: th}).withDefaults(f.m.L)
		wantOne(t, cfg.detectShortTransitions(f.m, 0), TransitionHardCut, 50, 50)
	}
}

// wipeMix returns a picture whose columns left of edge come from b and the
// rest from a (edge in cells, fractional cells are mixed).
func wipeMix(a, b []float32, edge float32) []float32 {
	t := make([]float32, len(a))
	for y := 0; y < ThumbH; y++ {
		for x := 0; x < ThumbW; x++ {
			c := edge - float32(x)
			if c < 0 {
				c = 0
			} else if c > 1 {
				c = 1
			}
			i := y*ThumbW + x
			t[i] = (1-c)*a[i] + c*b[i]
		}
	}
	return t
}

// A hard-edged wipe: cells switch one column band at a time, so the
// thumbnails do not travel in a straight line, but every cell changes once.
func TestShort_Wipe(t *testing.T) {
	f := newShortFixture(160, 0.25)
	a, b := scene(8), scene(9)
	f.fill(0, 60, a)
	for j := 60; j < 76; j++ {
		f.m.Thumb[j] = wipeMix(a, b, float32(j-59)*2)
		f.set(j, 0.6)
	}
	f.fill(76, 160, b)
	wantOne(t, f.detect(), TransitionDissolve, 59, 75)
}

// A stinger: wipe to a full-screen graphic, a frozen hold longer than
// MinSceneLen, wipe to the new scene. One span, from the last clean frame of
// the old scene to the first clean frame of the new one.
func TestShort_StingerIsOneSpan(t *testing.T) {
	f := newShortFixture(240, 0.25)
	a, g, b := scene(8), scene(10), scene(9)
	f.fill(0, 60, a)
	for j := 60; j < 76; j++ {
		f.m.Thumb[j] = wipeMix(a, g, float32(j-59)*2)
		f.set(j, 0.6)
	}
	f.fill(76, 116, g) // 40-frame hold
	for j := 116; j < 132; j++ {
		f.m.Thumb[j] = wipeMix(g, b, float32(j-115)*2)
		f.set(j, 0.6)
	}
	f.fill(132, 240, b)
	wantOne(t, f.detect(), TransitionDissolve, 59, 131)
}

// A full-screen graphic wiped off quickly at first and easing out at the
// end, over a new scene with some motion of its own. The pair of frames that
// captures the change most efficiently ends where the fast part does; the
// slowing part still uncovers picture and belongs to the wipe.
func TestShort_WipeTailSettles(t *testing.T) {
	f := newShortFixture(200, 0.25)
	g, b := scene(10), scene(9)
	f.fill(0, 60, g)
	for j := 60; j < 200; j++ {
		f.m.Thumb[j] = movingBy(b, j, 0.08)
	}
	// Columns uncovered by each frame: 4 a frame, then 2, 1, ½, ½.
	edges := []float32{4, 8, 12, 16, 20, 24, 28, 30, 31, 31.5, 32}
	for i, edge := range edges {
		j := 60 + i
		f.m.Thumb[j] = wipeMix(g, movingBy(b, j, 0.08), edge)
		f.set(j, 0.6)
	}
	wantOne(t, f.detect(), TransitionDissolve, 59, 70)
}

// The same two wipes around a hold that is a moving picture, not a frozen
// one, stay two transitions.
func TestShort_WipesAroundLiveSceneStaySeparate(t *testing.T) {
	const w = 200
	f := newShortFixture(240, 0.25)
	a, b := scene(8), scene(9)
	field := wide(11, w)
	g := window(field, w, 0)
	f.fill(0, 60, a)
	for j := 60; j < 76; j++ {
		f.m.Thumb[j] = wipeMix(a, g, float32(j-59)*2)
		f.set(j, 0.6)
	}
	for j := 76; j < 116; j++ { // live: drifts one cell every 4 frames
		f.m.Thumb[j] = window(field, w, (j-76)/4)
	}
	g2 := f.m.Thumb[115]
	for j := 116; j < 132; j++ {
		f.m.Thumb[j] = wipeMix(g2, b, float32(j-115)*2)
		f.set(j, 0.6)
	}
	f.fill(132, 240, b)
	if got := f.detect(); len(got) != 2 {
		t.Fatalf("want 2 transitions around a live scene, got %+v", got)
	}
}

// moving returns frame j of a shot in fast motion built on picture b: two
// fifths of its content wanders through unrelated textures, a third of the
// way to the next one per frame, so the thumbnail never settles.
func moving(b []float32, j int) []float32 { return movingBy(b, j, 0.4) }

// movingBy is moving with the wandering share of the content given.
func movingBy(b []float32, j int, share float32) []float32 {
	k := uint32(700 + j/3)
	return mix(b, mix(scene(k), scene(k+1), float32(j%3)/3), share)
}

// A dissolve from a calm shot into one in fast motion, between two views of
// similar content. The new shot's own movement bends the blend off the
// straight line, and its thumbnail moves as fast as the transition does, so
// the pooled flank speed shows no gain. The transition still stands out
// from the calm shot in speed and from the moving shot in prediction.
func TestShort_DissolveIntoMovingShot(t *testing.T) {
	f := newShortFixture(160, 0.30)
	a := scene(8)
	b := mix(a, scene(9), 0.15)
	f.fill(0, 60, a)
	for j := 60; j < 160; j++ {
		f.m.Thumb[j] = moving(b, j)
		f.set(j, 0.37)
	}
	for j := 60; j < 64; j++ {
		f.m.Thumb[j] = mix(a, moving(b, j), float32(j-59)/5)
		f.set(j, 0.62)
	}
	wantOne(t, f.detect(), TransitionDissolve, 59, 64)
}

// Motion starting within one shot: the thumbnail speeds up against the calm
// side, but prediction stays as bad afterwards as it became, so nothing
// separates the onset from the motion that follows.
func TestShort_MotionOnsetNotATransition(t *testing.T) {
	f := newShortFixture(160, 0.30)
	a := scene(8)
	f.fill(0, 60, a)
	for j := 60; j < 160; j++ {
		f.m.Thumb[j] = moving(a, j)
		f.set(j, 0.62)
	}
	if got := f.detect(); len(got) != 0 {
		t.Fatalf("want no transitions for a motion onset, got %+v", got)
	}
}

// Subject motion: an object crosses the picture and leaves. Cells change
// and change back, so the movement is large and the net change small.
func TestShort_ObjectCrossingNotATransition(t *testing.T) {
	f := newShortFixture(160, 0.25)
	a, obj := scene(8), scene(12)
	f.fill(0, 160, a)
	for j := 60; j < 80; j++ {
		t2 := append([]float32(nil), a...)
		x0 := (j - 60) * 2
		for y := 0; y < ThumbH; y++ {
			for x := x0; x < x0+12 && x < ThumbW; x++ {
				t2[y*ThumbW+x] = obj[y*ThumbW+x-x0] // texture moves with the object
			}
		}
		f.m.Thumb[j] = t2
		f.set(j, 0.7)
	}
	if got := f.detect(); len(got) != 0 {
		t.Fatalf("want no transitions for a crossing object, got %+v", got)
	}
}

// An overlay graphic fading in is a perfect blend of a small area — not a
// scene change.
func TestShort_OverlayFadeNotATransition(t *testing.T) {
	f := newShortFixture(120, 0.25)
	a := scene(1)
	b := append([]float32(nil), a...)
	for y := 12; y < 16; y++ {
		for x := 2; x < 10; x++ {
			b[y*ThumbW+x] = 255
		}
	}
	f.fill(0, 40, a)
	for j := 40; j < 46; j++ {
		f.m.Thumb[j] = mix(a, b, float32(j-39)/7)
		f.set(j, 0.5)
	}
	f.fill(46, 120, b)
	if got := f.detect(); len(got) != 0 {
		t.Fatalf("want no transitions for an overlay fade, got %+v", got)
	}
}

// A weak wipe is kept only where it adjoins a gradual transition that
// stands alone, and is merged into it.
func TestAttachWeakWipes(t *testing.T) {
	in := []SceneTransition{
		{Type: TransitionDissolve, StartFrame: 100, EndFrame: 110, weak: true}, // alone: dropped
		{Type: TransitionHardCut, StartFrame: 200, EndFrame: 200},
		{Type: TransitionDissolve, StartFrame: 202, EndFrame: 212, weak: true}, // next to a cut only: dropped
		{Type: TransitionDissolve, StartFrame: 300, EndFrame: 320, weak: true}, // lead-in of the stinger
		{Type: TransitionDissolve, StartFrame: 322, EndFrame: 330, weak: true}, // chained
		{Type: TransitionDissolve, StartFrame: 334, EndFrame: 350},
	}
	got := attachWeakWipes(in)
	if len(got) != 2 {
		t.Fatalf("want 2 transitions, got %+v", got)
	}
	if got[0].Type != TransitionHardCut || got[0].StartFrame != 200 {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].StartFrame != 300 || got[1].EndFrame != 350 || got[1].weak {
		t.Fatalf("got[1] = %+v", got[1])
	}
}

// Weak wipes whose bounds overlap each other still reach the transition
// they lead into, but not across a hard cut.
func TestAttachWeakWipes_OverlappingPieces(t *testing.T) {
	in := []SceneTransition{
		{Type: TransitionDissolve, StartFrame: 100, EndFrame: 115, weak: true},
		{Type: TransitionDissolve, StartFrame: 100, EndFrame: 106, weak: true}, // 18 frames from the wipe, inside the piece above
		{Type: TransitionDissolve, StartFrame: 124, EndFrame: 140},
		{Type: TransitionDissolve, StartFrame: 300, EndFrame: 310, weak: true},
		{Type: TransitionDissolve, StartFrame: 302, EndFrame: 306, weak: true},
		{Type: TransitionHardCut, StartFrame: 312, EndFrame: 312},
		{Type: TransitionDissolve, StartFrame: 316, EndFrame: 330},
	}
	got := attachWeakWipes(in)
	if len(got) != 3 {
		t.Fatalf("want 3 transitions, got %+v", got)
	}
	if got[0].StartFrame != 100 || got[0].EndFrame != 140 {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].Type != TransitionHardCut || got[2].StartFrame != 316 || got[2].EndFrame != 330 {
		t.Fatalf("weak wipes attached across a hard cut: %+v", got)
	}
}

// Cuts closer than MinSceneLen merge into one span: the material between
// them is transition, not a scene.
func TestEnforceMinSceneLen_MergesCloseCuts(t *testing.T) {
	in := []SceneTransition{
		{Type: TransitionHardCut, StartFrame: 100, EndFrame: 100, Score: 0.6},
		{Type: TransitionHardCut, StartFrame: 108, EndFrame: 108, Score: 0.7},
		{Type: TransitionHardCut, StartFrame: 300, EndFrame: 300, Score: 0.5},
	}
	got := enforceMinSceneLen(in, 15)
	if len(got) != 2 {
		t.Fatalf("want 2 transitions, got %+v", got)
	}
	if got[0].Type != TransitionDissolve || got[0].StartFrame != 99 || got[0].EndFrame != 108 {
		t.Fatalf("merged span: got %+v", got[0])
	}
	if got[1].Type != TransitionHardCut || got[1].StartFrame != 300 {
		t.Fatalf("isolated cut changed: %+v", got[1])
	}
}

// The short detector owns hard cuts; a legacy fade overlapping a short
// gradual detection passes on its type, and non-overlapping legacy
// dissolves are kept.
func TestMergeShortAndLegacy(t *testing.T) {
	short := []SceneTransition{
		{Type: TransitionHardCut, StartFrame: 50, EndFrame: 50},
		{Type: TransitionDissolve, StartFrame: 200, EndFrame: 230},
	}
	legacy := []SceneTransition{
		{Type: TransitionHardCut, StartFrame: 80, EndFrame: 80},
		{Type: TransitionFadeOut, StartFrame: 120, EndFrame: 231},
		{Type: TransitionDissolve, StartFrame: 400, EndFrame: 460},
	}
	got := mergeShortAndLegacy(short, legacy)
	if len(got) != 3 {
		t.Fatalf("want 3 transitions, got %+v", got)
	}
	if got[0].Type != TransitionHardCut || got[0].StartFrame != 50 {
		t.Fatalf("got[0] = %+v", got[0])
	}
	if got[1].Type != TransitionFadeOut || got[1].StartFrame != 200 || got[1].EndFrame != 230 {
		t.Fatalf("got[1] = %+v", got[1])
	}
	if got[2].Type != TransitionDissolve || got[2].StartFrame != 400 {
		t.Fatalf("got[2] = %+v", got[2])
	}
}

func TestLumaThumb_CellMeans(t *testing.T) {
	const w, h = 64, 36
	luma := make([]byte, w*h)
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			luma[y*w+x] = byte(x / 2 * 4)
		}
	}
	th := lumaThumb(luma, w, h, w)
	if len(th) != ThumbW*ThumbH {
		t.Fatalf("thumb len %d", len(th))
	}
	for cx := 0; cx < ThumbW; cx++ {
		if got, want := th[5*ThumbW+cx], float32(cx*4); got != want {
			t.Fatalf("cell %d: got %v want %v", cx, got, want)
		}
	}
}
