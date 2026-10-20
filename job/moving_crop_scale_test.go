// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

func TestMovingCropScale_TransformStreamInfo(t *testing.T) {
	proc, err := processors.Get("moving_crop_scale")
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Init(map[string]any{
		"output":    map[string]any{"width": 64, "height": 48},
		"keyframes": []any{map[string]any{"pts": 0, "x": 0, "y": 0, "w": 32, "h": 32}},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Close() })

	r := newGraphRunner(&Config{}, minimalPipeline())
	src := &graph.Node{ID: "src", Kind: graph.KindSource}
	node := &graph.Node{ID: "crop", Kind: graph.KindGoProcessor}
	enc := &graph.Node{ID: "enc", Kind: graph.KindEncoder}
	node.Inbound = []*graph.Edge{{
		From: src, To: node, FromPort: "v:0", Type: graph.PortVideo,
	}}
	// The encoder resolves the edge that leaves the processor. That is the
	// KindGoProcessor arm, which applies FrameTransformInfo.
	enc.Inbound = []*graph.Edge{{
		From: node, To: enc, Type: graph.PortVideo,
	}}
	r.sources["src"] = &sourceResources{
		streams: map[int]av.StreamInfo{
			3: {
				Type:              av.MediaTypeVideo,
				Width:             256,
				Height:            128,
				PixFmt:            av.PixFmtByName("yuv420p"),
				TimeBase:          [2]int{1, 1000},
				FrameRate:         [2]int{25, 1},
				SampleAspectRatio: [2]int{12, 11},
			},
		},
		trackOf: map[int]int{3: 0},
	}
	r.goProcessors["crop"] = proc

	si, err := r.resolveStreamInfo(nil, enc)
	if err != nil {
		t.Fatal(err)
	}
	if si.Width != 64 || si.Height != 48 {
		t.Fatalf("size %dx%d, want 64x48", si.Width, si.Height)
	}
	if si.PixFmt != av.PixFmtByName("yuv420p") || si.TimeBase != [2]int{1, 1000} || si.SampleAspectRatio != [2]int{12, 11} || si.FrameRate != [2]int{25, 1} {
		t.Fatalf("stream info changed beyond size: %+v", si)
	}
}

func TestHandleGoProcessor_TimeBaseDrivesMovingCropScale(t *testing.T) {
	proc, err := processors.Get("moving_crop_scale")
	if err != nil {
		t.Fatal(err)
	}
	// 0.5s in time base 1/25 is 12.5 ticks, which av_rescale_q rounds to 13.
	if err := proc.Init(map[string]any{
		"output": map[string]any{"width": 64, "height": 64},
		"keyframes": []any{
			map[string]any{"t": 0.0, "x": 0, "y": 0, "w": 64, "h": 64},
			map[string]any{"t": 0.5, "x": 20, "y": 0, "w": 64, "h": 64},
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Close() })

	r := newGraphRunner(&Config{}, minimalPipeline())
	r.goProcessors["p"] = proc
	r.goProcessorInputTB["p"] = [2]int{1, 25}
	node := &graph.Node{ID: "p", Kind: graph.KindGoProcessor}
	node.Inbound = []*graph.Edge{{To: node, Type: graph.PortVideo}}

	in := make(chan any, 2)
	out := make(chan any, 2)
	for _, pts := range []int64{12, 13} {
		f := av.NewTestFrame(t, 320, 64, av.PixFmtByName("yuv420p"))
		fillColumnLuma(f)
		f.SetPTS(pts)
		in <- f
	}
	close(in)
	if err := r.handleGoProcessor(context.Background(), node, []<-chan any{in}, []chan<- any{out}); err != nil {
		t.Fatal(err)
	}
	close(out)
	var got []*av.Frame
	for v := range out {
		got = append(got, v.(*av.Frame))
	}
	if len(got) != 2 {
		t.Fatalf("got %d frames", len(got))
	}
	defer got[0].Close()
	defer got[1].Close()
	// Tick 12 is between 0 and 13: x = 20 * 12/13 ≈ 18.46 → 18.
	if col := int(got[0].Plane(0)[0]); col != 18 || got[0].PTS() != 12 {
		t.Fatalf("tick 12 column %d pts %d, want 18", col, got[0].PTS())
	}
	if col := int(got[1].Plane(0)[0]); col != 20 || got[1].PTS() != 13 {
		t.Fatalf("tick 13 column %d pts %d, want 20", col, got[1].PTS())
	}
}

func TestHandleGoProcessor_RecordsSwsReinits(t *testing.T) {
	proc, err := processors.Get("moving_crop_scale")
	if err != nil {
		t.Fatal(err)
	}
	if err := proc.Init(map[string]any{
		"output": map[string]any{"width": 16, "height": 16},
		"keyframes": []any{
			map[string]any{"pts": 0, "x": 0, "y": 0, "w": 32, "h": 32},
			map[string]any{"pts": 10, "x": 0, "y": 0, "w": 48, "h": 48},
			map[string]any{"pts": 20, "x": 0, "y": 0, "w": 32, "h": 32},
		},
	}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { proc.Close() })

	pipe := minimalPipeline()
	r := newGraphRunner(&Config{}, pipe)
	r.goProcessors["p"] = proc
	node := &graph.Node{ID: "p", Kind: graph.KindGoProcessor}
	node.Inbound = []*graph.Edge{{To: node, Type: graph.PortVideo}}
	in := make(chan any, 3)
	out := make(chan any, 3)
	for _, pts := range []int64{0, 10, 20} {
		f := av.NewTestFrame(t, 80, 80, 0)
		f.SetPTS(pts)
		in <- f
	}
	close(in)
	if err := r.handleGoProcessor(context.Background(), node, []<-chan any{in}, []chan<- any{out}); err != nil {
		t.Fatal(err)
	}
	close(out)
	n := 0
	for v := range out {
		v.(*av.Frame).Close()
		n++
	}
	if n != 3 {
		t.Fatalf("got %d frames", n)
	}
	if got := pipe.Metrics().Node("p").Snapshot().SwsReinits; got != 0 {
		t.Fatalf("SwsReinits = %d, want 0", got)
	}
}

func fillColumnLuma(f *av.Frame) {
	data, ls := f.Plane(0), f.Linesize(0)
	for row := 0; row < f.PlaneHeight(0); row++ {
		for c := 0; c < f.PlaneWidth(0); c++ {
			data[row*ls+c] = byte(c)
		}
	}
}
