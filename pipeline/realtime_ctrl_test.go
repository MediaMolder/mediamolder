package pipeline

import (
	"context"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/pipeline/snap"
)

// ---------------------------------------------------------------------------
// ThreadBudget tests (pure, no CGO)
// ---------------------------------------------------------------------------

func TestThreadBudget_BasicAllocation(t *testing.T) {
	b := newThreadBudget(8)
	b.Seed("enc0", 2)

	if b.Current("enc0") != 2 {
		t.Fatalf("want 2, got %d", b.Current("enc0"))
	}
	if b.Available() != 8-2-2 { // total 8, reserved 2, allocated 2
		t.Fatalf("want 4 available, got %d", b.Available())
	}
}

func TestThreadBudget_CanAllocateEnforcesLimit(t *testing.T) {
	b := newThreadBudget(6)
	b.Seed("enc0", 2) // allocated=2, available=6-2-2=2

	// Requesting 4 would need 2 extra but only 2 free → ok (2+2=4 ≤ 6-2=4)
	if !b.CanAllocate("enc0", 4) {
		t.Fatal("should be able to grow to 4")
	}

	// Requesting 5 would need 3 extra but only 2 free → deny
	if b.CanAllocate("enc0", 5) {
		t.Fatal("should not be able to grow to 5")
	}
}

func TestThreadBudget_HWNodeAlwaysGranted(t *testing.T) {
	b := newThreadBudget(4) // very small CPU budget
	b.SetHWNode("enc_hw")
	// HW nodes bypass the CPU budget entirely.
	if !b.CanAllocate("enc_hw", 9999) {
		t.Fatal("HW node should always be granted allocation")
	}
}

func TestThreadBudget_AllocateUpdatesAvailable(t *testing.T) {
	b := newThreadBudget(8)
	b.Seed("enc0", 2)
	b.Allocate("enc0", 4)

	if b.Current("enc0") != 4 {
		t.Fatalf("want 4 after Allocate, got %d", b.Current("enc0"))
	}
	// available = 8 - 2(reserved) - 4(enc0) = 2
	if b.Available() != 2 {
		t.Fatalf("want 2 available, got %d", b.Available())
	}
}

// ---------------------------------------------------------------------------
// NodePerfTracker restart/drop API tests (pure, no CGO)
// ---------------------------------------------------------------------------

func TestNodePerfTracker_RequestPopRestartRoundTrip(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 30.0)

	tr.RequestRestart(8)

	count, ok := tr.PopRestartRequest()
	if !ok {
		t.Fatal("expected pending restart")
	}
	if count != 8 {
		t.Fatalf("want 8, got %d", count)
	}

	// Second Pop should return nothing.
	_, ok2 := tr.PopRestartRequest()
	if ok2 {
		t.Fatal("expected no pending restart after first pop")
	}
}

func TestNodePerfTracker_IncrementRestarts(t *testing.T) {
	tr := NewNodePerfTracker("enc0", 30.0)
	tr.IncrementRestarts()
	tr.IncrementRestarts()
	if tr.RestartCount() != 2 {
		t.Fatalf("want 2, got %d", tr.RestartCount())
	}
	s := tr.Snapshot()
	if s.ThreadRestarts != 2 {
		t.Fatalf("snapshot ThreadRestarts: want 2, got %d", s.ThreadRestarts)
	}
}

func TestNodePerfTracker_ShouldDrop_Disabled(t *testing.T) {
	tr := NewNodePerfTracker("src0", 0)
	// Period 0 means disabled — ShouldDrop must always return false.
	for i := 0; i < 100; i++ {
		if tr.ShouldDrop() {
			t.Fatal("ShouldDrop returned true with period=0")
		}
	}
}

func TestNodePerfTracker_ShouldDrop_Period(t *testing.T) {
	tr := NewNodePerfTracker("src0", 0)
	tr.SetDropPeriod(4)

	drops := 0
	for i := 0; i < 40; i++ {
		if tr.ShouldDrop() {
			drops++
		}
	}
	// Expected: 1 drop per 4 calls → ~10 drops in 40 calls.
	if drops < 8 || drops > 12 {
		t.Fatalf("expected ~10 drops in 40 calls with period=4, got %d", drops)
	}
}

// ---------------------------------------------------------------------------
// realtimeController.run() cancels promptly when context is done
// ---------------------------------------------------------------------------

func TestRealtimeController_RunCancelsOnContext(t *testing.T) {
	const nodeID = "enc2"
	reg := NewMetricsRegistry()
	tr := NewNodePerfTracker(nodeID, 30.0)
	reg.RegisterPerfTracker(nodeID, tr)

	n := &graph.Node{ID: nodeID, Kind: graph.KindEncoder}
	dag := &graph.Graph{
		Nodes: map[string]*graph.Node{nodeID: n},
		Order: []*graph.Node{n},
	}

	ctrl := &realtimeController{
		interval:        10 * time.Millisecond,
		budget:          newThreadBudget(8),
		registry:        reg,
		events:          NewEventBus(16),
		runner:          &graphRunner{trackers: make(map[string]*NodePerfTracker)},
		dag:             dag,
		prom:            nil,
		windowsSinceAdj: map[string]int{},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		ctrl.run(ctx)
		close(done)
	}()

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("control loop did not stop after context cancel")
	}
}

// ---------------------------------------------------------------------------
// snap.NodePerfSnapshot carries ThreadRestarts
// ---------------------------------------------------------------------------

func TestSnapNodePerfSnapshot_ThreadRestartsField(t *testing.T) {
	s := snap.NodePerfSnapshot{
		ThreadRestarts: 7,
	}
	if s.ThreadRestarts != 7 {
		t.Fatalf("want 7, got %d", s.ThreadRestarts)
	}
}
