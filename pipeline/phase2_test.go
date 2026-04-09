package pipeline

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

// ---- P2.3: Error Policy Engine Tests ----

func TestErrorPolicyAbort(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dec", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "abort"}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	perr := &PipelineError{NodeID: "dec", Stage: "decode", Err: errTest, Transient: false}
	err := engine.HandleError(context.Background(), perr)
	if err == nil {
		t.Fatal("abort policy should return error")
	}
}

func TestErrorPolicySkip(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "filt", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "skip"}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	perr := &PipelineError{NodeID: "filt", Stage: "filter", Err: errTest}
	err := engine.HandleError(context.Background(), perr)
	if err != nil {
		t.Fatalf("skip policy should return nil, got %v", err)
	}

	// Verify event was emitted.
	select {
	case e := <-bus.Chan():
		// First event is ErrorEvent, second is ErrorPolicyApplied.
		_ = e
		select {
		case e2 := <-bus.Chan():
			epa, ok := e2.(ErrorPolicyApplied)
			if !ok {
				t.Fatalf("expected ErrorPolicyApplied, got %T", e2)
			}
			if epa.Policy != "skip" {
				t.Errorf("policy = %q, want skip", epa.Policy)
			}
		case <-time.After(time.Second):
			t.Fatal("timeout waiting for ErrorPolicyApplied event")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for ErrorEvent")
	}
}

func TestErrorPolicyRetryExhaustion(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "enc", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "retry", MaxRetries: 2}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	perr := &PipelineError{NodeID: "enc", Stage: "encode", Err: errTest, Transient: true}

	// First two retries should succeed (return nil after backoff).
	for i := 0; i < 2; i++ {
		err := engine.HandleError(context.Background(), perr)
		if err != nil {
			t.Fatalf("retry %d should succeed, got %v", i, err)
		}
	}

	// Third attempt should exhaust retries and return error.
	err := engine.HandleError(context.Background(), perr)
	if err == nil {
		t.Fatal("expected error after retry exhaustion")
	}
}

func TestErrorPolicyRetryContextCancelled(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dec", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "retry", MaxRetries: 5}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately.

	perr := &PipelineError{NodeID: "dec", Stage: "decode", Err: errTest, Transient: true}
	err := engine.HandleError(ctx, perr)
	if err != context.Canceled {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestErrorPolicyFallback(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "n1", Type: "filter", ErrorPolicy: &ErrorPolicy{
					Policy:       "fallback",
					FallbackNode: "n2",
				}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	perr := &PipelineError{NodeID: "n1", Stage: "filter", Err: errTest}
	err := engine.HandleError(context.Background(), perr)

	fb, ok := err.(*FallbackRequested)
	if !ok {
		t.Fatalf("expected FallbackRequested, got %T: %v", err, err)
	}
	if fb.FallbackNode != "n2" {
		t.Errorf("fallback node = %q, want n2", fb.FallbackNode)
	}
}

func TestErrorPolicyFallbackNoNode(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "n1", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "fallback"}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	perr := &PipelineError{NodeID: "n1", Stage: "filter", Err: errTest}
	err := engine.HandleError(context.Background(), perr)
	if err == nil {
		t.Fatal("fallback with no node should return error")
	}
}

func TestDefaultPolicyIsAbort(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "x", Type: "filter"}},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)

	p := engine.PolicyFor("x")
	if PolicyKind(p.Policy) != PolicyAbort {
		t.Errorf("default policy = %q, want abort", p.Policy)
	}
}

// ---- P2.6: Node Restart Tests ----

func TestNodeRestarterTransientRecovery(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dec", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "retry", MaxRetries: 3}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)
	restarter := NewNodeRestarter(engine, bus)

	calls := 0
	err := restarter.Wrap(context.Background(), "dec", func(ctx context.Context) error {
		calls++
		if calls < 3 {
			return &PipelineError{NodeID: "dec", Stage: "decode", Err: errTest, Transient: true}
		}
		return nil // succeed on third attempt
	})
	if err != nil {
		t.Fatalf("expected nil after recovery, got %v", err)
	}
	if calls != 3 {
		t.Errorf("function called %d times, want 3", calls)
	}
}

func TestNodeRestarterNonTransient(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dec", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "retry", MaxRetries: 5}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)
	restarter := NewNodeRestarter(engine, bus)

	err := restarter.Wrap(context.Background(), "dec", func(ctx context.Context) error {
		return &PipelineError{NodeID: "dec", Stage: "decode", Err: errTest, Transient: false}
	})
	if err == nil {
		t.Fatal("non-transient error should not be retried")
	}
}

