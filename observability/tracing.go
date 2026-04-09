// Package observability provides OpenTelemetry tracing and structured logging
// for MediaMolder pipelines.
package observability

import (
	"context"
	"fmt"
	"log/slog"

	"go.opentelemetry.io/otel"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp"
	"go.opentelemetry.io/otel/sdk/resource"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	semconv "go.opentelemetry.io/otel/semconv/v1.26.0"
	"go.opentelemetry.io/otel/trace"
)

const tracerName = "github.com/MediaMolder/MediaMolder"

// Config holds observability configuration.
type Config struct {
	ServiceName  string
	OTLPEndpoint string
}

// Provider manages tracing resources.
type Provider struct {
	tp     *sdktrace.TracerProvider
	tracer trace.Tracer
}

// Init initializes the OpenTelemetry tracing provider.
func Init(ctx context.Context, cfg Config) (*Provider, error) {
	if cfg.ServiceName == "" {
		cfg.ServiceName = "mediamolder"
	}

	res, err := resource.New(ctx,
		resource.WithAttributes(
			semconv.ServiceNameKey.String(cfg.ServiceName),
		),
	)
	if err != nil {
		return nil, fmt.Errorf("create otel resource: %w", err)
	}

	var tp *sdktrace.TracerProvider

	if cfg.OTLPEndpoint == "" {
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithResource(res),
		)
	} else {
		exp, err := otlptracehttp.New(ctx,
			otlptracehttp.WithEndpoint(cfg.OTLPEndpoint),
			otlptracehttp.WithInsecure(),
		)
		if err != nil {
			return nil, fmt.Errorf("create otlp exporter: %w", err)
		}
		tp = sdktrace.NewTracerProvider(
			sdktrace.WithBatcher(exp),
			sdktrace.WithResource(res),
		)
	}

	otel.SetTracerProvider(tp)

	return &Provider{
		tp:     tp,
		tracer: tp.Tracer(tracerName),
	}, nil
}

// Shutdown flushes and shuts down the tracing provider.
func (p *Provider) Shutdown(ctx context.Context) error {
	if p.tp != nil {
		return p.tp.Shutdown(ctx)
	}
	return nil
}

// Tracer returns the tracer for creating spans.
func (p *Provider) Tracer() trace.Tracer {
	return p.tracer
}

// StartPipelineSpan creates a root span for a pipeline run.
func (p *Provider) StartPipelineSpan(ctx context.Context, pipelineID string) (context.Context, trace.Span) {
	return p.tracer.Start(ctx, "pipeline.run",
		trace.WithAttributes(
			attribute.String("pipeline.id", pipelineID),
		),
	)
}

// StartNodeSpan creates a child span for a pipeline node.
func StartNodeSpan(ctx context.Context, nodeID, kind, codec, mediaType string) (context.Context, trace.Span) {
	tracer := otel.Tracer(tracerName)
	return tracer.Start(ctx, "pipeline.node."+kind,
		trace.WithAttributes(
			attribute.String("node.id", nodeID),
			attribute.String("node.kind", kind),
			attribute.String("node.codec", codec),
			attribute.String("node.media_type", mediaType),
		),
	)
}

// EndSpanOK ends a span with OK status.
func EndSpanOK(span trace.Span) {
	span.SetStatus(codes.Ok, "")
	span.End()
}

// EndSpanError ends a span with an error.
func EndSpanError(span trace.Span, err error) {
	span.RecordError(err)
	span.SetStatus(codes.Error, err.Error())
	span.End()
}

// SpanEvent records an event on the current span.
func SpanEvent(ctx context.Context, name string, attrs ...attribute.KeyValue) {
	span := trace.SpanFromContext(ctx)
	span.AddEvent(name, trace.WithAttributes(attrs...))
}

// Logger returns a slog logger with trace context attributes.
func Logger(ctx context.Context) *slog.Logger {
	span := trace.SpanFromContext(ctx)
	sc := span.SpanContext()
	logger := slog.Default()
	if sc.HasTraceID() {
		logger = logger.With(
			slog.String("trace_id", sc.TraceID().String()),
			slog.String("span_id", sc.SpanID().String()),
		)
	}
	return logger
}
