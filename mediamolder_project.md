### 1. Project Overview
MediaMolder is an independent, open-source media processing engine written in Go. It provides a new interface/orchestration layer, built on top of the same battle-tested libraries that power FFmpeg; replacing the FFmpeg command-line interface with a clean, declarative JSON defining each job. Mediamolder includes a cross-platform graphical user interface that runs in your web browser, letting you create, edit and run media graphs.
![MediaMolder User Interface](docs/images/ABR_x264.png)
It is not a wrapper around the ffmpeg binary; it is a ground-up redesign of the high-level engine that retains full media conversion capability through direct libav* bindings.

Version 1.x should be considered experimental.

### 2. Goals

- **Deliver a modern media processing engine** that improves orchestration, usability, execution, observability, and reliability.
  See [MediaMolder Advantages](private_local/mediamolder_advantages.md)
- **Provide a fully declarative, version-controlled configuration model** using JSON pipeline files and native Go structs.
- **Significantly improve usability with an intuitive graphical user interface**,
  including a live **Hardware Capabilities dialog** that shows every available
  GPU/accelerator backend, its supported encode/decode codecs grouped by media
  type, and any unavailable backends with diagnostic messages.
- **Preserve all of FFmpeg’s modern media capabilities** (formats, codecs, filters, devices, bitstream filters) via direct, zero-overhead libav* bindings.
	- Some older (obsolete) features will be deprecated.
- **Support custom processor nodes** inside any media processing pipeline — no rebuilds, no C code, no cryptic filtergraph hacks (see below).
- **Replace error-prone CLI strings and cryptic filtergraphs** with a single, structured, validated JSON defining each job. This...
  - Eliminates command-line escaping nightmares and length limits.
  - Enables programmatic construction, validation, storage in databases, diffing, and versioning.
  - Treats pipelines as data, not opaque strings — making them introspectable and machine-friendly.
- **Improve metadata generation, extraction, and propagation** throughout the processing graph.
- **Offer first-class runtime observability, dynamic control, and resilience**. This is especially important for live streams and long-running jobs (metrics, tracing, graceful restarts, etc.).
- **Achieve near-identical performance to native FFmpeg** — Go’s orchestration layer adds negligible overhead since all heavy lifting stays in the libav* libraries.
- **Make the engine trivially embeddable** as a lightweight Go library in any application.
- **Remain fully LGPL compliant** (see [LICENSING.md](LICENSING.md)).
- **Enable easy migration from the FFmpeg CLI** with a robust FFmpeg command to MediaMolder JSON converter and detailed migration guide (see [FFmpeg Migration Guide](docs/ffmpeg-migration-guide.md)).
- **Manage the project openly and fairly** to attract and retain like-minded contributors who value clean APIs, reliability, and developer experience.

### 3. Non-Goals
- Competing with the FFmpeg project. 
  - There is no intent or desire to fork or manage development of the media processing libraries that power FFmpeg. 
- Rewriting existing codec or filter processing libraries in Go.