package av

import (
	"errors"
	"testing"
)

// Wave 7 #36b — av-layer source-only / sink-only filter graph constructors.

func TestNewSourceFilterGraph_VideoTestsrc(t *testing.T) {
	fg, err := NewSourceFilterGraph(SourceFilterGraphConfig{
		Outputs: []FilterOutputConfig{
			{Label: "out0", MediaType: MediaTypeVideo},
		},
		FilterSpec: "testsrc2=size=64x48:rate=10:duration=0.3[out0]",
	})
	if err != nil {
		t.Fatalf("NewSourceFilterGraph: %v", err)
	}
	defer fg.Close()

	if got := fg.NumInputs(); got != 0 {
		t.Errorf("NumInputs() = %d, want 0", got)
	}
	if got := fg.NumOutputs(); got != 1 {
		t.Errorf("NumOutputs() = %d, want 1", got)
	}

	// Pull at least one frame and confirm geometry.
	frame, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer frame.Close()

	count := 0
	for {
		err := fg.PullFrameAt(0, frame)
		if err == nil {
			count++
			if frame.Width() != 64 || frame.Height() != 48 {
				t.Errorf("frame %d: got %dx%d, want 64x48", count, frame.Width(), frame.Height())
			}
			frame.Unref()
			continue
		}
		if IsEAgain(err) {
			// testsrc2 with finite duration should reach EOF, not EAGAIN.
			t.Fatalf("unexpected EAGAIN from finite testsrc2 source")
		}
		if IsEOF(err) {
			break
		}
		t.Fatalf("PullFrameAt: %v", err)
	}
	if count == 0 {
		t.Fatalf("source graph produced zero frames")
	}
}

func TestNewSourceFilterGraph_AudioSine(t *testing.T) {
	fg, err := NewSourceFilterGraph(SourceFilterGraphConfig{
		Outputs: []FilterOutputConfig{
			{Label: "out0", MediaType: MediaTypeAudio},
		},
		FilterSpec: "sine=frequency=440:sample_rate=8000:duration=0.2[out0]",
	})
	if err != nil {
		t.Fatalf("NewSourceFilterGraph: %v", err)
	}
	defer fg.Close()

	frame, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer frame.Close()

	count := 0
	for {
		err := fg.PullFrameAt(0, frame)
		if err == nil {
			count++
			frame.Unref()
			continue
		}
		if IsEOF(err) {
			break
		}
		if IsEAgain(err) {
			t.Fatalf("unexpected EAGAIN from finite sine source")
		}
		t.Fatalf("PullFrameAt: %v", err)
	}
	if count == 0 {
		t.Fatalf("audio source graph produced zero frames")
	}
}

func TestNewSourceFilterGraph_RejectsEmpty(t *testing.T) {
	_, err := NewSourceFilterGraph(SourceFilterGraphConfig{
		FilterSpec: "testsrc2=duration=0.1[out0]",
	})
	if err == nil {
		t.Fatalf("expected error for zero outputs")
	}
	_, err = NewSourceFilterGraph(SourceFilterGraphConfig{
		Outputs: []FilterOutputConfig{{Label: "out0", MediaType: MediaTypeVideo}},
	})
	if err == nil {
		t.Fatalf("expected error for empty FilterSpec")
	}
}

func TestNewSinkFilterGraph_VideoNullsink(t *testing.T) {
	// Build a sink-only graph that swallows pushed video frames.
	fg, err := NewSinkFilterGraph(SinkFilterGraphConfig{
		Inputs: []FilterPadConfig{{
			Label:     "in0",
			MediaType: MediaTypeVideo,
			Width:     64, Height: 48,
			PixFmt: 0, // AV_PIX_FMT_YUV420P (constant 0 in libavutil)
			TBNum:  1, TBDen: 25,
			SARNum: 1, SARDen: 1,
		}},
		FilterSpec: "[in0]nullsink",
	})
	if err != nil {
		t.Fatalf("NewSinkFilterGraph: %v", err)
	}
	defer fg.Close()

	if got := fg.NumInputs(); got != 1 {
		t.Errorf("NumInputs() = %d, want 1", got)
	}
	if got := fg.NumOutputs(); got != 0 {
		t.Errorf("NumOutputs() = %d, want 0", got)
	}

	// Drive a single frame produced by a tiny source graph through the sink.
	srcGraph, err := NewSourceFilterGraph(SourceFilterGraphConfig{
		Outputs:    []FilterOutputConfig{{Label: "out0", MediaType: MediaTypeVideo}},
		FilterSpec: "testsrc2=size=64x48:rate=25:duration=0.04[out0]",
	})
	if err != nil {
		t.Fatalf("source graph: %v", err)
	}
	defer srcGraph.Close()

	frame, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer frame.Close()

	for {
		perr := srcGraph.PullFrameAt(0, frame)
		if perr == nil {
			if err := fg.PushFrameAt(0, frame); err != nil {
				t.Fatalf("PushFrameAt: %v", err)
			}
			frame.Unref()
			continue
		}
		if IsEOF(perr) {
			break
		}
		if IsEAgain(perr) {
			continue
		}
		t.Fatalf("source pull: %v", perr)
	}
	if err := fg.FlushAt(0); err != nil && !IsEOF(err) {
		t.Fatalf("FlushAt: %v", err)
	}
}

func TestNewSinkFilterGraph_RejectsEmpty(t *testing.T) {
	_, err := NewSinkFilterGraph(SinkFilterGraphConfig{
		FilterSpec: "[in0]nullsink",
	})
	if err == nil {
		t.Fatalf("expected error for zero inputs")
	}
	_, err = NewSinkFilterGraph(SinkFilterGraphConfig{
		Inputs: []FilterPadConfig{{
			Label: "in0", MediaType: MediaTypeVideo,
			Width: 64, Height: 48, TBNum: 1, TBDen: 25,
		}},
	})
	if err == nil {
		t.Fatalf("expected error for empty FilterSpec")
	}
}

func TestNewSinkFilterGraph_RejectsBadFilterSpec(t *testing.T) {
	_, err := NewSinkFilterGraph(SinkFilterGraphConfig{
		Inputs: []FilterPadConfig{{
			Label: "in0", MediaType: MediaTypeVideo,
			Width: 64, Height: 48, PixFmt: 0,
			TBNum: 1, TBDen: 25, SARNum: 1, SARDen: 1,
		}},
		// Output pad without a sink filter — leaves a dangling open output
		// after parse, which avfilter_graph_config rejects.
		FilterSpec: "[in0]copy[out0]",
	})
	if err == nil {
		t.Fatalf("expected error for sink graph with dangling open output")
	}
	// Sanity: error must surface as something we recognise.
	if errors.Is(err, nil) {
		t.Fatalf("nil-wrapped error: %v", err)
	}
}
