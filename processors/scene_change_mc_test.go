// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"bytes"
	"encoding/csv"
	"strconv"
	"testing"

	"github.com/MediaMolder/MediaMolder/lookahead"
)

func TestTransitionMetadata_HardCut_NoDissolveFrames(t *testing.T) {
	p := &SceneChangeMC{frameRate: 25}
	tr := lookahead.SceneTransition{
		Type:       lookahead.TransitionHardCut,
		StartFrame: 10,
		EndFrame:   10,
		Score:      0.8,
	}
	m := p.transitionMetadata(tr)
	if _, ok := m.Custom["dissolve_frames"]; ok {
		t.Fatal("dissolve_frames must be absent for hard cuts")
	}
	if m.Custom["transition_type"] != "hard_cut" {
		t.Fatalf("unexpected transition_type: %v", m.Custom["transition_type"])
	}
}

func TestTransitionMetadata_Dissolve_HasDissolveFrames(t *testing.T) {
	p := &SceneChangeMC{frameRate: 25}
	// StartFrame=100: last unblended frame before dissolve
	// EndFrame=136: first unblended frame after dissolve (35 blended frames in between)
	// dissolve_frames = 136 - 100 + 1 = 37 (inclusive of both unblended endpoints)
	tr := lookahead.SceneTransition{
		Type:       lookahead.TransitionDissolve,
		StartFrame: 100,
		EndFrame:   136,
		Score:      0.61,
	}
	m := p.transitionMetadata(tr)
	v, ok := m.Custom["dissolve_frames"]
	if !ok {
		t.Fatal("dissolve_frames must be present for dissolves")
	}
	if v.(int) != 37 {
		t.Fatalf("dissolve_frames: want 37, got %v", v)
	}
	if m.Custom["frame_index"] != 100 {
		t.Fatalf("frame_index: want 100, got %v", m.Custom["frame_index"])
	}
}

func TestTransitionMetadata_Fade_HasDissolveFrames(t *testing.T) {
	p := &SceneChangeMC{frameRate: 25}
	// StartFrame=50: last unblended frame before fade
	// EndFrame=74: first unblended frame after fade (23 blended frames in between)
	// dissolve_frames = 74 - 50 + 1 = 25 (inclusive of both unblended endpoints)
	tr := lookahead.SceneTransition{
		Type:       lookahead.TransitionFadeOut,
		StartFrame: 50,
		EndFrame:   74,
		Score:      0.55,
	}
	m := p.transitionMetadata(tr)
	v, ok := m.Custom["dissolve_frames"]
	if !ok {
		t.Fatal("dissolve_frames must be present for fades")
	}
	if v.(int) != 25 {
		t.Fatalf("dissolve_frames: want 25, got %v", v)
	}
}

func TestSceneChangeMC_LookbackFrames(t *testing.T) {
	p := &SceneChangeMC{}
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}
	defer p.Close()
	// After removal of streaming mode, scene_change_mc is batch-only.
	// LookbackFrames always returns 0 (no low-latency emission for IDR forcing).
	if got := p.LookbackFrames(); got != 0 {
		t.Fatalf("LookbackFrames: want 0, got %d", got)
	}

	p2 := &SceneChangeMC{}
	if err := p2.Init(map[string]any{"min_scene_len": 30, "coarse_prediction_distance": 8}); err != nil {
		t.Fatal(err)
	}
	defer p2.Close()
	if got := p2.LookbackFrames(); got != 0 {
		t.Fatalf("LookbackFrames (with new params): want 0, got %d", got)
	}
}

func TestSceneChangeMC_ImplementsFrameLookahead(t *testing.T) {
	var _ interface{ LookbackFrames() int } = (*SceneChangeMC)(nil)
}

func TestWriteCostMatrixCSV_EnergyColumn(t *testing.T) {
	sc, err := lookahead.NewLookaheadScannerWithLags([]int{1})
	if err != nil {
		t.Fatal(err)
	}
	const w, h = 64, 48
	luma := make([]byte, w*h)
	for i := range luma {
		luma[i] = byte(i*37 + 11)
	}
	for i := 0; i < 3; i++ {
		if err := sc.AddFrame(luma, w, h, w); err != nil {
			t.Fatal(err)
		}
	}
	var buf bytes.Buffer
	p := &SceneChangeMC{scanner: sc}
	p.costCSV = csv.NewWriter(&buf)
	p.writeCostMatrixCSV()
	rows, err := csv.NewReader(&buf).ReadAll()
	if err != nil || len(rows) < 2 {
		t.Fatalf("csv: %v rows=%d", err, len(rows))
	}
	col := map[string]int{}
	for c, name := range rows[0] {
		col[name] = c
	}
	for _, name := range []string{"intra_cost", "avg_luma", "avg_u", "avg_v", "energy"} {
		if _, ok := col[name]; !ok {
			t.Fatalf("header missing %q: %v", name, rows[0])
		}
	}
	if v, err := strconv.ParseFloat(rows[1][col["energy"]], 64); err != nil || v <= 0 {
		t.Errorf("energy value %q, want > 0", rows[1][col["energy"]])
	}
}

// bgrMeanUV: grey has centred chroma; pure primaries land on the BT.601
// corners (blue: U max; red: V max). The mean over a half-blue/half-grey
// frame is the midpoint, because the transform is linear in the means.
func TestBGRMeanUV(t *testing.T) {
	grey := bytesRepeat([]byte{128, 128, 128}, 64)
	if u, v := bgrMeanUV(grey, 64); abs(int(u+0.5)-128) > 0 || abs(int(v+0.5)-128) > 0 {
		t.Errorf("grey: U=%.1f V=%.1f, want 128,128", u, v)
	}
	blue := bytesRepeat([]byte{255, 0, 0}, 64)
	if u, v := bgrMeanUV(blue, 64); u < 255 || abs(int(v+0.5)-107) > 1 {
		t.Errorf("blue: U=%.1f V=%.1f, want ~255.5,~107", u, v)
	}
	half := append(bytesRepeat([]byte{255, 0, 0}, 32), bytesRepeat([]byte{128, 128, 128}, 32)...)
	u, _ := bgrMeanUV(half, 64)
	if abs(int(u+0.5)-192) > 1 { // midpoint of 255.5 and 128
		t.Errorf("half blue/grey: U=%.1f, want ~191.8", u)
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func bytesRepeat(px []byte, n int) []byte {
	out := make([]byte, 0, len(px)*n)
	for i := 0; i < n; i++ {
		out = append(out, px...)
	}
	return out
}
