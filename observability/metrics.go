package observability

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"sync"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Metrics holds registered Prometheus metrics for a pipeline.
type Metrics struct {
	Fps           *prometheus.GaugeVec
	BitrateBps    *prometheus.GaugeVec
	NodeLatency   *prometheus.HistogramVec
	NodeBufFill   *prometheus.GaugeVec
	ErrorsTotal   *prometheus.CounterVec
	FramesTotal   *prometheus.CounterVec
	BytesTotal    *prometheus.CounterVec
	PipelineState *prometheus.GaugeVec

	registry *prometheus.Registry
}

// NewMetrics creates and registers all pipeline metrics.
func NewMetrics(pipelineID string) *Metrics {
	reg := prometheus.NewRegistry()
	constLabels := prometheus.Labels{"pipeline": pipelineID}

	m := &Metrics{
		registry: reg,

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
	}

	reg.MustRegister(
		m.Fps, m.BitrateBps, m.NodeLatency, m.NodeBufFill,
		m.ErrorsTotal, m.FramesTotal, m.BytesTotal, m.PipelineState,
	)

	return m
}

// Registry returns the underlying Prometheus registry.
func (m *Metrics) Registry() *prometheus.Registry {
	return m.registry
}

// MetricsServer serves Prometheus metrics on an HTTP endpoint.
type MetricsServer struct {
	server *http.Server
	mu     sync.Mutex
}

// NewMetricsServer creates a new metrics server.
func NewMetricsServer(addr string, registry *prometheus.Registry) *MetricsServer {
	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.HandlerFor(registry, promhttp.HandlerOpts{}))
	mux.HandleFunc("/health", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("ok\n"))
	})

	return &MetricsServer{
		server: &http.Server{
			Addr:    addr,
			Handler: mux,
		},
	}
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
