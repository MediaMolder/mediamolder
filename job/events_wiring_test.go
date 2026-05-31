// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"sync"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
)

// stubEventProc is a Processor that also implements SegmentEventConsumer and
// AsyncMetadataProcessor. It records OnSegmentCompleted calls and
// the installed emitter.
type stubEventProc struct {
	mu       sync.Mutex
	events   []processors.SegmentEvent
	emit     processors.MetadataEmitter
	initDone bool
}

func (p *stubEventProc) Init(_ map[string]any) error {
	p.initDone = true
	return nil
}

func (p *stubEventProc) Process(f *av.Frame, _ processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
	return f, nil, nil
}

func (p *stubEventProc) Close() error { return nil }

func (p *stubEventProc) OnSegmentCompleted(_ context.Context, ev processors.SegmentEvent) {
	p.mu.Lock()
	p.events = append(p.events, ev)
	p.mu.Unlock()
}

func (p *stubEventProc) SetMetadataEmitter(emit processors.MetadataEmitter) {
	p.mu.Lock()
	p.emit = emit
	p.mu.Unlock()
}

const (
	stubProcName1 = "test_stub_proc_a"
	stubProcName2 = "test_stub_proc_b"
)

func init() {
	processors.Register(stubProcName1, func() processors.Processor { return &stubEventProc{} })
	processors.Register(stubProcName2, func() processors.Processor { return &stubEventProc{} })
}

// TestEventWiring_ChainedGoProcessors verifies that in an events-only pipeline
// (no AV edges), two chained go_processors are both registered in
// eventDrivenGoProcessors so that handleGoProcessor returns nil for each
// instead of failing with "expected 1 input, got 0".
//
// Pipeline topology:
//
//	in0 --[events]--> proc1 --[events]--> proc2
func TestEventWiring_ChainedGoProcessors(t *testing.T) {
	cfg := &Config{
		Inputs: []Input{{ID: "in0", URL: "fake://input"}},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "proc1", Type: "go_processor", Processor: stubProcName1},
				{ID: "proc2", Type: "go_processor", Processor: stubProcName2},
			},
			Edges: []EdgeDef{
				{From: "in0", To: "proc1", Type: "events"},
				{From: "proc1", To: "proc2", Type: "events"},
			},
		},
	}

	r := newGraphRunner(cfg, nil)

	// Manually populate goProcessors as the engine init loop would.
	proc1, err := processors.Get(stubProcName1)
	if err != nil {
		t.Fatalf("get proc1: %v", err)
	}
	if err := proc1.Init(nil); err != nil {
		t.Fatalf("init proc1: %v", err)
	}
	r.goProcessors["proc1"] = proc1

	proc2, err := processors.Get(stubProcName2)
	if err != nil {
		t.Fatalf("get proc2: %v", err)
	}
	if err := proc2.Init(nil); err != nil {
		t.Fatalf("init proc2: %v", err)
	}
	r.goProcessors["proc2"] = proc2

	// Run the events wiring section of runGraph.
	nodesByID := make(map[string]*NodeDef, len(cfg.Graph.Nodes))
	for i := range cfg.Graph.Nodes {
		nodesByID[cfg.Graph.Nodes[i].ID] = &cfg.Graph.Nodes[i]
	}
	for _, e := range cfg.Graph.Edges {
		if e.Type != "events" {
			continue
		}
		srcID := edgeNodeID(e.From)
		tgtID := edgeNodeID(e.To)
		tgt := nodesByID[tgtID]
		if tgt == nil || tgt.Type != "go_processor" {
			continue
		}
		if tgt.Processor == "metadata_file_writer" {
			continue
		}
		proc := r.goProcessors[tgtID]
		if proc == nil {
			continue
		}
		if consumer, ok := proc.(processors.SegmentEventConsumer); ok {
			r.segmentConsumers[srcID] = append(r.segmentConsumers[srcID], consumer)
			r.eventDrivenGoProcessors[tgtID] = struct{}{}
		}
		if _, ok := proc.(processors.AsyncMetadataProcessor); ok {
			r.eventDrivenGoProcessors[tgtID] = struct{}{}
		}
	}

	// Both processors should be in eventDrivenGoProcessors.
	for _, id := range []string{"proc1", "proc2"} {
		if _, ok := r.eventDrivenGoProcessors[id]; !ok {
			t.Errorf("node %q not in eventDrivenGoProcessors", id)
		}
	}

	// handleGoProcessor should return nil for both when ins is empty.
	fakeNode1 := &graph.Node{ID: "proc1", Kind: graph.KindGoProcessor}
	fakeNode2 := &graph.Node{ID: "proc2", Kind: graph.KindGoProcessor}

	if err := r.handleGoProcessor(context.Background(), fakeNode1, nil, nil); err != nil {
		t.Errorf("handleGoProcessor(proc1): unexpected error: %v", err)
	}
	if err := r.handleGoProcessor(context.Background(), fakeNode2, nil, nil); err != nil {
		t.Errorf("handleGoProcessor(proc2): unexpected error: %v", err)
	}
}
