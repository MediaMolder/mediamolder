module github.com/MediaMolder/MediaMolder

// Go version dependencies (last updated 2026-04-14)
//
// Dependency                Current    Max Go1.23  Max Go1.24  Notes
// ─────────────────────────────────────────────────────────────────────
// go.opentelemetry.io/otel  v1.38.0    v1.38.0     v1.41.0     v1.42+ requires Go 1.25
// golang.org/x/sync         v0.16.0    v0.16.0     ?           v0.20+ requires Go 1.25
// golang.org/x/net          v0.43.0    v0.43.0     v0.50.0     v0.44+ requires Go 1.24
// google.golang.org/grpc    v1.75.0    v1.75.1     v1.78.0     v1.76+ requires Go 1.24; vuln GO-2026-4762 fix in v1.79.3 (Go 1.24)
// prometheus/client_golang  v1.23.2    v1.23.2     ?           Go 1.23 compatible
// pgregory.net/rapid        v1.2.0     v1.2.0      v1.2.0      Go 1.23 compatible

go 1.23.0

require (
	github.com/prometheus/client_golang v1.23.2
	go.opentelemetry.io/otel v1.38.0
	go.opentelemetry.io/otel/exporters/otlp/otlptrace/otlptracehttp v1.38.0
	go.opentelemetry.io/otel/sdk v1.38.0
	go.opentelemetry.io/otel/trace v1.38.0
	golang.org/x/sync v0.16.0
	pgregory.net/rapid v1.2.0
)

require (
	github.com/beorn7/perks v1.0.1 // indirect
	github.com/cenkalti/backoff/v5 v5.0.3 // indirect
	github.com/cespare/xxhash/v2 v2.3.0 // indirect
	github.com/go-logr/logr v1.4.3 // indirect
	github.com/go-logr/stdr v1.2.2 // indirect
	github.com/google/uuid v1.6.0 // indirect
	github.com/grpc-ecosystem/grpc-gateway/v2 v2.27.2 // indirect
	github.com/munnerz/goautoneg v0.0.0-20191010083416-a7dc8b61c822 // indirect
	github.com/prometheus/client_model v0.6.2 // indirect
	github.com/prometheus/common v0.66.1 // indirect
	github.com/prometheus/procfs v0.16.1 // indirect
	go.opentelemetry.io/auto/sdk v1.1.0 // indirect
	go.opentelemetry.io/otel/exporters/otlp/otlptrace v1.38.0 // indirect
	go.opentelemetry.io/otel/metric v1.38.0 // indirect
	go.opentelemetry.io/proto/otlp v1.7.1 // indirect
	go.yaml.in/yaml/v2 v2.4.2 // indirect
	golang.org/x/net v0.43.0 // indirect
	golang.org/x/sys v0.35.0 // indirect
	golang.org/x/text v0.28.0 // indirect
	google.golang.org/genproto/googleapis/api v0.0.0-20250825161204-c5933d9347a5 // indirect
	google.golang.org/genproto/googleapis/rpc v0.0.0-20250825161204-c5933d9347a5 // indirect
	google.golang.org/grpc v1.75.0 // indirect
	google.golang.org/protobuf v1.36.8 // indirect
)