func TestNodeRestarterContextCancel(t *testing.T) {
	cfg := &Config{
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dec", Type: "filter", ErrorPolicy: &ErrorPolicy{Policy: "retry", MaxRetries: 5}},
			},
		},
	}
	bus := NewEventBus(64)
	engine := NewErrorPolicyEngine(cfg, bus)
	restarter := NewNodeRestarter(engine, bus)

	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()

	err := restarter.Wrap(ctx, "dec", func(ctx context.Context) error {
		calls++
		return &PipelineError{NodeID: "dec", Stage: "decode", Err: errTest, Transient: true}
	})
	if err == nil {
		t.Fatal("expected error after context cancel")
	}
}

// ---- P2.7: Crash Report Tests ----

func TestCrashReportFromPanic(t *testing.T) {
	dir := t.TempDir()
	reporter := NewCrashReporter(dir, 10)
	reporter.SetPipelineID("test-pipeline")

	// Record some events.
	reporter.RecordEvent(StateChanged{From: StateNull, To: StateReady})
	reporter.RecordEvent(StateChanged{From: StateReady, To: StatePlaying})

	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)

	report := reporter.CaptureFromPanic(p, "test panic value")
	if report.PanicValue != "test panic value" {
		t.Errorf("panic value = %q, want test panic value", report.PanicValue)
	}
	if report.PipelineID != "test-pipeline" {
		t.Errorf("pipeline ID = %q, want test-pipeline", report.PipelineID)
	}
	if len(report.LastEvents) != 2 {
		t.Errorf("last events = %d, want 2", len(report.LastEvents))
	}
	if report.StackTrace == "" {
		t.Error("stack trace should not be empty")
	}

	// Write report.
	path, err := reporter.WriteReport(report)
	if err != nil {
		t.Fatalf("WriteReport: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("crash report file not found: %v", err)
	}
}

func TestCrashReportFromError(t *testing.T) {
	dir := t.TempDir()
	reporter := NewCrashReporter(dir, 5)

	report := reporter.CaptureFromError(nil, errTest)
	if report.Error != errTest.Error() {
		t.Errorf("error = %q, want %q", report.Error, errTest.Error())
	}
}

func TestCrashReportRingBuffer(t *testing.T) {
	reporter := NewCrashReporter("", 3)
	for i := 0; i < 5; i++ {
		reporter.RecordEvent(StreamStart{NodeID: string(rune('A' + i))})
	}
	events := reporter.lastEvents()
	if len(events) != 3 {
		t.Fatalf("ring size = %d, want 3", len(events))
	}
}

// ---- P2.8: Extended Event Bus Types Tests ----

