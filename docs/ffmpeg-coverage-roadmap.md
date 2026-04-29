# FFmpeg Coverage Roadmap

**MediaMolder must be able to express, run, and GUI-author any job that an FFmpeg command line can express.**

> Companion to [roadmap.md](roadmap.md), which is phase-based. This document is **capability-based** — it enumerates the FFmpeg surface area, marks what MediaMolder covers today, and prioritises the gaps.
>
> **Document structure:** §1–§2 form the *gap assessment* — §1 explains how gaps were identified (community-scripts corpus and production-pattern review, detailed below), §2 catalogues the full FFmpeg surface area. §3–§4 define the strategy. §5 is the completed *initial backlog* (all 8 items done). §6 is the *ongoing wave plan* with items marked done as they are completed.

## 1. Testing with a wide range of FFmpeg commands

To validate the universal capability of mediamolder, a set of 35 example FFmpeg commands was identified in a public Github repository [NapoleonWils0n/ffmpeg-scripts](https://github.com/NapoleonWils0n/ffmpeg-scripts). A new test suite [pipeline/community_scripts_test.go](../pipeline/community_scripts_test.go) was developed. This document tracks our progress in being able to support these FFmpeg jobs by converting them to MediaMolder JSON jobs under [testdata/community-scripts/](../testdata/community-scripts/).

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

| Group of scripts                                                | Capability gap                                           | Status |
|-----------------------------------------------------------------|----------------------------------------------------------|--------|
| `audio-silence`                                                 | **Lavfi virtual-source inputs** (`anullsrc`, `color`, `sine`, `testsrc`, `smptebars`) | ✅ Done — `Input.Kind = "lavfi"` (§5 #3) |
| `chapter-add`, `chapter-extract`, `chapter-csv`                 | **Chapter metadata read/write** (and per-stream/global metadata IO) | ✅ Done — `Output.Chapters` / `Output.Metadata` shorthand (§5 #5) + `metadata_reader`/`metadata_writer` graph nodes (Wave 2 #11) |
| `extract-frame`, `tile-thumbnails`, `scene-images`              | **Per-output frame-count limits** (`-frames:v N`, `-vframes`) | ✅ Done — `Output.MaxFramesVideo` / `MaxFramesAudio` (§5 #2) |
| `scene-cut`, `scene-cut-to`                                     | **Multi-output batch from a cut-list** (one input, many time-windowed outputs in a single pass) | ✅ Done — resolved via `MaxFrames` + `select=gt(scene,…)` filter; fixtures `21_*`–`25_*` (§5 #2) |
| `waveform`                                                      | **Cross-media-type filters** (audio→video: `showwavespic`, `showspectrumpic`) wired through MediaMolder's encoder selection | ❌ Open — see §2.3 |
| `clip-time`, `scene-time`, `sexagesimal-time`                   | Pure CLI utilities — out of scope for the engine, in scope for a future `mediamolder util` subcommand | out of scope |
| `ebu-meter`, `scopes`                                           | ffplay-based interactive viewers — out of scope for the engine, possibly in scope for the GUI | out of scope |

These eight underlined capability gaps are the **first wave** of the roadmap below. They are sampled from a tiny corner of FFmpeg usage (20 hand-written shell scripts), so they should be treated as a representative *minimum*, not as a complete list.

### 1.1 Second corpus: production-pattern command lines

A review of typical production FFmpeg usage (animated `drawtext`, two-pass `loudnorm`, multi-resolution split-and-mux ABR, hardware pipelines mixing CUDA + NPP + NVENC, HDR `zscale`/`tonemap`, `minterpolate` slow-motion, RNNoise speech denoise, mixed labelled/unlabelled `-filter_complex` outputs) surfaced a second wave of gaps that the 35-script community corpus does not exercise:

| Gap                                                            | Note |
|----------------------------------------------------------------|------|
| **Filter expression engine** (`enable=between(t,2,8)`, `x=w-tw*t/5`, `t`, `n`, `frame`, `tw`, `th`, `text_w`, `text_h`) | ⚠️ Partial — `GET /api/filters/{name}/eval-expression` endpoint done (§5 #7); `expression: true` AVOption flag bit and syntax-highlighted GUI input still open (§6.5 #19–20). |
| **Two-pass `loudnorm`**                                        | ✅ Done — `Output.LoudnormPass` / `Output.LoudnormStatsFile` carry the EBU R128 shuttle. Pass 1 sets `print_format=json`+`stats_file` on every loudnorm filter so libavfilter writes input_i/tp/lra/thresh/target_offset to a JSON file (`af_loudnorm.c::uninit`); pass 2 reads it and injects `measured_I/TP/LRA/thresh`+`offset` AVOptions. No FFmpeg flag — orchestration sugar above the manual two-run recipe. |
| **`setsar` / `setdar` and explicit SAR/DAR encoding**          | Required for square-pixel correction of legacy 720×480 / 720×576 sources and for HDR/Dolby SAR enforcement. |
| **Audio channel manipulation** (`pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix=weights=…`) | Multi-track downmix / upmix / language-track splitting. |
| **Speech denoise model files** (`arnndn=model=cb.rnnn`)        | Filter takes a model path; we have no fixture/asset story for filter-side data files. Same problem as YOLO model paths but for filters rather than processors. |
| **HDR tonemap via `zscale`**                                   | Depends on libzimg in the build (separate from libswscale). Build-tag and feature-detection story missing. |
| **`minterpolate` motion-compensated frame interpolation**      | Requires VFR awareness and fps targets — touches the same FrameRate/TimeBase plumbing as xfade. |
| **Lossless intermediate codecs** (FFV1, ProRes, DNxHD/DNxHR, HuffYUV) | Multi-pass editorial workflows (decode → lossless intermediate → grade → final encode). Encoder availability + container compatibility validation. |
| **`scale_npp` (NVIDIA NPP) vs `scale_cuda`**                   | Different libraries; some FFmpeg builds expose only one. The hardware-filter mapping table needs a per-filter availability probe. |
| **`-init_hw_device` and multi-device graphs**                  | Pipelines that touch two hardware contexts (e.g. CUDA decode → CPU filter → QSV encode) need explicit device declarations and `hwmap` between them. |
| **First-class raw-stream input** (`-f rawvideo -pix_fmt yuv420p -s 1920x1080 -r 30`) | Raw inputs are the dominant test fixture format and the canonical bug-report repro. They work via AVDict today; deserve a typed schema. |
| **`-fps_mode` (cfr/vfr/passthrough/drop) and legacy `-vsync`** | ✅ Done — `Output.FPSMode`; `compat/ffcli` rewrites legacy `-vsync` aliases (Wave 1 #1). |
| **`-async N` audio resync**                                    | ✅ Done — `Output.AudioSync`; `spliceAudioSyncForOutputs` injects `aresample=async=N` (Wave 1). |
| **Mixed labelled / unlabelled `-filter_complex` outputs**      | `avfilter_graph_parse_ptr` quirk: a graph that exposes both `[v]` and an unlabelled trailing pad needs careful pad-binding order. The `compat/ffcli` importer must normalise this. |
| **Long command lines, quoting, and shell escaping**            | ✅ Done — `FuzzBuildFilterSpec`, `FuzzTokenize`, `FuzzParseFilterExpr` cover the `,;'` escape class; seed corpus locked in (§5 #8). |

These are folded into the matrix in §2 and the phase plan in §3.

## 2. The full FFmpeg surface area

The CLI is a thin shell around four subsystems: **demux**, **filter**, **encode**, **mux**. To match it, MediaMolder must cover every option each subsystem accepts. The matrix below groups options by subsystem, marks current coverage, and points at the relevant code.

Legend: ✅ supported · ⚠️ partial · ❌ missing

### 2.1 Inputs / demux

| FFmpeg flag(s)                                              | Status | MediaMolder location / note |
|-------------------------------------------------------------|:------:|------------------------------|
| `-i URL` (file, http, rtmp, rtsp, srt, pipe, device)        | ✅    | `pipeline/config.go` `Input.URL`; URL forwarded to `avformat_open_input` |
| `-ss`, `-t`, `-to` (input-side)                             | ✅    | `pipeline/timing.go` (matches FFmpeg conflict semantics) |
| `-itsoffset`                                                | ✅    | `Input.ITSOffset` (seconds). `pipeline/handlers.go::openSource` composes additively with the implicit `-ss` ts_offset (matches FFmpeg's `f->ts_offset = o->input_ts_offset - timestamp` in `fftools/ffmpeg_demux.c`); applied via `Packet.ShiftTS` for every demuxed packet. |
| `-stream_loop N`                                            | ✅    | `Input.StreamLoop` (0=off, N>0=play N+1 times, -1=infinite). `handleSource` tracks per-iteration min/max packet PTS in AV_TIME_BASE us; on EOF, if loops remain, calls `SeekFile(StartTime)`, accumulates `(max - min)` into `loopOffsetUS`, decrements the counter, and shifts subsequent packets so PTS stay monotone. Mirrors `fftools/ffmpeg_demux.c::seek_to_start` + `ts_fixup`. |
| `-readrate`, `-re` (real-time read)                         | ✅    | `Input.ReadRate` / `ReadRateInitialBurst` / `ReadRateCatchup`. `-re` is shorthand for `-readrate 1`. Implemented by `pipeline.readRatePacer` (faithful port of `fftools/ffmpeg_demux.c::readrate_sleep` including the 0.3 s lag-detection threshold); pacing sleep is context-aware so cancellation aborts immediately. |
| `-framerate`, `-r` (input override)                         | ⚠️    | Can be passed in `Input.Options` AVDict; not first-class and not validated |
| `-pix_fmt`, `-video_size`, `-pixel_format`                  | ⚠️    | Same: AVDict passthrough, no schema field |
| `-f` (force demuxer)                                        | ⚠️    | `Input.Kind = "lavfi"` covers the virtual-source case via `av.OpenInputWithFormat`; arbitrary forced demuxers (`rawvideo`, `s16le`) not yet first-class |
| `-thread_queue_size`                                        | ⚠️    | AVDict only |
| `-accurate_seek` / `-noaccurate_seek` / `-seek_timestamp`   | ❌    | Required for frame-accurate trim of long-GOP sources |
| `-protocol_whitelist`                                       | ⚠️    | AVDict only; should be elevated for security review |
| Lavfi virtual sources (`-f lavfi -i color=…`)               | ✅    | `Input.Kind = "lavfi"`; `URL` carries the filtergraph spec. libavdevice linked + `avdevice_register_all()` at init |
| `image2` glob pattern (`-i 'frames/*.png'`)                 | ⚠️    | Works via AVDict if user knows the syntax; no schema affordance |
| `concat` demuxer (listfile)                                 | ❌    | Today users must build a concat **filter** graph; no `concat:` input kind |
| Device capture (`-f avfoundation`, `-f dshow`, `-f v4l2`)   | ⚠️    | Works through AVDict; no GUI palette, no probe |
| `-hwaccel`, `-hwaccel_device`, `-hwaccel_output_format`     | ⚠️    | Global only; not per-input |

### 2.2 Stream selection / mapping

| FFmpeg flag(s)                                | Status | Note |
|-----------------------------------------------|:------:|------|
| Default automatic stream selection            | ✅    | "best video + best audio" implied if user picks `track: 0` |
| `-map 0:v:0` / `-map 1:a:0` style             | ✅    | `pipeline.StreamSelect.{InputIndex,Type,Track}`; ffcli `-map` parser (Wave 2 #9) |
| Negative / optional mapping (`-map -0:s`, `-map 0:s?`) | ✅ | `StreamSelect.{Negate,Optional}`; done Wave 2 #9 |
| Program selection (`-map 0:p:N[:type[:idx]]`) | ✅    | `StreamSelect.Program` (matches `AVProgram.id`); done Wave 2 #10 |
| `-map_metadata`, `-map_chapters`              | ✅    | `metadata_reader` / `metadata_writer` graph nodes + `Input.MapMetadata` / `Input.MapChapters` shorthand; done Wave 2 #11 |
| `-vn` / `-an` / `-sn` / `-dn` per output      | ⚠️    | Implied by which edges connect — works but undocumented |
| Reuse of one decoded stream by N filters/outputs (`split`/`asplit`) | ✅ | Works via multi-output filters |
| Per-input `-map` of *attachment* streams      | ❌    | (see §2.5 attachments) |

§2.2 is now covered for all four common selector grammars (track, all-of-type, optional, negate, program). FFmpeg's full `-map` grammar also supports `m:KEY[:VALUE]` metadata-based filters and `M:i:N` id-based selection, which remain out of scope; both have negligible real-world usage in the §6 corpus.

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
| `minterpolate` (motion-compensated interpolation)           | ⚠️    | Same FrameRate/TimeBase plumbing as xfade now done; remaining work is exposing motion-estimation params via the inspector |
| Audio channel manipulation: `pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix=weights` | ⚠️ | Available as filters; GUI has no per-channel routing UI |

### 2.4 Encoders

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| Codec selection per output and per stream                         | ✅    | `Output.CodecVideo/Audio/Subtitle` plus explicit encoder nodes |
| Stream copy (`-c copy`)                                           | ✅    | Implicit `KindCopy` expansion |
| Codec-specific AVOptions (`preset`, `crf`, `tune`, `profile`, `level`, `g`, `bf`, `refs`, `x264-params`, `x265-params`, `aq-mode`, `tier`, …) | ✅ | Forwarded to `avcodec_open2` via `EncoderParams*` dict |
| Hardware encoders (NVENC, QSV, VAAPI, VideoToolbox, AMF)          | ✅    | Per `av/hwencode.go`; tested for NVENC |
| Two-pass encoding (`-pass 1/2 -passlogfile`)                      | ✅    | `Output.Pass` + `Output.PassLogFile` (Wave 1 #6) |
| **Two-pass `loudnorm`** (measured-I/TP/LRA/thresh/offset feed-forward) | ✅ | Inter-pass shuttle done. Pass 1: libavfilter writes JSON via `print_format=json`+`stats_file`. Pass 2: runtime parses JSON and injects `measured_*`+`offset` AVOptions. Carried by `Output.LoudnormPass` (0/1/2) and `Output.LoudnormStatsFile` (prefix). |
| **Lossless intermediate codecs** (FFV1, ProRes, DNxHD/HR, HuffYUV) for editorial round-trips | ⚠️ | Encoders exist if FFmpeg compiled with them; no schema validation of codec ↔ container compatibility |
| `-fps_mode` (`cfr`/`vfr`/`passthrough`/`drop`) (formerly `-vsync`) | ✅    | `Output.FPSMode`; per-frame renumber/drop/duplicate logic in `pipeline/fps_mode.go` consumed by `handleEncoder` for video streams. `compat/ffcli` rewrites the legacy `-vsync` numeric/auto aliases. |
| `-async N` (audio resync via resampler)                            | ✅    | `Output.AudioSync`; `pipeline.spliceAudioSyncForOutputs` injects an `aresample=async=N[:first_pts=0 when N==1]` filter node in front of every audio encoder feeding the output. `compat/ffcli` accepts the legacy flag. |
| `-force_key_frames "expr:gte(t,n_forced*2)"` and chapter-driven IDR placement | ✅ | `Output.ForceKeyFrames` covers `expr:`, `source`, and time-list grammars (per-frame `pict_type = AV_PICTURE_TYPE_I` stamp via `av.Frame.SetPictType`). Chapter-driven IDR (`chapters[+offset]`) deferred. |
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
| Output-side `-ss`/`-t`/`-to`                                      | ✅    | `Output.Options.{ss,t,to}`; `pipeline.resolveOutputTiming` + `handleSink` drop packets below `start_time` and stop muxing at `start_time + recording_time`. With `Config.CopyTS`=true the trim window is interpreted as absolute timeline values; otherwise kept packets are shifted back so the file anchors at PTS 0 (mirrors `of_streamcopy`). |
| `-shortest`                                                       | ✅    | `Output.Shortest`; `handleSink` records the PTS at which the first feeder channel closes and drains-and-drops further packets on the remaining channels of the same output. Mirrors per-output sync-queue cap in `fftools/ffmpeg_mux_init.c`. |
| `-fs N` (file size limit)                                         | ✅    | `Output.MaxFileSize`; `handleSink` calls `av.OutputFormatContext.BytesWritten` (avio_tell) before every `WritePacket` and stops with a clean trailer once the limit is reached. |
| `-frames:v N`, `-frames:a N`                                      | ✅    | `Output.MaxFramesVideo` / `Output.MaxFramesAudio`; sink drains channel and stops writing once limit is hit (post-encoder count, matches ffmpeg semantics for filter-dropping graphs) |
| `-metadata key=value`                                             | ✅    | `Output.Metadata`; `compat/ffcli` parses bare `-metadata`, `handleSink::applyOutputMetadata` writes via `av_dict_set` on `AVFormatContext.metadata` before `WriteHeader` (mirrors `fftools/ffmpeg_mux_init.c::of_add_metadata`). |
| `-metadata:s:v:0 …` per-stream metadata                           | ✅    | `Output.Streams[*].Metadata`; per-stream resolution counts streams of the requested media type in muxer-add order (same convention as `check_stream_specifier` for `s:<type>:<idx>`). Required for language tags, stereoscopic flags, comments. |
| `-disposition:s:v:0 default+forced`                               | ✅    | `Output.Streams[*].Disposition`; forwards a `+`-separated AV_DISPOSITION_* flag list to `av_opt_set` on the AVStream's AVClass — same code path `fftools/ffmpeg_mux_init.c::set_dispositions` uses. |
| `-map_metadata`, `-map_chapters`                                  | ✅    | `metadata_reader` / `metadata_writer` graph nodes connected by a `metadata` edge route container metadata or chapters from any input into any output (Wave 2 #11); `Input.MapMetadata` / `Input.MapChapters` shorthand still works for single-input cases. `compat/ffcli` parses both flags into the node pair. |
| Chapter writing API                                               | ✅    | `Output.Chapters []ChapterInfo`; `metadata_writer` with `section=chapters` routes `AVChapter` entries from any input. |
| Attachments (fonts for ASS, cover art)                            | ❌    | |
| Cover art / thumbnail embed in MP4/M4A                            | ❌    | Common end-user request |
| Multiple outputs in one pipeline                                  | ✅    | Multiple `Output` entries |
| **`tee` muxer / single-pass multi-format** (`mp4 + hls + dash`)   | ❌    | The standard FFmpeg way to fan one encode into many containers without re-encoding |
| HLS muxer (`hls_time`, `hls_playlist_type`, EXT-X-MAP, byte-range, low-latency) | ⚠️ | Works via raw `Options` AVDict; no schema fields, no validation |
| DASH muxer (representations, adaptation sets, init segment)       | ⚠️    | Same |
| Segment muxer / fragmented MP4 (CMAF) / `movflags=+faststart`     | ⚠️    | `movflags` works; segment_* options require AVDict |
| `-muxdelay`, `-muxpreload`, `-copyts`, `-start_at_zero`, `-avoid_negative_ts` | ⚠️ | `Config.CopyTS` covers `-copyts` (suppresses demuxer ts_offset shift; switches output-side `-ss`/`-to` to absolute timeline). `-muxdelay`/`-muxpreload`/`-start_at_zero`/`-avoid_negative_ts` still missing. |
| Bitstream filter chains on output (`-bsf:v "h264_mp4toannexb,h264_redundant_pps"`) | ✅ | Chain syntax parsed by `av_bsf_list_parse_str`; per-stream-type via `BSFVideo`/`BSFAudio`/`BSFSubtitle` |

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
| Tee muxer (see §2.5)                                              | ✅    | `Output.Kind="tee"` + `Output.Targets[]` (Wave 1 #5) |
| Dynamic per-frame metadata via ZMQ filter                         | ❌    | |
| **`-init_hw_device` (multi-device graphs)**                       | ❌    | Pipelines that bridge CUDA decode → CPU filter → QSV encode need named device declarations + `hwmap` between them |
| **`scale_npp` availability separate from `scale_cuda`**           | ⚠️    | Different libraries; needs per-filter availability probe at startup |
| **First-class raw-stream input** (`-f rawvideo -pix_fmt yuv420p -s 1920x1080 -r 30 -i raw.yuv`) | ⚠️ | Works via AVDict; the canonical bug-report fixture format deserves a typed schema |

### 2.8 Frontend GUI gaps (in addition to schema gaps)

The GUI cannot be more powerful than the schema. Once §2.1–§2.7 are
filled, the GUI also needs:

- A palette section for **virtual source nodes** (color/testsrc/sine/anullsrc).
- A **multi-output inspector** that shows all `Output` entries in one pane, with per-stream encoder tabs.
- **BSF chain editor** (sortable list, not single field).
- **Chapter / metadata editor** at the output level (table of `(start, end, title)` for chapters; key/value table for metadata, with per-stream tabs).
- **HLS / DASH / Tee output wizards** with structured fields (segment duration, playlist type, variants, …).
- **Hardware filter mapping indicator** that surfaces which filters will run on GPU once `hw_accel` is set, and warns when a software filter is forcing a hwdownload/hwupload round-trip.
- **Live FFmpeg-CLI import** (`compat/ffcli`) extended to cover every flag the schema gains, with a clear "unsupported flag" report.
- **Live FFmpeg-CLI export**: round-trip the JSON job back to a CLI command for users who want to copy/paste into ffmpeg directly. This
  is the strongest correctness signal we can ship. Note that mediamolder has a superset of FFmpeg features, so some mediamolder JSONs may not have an FFmpeg CLI equivalent, and this feature must fail gracefully, explaining why no FFmpeg command line can be generated.

## 3. Strategy

The strategy is **library-first, schema-second, GUI-third**, in that order, for every capability:

1. Make sure the underlying libav* binding in `av/` exposes whatever AVOption / API is required.
2. Surface it as a schema field in `pipeline.Config` (and the matching `schema/v1.x.json`, `frontend/src/lib/jobTypes.ts`, and the `materializeImplicitEncoders` / `expandImplicitEncoders` adapters).
3. Add an inspector form in the GUI.

Two **horizontal** workstreams run alongside the per-feature work:

- **`compat/ffcli` round-trip tests.** Every new capability gets a test that takes an FFmpeg command line, runs it through `ffcli`, runs the resulting JSON job, and compares the output (size, hash, SSIM, PSNR, loudness, frame count) with what FFmpeg produces from the same command. This is the only way to *prove* parity at scale.
- **Capability registry.** A machine-readable inventory of every FFmpeg flag, with one of `{covered, partial, missing, out-of-scope}`
  and a link to the schema field that handles it. The GUI's "unsupported flag" report and the `ffcli` validator both consume this registry. Without it the matrix in §2 will rot.

### 3.1 Phase A — close the community-scripts gaps (sample-driven)

These are the smallest, best-scoped pieces of work and they unblock real user scripts today. Targets at the end of this phase: 35/35 community scripts converted, 0 skipped on a fully-featured ffmpeg build.

1. **Frame-rate metadata on `FilterPadConfig`.** Add `FrameRateNum`, `FrameRateDen`, `TimeBaseNum`, `TimeBaseDen` to the struct in `av/` and propagate them through complex filtergraph configuration. Unblocks `xfade`, `crossfade`, `interleave`, `framerate`, `setpts/setdar` with constant-FPS guarantees.
2. **`-frames:v N` / `-frames:a N`.** Add `MaxFramesVideo`, `MaxFramesAudio` to `Output`. Stop demuxing on the upstream side when any output reaches its limit. Unblocks `extract-frame`, `tile-thumbnails`, `scene-images`.
3. **Virtual-source input kind.** Add `Input.Kind ∈ {file, lavfi}` with a `lavfi_spec` field (e.g. `"color=black:size=1920x1080:rate=30"`). 
   Backed by an `avfilter_graph_alloc` source-only graph. Unblocks `audio-silence`, padding tracks, test cards.
4. **Cross-media-type filter contract.** Add `output_media_type` to filter node definitions so the engine knows that `showwavespic` returns video even though it consumes audio.
   The GUI must then show the edge as `video` downstream of the filter. Unblocks `waveform`, `showspectrum*`, `concat=v=1:a=1`.
5. **Chapter and per-stream metadata IO.** Two new node kinds:
   - `KindMetadataReader` (for `-map_metadata`, `-map_chapters`)
   - `KindMetadataWriter` (for `-metadata`, chapter tables)
   Plus an `Output.Chapters []Chapter` and `Output.Metadata map[string]string` shorthand for the common case.
   Unblocks `chapter-add`, `chapter-extract`, `chapter-csv`.
6. **Filter expression engine surface.** `params` values are already strings, so libavfilter receives expressions intact today, but the GUI cannot author them safely. 
   Ship: 
   (a) an `expression: true` flag on `FilterOption` schema entries (mined from `av_opt_next` flag bits), 
   (b) a syntax-highlighted expression input in the inspector, 
   (c) a server-side `/api/filters/{name}/eval-expression?expr=…&t=0`
   smoke-test endpoint that asks libavfilter to parse the expression without running the graph, and (d) round-trip tests for the common expressions in the production corpus enable=between `(t,a,b)`, scrolling `x=w-tw*t/k`, `frame_n%N`, `if(eq(n,0),…)`.
7. **Two-pass `loudnorm` shuttle.** A new pipeline-level orchestration primitive: declare a node `type: "loudnorm_2pass"` whose runner executes the graph once with `print_format=json`, captures the measured-I/TP/LRA/thresh/offset values from libavfilter's metadata side-data (we already plumb metadata to the event bus), and re-runs the graph with those values fed back into the filter. This is the minimum-viable pattern for any "measure, then process" workflow (also applies to `volumedetect`, `signalstats`, `astats`).

### 3.2 Phase B — the universal mapper

Make the schema express anything FFmpeg's `-map` can express. Concretely:

1. Promote `Input.Streams[].track` to a richer selector with `optional` and `negate` flags, plus `program_id`.
2. Add a top-level `mappings` array (or normalise it as a sugar over the existing typed-edges model) that lets users say `(input=0, type=v, index=0, optional=true) → out0`.
3. Integration tests: every example in the FFmpeg manual's "Stream selection" chapter, round-tripped through `ffcli`.

### 3.3 Phase C — output-side fidelity

Every production-grade ffmpeg pipeline depends on these:

1. `-shortest`, `-fs`, output-side `-ss`/`-t`/`-to` with `-copyts` semantics.
2. **Tee muxer support** as a first-class `Output.Kind = tee`. This is the biggest single feature; it changes the engine from "one mux per output" to "one encoded stream → many muxers".
3. Structured HLS / DASH / fragmented-MP4 / CMAF output (with a `Variants []EncoderSettings` for ABR ladders).
4. Two-pass encoding (`Encoder.Pass int`) for video; same scaffold reused by the Phase A loudnorm shuttle.
5. Per-stream encoder param overrides; per-stream metadata.
6. BSF chains.
7. Color metadata, HDR10 static metadata, Dolby Vision RPU passthrough — and validation that the chosen encoder/container can carry them.
8. **Lossless intermediate workflow validation.** Add an integration test that decodes BBB → re-encodes to FFV1/MKV → decodes the intermediate → re-encodes to H.264/MP4, and asserts that the round trip produces a file at least as good (PSNR, SSIM, audio loudness) as a single-pass encode. This is the canonical editorial pattern.
9. **`setsar`/`setdar` exposed as encoder-side `Output.SAR` / `Output.DAR` shorthand**, in addition to the filter.

### 3.4 Phase D — broadcast / live

For real-time and broadcast workflows:

1. `-readrate`/`-re`, `-stream_loop`, `-itsoffset`.
2. `-fps_mode`, `-async`, `-force_key_frames`, `-muxdelay`, `-muxpreload`, `-copyts`, `-start_at_zero`, `-avoid_negative_ts`.
3. RTP/RTSP/SRT/RIST/NDI as first-class input/output kinds, with schema validation and reconnect/backoff policies (we already have error policies — extend them to network errors).
4. Decklink SDI, ZMQ live filter parameter updates.
5. **Multi-device hardware graphs.** Implement `init_hw_device` semantics: a `hardware_devices: [{name, type, device}]` block at the top of the JSON pipeline plus `device:` selectors on encoder/filter nodes. Required for CUDA-decode → CPU-filter → QSV-encode pipelines and for fan-out across multiple GPUs.
6. **`scale_npp` vs `scale_cuda` per-filter availability probe.**
   Filter palette must reflect what the linked FFmpeg actually provides; today we only probe codecs.

### 3.5 Phase E — GUI completeness

GUI parity is gated on schema parity, but the work can run in parallel
once §3.1–§3.4 land:

1. Virtual-source palette.
2. Multi-output inspector with per-stream encoder tabs.
3. BSF chain editor.
4. Chapter / metadata editor.
5. HLS / DASH / tee output wizards.
6. Hardware-filter mapping indicator + multi-device picker.
7. Bidirectional FFmpeg-CLI conversion (existing `compat/ffcli` import + new export). The CLI export is the round-trip oracle for
   the entire schema and should be wired into the existing job-save flow as a "Show as ffmpeg command" panel.
8. **Filter expression authoring**: monospace input with `t`/`n`/`tw`/ `th`/`text_w`/`text_h`/`w`/`h` autocomplete, live
   syntax-validation against the server-side `eval-expression` endpoint, and a small expression cookbook (scrolling text, fade gates, frame-stamp overlays).
9. **Audio channel-routing UI**: a bus/matrix view for `pan`, `channelsplit`, `channelmap`, `join`, `amerge`. Today's free-form `params` dict is unusable for non-trivial routing.
10. **Asset/model-file manager**: shared by the YOLO processor and by filters such as `arnndn`, `subtitles=…:fontsdir=…`. Pipelines
    should reference assets by symbolic name, with the GUI managing paths and the runtime resolving them from a search list.

### 3.6 Phase F — proof of universality

1. **FFmpeg manual conformance suite.** Every example command in `ffmpeg-doc.html` becomes a test case. Pass criterion: same
   container, same stream count, same per-stream codec, output bytes within tolerance, SSIM ≥ 0.99, audio loudness within ±0.5 LU.
2. **Production-pattern conformance suite.** A second corpus assembled from the production-pattern command lines catalogued in §1.1 —
   animated `drawtext`, two-pass `loudnorm`, multi-resolution split-and-mux ABR, full GPU pipelines, HDR `zscale`/`tonemap`, `minterpolate` slow-mo, RNNoise, mixed labelled/unlabelled `-filter_complex` outputs, raw-stream inputs, lossless intermediates. Same pass criteria as §3.6.1.
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

## 5. Initial backlog (completed)

These eight items were the first wave of capability gaps identified in §1–§2.
All are now done. Listed in the order they were addressed:

1. **Frame-rate metadata on `FilterPadConfig`** (§3.1.1) — ✅ **done.**
   `FilterPadConfig` / `VideoFilterGraphConfig` gained `FRNum/FRDen`
   (TBNum/TBDen were already present), buffersink rate/timebase are
   re-queried after each upstream filter, and `handleFilter` now
   tolerates `EAGAIN`/`EOF` on `PushFrameAt` so xfade can close its
   second input mid-graph. 18 / 20 community scripts pass; only
   `06_fade_title` (drawtext/libfreetype) and `12_webp` (libwebp)
   remain skipped.
2. **`-frames:v N` / `-frames:a N`** (§3.1.2) — ✅ **done.**
   `Output` gained `MaxFramesVideo` /
   `MaxFramesAudio`; `handleSink` enforces the cap per inbound
   channel by dropping packets after the limit while still draining
   the channel (so upstream encoders never deadlock). Counts
   post-encoder packets, matching ffmpeg's `-frames:v` semantics
   when filters like `select=gt(scene,…)` drop frames. Five new
   fixtures written to `testdata/community-scripts/21_*.json`–
   `25_*.json`; image outputs use the `mjpeg` muxer (raw JPEG
   stream — sidesteps the `image2` `%d`-pattern requirement which
   conflicts with the muxer's atomic-rename of `out.tmp → out`).
3. **`Input.Kind = "lavfi"` (virtual-source inputs)** (§3.1.3) — ✅ **done.**
   `Input` gained `Kind` (`"file"` default, `"lavfi"`);
   new `av.OpenInputWithFormat(url, format, options)` wraps
   `av_find_input_format` + `avformat_open_input`, and
   `pipeline.openSource` switches on `Kind` to route lavfi specs
   through libavformat's `lavfi` virtual demuxer (FFmpeg's
   `-f lavfi`). libavdevice is now linked into the static build
   (`-lavdevice` + `-framework CoreAudio` on darwin for
   audiotoolbox.o) and registered at process start via
   `avdevice_register_all()` in `av/avdevice_init.go`. `SeekFile`
   is skipped for lavfi inputs (virtual sources don't seek; `-t`
   still applies via the per-packet `recording_time` stop check).
   New fixture `testdata/community-scripts/26_audio_silence.json`
   generates 2 s of silent stereo PCM via
   `anullsrc=channel_layout=stereo:sample_rate=44100` →
   `aformat` → `pcm_s16le` WAV end-to-end.
4. **Capability registry + first `compat/ffcli` round-trip tests** (§3.2) — ✅ **done.**
   `compat/capabilities.yaml` now ships with 105
   entries seeded from §2.1–§2.7 (30 covered, 35 partial, 37
   missing, 3 out-of-scope), loaded by `compat.LoadRegistry` via
   `embed`; `compat/registry_test.go` enforces well-formedness,
   valid statuses, all required sections, and a non-`n/a` schema
   pointer for every `covered` flag. The first batch of round-trip
   tests lives at `compat/ffcli/roundtrip_test.go`: for each
   command template the harness runs both `ffmpeg(1)` and the
   parsed `pipeline.Config` end-to-end, then `ffprobe(1)`s both
   outputs and asserts identical stream counts, per-stream codec /
   resolution, and format duration within 0.5s. Initial cases
   cover stream-copy MP4→MKV, `-c:v copy -c:a aac` transcode,
   input-side `-ss 1 -t 2 -c copy`, and `-c copy -f matroska`
   forced-format remux; the suite is skipped (not failed) when
   `ffmpeg`/`ffprobe` are missing from `PATH` so the default
   `go test ./...` run stays usable on machines without the CLI
   installed alongside the libraries. Two pre-existing pipeline
   bugs surfaced and are now fixed in the same series:
   `pipeline.buildFilterSpec` was emitting positional FFmpeg-style
   filter args (the `_pos*` keys synthesised by
   `compat/ffcli.parseFilterExpr` for `scale=320:240`-style
   expressions) verbatim as named options; libx264 fed by a filter
   graph was opened with `time_base=1/framerate` while the
   buffersink advertised the demuxer's finer TB, so frame PTS were
   reinterpreted in the encoder's coarser units and the container
   duration came out ~512x too long. Round-trip cases
   `vf_scale_positional_x264_audio_copy` and
   `vf_scale_named_x264_no_audio` lock both fixes in.
5. **Chapter and per-stream metadata IO** (§3.1.5) — ✅ **done (shorthand only).**
   `Output.Metadata`
   (`map[string]string`) and `Output.Chapters` (`[]Chapter`, seconds-based
   `Start`/`End`) now reach the muxer via `av.OutputFormatContext.SetMetadata`
   / `AddChapter`; `Input.MapMetadata` and `Input.MapChapters` provide
   `-map_metadata` / `-map_chapters` semantics with FFmpeg-faithful
   precedence (output overrides win; first-input-wins for chapters).
   Schemas v1.0/v1.1 + `frontend/src/lib/jobTypes.ts` synced; round-trip
   coverage in [pipeline/metadata_test.go](../pipeline/metadata_test.go).
   The heavier `KindMetadataReader` / `KindMetadataWriter` graph node
   kinds remain **deferred** — the shorthand covers the common case and
   the graph-kind work is reserved for a future PR that has a real
   per-stream / multi-source metadata-routing scenario to anchor it.
6. **Production-pattern conformance corpus stub** (§3.6.2) — ✅ **done (stub).**
   Six manifest JSONs seeded
   under [testdata/production-patterns/](../testdata/production-patterns/)
   (`01_animated_drawtext.json`, `02_abr_ladder.json`,
   `03_full_gpu_scale_npp_nvenc.json`, `04_hdr_zscale_tonemap.json`,
   `05_loudnorm_two_pass.json`, `06_raw_yuv_input.json`); harness lives
   at [compat/ffcli/production_patterns_test.go](../compat/ffcli/production_patterns_test.go)
   (`TestProductionPatternsCorpus`). Each manifest carries the
   canonical FFmpeg command, a free-form description, a structured
   `blockers: [string]` list naming the missing capability keys, and
   `roadmap_refs` pointing back into §1.1/§2/§3. The harness emits one
   `roadmap-ref:` log line per ref then either `Skip`s with a single
   greppable `blocked-by: <k1>; <k2>; ...` line or — once `blockers` is
   empty — drives the command through `ffcli.Parse` + `pipeline.Run`.
   Today's expected outcome is 6/6 skips; the success criterion for
   landing each upstream capability is "this pattern flips from skip
   to pass". Capability inventory mining:
   `go test -v -run TestProductionPatternsCorpus ./compat/ffcli/ 2>&1 | grep '^.*blocked-by:'`.
7. **Filter-expression `eval-expression` HTTP endpoint** (§3.1.6) — ✅ **done.**
   `av.EvalExpression` ([av/expr.go](../av/expr.go)) wraps libavutil's
   `av_expr_parse_and_eval`; `GET /api/filters/{name}/eval-expression?expr=…&t=…&w=…`
   ([internal/gui/filter_eval.go](../internal/gui/filter_eval.go)
   `handleFilterEvalExpression`) registered on the GUI mux. Ships a
   curated variable table per common filter (drawtext, overlay,
   crop, scale, pad, rotate, zoompan, setpts/asetpts, volume — names
   mined from each filter's `var_names[]` in libavfilter), defaulting
   each constant to 0; arbitrary `?name=value` query pairs override
   bindings. Response shape: `{filter, expr, variables, ok, value, error}`,
   HTTP 200 for both success and parse-failure (the `ok` flag is the
   truth). Round-trips covered in
   [av/expr_test.go](../av/expr_test.go) (`TestEvalExpression`) and
   [internal/gui/filter_eval_test.go](../internal/gui/filter_eval_test.go)
   (`TestHandleFilterEvalExpression`): `between(t,1,8)`, scrolling
   `w-mod(40*t,w+tw)`, `W-w` overlay arithmetic, syntax errors,
   unknown identifiers, fallback-on-unknown-filter, missing `expr`.
   Items (a) (the `expression: true` AVOption flag bit on
   `FilterOption`) and (b) (the syntax-highlighted GUI input) of
   §3.1 #6 remain open.
8. **Quoting/escaping fuzzer** (§3.6.5) — ✅ **done.**
   Three Go-native fuzzers seeded against the bug class:
   [pipeline/fuzz_filter_spec_test.go](../pipeline/fuzz_filter_spec_test.go)
   `FuzzBuildFilterSpec` drives the filter-spec renderer with
   arbitrary value bytes; asserts no panic, no unquoted `,`/`;`
   leaks (the exact 04f1a0c7 regression), and balanced single-quote
   runs under libavfilter's `'…'` + outside-`\X` escape grammar.
   [compat/ffcli/fuzz_quoting_test.go](../compat/ffcli/fuzz_quoting_test.go)
   `FuzzTokenize` exercises the shell-style tokenizer (no token can
   contain an unquoted space when the input had no quote bytes; total
   token byte budget never exceeds input length), and
   `FuzzParseFilterExpr` exercises the `key=val:val` filter-expression
   parser. Each fuzzer ran clean for ~900 k executions at 15 s
   `-fuzztime` on initial validation. Extend in CI with
   `go test -run=^$ -fuzz=FuzzBuildFilterSpec -fuzztime=60s ./pipeline/`
   (and analogous targets in `compat/ffcli/`); seed-corpus failures
   are auto-persisted to `pipeline/testdata/fuzz/FuzzBuildFilterSpec/`
   and `compat/ffcli/testdata/fuzz/FuzzTokenize/` for permanent
   regression coverage.

Each of these unblocks real user scripts today and pays down the
debt the §2 matrix is tracking.

## 6. Parity development plan (post-§5 burn-down)

The §5 backlog (items 1–8) is fully done. The plan below is the
**next wave**, ordered by *user-frequency × leverage* rather than by
§3's phase letters. Each item lists the gap it closes (with §2 / §3
back-reference) and a concrete first-PR scope. Items are sequenced so
that earlier items create scaffolding (orchestration vocabulary,
per-stream schema) reused by later ones.

### 6.1 Wave 1 — "the 90% of real jobs"

These show up in **almost every production ffmpeg invocation**. Shipping
them moves MediaMolder from "covers most demo scripts" to "covers most
real jobs."

1. **`-fps_mode` / `-async`** (§2.4, §2.5) — ✅ **done.**
   `Output.FPSMode ∈ {cfr,vfr,passthrough,drop}`; per-frame
   renumber/drop/duplicate logic in `pipeline/fps_mode.go` consumed
   by `handleEncoder` for video streams. `compat/ffcli` rewrites the
   legacy `-vsync` numeric/auto aliases. `Output.AudioSync int`;
   `pipeline.spliceAudioSyncForOutputs` injects an
   `aresample=async=N[:first_pts=0 when N==1]` filter node in front
   of every audio encoder feeding the output. `compat/ffcli` accepts
   the legacy `-async` flag. Closes the #1 cause of A/V drift in user
   reports.
2. **`-shortest`, `-fs`, output-side `-ss`/`-to` with `-copyts`**
   (§2.5) — ✅ **done.** `Output.Shortest`,
   `Output.MaxFileSize`, and `Config.CopyTS` enforced in
   `pipeline/handlers.go::handleSink` via `resolveOutputTiming` +
   `processOne` (drops below `start_time`, stops at
   `start_time + recording_time`, caps shortest stream, calls
   `av.OutputFormatContext.BytesWritten` / `avio_tell` before each
   `WritePacket`); `-copyts` suppresses the demuxer ts_offset shift
   and switches output-side `-ss`/`-to` to absolute timeline.
   Out of scope for this wave: `shortest_buf_duration` tuning, `-fs`
   SI suffix parsing, and the rest of the muxdelay cluster
   (`-muxdelay`/`-muxpreload`/`-start_at_zero`/`-avoid_negative_ts`).
3. **Per-stream encoder overrides + per-stream metadata** (§2.4,
   §2.5) — ✅ partially done: `Output.Streams []StreamSpec`
   exposes per-stream `Metadata` + `Disposition` (mirrors
   `-metadata:s:<type>:<idx>` and `-disposition:s:<type>:<idx>`),
   unblocking dual-language audio and language-tagged /
   forced-flagged subtitles. Per-stream codec/bitrate is
   intentionally deferred — model it with explicit encoder graph
   nodes (see `testdata/examples/35_abr_ladder.json`), which is the
   shape ABR ladders already use.
4. **`-stream_loop`, `-itsoffset`, `-re` / `-readrate`** (§2.1) —
   ✅ **done.** `Input.StreamLoop` (0/N/-1), `Input.ITSOffset`
   (seconds, may be negative), and `Input.ReadRate` /
   `ReadRateInitialBurst` / `ReadRateCatchup` (faithful port of
   `fftools/ffmpeg_demux.c::readrate_sleep` in
   `pipeline.readRatePacer`). Unblocks watermark loops, A/V slip
   correction, and live-restream rate-limit in one PR. Promoted
   from #5 ahead of `tee` because it is three small typed-field
   promotions with no new schema discriminators or orchestration
   primitives — a lower-risk way to keep Wave 1 cadence while the
   `tee` muxer (next) gets its larger PR.
5. **`tee` muxer** (§2.5, §3.3.2) — ✅ **done.**
   `Output.Kind = "tee"` with `Output.Targets []TeeTarget`. The
   runtime renders the FFmpeg slaves URL
   (`[opt=val:opt=val]url|[opt=val]url`) deterministically via
   `pipeline.buildTeeSlavesURL` and opens libavformat's built-in
   tee muxer once via `av.OpenTeeOutput`; encoding happens once,
   the tee muxer fans the encoded packet stream out to every
   slave with no re-encoding. Promoted typed fields per target:
   `Format` (`f=`), `Select` (`select=`), `BSFs` (`bsfs=`),
   `OnFail` (`abort`/`ignore`), `UseFifo`, `FifoOptions`; obscure
   slave AVOptions land in `Options`. `compat/ffcli` parses
   `-f tee "[f=mp4]a.mp4|[f=hls:hls_time=4]b.m3u8"` end-to-end
   into the typed structure. Per-slave metadata/disposition is
   not supported by libavformat (slaves clone parent metadata) —
   set values via the parent `Output.Metadata` / `Output.Streams`
   instead. The encoder graph still wires through one logical
   sink, so the per-stream metadata / disposition schema (#3)
   composes naturally.
6. **Two-pass video encoding** (`-pass 1/2 -passlogfile`) (§2.4) —
   ✅ **done.** `Output.Pass` (bit-field 1 / 2 / 3 mirroring
   `AV_CODEC_FLAG_PASS1` / `PASS2`) and `Output.PassLogFile`
   (prefix; final filename rendered as `<prefix>-<idx>.log` where
   `<idx>` is the per-run video-encoder ordinal — matches FFmpeg's
   `<prefix>-<ost_idx>.log` naming). The runtime branches on the
   encoder name in `pipeline/handlers.go::createEncoder`, faithfully
   porting `fftools/ffmpeg_mux_init.c:705`: libx264 / libvvenc set
   the `stats` AVOption, libx265 sets `x265-stats`, every other
   codec uses the generic `AVCodecContext.stats_in` (pass 2,
   contents `os.ReadFile`d into `av.EncoderOptions.StatsIn` →
   `av_malloc`'d C buffer that the encoder owns) /
   `stats_out` (pass 1, appended to a Go-owned `*os.File` after
   each `ReceivePacket` in `handleEncoder`). Job is run twice by
   the caller against the same prefix. `compat/ffcli` parses
   `-pass N` + `-passlogfile P`. Sixth Wave 1 item.
7. **Two-pass `loudnorm` shuttle** (§3.1.7) — ✅ **done.**
   `Output.LoudnormPass` (0 / 1 / 2 — sequential, not a bit-field
   because libavfilter exposes no AV_CODEC_FLAG-equivalent for
   loudnorm) and `Output.LoudnormStatsFile` (prefix; final
   filename rendered as `<prefix>-<idx>.json` where `<idx>` is the
   per-run loudnorm-node ordinal). Pass 1: the runtime walks the
   graph in `pipeline/loudnorm.go::applyLoudnormShuttle` for every
   `filter == "loudnorm"` node, sets `print_format=json` and
   `stats_file=<prefix>-<idx>.json` directly on the node so
   `libavfilter/af_loudnorm.c::uninit` (lines 830-935) writes the
   EBU R128 measurements (input_i / input_tp / input_lra /
   input_thresh / target_offset) to the JSON file via
   `avpriv_fopen_utf8` — exactly the same code path FFmpeg's
   `print_format=json:stats_file=…` uses. Pass 2: the runtime
   reads the JSON in `createFilter`, parses it with the
   `loudnormStatsJSON` struct (every value is a `"%.2f"` string in
   the source, so we use `strconv.ParseFloat` rather than letting
   `encoding/json` coerce numerics), and injects `measured_I` /
   `measured_TP` / `measured_LRA` / `measured_thresh` / `offset`
   into the same loudnorm node before instantiating the filter
   graph. Job is run twice by the caller against the same prefix.
   FFmpeg has no flag for the shuttle itself — every documented
   two-pass loudnorm recipe wires it by hand via stderr-scraping;
   this is the orchestration sugar that makes the recipe
   declarative. Seventh Wave 1 item.
8. **`-force_key_frames "expr:gte(t,n_forced*2)"`** (§2.4) — ✅
   **done.** `Output.ForceKeyFrames` accepts the three FFmpeg
   grammars (`expr:EXPR` libavutil expression evaluated per video
   frame; `source` copy keyframes from input; comma-separated
   float-second time list) parsed by `pipeline/force_key_frames.go::
   parseForceKeyFrames` at config-load time so a malformed spec is
   rejected up-front. Per-encoder runtime state lives in
   `forceKeyFramesMatcher` (built once in `handleEncoder` from the
   encoder's time-base; expression is compiled once via
   `av.ParseExpression` so the per-frame hot loop only does
   `av_expr_eval`). The matcher is consulted in `sendOne` exactly
   once per frame in PTS order; on a match it stamps
   `frame.pict_type = AV_PICTURE_TYPE_I` via `av.Frame.SetPictType`,
   which libavcodec honours as an IDR request regardless of GOP
   cadence (faithful port of `fftools/ffmpeg_enc.c::forced_kf_apply`
   line 738). Expression vars (`n` / `n_forced` / `prev_forced_n`
   / `prev_forced_t` / `t`, mirrors ffmpeg.h:557-561) advance on
   every call, including drops, so counters track the post-rewrite
   PTS stream the encoder actually sees. New av-layer surface:
   `av.Frame.SetPictType` + `PictType` accessors and the
   `AV_PICTURE_TYPE_*` constants; `av.ParsedExpression`
   (`ParseExpression` / `Eval` / `Close`) wrapping `av_expr_parse`
   + `av_expr_eval` + `av_expr_free` for repeated evaluation.
   `compat/ffcli` parses `-force_key_frames SPEC` end-to-end.
   Eighth (and final) Wave 1 item.


### 6.2 Wave 2 — "the universal mapper" (Phase B)

9. **Negative / optional `-map`** (`-map 0:s?`, `-map -0:s`) (§2.2) —
   ✅ done. `pipeline.StreamSelect` gained `All`, `Optional`,
   `Negate`, `Program` fields. Runtime resolver
   (`pipeline/stream_selection.go::resolveStreamSelection`) walks
   selectors in declaration order, treating `Negate` as a removal
   pass and `Optional` as a silent-on-miss flag (mirrors
   `fftools/ffmpeg_opt.c::map_manual`). `compat/ffcli` parses
   `-map [-]N[:p:M][:T[:I]][?]` end-to-end via
   `compat/ffcli/map.go::parseMapArg`. Unblocks "include subtitles
   if present" with no per-job branching (see
   `testdata/examples/39_optional_subtitle.json`).
10. **Program selection (`-map 0:p:N[:type[:idx]]`)** (§2.2) —
    ✅ done. `StreamSelect.Program` matches the `AVProgram.id`
    (NOT array index — mirrors
    `cmdutils.c::check_stream_specifier`'s `p:N`). The av layer
    grew `InputFormatContext.Programs() []ProgramInfo` to expose
    the AVProgram table. Required for MPEG-TS broadcast inputs;
    done alongside #9 since they share the same struct.
11. **`KindMetadataReader` / `KindMetadataWriter` graph nodes**
    (§5#5 deferred half) — ✅ done. New `metadata_reader` /
    `metadata_writer` `pipeline.NodeDef.Type` values, connected by
    a new `metadata` edge type, route container metadata or chapters
    from any input into any output. Pipeline runtime resolves the
    pair in `applyOutputMetadata` / `applyOutputChapters` ahead of
    the `Input.MapMetadata` / `Input.MapChapters` shorthand
    fallback. `compat/ffcli` parses `-map_metadata IDX` /
    `-map_chapters IDX` into the node pair so multi-input jobs can
    route per-output independently. Validation gates: reader
    requires `params.source` matching an input id, writer requires
    `params.target` matching an output id, `params.section` ∈
    {`global`, `chapters`}.

### 6.3 Wave 3 — "modern delivery" (Phase C completion)

12. **Structured HLS / DASH / CMAF outputs with ABR `Variants`**
    (§2.5) — Promote the AVDict bag to typed fields: `hls_time`,
    `hls_playlist_type`, `dash_segment_duration`, `init_segment`.
    Gating for any commercial deployment. ✅ **done.**
    `Output.HLS *HLSOptions` + `Output.DASH *DASHOptions` land the
    full hlsenc / dashenc AVOption tables as typed fields with the
    `Options` bag retained as escape hatch (typed wins on key
    collision). CMAF = `HLS.SegmentType="fmp4"` or
    `DASH.HLSPlaylist=true`. ABR ladders continue to use the
    explicit per-encoder graph node pattern from
    [testdata/examples/35_abr_ladder.json](../testdata/examples/35_abr_ladder.json),
    bound to the playlist via the typed `MasterPlName` /
    `VarStreamMap` (HLS) or `AdaptationSets` (DASH). Smoke tests
    [41_hls_vod.json](../testdata/examples/41_hls_vod.json) +
    [42_dash_basic.json](../testdata/examples/42_dash_basic.json).
13. **BSF chains on output** (§2.5) — ✅ `Output.BSFVideo` /
    `BSFAudio` / `BSFSubtitle` accept FFmpeg chain syntax
    (`f1[=k=v[:k=v]][,f2]`) parsed by `av_bsf_list_parse_str`.
    Runtime ports `fftools/ffmpeg_mux.c::bsf_init` (par_in copy →
    time_base_in → av_bsf_init → par_out copy back → time_base_out
    adopt before `WriteHeader`); per-packet flow drains via
    `av_bsf_send_packet` / `av_bsf_receive_packet` between rescale
    and `WritePacket`; channel-close drains residuals.
14. **Color metadata + HDR10 mastering / CLL** (§2.4) — ✅ `Output.Color`
    (range / primaries / transfer / space / chroma_location, applied
    via `av_opt_set` on the output AVStream) + `Output.HDR`
    (SMPTE ST 2086 mastering display + CTA-861.3 MaxCLL/MaxFALL,
    attached as `AV_PKT_DATA_MASTERING_DISPLAY_METADATA` /
    `AV_PKT_DATA_CONTENT_LIGHT_LEVEL` on stream codecpar.coded_side_data
    via `av_packet_side_data_add` before `WriteHeader`). Schema-time
    validation rejects HDR + audio-only outputs, HDR + non-HDR codecs
    (only hevc/av1/vp9 or copy), HDR + non-HDR-capable containers
    (only mp4/mov/matroska/webm/mpegts), and color.transfer ∉
    {smpte2084 (PQ), arib-std-b67 (HLG)} when paired with HDR.
    `compat/ffcli` parses `-color_range`, `-color_primaries`,
    `-color_trc`, `-colorspace`, `-chroma_sample_location`,
    `-mastering_display_metadata` (canonical x265
    `G(x,y)B(x,y)R(x,y)WP(x,y)L(max,min)` grammar) and
    `-content_light_level "MaxCLL,MaxFALL"`. End-to-end coverage
    in [44_hdr10.json](../testdata/examples/44_hdr10.json).
15. **`setsar` / `setdar` shorthand on `Output`** (§3.3.9) — ✅ done.
    `Output.SAR` / `Output.DAR` accept the canonical `A:B`, `A/B`,
    or decimal-float forms (parsed by `pipeline.parseAspectRatio`,
    which mirrors `av_parse_ratio`). `SAR` is written verbatim onto
    the encoder's `sample_aspect_ratio` (and propagated to
    `AVStream.codecpar.sample_aspect_ratio`); `DAR` is resolved to
    SAR using the encoder's just-decided width/height (SAR_num/den
    = (DAR_num × H) / (DAR_den × W)) so the canonical legacy SD
    shapes (DV-PAL 720×576 @ 4:3 → SAR 16:15; NTSC 720×480 @ 4:3 →
    SAR 8:9; HD square pixels 1920×1080 @ 16:9 → SAR 1:1) all fall
    out of the plumbing for free. Mutually exclusive at validate
    time. `compat/ffcli` rewrites the legacy `-aspect A:B` to
    `Output.DAR` (per §6.8) and accepts `-setsar` / `-setdar` as
    explicit aliases. New av-layer surface: `EncoderOptions
    .SampleAspectRatio` is plumbed into `AVCodecContext
    .sample_aspect_ratio` in `OpenEncoder`. End-to-end coverage
    in [45_setdar_shorthand.json](../testdata/examples/45_setdar_shorthand.json)
    plus `TestApplyDARShorthand` / `TestApplySARShorthand`
    (ffprobe-asserts the muxed-in SAR matches 16:15 for DV-PAL
    and 8:9 for NTSC respectively).

### 6.4 Wave 4 — "hardware everywhere" (Phase D)

16. **`-init_hw_device` + per-node `device:` selector** (§3.4.5) —
    Mandatory for any mixed-vendor pipeline (CUDA decode → CPU
    filter → QSV encode).
17. **Per-filter availability probe** (`scale_npp` vs `scale_cuda`)
    (§3.4.6) — Already partially done for codecs; same harness
    pattern.
18. **Hardware filter auto-mapping** (`scale` ↔ `scale_cuda` /
    `scale_npp` / `scale_qsv` / `scale_vt`) (§2.3) — The GUI's
    "this will run on GPU" indicator depends on this.

### 6.5 Wave 5 — "expression authoring polish" (Phase E)

19. **`expression: true` AVOption flag bit** (§3.1.6.a; deferred
    from §5#7) — Mine `AV_OPT_FLAG_*` via `av_opt_next`; wire into
    schema so the GUI knows which fields render the expression
    input.
20. **Syntax-highlighted GUI expression input** (§3.1.6.b, §3.5.8) —
    Live-validates against the eval-expression endpoint shipped in
    §5#7. Cookbook UI for top-5 patterns
    (between/scroll/frame-stamp/fade-gate/conditional).

### 6.6 Wave 6 — "the editorial round-trip" (Phase C.8)

21. **Lossless intermediate validation harness** (FFV1/MKV,
    ProRes/MOV, DNxHR/MXF) (§3.3.8) — Single test exercising
    decode → intermediate → decode → final, asserting no quality
    loss. Catches container/encoder compatibility bugs systemically.
22. **Asset / model-file manager** (§3.5.10) — Symbolic asset
    references (fonts, RNNoise models, YOLO weights, fontsdir for
    ASS). Touches schema, GUI, runtime; can run in parallel with
    Wave 1 (no engine dependencies).

### 6.7 Cross-cutting accelerators (parallel with all waves)

- **Capability-registry CI gate** — every PR touching
  `pipeline.Config` must update `compat/capabilities.yaml` or the
  build fails. Registry exists (§5#4); add the gate.
- **`compat/ffcli` round-trip oracle expansion** — every Wave 1 item
  ships ≥ 3 round-trip cases against real `ffmpeg(1)` (codec, stream
  count, duration ±0.5s, SSIM ≥ 0.99). Harness exists; keep adding
  fixtures.
- **CLI export (JSON → ffmpeg command line)** (§3.5.7) — strongest
  correctness signal we can build. Land as soon as Wave 1 #3 lands,
  because per-stream syntax is where it gets interesting.

### 6.8 Suggested deprecations / out-of-scope

Mark these `out-of-scope` in the capability registry rather than chase
them. Importer (`compat/ffcli`) may still accept the legacy spelling
and rewrite it.

| Flag(s) | Rationale |
|---|---|
| `-fpre`, `-vpre`, `-spre` (encoder presets from disk) | Superseded by encoder AVOptions with named values. The GUI's per-encoder inspector already does what `-vpre` did. |
| `-vsync` (legacy alias) | Deprecated upstream. Implement only the modern `-fps_mode` (Wave 1 #1). Importer rewrites; no schema field. |
| `-deinterlace` (legacy global flag) | Deprecated upstream since 2013 in favour of `yadif`/`bwdif`/`w3fdif` filters. Importer rewrites to `yadif`. No schema field. |
| `-target` (DVD/VCD/PAL presets) | Targets formats that are commercially dead. Importer can expand the macro; no GUI surface. |
| `ffplay`-style interactive viewers (`scopes`, `ebu-meter`) | Already out-of-scope per §1. GUI may grow live monitoring but shouldn't pretend to be `ffplay`. |
| `-xerror`, `-stats`, `-stats_period` | MediaMolder has its own progress/error event bus; don't mirror the CLI flags. |
| `clip-time`, `scene-time`, `sexagesimal-time` (CLI utilities) | Move to a future `mediamolder util` subcommand if demand surfaces. Not engine work. |
| `-bsf` shorthand without `:stream_specifier` | Importer normalises to `-bsf:v`. No deprecated form in schema. |
| `-aspect` (encoder side) | Subsumed by `Output.SAR` / `Output.DAR` (Wave 3 #15). Don't ship two ways to spell the same thing. |
| `image2`'s `%d`-pattern globbing for **inputs** | Already side-stepped by `mjpeg` muxer choice in §5#2. For inputs, accept only explicit `-pattern_type glob` / `sequence`; reject `printf`-style patterns at schema validation as a footgun. |
| Decklink / NDI **GUI** wizards | Keep the URL handlers (no work needed) but don't build dedicated inspectors until customer demand. AVDict passthrough is acceptable indefinitely for these. |
| `-streamid`, `-bitexact`, `-tag` | Edge cases for spec-conformance testing. Ship as AVDict, never promote. |

#### 6.8.1 Rejected deprecations (refactor as needed)

These parameters were suggested to be deprecated, but should be supported and refactored for mediamolder.
| Flag(s) | Rationale |
|---|---|
| `-psnr`, `-ssim` (encoder side) | Tells encoder to calculate these distortion metrics while encoding (which is much more efficient than calculating after encoding) |
| `-tune <macro>` for x264/x265 when codec-specific `*-params` already covers it | Importer flattens `-tune` into the relevant `*-params` string. |
| `-dump`, `-hex`, `-debug_ts` | Pure debugging; route to MediaMolder's logging instead. |
