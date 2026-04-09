package pipeline

import (
	"context"
	"runtime"
	"testing"
	"time"
)

// BenchmarkPipelineStartup measures the time to create a pipeline and transition
// from NULL → PLAYING, excluding actual data processing.
func BenchmarkPipelineStartup(b *testing.B) {
	raw := `{
		"schema_version": "1.0",
		"inputs": [{"id": "in", "url": "dummy.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
		"graph": {"nodes": [], "edges": []},
		"outputs": [{"id": "out", "url": "dummy_out.mp4"}]
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		eng, err := NewPipeline(cfg)
		if err != nil {
			b.Fatal(err)
		}
		_ = eng
	}
}

// BenchmarkParseConfig measures JSON config parsing throughput.
func BenchmarkParseConfig(b *testing.B) {
	raw := []byte(`{
		"schema_version": "1.0",
		"inputs": [
			{"id": "src1", "url": "input1.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}, {"input_index": 0, "type": "audio", "track": 0}]},
			{"id": "src2", "url": "input2.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}]}
		],
		"graph": {
			"nodes": [
				{"id": "scale", "type": "filter", "filter": "scale", "params": {"w": 1280, "h": 720}},
				{"id": "fps", "type": "filter", "filter": "fps", "params": {"fps": 30}},
				{"id": "loudnorm", "type": "filter", "filter": "loudnorm"}
			],
			"edges": [
				{"from": "src1:v:0", "to": "scale:default", "type": "video"},
				{"from": "scale:default", "to": "fps:default", "type": "video"},
				{"from": "fps:default", "to": "hd:v", "type": "video"},
				{"from": "src1:a:0", "to": "loudnorm:default", "type": "audio"},
				{"from": "loudnorm:default", "to": "hd:a", "type": "audio"}
			]
		},
		"outputs": [
			{"id": "hd", "url": "hd.mp4", "codec_video": "libx264", "codec_audio": "aac"},
			{"id": "sd", "url": "sd.mp4", "codec_video": "libx264", "codec_audio": "aac"}
		],
		"global_options": {"threads": 4, "hw_accel": "auto"}
	}`)
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_, err := ParseConfig(raw)
		if err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkStateTransition measures the cost of pipeline state transitions.
func BenchmarkStateTransition(b *testing.B) {
	raw := `{
		"schema_version": "1.0",
		"inputs": [{"id": "in", "url": "dummy.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
		"graph": {"nodes": [], "edges": []},
		"outputs": [{"id": "out", "url": "dummy_out.mp4"}]
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		b.Fatal(err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		b.Fatal(err)
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Cycle through states
		_ = eng.SetState(StateReady)
		_ = eng.SetState(StateNull)
	}
}

// TestStartupTime validates that pipeline creation completes within 500ms.
func TestStartupTime(t *testing.T) {
	raw := `{
		"schema_version": "1.0",
		"inputs": [{"id": "in", "url": "dummy.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
		"graph": {"nodes": [], "edges": []},
		"outputs": [{"id": "out", "url": "dummy_out.mp4"}]
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	start := time.Now()
	_, err = NewPipeline(cfg)
	elapsed := time.Since(start)

	if err != nil {
		t.Fatal(err)
	}
	if elapsed > 500*time.Millisecond {
		t.Errorf("startup time %v exceeds 500ms target", elapsed)
	}
	t.Logf("startup time: %v", elapsed)
}

// TestMemoryOverhead validates that pipeline creation does not allocate
// excessive memory (target < 50MB total).
func TestMemoryOverhead(t *testing.T) {
	raw := `{
		"schema_version": "1.0",
		"inputs": [{"id": "in", "url": "dummy.mp4", "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
		"graph": {"nodes": [], "edges": []},
		"outputs": [{"id": "out", "url": "dummy_out.mp4"}]
	}`
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	_ = eng

	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	allocBytes := after.TotalAlloc - before.TotalAlloc
	allocMB := float64(allocBytes) / (1024 * 1024)

	t.Logf("pipeline allocation: %.2f MB", allocMB)
	if allocMB > 50 {
		t.Errorf("memory overhead %.2f MB exceeds 50MB target", allocMB)
	}
}

// BenchmarkSchedulingLatency measures event bus throughput (proxy for per-frame overhead).
func BenchmarkSchedulingLatency(b *testing.B) {
	bus := NewEventBus(256)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Drain events
	go func() {
		for {
			select {
			case <-bus.Chan():
			case <-ctx.Done():
				return
			}
		}
	}()

	evt := StateChanged{From: StateNull, To: StateReady}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		bus.Post(evt)
	}
}
