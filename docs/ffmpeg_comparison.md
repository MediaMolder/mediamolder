# Comparison with FFmpeg Core

## FFmpeg Core: Strengths & Weaknesses

**Strengths:**
- Extremely flexible: Any pipeline can be described via CLI arguments and filtergraph strings.
- Mature, battle-tested: Decades of real-world use, huge format/codec support.
- Scriptable: CLI is easy to automate in shell scripts, batch jobs, etc.
- Performance: Minimal overhead, direct C code, no extra abstraction.
- Community: Large user base, lots of documentation/examples.

**Weaknesses:**
- Usability: CLI syntax is cryptic, filtergraph strings are hard to write/maintain.
- Error handling: Poor diagnostics, errors are often cryptic or non-actionable.
- Observability: No built-in metrics, hard to introspect running jobs.
- Dynamic control: No way to pause/resume/reconfigure a running job.
- Embeddability: Not designed as a library; embedding is awkward and error-prone.
- Extensibility: Adding new high-level features (e.g., new control surfaces) is difficult.
- Configuration: No structured config, no schema validation, hard to version/validate.
- Threading: All concurrency is managed in C, not easily composable with other systems.

---

# MediaMolder Spec: How It Addresses These

**Strengths Preserved:**
- **Media capabilities:** By directly binding libav* libraries, all formats/codecs/filters/devices are preserved.
- **Performance:** Minimal Go-layer overhead (<5% target), direct C calls for heavy lifting.
- **Scriptability:** CLI tool (`mediamolder run ...`) and JSON config allow automation.
- **Extensibility:** Go API and modular design make it easier to add new features.

**Weaknesses Addressed:**
- **Usability:** Replaces CLI/filtergraph with structured, declarative JSON config and Go API. No string escaping or cryptic syntax.
- **Error handling:** Structured error model, per-node error policies, actionable diagnostics.
- **Observability:** Built-in metrics, OpenTelemetry/Prometheus support, structured events.
- **Dynamic control:** Go API allows live pause/resume/reconfigure/add-output (HTTP/gRPC planned for future).
- **Embeddability:** Designed as a Go library from the start, with idiomatic APIs.
- **Configuration:** Strict JSON schema, versioned configs, migration tooling.
- **Threading:** Uses Go goroutines/channels for pipeline concurrency, integrates with Go apps.

**Potential Weaknesses / Tradeoffs:**
- **CLI compatibility:** Not a drop-in replacement for all FFmpeg CLI scripts; requires migration to JSON or use of the CLI parser.
- **Initial control surface:** No HTTP/gRPC control in v1 (planned for future), so remote/dynamic control is Go-only at first.
- **Community/maturity:** New project, so lacks the vast ecosystem and documentation of FFmpeg.
- **Performance edge cases:** Some advanced C-level optimizations (e.g., custom thread pools, low-level scheduling) may not be fully matched in Go.
- **Advanced scripting:** Some esoteric FFmpeg CLI tricks (e.g., shell expansions, on-the-fly filtergraph edits) may not map 1:1.

---

## Summary

**MediaMolder’s design directly addresses nearly all of FFmpeg’s top-layer weaknesses while preserving its core strengths.** The only major tradeoffs are initial lack of remote control (Go API only at first), and the need for users to migrate from CLI to JSON/config-driven workflows. For most modern use cases—especially embedding, automation, and reliability—MediaMolder is a clear improvement. For ultra-advanced CLI scripting or edge-case performance tuning, FFmpeg’s native CLI may still have an edge.