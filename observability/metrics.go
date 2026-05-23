// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline/snap"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// stallDurationBuckets are the histogram buckets (seconds) for stall event
// durations. Range: 1 ms – 500 ms, which covers typical media pipeline stalls.
var stallDurationBuckets = []float64{0.001, 0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500}

// frameLatencyBuckets are the histogram buckets (seconds) for per-frame
// processing latency. Range: 1 ms – 500 ms.
var frameLatencyBuckets = []float64{0.001, 0.005, 0.010, 0.025, 0.050, 0.100, 0.250, 0.500}

// Metrics holds registered Prometheus metrics for a pipeline.
type Metrics struct {
	// Pre-existing metrics.
	Fps           *prometheus.GaugeVec
	BitrateBps    *prometheus.GaugeVec
	NodeLatency   *prometheus.HistogramVec
	NodeBufFill   *prometheus.GaugeVec
	ErrorsTotal   *prometheus.CounterVec
	FramesTotal   *prometheus.CounterVec
	BytesTotal    *prometheus.CounterVec
	PipelineState *prometheus.GaugeVec

	// Phase 3: per-node state fractions.
	NodeActiveFrac  *prometheus.GaugeVec
	NodeIdleFrac    *prometheus.GaugeVec
	NodeStalledFrac *prometheus.GaugeVec

	// Phase 3: per-node stall events.
	NodeStallDuration *prometheus.HistogramVec // distribution of per-snapshot max stall durations
	NodeStallCount    *prometheus.CounterVec   // total stall events

	// Phase 3: per-node throughput.
	NodeFPS        *prometheus.GaugeVec
	NodeFPSTarget  *prometheus.GaugeVec
	NodeFPSDeficit *prometheus.GaugeVec
	NodeQueueFill  *prometheus.GaugeVec

	// Phase 3: per-node thread visibility.
	NodeThreadsConfigured *prometheus.GaugeVec
	NodeThreadsBusy       *prometheus.GaugeVec
	NodeCPUCoresEstimated *prometheus.GaugeVec
	NodeThreadRestarts    *prometheus.CounterVec // populated in Phase 5

	// Phase 3: per-node frame latency.
	NodeFrameLatency *prometheus.HistogramVec

	// Phase 3: pipeline-level.
	PipelineFramesInFlight    *prometheus.GaugeVec // populated in Phase 4/5
	PipelineRealtimeSatisfied *prometheus.GaugeVec

	// Phase 6: adaptive preset stepping observability.
	NodePresetCurrent  *prometheus.GaugeVec   // ladder index (0 = slowest)
	NodePresetSwitches *prometheus.CounterVec // lifetime preset transitions
	PipelineFPSTarget  *prometheus.GaugeVec   // graph-level realtime target
	PipelineFPSActual  *prometheus.GaugeVec   // graph-level achieved fps
	RealtimeDecisions  *prometheus.CounterVec // labelled by action

	// Phase 7: per-output preroll buffer.
	OutputBufferDuration  *prometheus.GaugeVec   // labels: node
	OutputBufferTarget    *prometheus.GaugeVec   // labels: node
	OutputBufferState     *prometheus.GaugeVec   // labels: node,state
	OutputBufferEvictions *prometheus.CounterVec // labels: node,reason
	PipelineReady         *prometheus.GaugeVec   // 1 once all outputs ready

	registry *prometheus.Registry

	// mu guards prevStallCount and prevRestartCount for counter delta
	// computation. Update must be called from a single goroutine (e.g.
	// MetricsEmitter's tick goroutine); the lock is present for defensive
	// correctness.
	mu               sync.Mutex
	prevStallCount   map[string]int64
	prevRestartCount map[string]int64
}

