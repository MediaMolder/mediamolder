# Build & Packaging

*Extracted from the [Project Specification](spec_v3.md).*

- Official binaries for Linux/macOS/Windows (dynamically linked by default; static builds available with source distribution per the Licensing & LGPL Compliance section of the spec).
- Docker images with common FFmpeg library sets.
- pkg-config based build for custom libav* installations.
- Minimum FFmpeg version: 8.1. CI tests against FFmpeg 8.1.x (latest patch release).
- Clear documentation on how to vendor or point to a specific FFmpeg build.
