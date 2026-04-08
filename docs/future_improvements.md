# Possible Future Improvements


- **HTTP/JSON REST Control Plane**: Expose the Go pipeline control API (`Pause`, `Resume`, `Seek`, `Reconfigure`, `AddOutput`, `GetMetrics`, `GetGraphSnapshot`) as an HTTP/JSON REST server for remote management of running pipelines. Includes bearer token authentication, localhost-only binding by default, and rate limiting.
- **gRPC Control API**: Add a gRPC server (with protobuf definitions) as an alternative to the HTTP/JSON control plane. gRPC offers strongly-typed service contracts, bidirectional streaming for real-time metrics/events, and efficient binary serialization — well-suited for high-frequency control interactions and language-agnostic client generation.
- **Language bindings**: Python, Rust, and other language bindings via FFI or gRPC client stubs.
- **YAML/TOML configuration support**: Round-tripping of JSON command payloads to/from YAML and TOML for users who prefer those formats.
- **Caps negotiation / auto-insertion of format converters**: Automatic detection of pixel format, sample rate, and channel layout mismatches between connected nodes, with auto-insertion of conversion nodes (scale, format, aresample) when formats don't match. Eliminates the need for users to manually wire converter nodes.
- **Bins / composite subgraphs**: Named, reusable subgraph templates that can be referenced in configs as a single node with exposed input/output ports (inspired by GStreamer's `GstBin`). Enables packaging common patterns (e.g., "decode bin", "transcode profile") for reuse across configs.
- **Pad probes (data interception points)**: Ability to attach inspection/modification callbacks to any edge in the graph to inspect, drop, or modify frames/packets in flight. Useful for debugging, watermarking, frame counting, and conditional routing.
- **Web UI / Dashboard**: A browser-based interface for pipeline monitoring and control built on top of the HTTP or gRPC APIs.