// NewMetrics creates and registers all pipeline metrics.
func NewMetrics(pipelineID string) *Metrics {
	reg := prometheus.NewRegistry()
	constLabels := prometheus.Labels{"pipeline": pipelineID}

	m := &Metrics{
		registry:         reg,
		prevStallCount:   make(map[string]int64),
		prevRestartCount: make(map[string]int64),

		// --- pre-existing ---

		Fps: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_fps",
			Help:        "Current processing frame rate per node.",
			ConstLabels: constLabels,
		}, []string{"node", "media_type"}),

		BitrateBps: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_bitrate_bps",
			Help:        "Current bitrate in bits per second per node.",
			ConstLabels: constLabels,
		}, []string{"node", "media_type"}),

		NodeLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "mediamolder_node_latency_seconds",
			Help:        "Processing latency per frame in seconds.",
			ConstLabels: constLabels,
			Buckets:     prometheus.DefBuckets,
		}, []string{"node", "media_type"}),

		NodeBufFill: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_buffer_fill",
			Help:        "Current buffer fill level (0.0-1.0).",
			ConstLabels: constLabels,
		}, []string{"node"}),

		ErrorsTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_pipeline_errors_total",
			Help:        "Total number of errors per node.",
			ConstLabels: constLabels,
		}, []string{"node", "media_type"}),

		FramesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_pipeline_frames_total",
			Help:        "Total frames processed per node.",
			ConstLabels: constLabels,
		}, []string{"node", "media_type"}),

		BytesTotal: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_pipeline_bytes_total",
			Help:        "Total bytes processed per node.",
			ConstLabels: constLabels,
		}, []string{"node", "media_type"}),

		PipelineState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_state",
			Help:        "Current pipeline state (0=NULL, 1=READY, 2=PAUSED, 3=PLAYING).",
			ConstLabels: constLabels,
		}, []string{}),

		// --- Phase 3: per-node state fractions ---

		NodeActiveFrac: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_active_fraction",
			Help:        "Fraction of wall time the node spends in PROCESSING state.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeIdleFrac: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_idle_fraction",
			Help:        "Fraction of wall time the node spends in IDLE state (waiting for input).",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeStalledFrac: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_stalled_fraction",
			Help:        "Fraction of wall time the node spends in STALLED state (output channel full).",
			ConstLabels: constLabels,
		}, []string{"node"}),

		// --- Phase 3: per-node stall events ---

		NodeStallDuration: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "mediamolder_node_stall_duration_seconds",
			Help:        "Distribution of per-snapshot maximum stall event durations in seconds.",
			ConstLabels: constLabels,
			Buckets:     stallDurationBuckets,
		}, []string{"node"}),

		NodeStallCount: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_node_stall_count_total",
			Help:        "Total number of stall events (blocked on full output channel) per node.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		// --- Phase 3: per-node throughput ---

		NodeFPS: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_fps",
			Help:        "Windowed frames-per-second for this node.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeFPSTarget: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_fps_target",
			Help:        "Configured FPS target for this node; 0 if no target is set.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeFPSDeficit: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_fps_deficit",
			Help:        "fps_target − fps_actual; positive = falling behind; negative = headroom.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeQueueFill: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_queue_fill",
			Help:        "EWMA of the output channel fill fraction at send time (0.0–1.0).",
			ConstLabels: constLabels,
		}, []string{"node"}),

		// --- Phase 3: per-node thread visibility ---

		NodeThreadsConfigured: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_threads_configured",
			Help:        "libav configured thread count for this node.",
			ConstLabels: constLabels,
		}, []string{"node", "thread_mode"}),

		NodeThreadsBusy: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_threads_busy",
			Help:        "Live tasks in-flight from execute2/execute callback; omitted when unavailable.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeCPUCoresEstimated: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_cpu_cores_estimated",
			Help:        "threads_configured × active_fraction; upper-bound CPU core estimate.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		NodeThreadRestarts: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_node_thread_restarts_total",
			Help:        "Number of graceful node restarts for thread reallocation (Phase 5).",
			ConstLabels: constLabels,
		}, []string{"node"}),

		// --- Phase 3: per-node frame latency ---

		NodeFrameLatency: prometheus.NewHistogramVec(prometheus.HistogramOpts{
			Name:        "mediamolder_node_frame_latency_seconds",
			Help:        "Distribution of per-snapshot EWMA frame processing latency in seconds.",
			ConstLabels: constLabels,
			Buckets:     frameLatencyBuckets,
		}, []string{"node"}),

		// --- Phase 3: pipeline-level ---

		PipelineFramesInFlight: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_frames_in_flight",
			Help:        "Total frames buffered across all pipeline channels (populated in Phase 4).",
			ConstLabels: constLabels,
		}, []string{}),

		PipelineRealtimeSatisfied: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_realtime_satisfied",
			Help:        "1 if all nodes are meeting their fps_target, 0 otherwise.",
			ConstLabels: constLabels,
		}, []string{}),

		// --- Phase 6: adaptive preset stepping ---

		NodePresetCurrent: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_node_preset_current",
			Help:        "Current encoder preset, expressed as ladder index (0 = slowest).",
			ConstLabels: constLabels,
		}, []string{"node", "codec"}),

		NodePresetSwitches: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_node_preset_switches_total",
			Help:        "Number of completed adaptive preset transitions.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		PipelineFPSTarget: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_fps_target",
			Help:        "Graph-level real-time fps target (max of per-node targets).",
			ConstLabels: constLabels,
		}, []string{}),

		PipelineFPSActual: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_fps_actual",
			Help:        "Graph-level achieved fps (min of per-video-node fps).",
			ConstLabels: constLabels,
		}, []string{}),

		RealtimeDecisions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_realtime_decisions_total",
			Help:        "Real-time controller decisions, labelled by action.",
			ConstLabels: constLabels,
		}, []string{"action"}),

		// --- Phase 7: per-output preroll buffer ---

		OutputBufferDuration: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_output_buffer_duration_seconds",
			Help:        "Current preroll buffered duration per output, in seconds.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		OutputBufferTarget: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_output_buffer_target_seconds",
			Help:        "Per-output preroll fill target, in seconds.",
			ConstLabels: constLabels,
		}, []string{"node"}),

		OutputBufferState: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_output_buffer_state",
			Help:        "1 for the active preroll state of each output (FILLING/READY/READY_PARTIAL/STREAMING/DRAINING).",
			ConstLabels: constLabels,
		}, []string{"node", "state"}),

		OutputBufferEvictions: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name:        "mediamolder_output_buffer_evictions_total",
			Help:        "Packets dropped from the preroll buffer, labelled by reason (overflow).",
			ConstLabels: constLabels,
		}, []string{"node", "reason"}),

		PipelineReady: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name:        "mediamolder_pipeline_ready",
			Help:        "1 when every output sink has reached READY/READY_PARTIAL/STREAMING, 0 otherwise.",
			ConstLabels: constLabels,
		}, []string{}),
	}

	reg.MustRegister(
		// pre-existing
		m.Fps, m.BitrateBps, m.NodeLatency, m.NodeBufFill,
		m.ErrorsTotal, m.FramesTotal, m.BytesTotal, m.PipelineState,
		// Phase 3
		m.NodeActiveFrac, m.NodeIdleFrac, m.NodeStalledFrac,
		m.NodeStallDuration, m.NodeStallCount,
		m.NodeFPS, m.NodeFPSTarget, m.NodeFPSDeficit, m.NodeQueueFill,
		m.NodeThreadsConfigured, m.NodeThreadsBusy, m.NodeCPUCoresEstimated,
		m.NodeThreadRestarts,
		m.NodeFrameLatency,
		m.PipelineFramesInFlight, m.PipelineRealtimeSatisfied,
		// Phase 6
		m.NodePresetCurrent, m.NodePresetSwitches,
		m.PipelineFPSTarget, m.PipelineFPSActual, m.RealtimeDecisions,
		// Phase 7
		m.OutputBufferDuration, m.OutputBufferTarget, m.OutputBufferState,
		m.OutputBufferEvictions, m.PipelineReady,
	)

	return m
}

