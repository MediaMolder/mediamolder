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

// replaceFrameProc returns a new, half-width frame for every input and
// no metadata, or drops the frame when drop is set.
type replaceFrameProc struct {
	drop bool
	made []*av.Frame
}

func (p *replaceFrameProc) Init(map[string]any) error { return nil }

func (p *replaceFrameProc) Process(f *av.Frame, _ processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
	if p.drop {
		return nil, nil, nil
	}
	out, err := av.NewVideoFrame(f.Width()/2, f.Height(), f.PixFmt())
	if err != nil {
		return nil, nil, err
	}
	out.SetPTS(f.PTS())
	p.made = append(p.made, out)
	return out, nil, nil
}

func (p *replaceFrameProc) Close() error { return nil }

func runReplaceProc(t *testing.T, proc processors.Processor, n int) []*av.Frame {
	t.Helper()
	r := newGraphRunner(&Config{}, minimalPipeline())
	r.goProcessors["p"] = proc
	node := &graph.Node{ID: "p", Kind: graph.KindGoProcessor}
	node.Inbound = []*graph.Edge{{To: node, Type: graph.PortVideo}}

	in := make(chan any, n)
	out := make(chan any, n)
	for i := 0; i < n; i++ {
		f := av.NewTestFrame(t, 64, 48, 0)
		f.SetPTS(int64(i))
		in <- f
	}
	close(in)
	if err := r.handleGoProcessor(context.Background(), node, []<-chan any{in}, []chan<- any{out}); err != nil {
		t.Fatalf("handleGoProcessor: %v", err)
	}
	close(out)
	var got []*av.Frame
	for v := range out {
		got = append(got, v.(*av.Frame))
	}
	return got
}

// TestGoProcessor_ForwardsReplacementFrameWithoutMetadata is the regression
// test for a processor that returns a new frame and nil metadata. The runtime
// forwarded the original frame and leaked the new one, so a cropping
// processor's output never reached the encoder.
func TestGoProcessor_ForwardsReplacementFrameWithoutMetadata(t *testing.T) {
	proc := &replaceFrameProc{}
	got := runReplaceProc(t, proc, 3)
	if len(got) != 3 {
		t.Fatalf("got %d frames, want 3", len(got))
	}
	for i, f := range got {
		if f != proc.made[i] {
			t.Errorf("frame %d: runtime forwarded %dx%d, not the processor's %dx%d frame",
				i, f.Width(), f.Height(), proc.made[i].Width(), proc.made[i].Height())
		}
		f.Close()
	}
}

// TestGoProcessor_DropWithoutMetadata checks that a nil frame with nil
// metadata drops the frame and forwards nothing.
func TestGoProcessor_DropWithoutMetadata(t *testing.T) {
	got := runReplaceProc(t, &replaceFrameProc{drop: true}, 3)
	if len(got) != 0 {
		t.Fatalf("got %d frames, want 0", len(got))
	}
}
