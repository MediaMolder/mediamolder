// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestSceneChange_DefaultInit(t *testing.T) {
	p := &SceneChange{}
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}
	if p.threshold != 10.0 {
		t.Fatalf("expected default threshold 10.0, got %f", p.threshold)
	}
	if p.ptsThreshold != 0 {
		t.Fatalf("expected default pts_threshold 0, got %d", p.ptsThreshold)
	}
}

func TestSceneChange_FirstFrameNoMetadata(t *testing.T) {
	p := &SceneChange{}
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	frame := av.NewTestFrame(t, 64, 64, 2) // RGB24
	av.FillTestFrameRGB24(frame, 128, 128, 128)
	defer frame.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 0, MediaType: av.MediaTypeVideo}
	out, md, err := p.Process(frame, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected frame passthrough")
	}
	if md != nil {
		t.Fatal("expected nil metadata on first frame (no previous to compare)")
	}
}

func TestSceneChange_ContentChange(t *testing.T) {
	p := &SceneChange{}
	// Use a low threshold so the dark→bright transition triggers.
	// MAFD for (20,20,20) → (240,240,240) is very high (~86 on 0-100 scale).
	// But scdet score = min(mafd, |mafd - prevMAFD|). Since prevMAFD starts
	// at 0 and mafd is ~86, score = min(86, 86) = 86. Threshold of 5 should fire.
	if err := p.Init(map[string]any{"threshold": 5.0}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	// Frame 1: dark
	dark := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(dark, 20, 20, 20)
	defer dark.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 0, MediaType: av.MediaTypeVideo}
	_, _, err := p.Process(dark, ctx)
	if err != nil {
		t.Fatal(err)
	}

	// Frame 2: bright — should trigger scene change
	bright := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(bright, 240, 240, 240)
	defer bright.Close()

	ctx.FrameIndex = 1
	ctx.PTS = 1000
	_, md, err := p.Process(bright, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md == nil {
		t.Fatal("expected scene_change metadata for dark→bright transition")
	}
	if md.Custom["scene_change"] != true {
		t.Fatal("expected scene_change=true in custom metadata")
	}
	reasons, ok := md.Custom["reasons"].([]string)
	if !ok || len(reasons) == 0 {
		t.Fatal("expected reasons slice with content_change")
	}
	found := false
	for _, r := range reasons {
		if r == "content_change" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'content_change' in reasons, got %v", reasons)
	}
	// Verify the SAD score is included.
	if _, ok := md.Custom["score"]; !ok {
		t.Fatal("expected 'score' in custom metadata")
	}
}

func TestSceneChange_NoContentChange(t *testing.T) {
	p := &SceneChange{}
	// threshold=30 — the minor (128→130) change won't trigger.
	if err := p.Init(map[string]any{"threshold": 30.0}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	f1 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f1, 128, 128, 128)
	defer f1.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 0, MediaType: av.MediaTypeVideo}
	p.Process(f1, ctx)

	// Nearly same content — no scene change.
	f2 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f2, 130, 130, 130)
	defer f2.Close()

	ctx.FrameIndex = 1
	ctx.PTS = 1000
	_, md, err := p.Process(f2, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md != nil {
		t.Fatalf("expected no scene change, got %v", md.Custom)
	}
}

func TestSceneChange_PTSGap(t *testing.T) {
	p := &SceneChange{}
	if err := p.Init(map[string]any{
		"pts_threshold": float64(5000),
		"threshold":     0.0, // disable SAD check
	}); err != nil {
		t.Fatal(err)
	}
	defer p.Close()

	f1 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f1, 128, 128, 128)
	defer f1.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 0, MediaType: av.MediaTypeVideo}
	p.Process(f1, ctx)

	// Large PTS jump.
	f2 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f2, 128, 128, 128)
	defer f2.Close()

	ctx.FrameIndex = 1
	ctx.PTS = 90000 // big gap
	_, md, err := p.Process(f2, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if md == nil {
		t.Fatal("expected scene_change metadata for PTS gap")
	}
	reasons, ok := md.Custom["reasons"].([]string)
	if !ok {
		t.Fatal("expected reasons slice")
	}
	found := false
	for _, r := range reasons {
		if r == "pts_gap" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected 'pts_gap' in reasons, got %v", reasons)
	}
}

func TestSceneChange_BadThreshold(t *testing.T) {
	p := &SceneChange{}
	err := p.Init(map[string]any{"threshold": 150.0})
	if err == nil {
		t.Fatal("expected error for out-of-range threshold")
	}
}

func TestSceneChange_CloseReleasesFrame(t *testing.T) {
	p := &SceneChange{}
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}

	frame := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(frame, 128, 128, 128)
	defer frame.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 0, MediaType: av.MediaTypeVideo}
	p.Process(frame, ctx)

	if p.prevFrame == nil {
		t.Fatal("expected prevFrame to be set after first Process")
	}

	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if p.prevFrame != nil {
		t.Fatal("expected prevFrame to be nil after Close")
	}
}

func TestSceneChange_Registered(t *testing.T) {
	p, err := Get("scene_change")
	if err != nil {
		t.Fatalf("scene_change not registered: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
}

func TestFrameSceneScore_IdenticalFrames(t *testing.T) {
	f1 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f1, 100, 100, 100)
	defer f1.Close()

	f2 := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(f2, 100, 100, 100)
	defer f2.Close()

	score, err := av.FrameSceneScore(f1, f2)
	if err != nil {
		t.Fatal(err)
	}
	if score != 0 {
		t.Fatalf("expected score 0 for identical frames, got %f", score)
	}
}

func TestFrameSceneScore_OppositeFrames(t *testing.T) {
	black := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(black, 0, 0, 0)
	defer black.Close()

	white := av.NewTestFrame(t, 64, 64, 2)
	av.FillTestFrameRGB24(white, 255, 255, 255)
	defer white.Close()

	score, err := av.FrameSceneScore(black, white)
	if err != nil {
		t.Fatal(err)
	}
	// Black vs white should give a very high MAFD (close to 100).
	if score < 80 {
		t.Fatalf("expected high MAFD for black vs white, got %f", score)
	}
}