// Registry returns the underlying Prometheus registry.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// Update populates all Prometheus metrics from a pipeline MetricsSnapshot.
// It should be called from a single goroutine (e.g. the MetricsEmitter tick
// goroutine via SetSnapshotCallback). The internal mutex guards the
// delta-tracking state; all prometheus operations are concurrency-safe.
func (m *Metrics) Update(s snap.MetricsSnapshot) {
	// Pipeline-level state (prometheus methods are thread-safe; no lock needed).
	m.PipelineState.WithLabelValues().Set(pipelineStateFloat(s.State))

	m.mu.Lock()
	defer m.mu.Unlock()

	anyTarget := false
	allMet := true

	for _, p := range s.Perf {
		// Populate previously-registered-but-unpopulated placeholders.
		m.Fps.WithLabelValues(p.NodeID, "all").Set(p.FPS)
		m.NodeBufFill.WithLabelValues(p.NodeID).Set(p.QueueFillFrac)

		// State fractions.
		m.NodeActiveFrac.WithLabelValues(p.NodeID).Set(p.ActiveFrac)
		m.NodeIdleFrac.WithLabelValues(p.NodeID).Set(p.IdleFrac)
		m.NodeStalledFrac.WithLabelValues(p.NodeID).Set(p.StalledFrac)

		// Throughput.
		m.NodeFPS.WithLabelValues(p.NodeID).Set(p.FPS)
		m.NodeFPSTarget.WithLabelValues(p.NodeID).Set(p.FPSTarget)
		m.NodeFPSDeficit.WithLabelValues(p.NodeID).Set(p.FPSDeficit)
		m.NodeQueueFill.WithLabelValues(p.NodeID).Set(p.QueueFillFrac)

		// Thread visibility.
		m.NodeThreadsConfigured.WithLabelValues(p.NodeID, p.ThreadMode).Set(float64(p.ThreadsConfigured))
		if p.ThreadsBusy >= 0 {
			m.NodeThreadsBusy.WithLabelValues(p.NodeID).Set(float64(p.ThreadsBusy))
		}
		m.NodeCPUCoresEstimated.WithLabelValues(p.NodeID).Set(p.EstimatedCPUCores)

		// Frame latency: observe the EWMA as a histogram sample.
		// Individual per-frame observations require a direct callback; the EWMA
		// gives a distribution of the smoothed latency over the pipeline run.
		if p.FrameLatencyMean > 0 {
			secs := p.FrameLatencyMean.Seconds()
			m.NodeLatency.WithLabelValues(p.NodeID, "all").Observe(secs)
			m.NodeFrameLatency.WithLabelValues(p.NodeID).Observe(secs)
		}

		// Stall duration: observe the per-snapshot maximum as a histogram sample.
		if p.MaxStallDuration > 0 {
			m.NodeStallDuration.WithLabelValues(p.NodeID).Observe(p.MaxStallDuration.Seconds())
		}

		// Stall count counter: compute delta from previous snapshot.
		delta := p.StallCount - m.prevStallCount[p.NodeID]
		if delta > 0 {
			m.NodeStallCount.WithLabelValues(p.NodeID).Add(float64(delta))
		}
		m.prevStallCount[p.NodeID] = p.StallCount

		// Thread restart counter (Phase 5): delta since last snapshot.
		deltaRestarts := p.ThreadRestarts - m.prevRestartCount[p.NodeID]
		if deltaRestarts > 0 {
			m.NodeThreadRestarts.WithLabelValues(p.NodeID).Add(float64(deltaRestarts))
		}
		m.prevRestartCount[p.NodeID] = p.ThreadRestarts

		// Real-time satisfied flag.
		if p.FPSTarget > 0 {
			anyTarget = true
			if p.FPSDeficit > 0.5 {
				allMet = false
			}
		}

		// Phase 6: per-node current preset (ladder index).
		if p.CurrentPreset != "" && len(p.PresetLadder) > 0 && p.PresetIndex >= 0 {
			m.NodePresetCurrent.WithLabelValues(p.NodeID, p.CodecName).Set(float64(p.PresetIndex))
		}
	}

	if anyTarget {
		satisfied := 0.0
		if allMet {
			satisfied = 1.0
		}
		m.PipelineRealtimeSatisfied.WithLabelValues().Set(satisfied)
	}

	// Phase 6: graph-level fps_target / fps_actual + decision counter.
	if s.Realtime.Enabled {
		m.PipelineFPSTarget.WithLabelValues().Set(s.Realtime.FPSTarget)
		m.PipelineFPSActual.WithLabelValues().Set(s.Realtime.FPSActual)
		// Decisions are append-only in the snapshot; the controller
		// also bumps the counter directly when it emits them, so this
		// pass only updates the gauges.

		// Phase 7: per-output preroll gauges.
		ready := 0.0
		if s.Realtime.Ready {
			ready = 1.0
		}
		m.PipelineReady.WithLabelValues().Set(ready)
		for _, o := range s.Realtime.Outputs {
			m.OutputBufferDuration.WithLabelValues(o.NodeID).Set(o.BufferedDur.Seconds())
			m.OutputBufferTarget.WithLabelValues(o.NodeID).Set(o.TargetDur.Seconds())
			for _, st := range []string{"FILLING", "READY", "READY_PARTIAL", "STREAMING", "DRAINING"} {
				v := 0.0
				if st == o.State {
					v = 1.0
				}
				m.OutputBufferState.WithLabelValues(o.NodeID, st).Set(v)
			}
		}
	}
}

