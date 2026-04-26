# LGPL Compliance Guide

MediaMolder and the libav* libraries it links are licensed under **LGPL-2.1-or-later**.

> **Note:** MediaMolder links to the **libav\* libraries** (libavcodec,
> libavformat, libavfilter, libavutil, libswscale, libswresample) — not the
> FFmpeg CLI application. The FFmpeg CLI (`fftools/ffmpeg.c`) is a separate
> program and is not included in MediaMolder binaries.

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

## FFmpeg license escalation

The effective license of a MediaMolder binary depends on the FFmpeg libraries
it links against. FFmpeg's license varies based on its `./configure` flags:

| FFmpeg build flags               | FFmpeg license      | Combined binary license  |
|----------------------------------|---------------------|--------------------------|
| (default)                        | LGPL-2.1-or-later  | LGPL-2.1-or-later        |
| `--enable-version3`              | LGPL-3.0-or-later  | LGPL-3.0-or-later        |
| `--enable-gpl`                   | GPL-2.0-or-later   | GPL-2.0-or-later         |
| `--enable-gpl --enable-version3` | GPL-3.0-or-later   | GPL-3.0-or-later         |
| `--enable-nonfree`               | Non-redistributable | **Cannot be distributed** |

MediaMolder prints a license notice at startup when the linked FFmpeg was built
with `--enable-gpl` or `--enable-nonfree`. You can also check with
`mediamolder version`.

If you distribute a MediaMolder binary, verify the linked FFmpeg's license and
ensure your distribution complies with the most restrictive applicable license.

## Patent notice

The LGPL license allows you to use this copyrighted source code — it does not 
grant rights under any third-party patents.

MediaMolder links to FFmpeg's libav* libraries, which implement technologies that
may be covered by patents in some jurisdictions. This includes, but is not limited
to, audio and video codecs, container formats, multiplexing and demultiplexing
methods, filtering and signal processing algorithms, and hardware acceleration
interfaces. 

Anyone building, distributing, or deploying MediaMolder is responsible for
understanding the applicable patent laws in their jurisdiction and obtaining
licenses for any patented technologies they use.

This is the same obligation that applies to all software built on FFmpeg's
libraries, including FFmpeg itself.
