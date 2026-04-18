// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestFrameInfo_DefaultEmitsEveryFrame(t *testing.T) {
	p := &FrameInfo{}
	if err := p.Init(nil); err != nil {
		t.Fatal(err)
	}

	frame := av.NewTestFrame(t, 320, 240, 2) // RGB24
	defer frame.Close()

	ctx := ProcessorContext{FrameIndex: 0, PTS: 1000, StreamID: "v:0", MediaType: av.MediaTypeVideo}
	out, md, err := p.Process(frame, ctx)
	if err != nil {
		t.Fatal(err)
	}
	if out == nil {
		t.Fatal("expected frame passthrough")
	}
	if md == nil {
		t.Fatal("expected metadata on first frame")
	}
	if md.Custom["width"] != 320 {
		t.Fatalf("expected width 320, got %v", md.Custom["width"])
	}
	if md.Custom["height"] != 240 {
		t.Fatalf("expected height 240, got %v", md.Custom["height"])
	}
	if md.Custom["stream_id"] != "v:0" {
		t.Fatalf("expected stream_id v:0, got %v", md.Custom["stream_id"])
	}
}

func TestFrameInfo_LogEvery(t *testing.T) {
	p := &FrameInfo{}
	if err := p.Init(map[string]any{"log_every": float64(3)}); err != nil {
		t.Fatal(err)
	}

	frame := av.NewTestFrame(t, 640, 480, 2)
	defer frame.Close()

	ctx := ProcessorContext{MediaType: av.MediaTypeVideo}
	var emitted int
	for i := uint64(0); i < 9; i++ {
		ctx.FrameIndex = i
		_, md, err := p.Process(frame, ctx)
		if err != nil {
			t.Fatal(err)
		}
		if md != nil {
			emitted++
		}
	}
	if emitted != 3 {
		t.Fatalf("expected 3 metadata emissions (every 3rd frame), got %d", emitted)
	}
}

func TestFrameInfo_BadLogEveryType(t *testing.T) {
	p := &FrameInfo{}
	err := p.Init(map[string]any{"log_every": "five"})
	if err == nil {
		t.Fatal("expected error for non-numeric log_every")
	}
}

func TestFrameInfo_Registered(t *testing.T) {
	p, err := Get("frame_info")
	if err != nil {
		t.Fatalf("frame_info not registered: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
}
