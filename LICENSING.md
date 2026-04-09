# LGPL Compliance Guide

MediaMolder and the libav* libraries it links are licensed under **LGPL-2.1-or-later**.

## Dynamic linking (default — recommended)

The `mediamolder` binary dynamically links to the libav* shared libraries
(`libavcodec.so`, `libavformat.so`, etc.) at runtime. This satisfies LGPL
requirements: end users can replace the shared libraries with their own builds
without recompiling MediaMolder.

No additional obligations apply beyond attribution (preserving copyright notices).

## Static linking (`-tags=static`)

Supported for convenience (e.g., Docker images, single-binary deployment).

Because MediaMolder itself is also LGPL, static linking is *permissible* —
but you must provide the complete corresponding source for both MediaMolder
and the linked libav* libraries alongside any distributed binary.

Practically: ensure your distribution includes download links to both
`https://github.com/MediaMolder/MediaMolder` and `https://ffmpeg.org/releases/`.

## Third-party embedders

Any proprietary application embedding MediaMolder as a library must either:

1. **Dynamically link** — end users must be able to replace the libav* and
   MediaMolder shared libraries. The application must expose the necessary
   LGPL re-linking mechanism (e.g., provide object files or use a shared lib).
2. **Comply with LGPL re-linking requirements** — provide all necessary files so
   a user can relink the application with a modified version of MediaMolder.

Consult the full LGPL-2.1 text at https://www.gnu.org/licenses/lgpl-2.1.html
and seek legal advice if in doubt.
