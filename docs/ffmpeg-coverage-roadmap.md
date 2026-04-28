# FFmpeg Coverage Roadmap

> Strategy and gap analysis for the goal: **MediaMolder must be able to
> express, run, and GUI-author any job that an FFmpeg command line can
> express.**
>
> Companion to [roadmap.md](roadmap.md), which is phase-based. This document
> is **capability-based** — it enumerates the FFmpeg surface area, marks
> what MediaMolder covers today, and prioritises the gaps.

## 1. Provenance: the community-scripts probe

The new test suite [pipeline/community_scripts_test.go](../pipeline/community_scripts_test.go)
converts 20 of the 35 [NapoleonWils0n/ffmpeg-scripts](https://github.com/NapoleonWils0n/ffmpeg-scripts)
to MediaMolder JSON jobs under [testdata/community-scripts/](../testdata/community-scripts/).

Current state (commit `04f1a0c7`):

| Bucket                                     | Count |
|--------------------------------------------|------:|
| Converted and passing                      | 16 / 20 |
| Converted, skipped pending capability work | 4 / 20  |
| Not convertible at all today               | 15 / 35 |

Skipped (converted but blocked):

| Job                       | Blocker                                                                                |
|---------------------------|----------------------------------------------------------------------------------------|
| `06_fade_title`           | `drawtext` requires libfreetype build option (missing in current ffstatic build)       |
| `12_webp`                 | `libwebp_anim` encoder not in current build                                            |

Not convertible (15 / 35) and the underlying capability gap each one
exposes:

| Group of scripts                                                | Missing capability                                       |
|-----------------------------------------------------------------|----------------------------------------------------------|
| `audio-silence`                                                 | **Lavfi virtual-source inputs** (`anullsrc`, `color`, `sine`, `testsrc`, `smptebars`) |
| `chapter-add`, `chapter-extract`, `chapter-csv`                 | **Chapter metadata read/write** (and per-stream/global metadata IO) |
| `extract-frame`, `tile-thumbnails`, `scene-images`              | **Per-output frame-count limits** (`-frames:v N`, `-vframes`) |
| `scene-cut`, `scene-cut-to`                                     | **Multi-output batch from a cut-list** (one input, many time-windowed outputs in a single pass) |
| `waveform`                                                      | **Cross-media-type filters** (audio→video: `showwavespic`, `showspectrumpic`) wired through MediaMolder's encoder selection |
| `clip-time`, `scene-time`, `sexagesimal-time`                   | Pure CLI utilities — out of scope for the engine, in scope for a future `mediamolder util` subcommand |
| `ebu-meter`, `scopes`                                           | ffplay-based interactive viewers — out of scope for the engine, possibly in scope for the GUI |

These eight underlined capability gaps are the **first wave** of the
roadmap below. They are sampled from a tiny corner of FFmpeg usage
(20 hand-written shell scripts), so they should be treated as a
representative *minimum*, not as a complete list.

### 1.1 Second corpus: production-pattern command lines

A review of typical production FFmpeg usage (animated `drawtext`,
two-pass `loudnorm`, multi-resolution split-and-mux ABR, hardware
pipelines mixing CUDA + NPP + NVENC, HDR `zscale`/`tonemap`,
`minterpolate` slow-motion, RNNoise speech denoise, mixed
labelled/unlabelled `-filter_complex` outputs) surfaced a second wave
of gaps that the 35-script community corpus does not exercise:

| Gap                                                            | Note |
|----------------------------------------------------------------|------|
| **Filter expression engine** (`enable=between(t,2,8)`, `x=w-tw*t/5`, `t`, `n`, `frame`, `tw`, `th`, `text_w`, `text_h`) | Used by `drawtext`, `overlay`, `fade`, `crop`, `zoompan`, `geq`, … — `params` values today are stringified verbatim, but the GUI cannot help users author or validate them. |
| **Two-pass `loudnorm`**                                        | Pass 1 emits JSON to stderr (`measured_I`, `measured_TP`, `measured_LRA`, `measured_thresh`, `offset`); pass 2 must consume those values. Distinct from generic two-pass video encoding — needs an inter-pass shuttle in the engine. |
| **`setsar` / `setdar` and explicit SAR/DAR encoding**          | Required for square-pixel correction of legacy 720×480 / 720×576 sources and for HDR/Dolby SAR enforcement. |
| **Audio channel manipulation** (`pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix=weights=…`) | Multi-track downmix / upmix / language-track splitting. |
| **Speech denoise model files** (`arnndn=model=cb.rnnn`)        | Filter takes a model path; we have no fixture/asset story for filter-side data files. Same problem as YOLO model paths but for filters rather than processors. |
| **HDR tonemap via `zscale`**                                   | Depends on libzimg in the build (separate from libswscale). Build-tag and feature-detection story missing. |
| **`minterpolate` motion-compensated frame interpolation**      | Requires VFR awareness and fps targets — touches the same FrameRate/TimeBase plumbing as xfade. |
| **Lossless intermediate codecs** (FFV1, ProRes, DNxHD/DNxHR, HuffYUV) | Multi-pass editorial workflows (decode → lossless intermediate → grade → final encode). Encoder availability + container compatibility validation. |
| **`scale_npp` (NVIDIA NPP) vs `scale_cuda`**                   | Different libraries; some FFmpeg builds expose only one. The hardware-filter mapping table needs a per-filter availability probe. |
| **`-init_hw_device` and multi-device graphs**                  | Pipelines that touch two hardware contexts (e.g. CUDA decode → CPU filter → QSV encode) need explicit device declarations and `hwmap` between them. |
| **First-class raw-stream input** (`-f rawvideo -pix_fmt yuv420p -s 1920x1080 -r 30`) | Raw inputs are the dominant test fixture format and the canonical bug-report repro. They work via AVDict today; deserve a typed schema. |
| **`-fps_mode` (cfr/vfr/passthrough/drop) and legacy `-vsync`** | Already in §2.4 but worth reiterating: this is the single most common cause of A/V drift in user reports. |
| **`-async N` audio resync**                                    | Audio-side counterpart to `fps_mode`; resamples to maintain sync. |
| **Mixed labelled / unlabelled `-filter_complex` outputs**      | `avfilter_graph_parse_ptr` quirk: a graph that exposes both `[v]` and an unlabelled trailing pad needs careful pad-binding order. The `compat/ffcli` importer must normalise this. |
| **Long command lines, quoting, and shell escaping**            | Already partially addressed by the `,;'` fix in commit `04f1a0c7`; needs round-trip fuzzing. |

These are folded into the matrix in §2 and the phase plan in §3.

## 2. The full FFmpeg surface area

The CLI is a thin shell around four subsystems: **demux**, **filter**,
**encode**, **mux**. To match it, MediaMolder must cover every option
each subsystem accepts. The matrix below groups options by subsystem,
marks current coverage, and points at the relevant code.

Legend: ✅ supported · ⚠️ partial · ❌ missing

### 2.1 Inputs / demux

| FFmpeg flag(s)                                              | Status | MediaMolder location / note |
|-------------------------------------------------------------|:------:|------------------------------|
| `-i URL` (file, http, rtmp, rtsp, srt, pipe, device)        | ✅    | `pipeline/config.go` `Input.URL`; URL forwarded to `avformat_open_input` |
| `-ss`, `-t`, `-to` (input-side)                             | ✅    | `pipeline/timing.go` (matches FFmpeg conflict semantics) |
| `-itsoffset`                                                | ❌    | Per-input PTS offset; required for A/V re-sync workflows |
| `-stream_loop N`                                            | ❌    | Required for looping image-sequence intros, watermarks, etc. |
| `-readrate`, `-re` (real-time read)                         | ❌    | Live-stream restreaming, broadcast workflows |
| `-framerate`, `-r` (input override)                         | ⚠️    | Can be passed in `Input.Options` AVDict; not first-class and not validated |
| `-pix_fmt`, `-video_size`, `-pixel_format`                  | ⚠️    | Same: AVDict passthrough, no schema field |
| `-f` (force demuxer)                                        | ❌    | Required for headless raw streams (`-f rawvideo`, `-f s16le`, `-f lavfi`) |
| `-thread_queue_size`                                        | ⚠️    | AVDict only |
| `-accurate_seek` / `-noaccurate_seek` / `-seek_timestamp`   | ❌    | Required for frame-accurate trim of long-GOP sources |
| `-protocol_whitelist`                                       | ⚠️    | AVDict only; should be elevated for security review |
| Lavfi virtual sources (`-f lavfi -i color=…`)               | ❌    | No virtual-source input kind — see §3.1 |
| `image2` glob pattern (`-i 'frames/*.png'`)                 | ⚠️    | Works via AVDict if user knows the syntax; no schema affordance |
| `concat` demuxer (listfile)                                 | ❌    | Today users must build a concat **filter** graph; no `concat:` input kind |
| Device capture (`-f avfoundation`, `-f dshow`, `-f v4l2`)   | ⚠️    | Works through AVDict; no GUI palette, no probe |
| `-hwaccel`, `-hwaccel_device`, `-hwaccel_output_format`     | ⚠️    | Global only; not per-input |

### 2.2 Stream selection / mapping

| FFmpeg flag(s)                                | Status | Note |
|-----------------------------------------------|:------:|------|
| Default automatic stream selection            | ✅    | "best video + best audio" implied if user picks `track: 0` |
| `-map 0:v:0` / `-map 1:a:0` style             | ⚠️    | Modelled by `Input.Streams[].track`; covers the common case |
| Negative / optional mapping (`-map -0:s`, `-map 0:?`) | ❌ | Required for "include subtitle if present" patterns |
| Program selection (`-map p:1`)                | ❌    | MPEG-TS multi-program inputs |
| `-map_metadata`, `-map_chapters`              | ❌    | See §2.5 |
| `-vn` / `-an` / `-sn` / `-dn` per output      | ⚠️    | Implied by which edges connect — works but undocumented |
| Reuse of one decoded stream by N filters/outputs (`split`/`asplit`) | ✅ | Works via multi-output filters |
| Per-input `-map` of *attachment* streams      | ❌    | (see §2.5 attachments) |

This is the single **biggest** gap. FFmpeg's `-map` is universal
addressing of `(input, stream-type, stream-index, optional, negation)`
across **inputs and outputs**. MediaMolder's edge model can express any
mapping, but the schema does not yet have first-class options for
optional/negative mapping, program selection, or fall-back-when-absent
semantics.

### 2.3 Filtergraph

| Capability                                                  | Status | Note |
|-------------------------------------------------------------|:------:|------|
| Simple filter chains (1-in, 1-out)                          | ✅    | `pipeline/handlers.go` simple path |
| Complex filtergraphs (N-in, M-out)                          | ✅    | Same file, complex path via `avfilter_graph_parse_ptr` |
| Multi-input filters (`overlay`, `concat`, `hstack`, `amix`) | ✅    | Demonstrated by 09–10, 14 community scripts |
| Multi-output filters (`split`, `asplit`, `tile`)            | ✅    | |
| Source/virtual filters (`color=`, `testsrc=`, `anullsrc=`, `sine=`, `smptebars=`, `movie=`, `amovie=`) | ❌ | No node kind for "filter that has zero inputs" |
| Sink filters (`nullsink`, `nullaudiosink`)                  | ❌    | No node kind for "filter that has zero outputs" |
| Cross-media-type filters (`showwavespic`, `showspectrum*`, `concat=v=1:a=1`) | ⚠️ | The library supports them but the engine assumes 1 media-type per edge; needs explicit "this filter promotes audio→video" handling |
| Frame-rate / time-base advertised on `FilterPadConfig`      | ✅    | `FRNum/FRDen` added; `make_video_src_args` emits `frame_rate=N/D`; buffersink rate re-queried after each upstream filter. Unblocked `xfade`/`acrossfade` (13/14 community-scripts now pass). |
| `-filter_complex_threads`                                   | ❌    | Per-graph thread cap |
| `-filter_threads`                                           | ⚠️    | Set globally only |
| Filter quoting (`,`, `;`, `'` in values)                    | ✅    | Fixed in commit `04f1a0c7` (`pipeline/engine.go` `buildFilterSpec`) |
| Sidedata / per-frame metadata propagation                   | ⚠️    | Frames carry `AVFrame->metadata` but there is no JSON-side `metadata` filter wiring |
| Hardware filter auto-mapping (sw `scale` → `scale_cuda` etc.) | ❌  | User must spell the hardware filter name today |
| `hwupload`, `hwdownload`, `hwmap` filters                   | ⚠️    | Available via filter name, no first-class palette |
| **Filter expression engine** (`t`, `n`, `frame`, `tw`, `th`, `text_w`, `text_h`, `w`, `h`, `enable=between(t,2,8)`, arithmetic) | ⚠️ | Strings reach libavfilter intact; GUI has no expression authoring/validation; `compat/ffcli` does not normalise quoting |
| **Mixed labelled + unlabelled `-filter_complex` outputs**   | ⚠️    | Works when constructed manually; importer/exporter round-trip not yet tested |
| `setsar`, `setdar` (SAR/DAR overrides)                      | ⚠️    | Available as filter; not surfaced in encoder color metadata |
| `arnndn` (RNNoise) and other model-file filters             | ⚠️    | Filter runs if model path is correct; no fixture story for filter-side data files |
| `zscale` + `tonemap` (HDR)                                  | ⚠️    | Requires libzimg in build; no feature probe |
| `minterpolate` (motion-compensated interpolation)           | ⚠️    | Same FrameRate/TimeBase plumbing as xfade now landed; remaining work is exposing motion-estimation params via the inspector |
| Audio channel manipulation: `pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix=weights` | ⚠️ | Available as filters; GUI has no per-channel routing UI |

### 2.4 Encoders

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| Codec selection per output and per stream                         | ✅    | `Output.CodecVideo/Audio/Subtitle` plus explicit encoder nodes |
| Stream copy (`-c copy`)                                           | ✅    | Implicit `KindCopy` expansion |
| Codec-specific AVOptions (`preset`, `crf`, `tune`, `profile`, `level`, `g`, `bf`, `refs`, `x264-params`, `x265-params`, `aq-mode`, `tier`, …) | ✅ | Forwarded to `avcodec_open2` via `EncoderParams*` dict |
| Hardware encoders (NVENC, QSV, VAAPI, VideoToolbox, AMF)          | ✅    | Per `av/hwencode.go`; tested for NVENC |
| Two-pass encoding (`-pass 1/2 -passlogfile`)                      | ❌    | No `pass` field in schema; not implemented in runner |
| **Two-pass `loudnorm`** (measured-I/TP/LRA/thresh/offset feed-forward) | ❌ | Distinct inter-pass shuttle from video two-pass; pass 1 parses JSON from stderr, pass 2 consumes it. Frequently requested. |
| **Lossless intermediate codecs** (FFV1, ProRes, DNxHD/HR, HuffYUV) for editorial round-trips | ⚠️ | Encoders exist if FFmpeg compiled with them; no schema validation of codec ↔ container compatibility |
| `-fps_mode` (`cfr`/`vfr`/`passthrough`/`drop`) (formerly `-vsync`) | ❌    | Required for stable broadcast/HLS output; single biggest cause of A/V drift |
| `-async N` (audio resync via resampler)                            | ❌    | Audio-side counterpart to `fps_mode` |
| `-force_key_frames "expr:gte(t,n_forced*2)"` and chapter-driven IDR placement | ❌ | |
| Per-stream encoder options (`-b:v:0` ≠ `-b:v:1` in ABR ladders)   | ❌    | Schema has one `EncoderParamsVideo`, no per-stream override |
| Color metadata on encoder (`-color_range`, `-color_primaries`, `-color_trc`, `-colorspace`, `-chroma_sample_location`) | ⚠️ | Forwardable as AVOpts; not first-class, not validated |
| HDR10 mastering display + content light level metadata            | ❌    | Required for HDR delivery |
| Dolby Vision RPU passthrough                                      | ❌    | Required for premium HDR pipelines |
| `-aspect`                                                         | ❌    | Sample aspect ratio override on encoder |
| `-enc_time_base`                                                  | ❌    | |
| Field order (`-field_order`), interlaced encode                   | ❌    | Broadcast workflows |
| Encoder presets discovered from disk (`-fpre`, `-vpre`)           | ❌    | |

### 2.5 Muxers / outputs

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| Container auto-detect from extension                              | ✅    | |
| Force format (`-f mp4`)                                           | ✅    | `Output.Format` |
| Output-side `-ss`/`-t`/`-to`                                      | ⚠️    | Engine cuts at filter level today; output-side trim with `-copyts` semantics not separately modelled |
| `-shortest`                                                       | ❌    | "Stop when the shortest input ends" — common for music videos and overlays |
| `-fs N` (file size limit)                                         | ❌    | |
| `-frames:v N`, `-frames:a N`                                      | ✅    | `Output.MaxFramesVideo` / `Output.MaxFramesAudio`; sink drains channel and stops writing once limit is hit (post-encoder count, matches ffmpeg semantics for filter-dropping graphs) |
| `-metadata key=value`                                             | ❌    | Global metadata write |
| `-metadata:s:v:0 …` per-stream metadata                           | ❌    | Required for language tags, stereoscopic flags, comments |
| `-map_metadata`, `-map_chapters`                                  | ❌    | Required for `chapter-add`, `chapter-extract` |
| Chapter writing API                                               | ❌    | Even without map, no node can emit `AVChapter` entries |
| Attachments (fonts for ASS, cover art)                            | ❌    | |
| Cover art / thumbnail embed in MP4/M4A                            | ❌    | Common end-user request |
| Multiple outputs in one pipeline                                  | ✅    | Multiple `Output` entries |
| **`tee` muxer / single-pass multi-format** (`mp4 + hls + dash`)   | ❌    | The standard FFmpeg way to fan one encode into many containers without re-encoding |
| HLS muxer (`hls_time`, `hls_playlist_type`, EXT-X-MAP, byte-range, low-latency) | ⚠️ | Works via raw `Options` AVDict; no schema fields, no validation |
| DASH muxer (representations, adaptation sets, init segment)       | ⚠️    | Same |
| Segment muxer / fragmented MP4 (CMAF) / `movflags=+faststart`     | ⚠️    | `movflags` works; segment_* options require AVDict |
| `-muxdelay`, `-muxpreload`, `-copyts`, `-start_at_zero`, `-avoid_negative_ts` | ❌ | Required for accurate broadcast / HLS PTS handling |
| Bitstream filter chains on output (`-bsf:v "h264_mp4toannexb,h264_redundant_pps"`) | ⚠️ | Single BSF only |

### 2.6 Subtitles

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| Passthrough (text and bitmap)                                     | ✅    | Demonstrated by `18_subtitle_add` |
| Burn-in via `subtitles=` filter                                   | ✅    | (works once libass is available in the build) |
| Codec conversion (`mov_text` ↔ `srt` ↔ `ass` ↔ `webvtt`)          | ⚠️    | Works through encoder selection but not validated for incompatible pairs |
| Subtitle charset (`-sub_charenc`)                                 | ❌    | Required for non-UTF-8 SRT files |
| Forced / hearing-impaired flags                                   | ❌    | Per-stream metadata gap |
| Karaoke ASS effects, fontconfig integration                       | ⚠️    | Filter passes through; no GUI affordance |

### 2.7 Devices, networking, advanced

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| RTP / RTSP / RTMP / SRT / RIST / NDI input/output                 | ⚠️    | Works through libavformat URL handlers; no schema validation, no GUI |
| Screen capture (`avfoundation`, `gdigrab`, `x11grab`)             | ⚠️    | Same |
| Decklink SDI input/output                                         | ⚠️    | Same |
| `ffprobe` equivalence (stream summary)                            | ⚠️    | `/api/probe` exists but does not expose every probe field |
| Tee muxer (see §2.5)                                              | ❌    | |
| Dynamic per-frame metadata via ZMQ filter                         | ❌    | |
| **`-init_hw_device` (multi-device graphs)**                       | ❌    | Pipelines that bridge CUDA decode → CPU filter → QSV encode need named device declarations + `hwmap` between them |
| **`scale_npp` availability separate from `scale_cuda`**           | ⚠️    | Different libraries; needs per-filter availability probe at startup |
| **First-class raw-stream input** (`-f rawvideo -pix_fmt yuv420p -s 1920x1080 -r 30 -i raw.yuv`) | ⚠️ | Works via AVDict; the canonical bug-report fixture format deserves a typed schema |

### 2.8 Frontend GUI gaps (in addition to schema gaps)

The GUI cannot be more powerful than the schema. Once §2.1–§2.7 are
filled, the GUI also needs:

- A palette section for **virtual source nodes** (color/testsrc/sine/anullsrc).
- A **multi-output inspector** that shows all `Output` entries in one
  pane, with per-stream encoder tabs.
- **BSF chain editor** (sortable list, not single field).
- **Chapter / metadata editor** at the output level (table of `(start,
  end, title)` for chapters; key/value table for metadata, with
  per-stream tabs).
- **HLS / DASH / Tee output wizards** with structured fields
  (segment duration, playlist type, variants, …).
- **Hardware filter mapping indicator** that surfaces which filters
  will run on GPU once `hw_accel` is set, and warns when a software
  filter is forcing a hwdownload/hwupload round-trip.
- **Live FFmpeg-CLI import** (`compat/ffcli`) extended to cover every
  flag the schema gains, with a clear "unsupported flag" report.
- **Live FFmpeg-CLI export**: round-trip the JSON job back to a CLI
  command for users who want to copy/paste into ffmpeg directly. This
  is the strongest correctness signal we can ship.

## 3. Strategy

The strategy is **library-first, schema-second, GUI-third**, in that
order, for every capability:

1. Make sure the underlying libav* binding in `av/` exposes whatever
   AVOption / API is required.
2. Surface it as a schema field in `pipeline.Config` (and the matching
   `schema/v1.x.json`, `frontend/src/lib/jobTypes.ts`, and the
   `materializeImplicitEncoders` / `expandImplicitEncoders` adapters).
3. Add an inspector form in the GUI.

Two **horizontal** workstreams run alongside the per-feature work:

- **`compat/ffcli` round-trip tests.** Every new capability gets a
  test that takes an FFmpeg command line, runs it through `ffcli`, runs
  the resulting JSON job, and compares the output (size, hash, SSIM,
  PSNR, loudness, frame count) with what FFmpeg produces from the
  same command. This is the only way to *prove* parity at scale.
- **Capability registry.** A machine-readable inventory of every
  FFmpeg flag, with one of `{covered, partial, missing, out-of-scope}`
  and a link to the schema field that handles it. The GUI's
  "unsupported flag" report and the `ffcli` validator both consume
  this registry. Without it the matrix in §2 will rot.

### 3.1 Phase A — close the community-scripts gaps (sample-driven)

These are the smallest, best-scoped pieces of work and they unblock
real user scripts today. Targets at the end of this phase: 35/35
community scripts converted, 0 skipped on a fully-featured ffmpeg
build.

1. **Frame-rate metadata on `FilterPadConfig`.** Add `FrameRateNum`,
   `FrameRateDen`, `TimeBaseNum`, `TimeBaseDen` to the struct in `av/`
   and propagate them through complex filtergraph configuration.
   Unblocks `xfade`, `crossfade`, `interleave`, `framerate`,
   `setpts/setdar` with constant-FPS guarantees.
2. **`-frames:v N` / `-frames:a N`.** Add `MaxFramesVideo`,
   `MaxFramesAudio` to `Output`. Stop demuxing on the upstream side
   when any output reaches its limit. Unblocks `extract-frame`,
   `tile-thumbnails`, `scene-images`.
3. **Virtual-source input kind.** Add `Input.Kind ∈ {file, lavfi}`
   with a `lavfi_spec` field (e.g. `"color=black:size=1920x1080:rate=30"`).
   Backed by an `avfilter_graph_alloc` source-only graph.
   Unblocks `audio-silence`, padding tracks, test cards.
4. **Cross-media-type filter contract.** Add `output_media_type` to
   filter node definitions so the engine knows that
   `showwavespic` returns video even though it consumes audio. The
   GUI must then show the edge as `video` downstream of the filter.
   Unblocks `waveform`, `showspectrum*`, `concat=v=1:a=1`.
5. **Chapter and per-stream metadata IO.** Two new node kinds:
   - `KindMetadataReader` (for `-map_metadata`, `-map_chapters`)
   - `KindMetadataWriter` (for `-metadata`, chapter tables)
   Plus an `Output.Chapters []Chapter` and `Output.Metadata
   map[string]string` shorthand for the common case.
   Unblocks `chapter-add`, `chapter-extract`, `chapter-csv`.
6. **Filter expression engine surface.** `params` values are already
   strings, so libavfilter receives expressions intact today, but the
   GUI cannot author them safely. Ship: (a) an `expression: true` flag
   on `FilterOption` schema entries (mined from `av_opt_next` flag
   bits), (b) a syntax-highlighted expression input in the inspector,
   (c) a server-side `/api/filters/{name}/eval-expression?expr=…&t=0`
   smoke-test endpoint that asks libavfilter to parse the expression
   without running the graph, and (d) round-trip tests for the
   common expressions in the production corpus (`enable=between(t,a,b)`,
   scrolling `x=w-tw*t/k`, `frame_n%N`, `if(eq(n,0),…)`).
7. **Two-pass `loudnorm` shuttle.** A new pipeline-level orchestration
   primitive: declare a node `type: "loudnorm_2pass"` whose runner
   executes the graph once with `print_format=json`, captures the
   measured-I/TP/LRA/thresh/offset values from libavfilter's metadata
   side-data (we already plumb metadata to the event bus), and re-runs
   the graph with those values fed back into the filter. This is the
   minimum-viable pattern for any "measure, then process" workflow
   (also applies to `volumedetect`, `signalstats`, `astats`).

### 3.2 Phase B — the universal mapper

Make the schema express anything FFmpeg's `-map` can express. Concretely:

1. Promote `Input.Streams[].track` to a richer selector with
   `optional` and `negate` flags, plus `program_id`.
2. Add a top-level `mappings` array (or normalise it as a sugar over
   the existing typed-edges model) that lets users say
   `(input=0, type=v, index=0, optional=true) → out0`.
3. Integration tests: every example in the FFmpeg manual's
   "Stream selection" chapter, round-tripped through `ffcli`.

### 3.3 Phase C — output-side fidelity

Every production-grade ffmpeg pipeline depends on these:

1. `-shortest`, `-fs`, output-side `-ss`/`-t`/`-to` with `-copyts`
   semantics.
2. **Tee muxer support** as a first-class `Output.Kind = tee`. This is
   the biggest single feature; it changes the engine from "one mux per
   output" to "one encoded stream → many muxers".
3. Structured HLS / DASH / fragmented-MP4 / CMAF output (with a
   `Variants []EncoderSettings` for ABR ladders).
4. Two-pass encoding (`Encoder.Pass int`) for video; same scaffold
   reused by the Phase A loudnorm shuttle.
5. Per-stream encoder param overrides; per-stream metadata.
6. BSF chains.
7. Color metadata, HDR10 static metadata, Dolby Vision RPU
   passthrough — and validation that the chosen encoder/container can
   carry them.
8. **Lossless intermediate workflow validation.** Add an integration
   test that decodes BBB → re-encodes to FFV1/MKV → decodes the
   intermediate → re-encodes to H.264/MP4, and asserts that the round
   trip produces a file at least as good (PSNR, SSIM, audio loudness)
   as a single-pass encode. This is the canonical editorial pattern.
9. **`setsar`/`setdar` exposed as encoder-side `Output.SAR` /
   `Output.DAR` shorthand**, in addition to the filter.

### 3.4 Phase D — broadcast / live

For real-time and broadcast workflows:

1. `-readrate`/`-re`, `-stream_loop`, `-itsoffset`.
2. `-fps_mode`, `-async`, `-force_key_frames`, `-muxdelay`,
   `-muxpreload`, `-copyts`, `-start_at_zero`, `-avoid_negative_ts`.
3. RTP/RTSP/SRT/RIST/NDI as first-class input/output kinds, with
   schema validation and reconnect/backoff policies (we already have
   error policies — extend them to network errors).
4. Decklink SDI, ZMQ live filter parameter updates.
5. **Multi-device hardware graphs.** Implement `init_hw_device`
   semantics: a `hardware_devices: [{name, type, device}]` block at
   the top of the JSON pipeline plus `device:` selectors on encoder/
   filter nodes. Required for CUDA-decode → CPU-filter → QSV-encode
   pipelines and for fan-out across multiple GPUs.
6. **`scale_npp` vs `scale_cuda` per-filter availability probe.**
   Filter palette must reflect what the linked FFmpeg actually
   provides; today we only probe codecs.

### 3.5 Phase E — GUI completeness

GUI parity is gated on schema parity, but the work can run in parallel
once §3.1–§3.4 land:

1. Virtual-source palette.
2. Multi-output inspector with per-stream encoder tabs.
3. BSF chain editor.
4. Chapter / metadata editor.
5. HLS / DASH / tee output wizards.
6. Hardware-filter mapping indicator + multi-device picker.
7. Bidirectional FFmpeg-CLI conversion (existing `compat/ffcli`
   import + new export). The CLI export is the round-trip oracle for
   the entire schema and should be wired into the existing job-save
   flow as a "Show as ffmpeg command" panel.
8. **Filter expression authoring**: monospace input with `t`/`n`/`tw`/
   `th`/`text_w`/`text_h`/`w`/`h` autocomplete, live
   syntax-validation against the server-side `eval-expression`
   endpoint, and a small expression cookbook (scrolling text, fade
   gates, frame-stamp overlays).
9. **Audio channel-routing UI**: a bus/matrix view for `pan`,
   `channelsplit`, `channelmap`, `join`, `amerge`. Today's free-form
   `params` dict is unusable for non-trivial routing.
10. **Asset/model-file manager**: shared by the YOLO processor and by
    filters such as `arnndn`, `subtitles=…:fontsdir=…`. Pipelines
    should reference assets by symbolic name, with the GUI managing
    paths and the runtime resolving them from a search list.

### 3.6 Phase F — proof of universality

1. **FFmpeg manual conformance suite.** Every example command in
   `ffmpeg-doc.html` becomes a test case. Pass criterion: same
   container, same stream count, same per-stream codec, output bytes
   within tolerance, SSIM ≥ 0.99, audio loudness within ±0.5 LU.
2. **Production-pattern conformance suite.** A second corpus assembled
   from the production-pattern command lines catalogued in §1.1 —
   animated `drawtext`, two-pass `loudnorm`, multi-resolution
   split-and-mux ABR, full GPU pipelines, HDR `zscale`/`tonemap`,
   `minterpolate` slow-mo, RNNoise, mixed labelled/unlabelled
   `-filter_complex` outputs, raw-stream inputs, lossless
   intermediates. Same pass criteria as §3.6.1.
3. **Random-corpus fuzzer.** Generate random valid ffmpeg command
   lines from a grammar derived from the capability registry; run
   both ffmpeg and `mediamolder run --import-cli ...`; diff outputs.
4. **Capability registry coverage gate.** No PR can merge that adds
   a new flag to the registry without also adding either a schema
   field marking it `covered` or an explicit `out-of-scope` rationale.
5. **Quoting / escaping fuzzer.** Targeted at `pipeline/engine.go`
   `buildFilterSpec` and at the `compat/ffcli` lexer. Generate
   filter-graph specs containing every combination of `,`, `;`, `'`,
   `"`, `\`, `:`, `=`, `[`, `]`, and unicode whitespace; assert that
   `parse → spec → libavfilter → re-parse` is idempotent. The
   `04f1a0c7` quoting fix is the first known instance of a class of
   bugs that only fuzzing will surface.

## 4. Cross-cutting principles

- **Library-first.** Every feature has to be a real binding in `av/`,
  not a string forwarded blindly to AVDict. AVDict passthrough is a
  legitimate transitional state but it should be tracked in the
  registry as `partial` and burned down.
- **Schema-validated.** Every new schema field needs matching JSON
  Schema entries in `schema/v1.0.json` and `schema/v1.1.json`, and
  matching TS types in `frontend/src/lib/jobTypes.ts`. The existing
  `TestSchemaSyncWithGoStructs` is the enforcement mechanism.
- **GUI must be able to author it.** A schema field that the GUI
  cannot produce or edit is a schema field nobody will use. Every
  Phase-B/C/D feature has a GUI deliverable in Phase E.
- **The oracle is FFmpeg.** Round-trip CLI ↔ JSON conversion plus
  byte/SSIM/loudness comparison against ffmpeg is what defines
  "covered". Anything not in a regression test will regress.

## 5. Immediate next actions

In priority order, starting from the current `feature/front-end`
branch:

1. ~~Add `FrameRateNum/Den` and `TimeBaseNum/Den` to
   `av.FilterPadConfig` and propagate through
   `pipeline/handlers.go` complex-filtergraph wiring. Re-enable
   `13_xfade.json` and `14_crossfade_clips.json` in the
   community-scripts test (drop the `t.Skip` guard). Same plumbing
   unblocks `minterpolate` and `framerate`.~~ **Landed** —
   `FilterPadConfig` / `VideoFilterGraphConfig` gained `FRNum/FRDen`
   (TBNum/TBDen were already present), buffersink rate/timebase are
   re-queried after each upstream filter, and `handleFilter` now
   tolerates `EAGAIN`/`EOF` on `PushFrameAt` so xfade can close its
   second input mid-graph. 18 / 20 community scripts pass; only
   `06_fade_title` (drawtext/libfreetype) and `12_webp` (libwebp)
   remain skipped.
2. Build `Output.MaxFramesVideo` / `MaxFramesAudio` plumbing and
   write the missing five community scripts (`extract-frame`,
   `tile-thumbnails`, `scene-images`, `scene-cut`, `scene-cut-to`).
   ~~**Landed.**~~ `Output` gained `MaxFramesVideo` /
   `MaxFramesAudio`; `handleSink` enforces the cap per inbound
   channel by dropping packets after the limit while still draining
   the channel (so upstream encoders never deadlock). Counts
   post-encoder packets, matching ffmpeg's `-frames:v` semantics
   when filters like `select=gt(scene,…)` drop frames. Five new
   fixtures landed at `testdata/community-scripts/21_*.json`–
   `25_*.json`; image outputs use the `mjpeg` muxer (raw JPEG
   stream — sidesteps the `image2` `%d`-pattern requirement which
   conflicts with the muxer's atomic-rename of `out.tmp → out`).
3. Introduce `Input.Kind = "lavfi"` and write the `audio-silence`
   community script.
4. Land the **capability registry** (a YAML file under `compat/` that
   lists every ffmpeg flag with status + schema-pointer) and the
   first batch of `compat/ffcli` round-trip tests.
5. Open the schema-evolution work for chapter and per-stream
   metadata IO (`KindMetadataReader`, `KindMetadataWriter`,
   `Output.Chapters`).
6. Stand up the **production-pattern conformance corpus** stub at
   `testdata/production-patterns/` with the highest-leverage
   commands from §1.1 (animated `drawtext`, multi-resolution ABR,
   full GPU `scale_npp`+`h264_nvenc`, `zscale`+`tonemap`,
   `loudnorm` two-pass, raw-stream input). Even before each one
   runs, the failing `t.Skip` reason becomes machine-readable
   roadmap signal.
7. Add the **filter-expression `eval-expression` HTTP endpoint** so
   the GUI can validate `enable=`, `x=`, `y=`, `text=` expressions
   without running the full graph. Cheap to ship, immediately
   useful for `drawtext` / `overlay` / `crop` authoring.
8. Add the **quoting/escaping fuzzer** (Phase F.5) on top of
   `pipeline/engine.go` `buildFilterSpec` and the `compat/ffcli`
   lexer. The 04f1a0c7 fix proved this is real bug territory.

Each of these unblocks real user scripts today and pays down the
debt the §2 matrix is tracking.
