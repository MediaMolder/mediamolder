package job

import (
	"testing"
	"time"
)

// TestNodeMetrics_OutputPTSPerStreamMin verifies that within a single
// sink, OutputPTS reported by Snapshot is the *minimum* of per-stream
// PTS values, not the maximum. Prior to the fix, a fast audio encoder
// could push outputPTSNs to end-of-file while a slow video encoder
// was still seconds in, making the GUI progress bar jump to 100%
// long before the run actually finished.
func TestNodeMetrics_OutputPTSPerStreamMin(t *testing.T) {
	m := &NodeMetrics{NodeID: "out0", StartTime: time.Now()}

	// Stream 0 (video) advances slowly: ~1 second of media written.
	m.AdvanceOutputPTSStream(0, 1*time.Second)
	// Stream 1 (audio) races ahead to ~10 minutes (full file length).
	m.AdvanceOutputPTSStream(1, 10*time.Minute)

	snap := m.Snapshot()
	if snap.OutputPTS != 1*time.Second {
		t.Fatalf("OutputPTS = %v; want 1s (min across streams)", snap.OutputPTS)
	}

	// Video catches up; min should track it monotonically forward.
	m.AdvanceOutputPTSStream(0, 5*time.Second)
	snap = m.Snapshot()
	if snap.OutputPTS != 5*time.Second {
		t.Fatalf("OutputPTS = %v; want 5s after video advanced", snap.OutputPTS)
	}

	// Out-of-order advance on a stream is ignored (monotonic per
	// stream).
	m.AdvanceOutputPTSStream(0, 2*time.Second)
	snap = m.Snapshot()
	if snap.OutputPTS != 5*time.Second {
		t.Fatalf("OutputPTS = %v; want 5s (per-stream monotonic)", snap.OutputPTS)
	}
}

// TestNodeMetrics_OutputPTSLegacyFallback verifies that callers that
// still use AdvanceOutputPTS (no stream index known) get the legacy
// max-aggregate behaviour reported via Snapshot.
func TestNodeMetrics_OutputPTSLegacyFallback(t *testing.T) {
	m := &NodeMetrics{NodeID: "out0", StartTime: time.Now()}
	m.AdvanceOutputPTS(2 * time.Second)
	m.AdvanceOutputPTS(5 * time.Second)
	m.AdvanceOutputPTS(3 * time.Second) // ignored (monotonic).
	if got := m.Snapshot().OutputPTS; got != 5*time.Second {
		t.Fatalf("OutputPTS = %v; want 5s", got)
	}
}