// pipelineStateFloat converts a pipeline State string to its numeric encoding:
// 0=null, 1=ready, 2=paused, 3=playing, -1=unknown.
func pipelineStateFloat(s string) float64 {
	switch s {
	case "null", "NULL", "Null":
		return 0
	case "ready", "READY", "Ready":
		return 1
	case "paused", "PAUSED", "Paused":
		return 2
	case "playing", "PLAYING", "Playing":
		return 3
	default:
		return -1
	}
}

// MetricsServer serves Prometheus metrics on an HTTP endpoint.
type MetricsServer struct {
	server *http.Server
	mux    *http.ServeMux
}

// NewMetricsServer creates a new metrics server.
func NewMetricsServer(addr string, registry *prometheus.Registry) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title>MediaMolder metrics</title></head><body>
<h1>MediaMolder metrics server</h1>
<ul>
<li><a href="/metrics">/metrics</a> — Prometheus scrape endpoint</li>
<li><a href="/health">/health</a> — Liveness probe</li>
<li><a href="/perf">/perf</a> — Pipeline metrics snapshot (JSON)</li>
<li><a href="/perf/stream">/perf/stream</a> — Live metrics stream (SSE)</li>
<li><a href="/realtime/status">/realtime/status</a> — Real-time controller status</li>
<li><a href="/realtime/snapshot">/realtime/snapshot</a> — Real-time controller snapshot</li>
</ul>
</body></html>
`))
	})

	return &MetricsServer{
		mux: mux,
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
}

// setCORSHeaders adds permissive CORS headers suitable for a local monitoring
// server. All origins are allowed because the metrics server is intended for
// localhost use only and does not expose sensitive data.
func setCORSHeaders(w http.ResponseWriter) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
}

// RegisterPerfHandler adds a /perf endpoint that serves a live
// pipeline.MetricsSnapshot encoded as JSON. snapFn is called on each request.
// Must be called before Start.
func (s *MetricsServer) RegisterPerfHandler(snapFn func() snap.MetricsSnapshot) {
	s.mux.HandleFunc("/perf", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		shot := snapFn()
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		if err := json.NewEncoder(w).Encode(shot); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
		}
	})
}

// RegisterPerfStreamHandler adds a /perf/stream Server-Sent Events endpoint
// that pushes []NodePerfSnapshot updates at ~2 Hz to browser clients.
// The stream sends a "data:" line with a JSON-encoded []NodePerfSnapshot every
// 500 ms; clients reconnect automatically via the EventSource API.
// Must be called before Start.
func (s *MetricsServer) RegisterPerfStreamHandler(snapFn func() snap.MetricsSnapshot) {
	s.mux.HandleFunc("/perf/stream", func(w http.ResponseWriter, r *http.Request) {
		setCORSHeaders(w)
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming not supported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream; charset=utf-8")
		w.Header().Set("Cache-Control", "no-cache")
		w.Header().Set("Connection", "keep-alive")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				shot := snapFn()
				perf := shot.Perf
				if perf == nil {
					perf = []snap.NodePerfSnapshot{}
				}
				data, err := json.Marshal(perf)
				if err != nil {
					continue
				}
				fmt.Fprintf(w, "data: %s\n\n", data)
				flusher.Flush()
			case <-r.Context().Done():
				return
			}
		}
	})
}

// Start begins serving metrics. It binds to the address and returns the
// actual listener address (useful when port 0 is specified).
func (s *MetricsServer) Start() (string, error) {
	ln, err := net.Listen("tcp", s.server.Addr)
	if err != nil {
		return "", fmt.Errorf("metrics listen: %w", err)
	}
	addr := ln.Addr().String()

	go func() {
		if err := s.server.Serve(ln); err != nil && err != http.ErrServerClosed {
			// Log but don't crash—metrics are optional.
			fmt.Printf("metrics server error: %v\n", err)
		}
	}()

	return addr, nil
}

// Shutdown gracefully stops the metrics server.
func (s *MetricsServer) Shutdown(ctx context.Context) error {
	return s.server.Shutdown(ctx)
}