func TestBufferingPercentEvent(t *testing.T) {
	bus := NewEventBus(16)
	bus.Post(BufferingPercent{NodeID: "dec", Percent: 0.75, Time: time.Now()})

	select {
	case e := <-bus.Chan():
		bp, ok := e.(BufferingPercent)
		if !ok {
			t.Fatalf("expected BufferingPercent, got %T", e)
		}
		if bp.Percent != 0.75 {
			t.Errorf("percent = %f, want 0.75", bp.Percent)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestClockLostEvent(t *testing.T) {
	bus := NewEventBus(16)
	bus.Post(ClockLost{Reason: "source disconnected", Time: time.Now()})

	select {
	case e := <-bus.Chan():
		cl, ok := e.(ClockLost)
		if !ok {
			t.Fatalf("expected ClockLost, got %T", e)
		}
		if cl.Reason != "source disconnected" {
			t.Errorf("reason = %q", cl.Reason)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestMetricsEmitter(t *testing.T) {
	registry := NewMetricsRegistry()
	bus := NewEventBus(64)
	state := StatePlaying

	emitter := NewMetricsEmitter(50*time.Millisecond, registry, bus, func() State { return state })
	emitter.Start()
	defer emitter.Stop()

	// Wait for at least one snapshot.
	select {
	case e := <-bus.Chan():
		mse, ok := e.(MetricsSnapshotEvent)
		if !ok {
			t.Fatalf("expected MetricsSnapshotEvent, got %T", e)
		}
		if mse.Snapshot.State != "PLAYING" {
			t.Errorf("state = %q, want PLAYING", mse.Snapshot.State)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for MetricsSnapshotEvent")
	}
}

// ---- P2.9: Security Tests ----

func TestURLSchemeAllowlist(t *testing.T) {
	sc := DefaultSecurityConfig()

	tests := []struct {
		url     string
		wantErr bool
	}{
		{"file:///tmp/test.mp4", false},
		{"http://example.com/stream", false},
		{"https://example.com/stream", false},
		{"rtmp://live.example.com/app/stream", false},
		{"rtsp://camera.local/stream", false},
		{"srt://relay.example.com:9000", false},
		{"ftp://evil.com/data", true},
		{"gopher://old.school", true},
		{"javascript:alert(1)", true},
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			err := sc.ValidateURL(tt.url)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateURL(%q) error = %v, wantErr %v", tt.url, err, tt.wantErr)
			}
		})
	}
}

func TestPathTraversalPrevention(t *testing.T) {
	dir := t.TempDir()
	sc := SecurityConfig{BaseDir: dir}

	// Create a legit file.
	legit := filepath.Join(dir, "ok.mp4")
	os.WriteFile(legit, []byte{}, 0o644)

	if err := sc.ValidateURL(legit); err != nil {
		t.Errorf("legit path rejected: %v", err)
	}

	// Traversal attempt.
	traversal := filepath.Join(dir, "..", "..", "etc", "passwd")
	if err := sc.ValidateURL(traversal); err == nil {
		t.Error("traversal path should be rejected")
	}
}

func TestDimensionLimits(t *testing.T) {
	sc := DefaultSecurityConfig()

	if err := sc.ValidateDimensions(1920, 1080); err != nil {
		t.Errorf("1080p should be allowed: %v", err)
	}
	if err := sc.ValidateDimensions(99999, 1080); err == nil {
		t.Error("excessive width should be rejected")
	}
	if err := sc.ValidateDimensions(1920, 99999); err == nil {
		t.Error("excessive height should be rejected")
	}
}

func TestStreamCountLimit(t *testing.T) {
	sc := DefaultSecurityConfig()

	if err := sc.ValidateStreamCount(10); err != nil {
		t.Errorf("10 streams should be OK: %v", err)
	}
	if err := sc.ValidateStreamCount(100); err == nil {
		t.Error("100 streams should be rejected (max 64)")
	}
}

func TestSecurityConfigValidation(t *testing.T) {
	sc := DefaultSecurityConfig()
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs: []Input{
			{ID: "in", URL: "http://example.com/video.mp4"},
		},
		Outputs: []Output{
			{ID: "out", URL: "file:///tmp/out.mp4"},
		},
	}
	if err := sc.ValidateConfig(cfg); err != nil {
		t.Errorf("valid config rejected: %v", err)
	}

	cfg.Inputs[0].URL = "ftp://evil.com/stream"
	if err := sc.ValidateConfig(cfg); err == nil {
		t.Error("ftp input should be rejected")
	}
}

func TestConcurrencyLimiter(t *testing.T) {
	limiter := NewConcurrencyLimiter(2)

	if !limiter.TryAcquire() {
		t.Fatal("first acquire should succeed")
	}
	if !limiter.TryAcquire() {
		t.Fatal("second acquire should succeed")
	}
	if limiter.TryAcquire() {
		t.Fatal("third acquire should fail (limit 2)")
	}

	limiter.Release()
	if !limiter.TryAcquire() {
		t.Fatal("acquire after release should succeed")
	}
}

func TestConcurrencyLimiterUnlimited(t *testing.T) {
	limiter := NewConcurrencyLimiter(0)
	for i := 0; i < 100; i++ {
		if !limiter.TryAcquire() {
			t.Fatalf("unlimited limiter should always succeed (failed at %d)", i)
		}
	}
}

// ---- P2.2: Observability Metrics Tests ----
// (Observability metrics are in the observability package; tested in observability/metrics_test.go)

// ---- P2.5: Dynamic Output Addition Tests ----

func TestAddOutputInvalidState(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)

	_, err := p.AddOutput(Output{ID: "out2", URL: "test.mp4"})
	if err == nil {
		t.Error("should fail in NULL state")
	}
}

func TestAddOutputDuplicateID(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)
	p.SetState(StatePaused)

	_, err := p.AddOutput(Output{ID: "out", URL: "test.mp4"})
	if err == nil {
		t.Error("duplicate ID should be rejected")
	}
	p.Close()
}

func TestAddOutputMissingURL(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)
	p.SetState(StatePaused)

	_, err := p.AddOutput(Output{ID: "out2"})
	if err == nil {
		t.Error("missing URL should be rejected")
	}
	p.Close()
}

// ---- P2.4: Reconfigure Tests ----

func TestReconfigureInvalidState(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)

	err := p.Reconfigure("filter1", map[string]any{"volume": "0.5"})
	if err == nil {
		t.Error("should fail in NULL state")
	}
}

func TestReconfigureNoGraphRunner(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.0",
		Inputs:        []Input{{ID: "in", URL: "dummy.mp4", Streams: []StreamSelect{{Type: "video"}}}},
		Outputs:       []Output{{ID: "out", URL: "dummy_out.mp4", CodecVideo: "libx264"}},
	}
	p, _ := NewPipeline(cfg)
	p.SetState(StatePaused)

	err := p.Reconfigure("filter1", map[string]any{"volume": "0.5"})
	if err == nil {
		t.Error("should fail with no graph runner")
	}
	p.Close()
}

// ---- Helpers ----

var errTest = &testError{msg: "test error"}

type testError struct{ msg string }

func (e *testError) Error() string { return e.msg }
