# FFmpeg Coverage Roadmap

**MediaMolder must be able to express, run, and GUI-author any job that an FFmpeg command line can express.**

> Companion to [roadmap.md](roadmap.md), which is phase-based. This document is **capability-based** — it enumerates the FFmpeg surface area, marks what MediaMolder covers today, and prioritises the gaps.
>
> **Document structure:** §1–§2 form the *gap assessment* — §1 explains how gaps were identified (community-scripts corpus and production-pattern review, detailed below), §2 catalogues the full FFmpeg surface area. §3–§4 define the strategy. §5 is the completed *initial backlog* (all 8 items done). §6 is the *ongoing wave plan* — Waves 1–4 (items 1–22) are complete; Waves 5–9 (items 23–55) close every remaining non-deprecated CLI option and GUI gap; Wave 10 (items 56–60) deliberately defers hardware acceleration until everything else lands; Wave 11 (items 61–67) closes live-capture GUI, cover art embedding, attachment stream mapping, model-file filter assets, and network-source schema validation.

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
| `waveform`                                                      | **Cross-media-type filters** (audio→video: `showwavespic`, `showspectrumpic`) wired through MediaMolder's encoder selection | ✅ Done — `output_media_type` field + cross-media registry (Wave 7 #37); fixture `50_audio_waveform.json` |
| `clip-time`, `scene-time`, `sexagesimal-time`                   | Pure CLI utilities — out of scope for the engine, in scope for a future `mediamolder util` subcommand | out of scope |
| `ebu-meter`, `scopes`                                           | ffplay-based interactive viewers — out of scope for the engine, possibly in scope for the GUI | out of scope |

These eight underlined capability gaps are the **first wave** of the roadmap below. They are sampled from a tiny corner of FFmpeg usage (20 hand-written shell scripts), so they should be treated as a representative *minimum*, not as a complete list.

### 1.1 Second corpus: production-pattern command lines

A review of typical production FFmpeg usage (animated `drawtext`, two-pass `loudnorm`, multi-resolution split-and-mux ABR, hardware pipelines mixing CUDA + NPP + NVENC, HDR `zscale`/`tonemap`, `minterpolate` slow-motion, RNNoise speech denoise, mixed labelled/unlabelled `-filter_complex` outputs) surfaced a second wave of gaps that the 35-script community corpus does not exercise:

| Gap                                                            | Note |
|----------------------------------------------------------------|------|
| **Filter expression engine** (`enable=between(t,2,8)`, `x=w-tw*t/5`, `t`, `n`, `frame`, `tw`, `th`, `text_w`, `text_h`) | ✅ Done — `eval-expression` validator (§5 #7), `expression: true` flag mining (Wave 4 #19), `ExpressionInput` GUI control with cookbook + live validation (Wave 4 #20). |
| **Two-pass `loudnorm`**                                        | ✅ Done — `Output.LoudnormPass` / `Output.LoudnormStatsFile` carry the EBU R128 shuttle. Pass 1 sets `print_format=json`+`stats_file` on every loudnorm filter so libavfilter writes input_i/tp/lra/thresh/target_offset to a JSON file (`af_loudnorm.c::uninit`); pass 2 reads it and injects `measured_I/TP/LRA/thresh`+`offset` AVOptions. No FFmpeg flag — orchestration sugar above the manual two-run recipe. |
| **`setsar` / `setdar` and explicit SAR/DAR encoding**          | ✅ Done — `Output.SAR` / `Output.DAR` shorthand with canonical SD shapes auto-derived; Wave 3 #15. |
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
| `-framerate`, `-r` (input override)                         | ✅    | `Input.FrameRate` (typed `float64` fps); ffcli `-framerate` and `-r`-before-`-i` latch into pendingFileOpts and drain into the typed field; rejected when `<= 0` |
| `-pix_fmt`, `-video_size`, `-pixel_format`                  | ✅    | `Input.PixelFormat` and `Input.VideoSize` (`WxH` or libavutil named preset); ffcli `-pix_fmt`/`-pixel_format`/`-s`/`-video_size` route to the input when seen before `-i` and to the encoder otherwise |
| `-f` (force demuxer)                                        | ✅    | `Input.Format` is now first-class for arbitrary demuxers (`mpegts`, `mxf`, `rawvideo`, `s16le`, …). `Input.Kind = "raw"` is auto-detected by ffcli when `-f` names a known raw audio/video format; `Kind = "lavfi"` and `Kind = "concat"` similarly auto-detected |
| `-thread_queue_size`                                        | ✅    | `Input.ThreadQueueSize` (validated `>= 0`) |
| `-accurate_seek` / `-noaccurate_seek` / `-seek_timestamp`   | ✅    | `Input.AccurateSeek` (`*bool`; only emits `accurate_seek=0` to libav when explicitly false, matching FFmpeg default) and `Input.SeekTimestamp` (bool) |
| `-protocol_whitelist`                                       | ✅    | `Input.ProtocolWhitelist []string`; comma-joined into the demuxer AVDict at open time |
| Lavfi virtual sources (`-f lavfi -i color=…`)               | ✅    | `Input.Kind = "lavfi"`; `URL` carries the filtergraph spec. libavdevice linked + `avdevice_register_all()` at init |
| `image2` glob pattern (`-i 'frames/*.png'`)                 | ✅    | `Input.PatternType` (`""`/`"none"`/`"sequence"`/`"glob"`/`"glob_sequence"`); validated against the libavformat enum |
| `concat` demuxer (listfile)                                 | ✅    | `Input.Kind = "concat"` + `Input.ConcatList []ConcatEntry` (file/duration/inpoint/outpoint/metadata). `pipeline.materialiseConcatList` writes an `ffconcat 1.0` listfile to a temp path, opened with `format="concat"`; cleanup runs at input close. Apostrophes/newlines in filenames are rejected up front |
| Device capture (`-f avfoundation`, `-f dshow`, `-f v4l2`)   | ⚠️    | Works through AVDict; no GUI palette, no probe (Wave 11 #61–63) |
| `-hwaccel`, `-hwaccel_device`, `-hwaccel_output_format`     | ✅    | Per-input via `Input.HWAccel`, `Input.HWAccelDevice`, `Input.HWAccelOutputFormat`; `hwaccel_device` may name a pre-declared `hardware_devices` entry. (Wave 10 #59) |

### 2.2 Stream selection / mapping

| FFmpeg flag(s)                                | Status | Note |
|-----------------------------------------------|:------:|------|
| Default automatic stream selection            | ✅    | "best video + best audio" implied if user picks `track: 0` |
| `-map 0:v:0` / `-map 1:a:0` style             | ✅    | `pipeline.StreamSelect.{InputIndex,Type,Track}`; ffcli `-map` parser (Wave 2 #9) |
| Negative / optional mapping (`-map -0:s`, `-map 0:s?`) | ✅ | `StreamSelect.{Negate,Optional}`; done Wave 2 #9 |
| Program selection (`-map 0:p:N[:type[:idx]]`) | ✅    | `StreamSelect.Program` (matches `AVProgram.id`); done Wave 2 #10 |
| `-map_metadata`, `-map_chapters`              | ✅    | `metadata_reader` / `metadata_writer` graph nodes + `Input.MapMetadata` / `Input.MapChapters` shorthand; done Wave 2 #11 |
| `-vn` / `-an` / `-sn` / `-dn` per output      | ✅    | `Output.DisableVideo`/`DisableAudio`/`DisableSubtitle`/`DisableData` drop every inbound edge of the corresponding media type at the sink before `expandImplicitEncoders` runs (mirrors fftools/ffmpeg_opt.c L1977/2078/2115/2187 — the OPT_OUTPUT half of the dual-purpose disable bools). Validator rejects all-four-set. |
| Reuse of one decoded stream by N filters/outputs (`split`/`asplit`) | ✅ | Works via multi-output filters |
| Per-input `-map` of *attachment* streams      | ❌    | (see §2.5 attachments; Wave 11 #65) |

§2.2 is now covered for all four common selector grammars (track, all-of-type, optional, negate, program). FFmpeg's full `-map` grammar also supports `m:KEY[:VALUE]` metadata-based filters and `M:i:N` id-based selection, which remain out of scope; both have negligible real-world usage in the §6 corpus.

### 2.3 Filtergraph

| Capability                                                  | Status | Note |
|-------------------------------------------------------------|:------:|------|
| Simple filter chains (1-in, 1-out)                          | ✅    | `pipeline/handlers.go` simple path |
| Complex filtergraphs (N-in, M-out)                          | ✅    | Same file, complex path via `avfilter_graph_parse_ptr` |
| Multi-input filters (`overlay`, `concat`, `hstack`, `amix`) | ✅    | Demonstrated by 09–10, 14 community scripts |
| Multi-output filters (`split`, `asplit`, `tile`)            | ✅    | |
| Source/virtual filters (`color=`, `testsrc=`, `anullsrc=`, `sine=`, `smptebars=`, `movie=`, `amovie=`) | ✅ | `KindFilterSource` (Wave 7 #36a enum; #36b av-layer; #36c runtime). `movie=`/`amovie=` get security validation + `protocol_whitelist` injection (#36e). GUI palette deferred to Wave 8 #44. |
| Sink filters (`nullsink`, `anullsink`)                       | ✅    | `KindFilterSink` (Wave 7 #36a enum; #36b av-layer; #36d runtime). Pure-analyser pipeline (no muxer output) now valid. |
| Cross-media-type filters (`showwavespic`, `showspectrum*`, `concat=v=1:a=1`) | ✅ | `output_media_type` field + `crossMediaTypeFilters` registry + validator + auto-fill + complex-graph dispatch (Wave 7 #37). `concat=v=1:a=1` multi-pad routing (per-output-pad lists) deferred. |
| Frame-rate / time-base advertised on `FilterPadConfig`      | ✅    | `FRNum/FRDen` added; `make_video_src_args` emits `frame_rate=N/D`; buffersink rate re-queried after each upstream filter. Unblocked `xfade`/`acrossfade` (13/14 community-scripts now pass). |
| `-filter_complex_threads`                                   | ✅    | Per-graph thread cap (Wave 7 #38) |
| `-filter_threads`                                           | ✅    | Per-node override via `NodeDef.Threads`; pipeline-wide cap via `Config.FilterComplexThreads`. Both map to `AVFilterGraph.nb_threads`. (Wave 7 #38) |
| Filter quoting (`,`, `;`, `'` in values)                    | ✅    | Fixed in commit `04f1a0c7` (`pipeline/engine.go` `buildFilterSpec`) |
| Sidedata / per-frame metadata propagation                   | ✅    | `AVFrame->metadata` propagates through libavfilter natively; `metadata`/`ametadata` filters wired as regular `filter` nodes; av-layer `Frame.Metadata()`/`GetMetadata`/`SetMetadata` exposed for Go processors (Wave 7 #39). Fixture `51_metadata_filter.json`. |
| Hardware filter auto-mapping (sw `scale` → `scale_cuda` etc.) | ✅  | Per-node `auto_map_hw` promotes sw filter to hw equivalent + inserts hwupload/hwdownload at device boundaries. 21 mappings: CUDA/VAAPI/QSV/VT/Vulkan/OpenCL. (Wave 10 #58) |
| `hwupload`, `hwdownload`, `hwmap` filters                   | ⚠️    | Available via filter name, no first-class palette |
| **Filter expression engine** (`t`, `n`, `frame`, `tw`, `th`, `text_w`, `text_h`, `w`, `h`, `enable=between(t,2,8)`, arithmetic) | ✅ | Strings reach libavfilter intact; `GET /api/filters/{name}/eval-expression` validator (§5#7); `expression: true` AVOption flag bit + curated per-filter variable registry (Wave 4 #19); syntax-highlighted `ExpressionInput` GUI control with cookbook + live validation (Wave 4 #20). |
| **Mixed labelled + unlabelled `-filter_complex` outputs**   | ✅    | `compat/ffcli.NormalizeFilterComplex` rewrites dangling pads to synthetic `[mm_fc_out_N]`/`[mm_fc_in_0]` labels; idempotent (Wave 7 #40). Full `-filter_complex` parse → node+edge wiring deferred to Wave 8 #53. |
| `setsar`, `setdar` (SAR/DAR overrides)                      | ✅    | `Output.SAR` / `Output.DAR` shorthand; `compat/ffcli` rewrites legacy `-aspect`. Wave 3 #15. |
| `arnndn` (RNNoise) and other model-file filters             | ⚠️    | Filter runs if model path is correct; no fixture story for filter-side data files |
| `zscale` + `tonemap` (HDR)                                  | ✅    | Validator (`pipeline.validateFilterAvailability`) rejects unknown / unbuilt filters with an actionable hint (`zscale` → `--enable-libzimg`, `tonemap_opencl` → `--enable-opencl`, …) instead of waiting for a runtime "filter not found". Palette (`/api/nodes`) only lists filters reported by `av.ListFilters()`, so unbuilt entries are absent automatically. (Wave 7 #42) |
| `minterpolate` (motion-compensated interpolation)           | ✅    | Frame-rate / time-base plumbing done in §5 #1; the AVOption miner (§4 #19) exposes `mi_mode` / `mc_mode` / `me_mode` / `me` (and `vsbmc`) as typed `int` options carrying their named constants — the GUI Inspector renders them as enum dropdowns. (Wave 7 #43) |
| Audio channel manipulation: `pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix=weights` | ✅ | Backend works (Wave 7 #41); GUI channel-routing matrix done (Wave 8 #49): `PanForm` gain matrix, `ChannelMapForm` source dropdowns, `ChannelSplitForm` layout selector, `JoinForm` stream+channel pickers, `AMergeForm` input count, `AMixForm` weighted mix |

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
| Per-stream encoder options (`-b:v:0` ≠ `-b:v:1` in ABR ladders)   | ✅    | `Output.Streams[].Encoder *EncoderOverride` (`Codec`, `Options`); ffcli round-trips `-b:v:0`/`-crf:v:1`/`-preset:v:0`/`-c:v:1` etc.; Wave 6 #30. |
| Color metadata on encoder (`-color_range`, `-color_primaries`, `-color_trc`, `-colorspace`, `-chroma_sample_location`) | ✅ | `Output.Color` first-class + validated; Wave 3 #14. |
| HDR10 mastering display + content light level metadata            | ✅    | `Output.HDR` (SMPTE ST 2086 + CTA-861.3); validated against codec/container; Wave 3 #14. |
| Dolby Vision RPU passthrough                                      | ✅    | Stream-level `AVDOVIDecoderConfigurationRecord` muxed via `AV_PKT_DATA_DOVI_CONF` (mp4/mov/matroska). Per-frame RPU NAL injection out of scope — bitstream RPU NALs must already be present (`-c:v copy` or encoder-emitted). |
| `-aspect`                                                         | ✅    | Subsumed by `Output.SAR` / `Output.DAR` (Wave 3 #15); importer rewrites legacy `-aspect`. |
| `-enc_time_base`                                                  | ✅    | `Output.EncoderTimeBase` (`demux`/`filter` sentinels or `N/D` rational); Wave 6 #33. |
| Field order (`-field_order`), interlaced encode                   | ✅    | `Output.FieldOrder` enum + `Output.InterlacedEncode` (`AV_CODEC_FLAG_INTERLACED_DCT\|ME`); Wave 6 #33. |
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
| Attachments (fonts for ASS, cover art)                            | ✅    | `Output.Attachments []Attachment` ({path, filename?, mimetype?}); muxed as `AVMEDIA_TYPE_ATTACHMENT` streams in matroska / mkv / webm. ffcli `-attach FILE`. (Wave 6 #31) |
| Cover art / thumbnail embed in MP4/M4A                            | ✅    | `Output.CoverArt string`; `AVMEDIA_TYPE_VIDEO` stream with `AV_DISPOSITION_ATTACHED_PIC`; allow-list: mp4/m4a/mov/mp3/mkv; GUI file-picker; `compat/ffcli` import/export. (Wave 11 #64) |
| Multiple outputs in one pipeline                                  | ✅    | Multiple `Output` entries |
| **`tee` muxer / single-pass multi-format** (`mp4 + hls + dash`)   | ✅    | `Output.Kind="tee"` + typed `Output.Targets[]`; Wave 1 #5. |
| HLS muxer (`hls_time`, `hls_playlist_type`, EXT-X-MAP, byte-range, low-latency) | ✅ | `Output.HLS *HLSOptions` typed (full hlsenc table); Wave 3 #12. |
| DASH muxer (representations, adaptation sets, init segment)       | ✅    | `Output.DASH *DASHOptions` typed (full dashenc table); Wave 3 #12. |
| Segment muxer / fragmented MP4 (CMAF) / `movflags=+faststart`     | ✅    | CMAF via `HLS.SegmentType="fmp4"` or `DASH.HLSPlaylist=true`; `movflags` first-class; Wave 3 #12. |
| `-muxdelay`, `-muxpreload`, `-copyts`, `-start_at_zero`, `-avoid_negative_ts` | ✅ | All five covered. `Config.CopyTS` + `Config.StartAtZero` carry the global flags (StartAtZero modulates CopyTS — re-enables the demuxer ts_offset shift even under -copyts; mirrors fftools/ffmpeg_demux.c L486). `Output.MuxDelay`/`Output.MuxPreload` (float seconds) render into the muxer AVDict as `max_delay`/`preload` in AV_TIME_BASE microseconds (mirrors fftools/ffmpeg_mux_init.c L3444/L3447). `Output.AvoidNegativeTS` ∈ {auto, disabled, make_non_negative, make_zero} passes through to libavformat's avoid_negative_ts AVOption. |
| Bitstream filter chains on output (`-bsf:v "h264_mp4toannexb,h264_redundant_pps"`) | ✅ | Chain syntax parsed by `av_bsf_list_parse_str`; per-stream-type via `BSFVideo`/`BSFAudio`/`BSFSubtitle` |

### 2.6 Subtitles

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| Passthrough (text and bitmap)                                     | ✅    | Demonstrated by `18_subtitle_add` |
| Burn-in via `subtitles=` filter                                   | ✅    | (works once libass is available in the build) |
| Codec conversion (`mov_text` ↔ `srt` ↔ `ass` ↔ `webvtt`)          | ⚠️    | Works through encoder selection; GUI now warns on known-incompatible codec+container pairs (Wave 8 #52) |
| Subtitle charset (`-sub_charenc`)                                 | ✅    | `Input.SubtitleCharenc` (Wave 6 #34); charset picker in Input inspector (Wave 8 #52) |
| Forced / hearing-impaired flags                                   | ✅    | `Output.Streams[].Disposition` carries `AV_DISPOSITION_FORCED` / `AV_DISPOSITION_HEARING_IMPAIRED` (Wave 1 #3); `compat/ffcli` parses `-disposition:s:v:0` (Wave 2 #9); dedicated Forced + HI checkboxes in `StreamSpecForm` for `type=s` streams (Wave 8 #52). |
| Karaoke ASS effects, fontconfig integration                       | ⚠️    | Filter passes through; no GUI affordance |

### 2.7 Devices, networking, advanced

| Capability                                                        | Status | Note |
|-------------------------------------------------------------------|:------:|------|
| RTP / RTSP / RTMP / SRT / RIST / NDI input/output                 | ⚠️    | Works through libavformat URL handlers; no schema validation, no GUI (Wave 11 #67) |
| Screen capture (`avfoundation`, `gdigrab`, `x11grab`)             | ⚠️    | Same; see Wave 11 #61–63 |
| Decklink SDI input/output                                         | ⚠️    | Same |
| `ffprobe` equivalence (stream summary)                            | ⚠️    | `/api/probe` exists but does not expose every probe field |
| Tee muxer (see §2.5)                                              | ✅    | `Output.Kind="tee"` + `Output.Targets[]` (Wave 1 #5) |
| Dynamic per-frame metadata via ZMQ filter                         | ❌    | |
| **`-init_hw_device` (multi-device graphs)**                       | ✅    | `Config.HardwareDevices []HardwareDevice` ({name, type, device?, options?}) + `NodeDef.Device` selector; opened via `av_hwdevice_ctx_create` at pipeline start. Parse/export via `compat/ffcli`. (Wave 10 #56) |
| **`scale_npp` availability separate from `scale_cuda`**           | ⚠️    | Different libraries; needs per-filter availability probe at startup |
| **First-class raw-stream input** (`-f rawvideo -pix_fmt yuv420p -s 1920x1080 -r 30 -i raw.yuv`) | ✅ | `Input.Kind = "raw"` + typed `Format`/`PixelFormat`/`VideoSize`/`FrameRate`/`SampleRate`/`Channels`/`SampleFormat`. Validated up front (raw inputs require `Format` plus the matching geometry/format fields). Round-trip-tested via `compat/ffcli` and `testdata/community-scripts/27_raw_yuv.json` |

### 2.8 Frontend GUI gaps (in addition to schema gaps)

The GUI cannot be more powerful than the schema. Once §2.1–§2.7 are
filled, the GUI also needs:

- A palette section for **virtual source nodes** (color/testsrc/sine/anullsrc).
- A **multi-output inspector** that shows all `Output` entries in one pane, with per-stream encoder tabs.
- **BSF chain editor** (sortable list, not single field).
- **Chapter / metadata editor** at the output level (table of `(start, end, title)` for chapters; key/value table for metadata, with per-stream tabs).
- ✅ **HLS / DASH / Tee output wizards** — `HLSForm` (segment timing, playlist type, mpegts/fmp4 segment type, filename templates, `var_stream_map` ABR builder, `hls_flags` checkboxes), `DASHForm` (segment/fragment durations, SegmentTemplate/SegmentTimeline tri-state toggles, LL-DASH booleans, `dash_flags`), `TeeForm` (collapsible per-target rows with URL, format, stream-select, BSF chain, `onfail`, FIFO options, key/value overrides) — Wave 8 #48 ✅.
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
5. HLS / DASH / tee output wizards. ✅ Wave 8 #48 — done.
6. Hardware-filter mapping indicator + multi-device picker.
7. Bidirectional FFmpeg-CLI conversion (existing `compat/ffcli` import + new export). The CLI export is the round-trip oracle for
   the entire schema and should be wired into the existing job-save flow as a "Show as ffmpeg command" panel.
8. **Filter expression authoring**: monospace input with `t`/`n`/`tw`/ `th`/`text_w`/`text_h`/`w`/`h` autocomplete, live
   syntax-validation against the server-side `eval-expression` endpoint, and a small expression cookbook (scrolling text, fade gates, frame-stamp overlays). ✅ Wave 8 #50 — done. Variable autocomplete dropdown (↑/↓/Enter/Tab/Esc keyboard navigation, functions tinted purple, variables tinted teal), context-aware eval preview using upstream pad hints (width, height, fps, sample_rate extracted from the first probed input node upstream), and expanded cookbook (5 → 19 patterns: timeline gates, PTS speed controls, drawtext positioning, overlay centering, crop/pad centering, volume fades, `select` keyframes, `zoompan` Ken Burns). Backend registry expanded from 10 to 16 filters (`select`, `aselect`, `hue`, `geq`, `trim`, `atrim`).
9. **Audio channel-routing UI**: a bus/matrix view for `pan`, `channelsplit`, `channelmap`, `join`, `amerge`, `amix`. ✅ Wave 8 #49 — done. `AudioChannelForm` replaces the free-form params dict with a 2-D gain matrix (pan), per-output-channel source dropdowns (channelmap), layout selector (channelsplit), stream+channel pickers (join), input-count spinner (amerge/amix), and per-input weight fields (amix).
10. **Asset/model-file manager**: shared by the YOLO processor and by filters such as `arnndn`, `subtitles=…:fontsdir=…`. Pipelines
    should reference assets by symbolic name, with the GUI managing paths and the runtime resolving them from a search list.
    ✅ Wave 8 #51 — done. `Config.Assets map[string]AssetRef`; runtime resolves `$asset:<name>` refs in filter params via
    `MEDIAMOLDER_ASSET_PATH` search; `AssetManager` GUI panel manages the registry.

### 3.6 Phase F — proof of universality

1. **FFmpeg manual conformance suite.** Every example command in `ffmpeg-doc.html` becomes a test case. Pass criterion: same
   container, same stream count, same per-stream codec, output bytes within tolerance, SSIM ≥ 0.99, audio loudness within ±0.5 LU.
2. **Production-pattern conformance suite.** A second corpus assembled from the production-pattern command lines catalogued in §1.1 —
   animated `drawtext`, two-pass `loudnorm`, multi-resolution split-and-mux ABR, full GPU pipelines, HDR `zscale`/`tonemap`, `minterpolate` slow-mo, RNNoise, mixed labelled/unlabelled `-filter_complex` outputs, raw-stream inputs, lossless intermediates. Same pass criteria as §3.6.1.
3. **Random-corpus fuzzer.** Generate random valid ffmpeg command lines from a grammar derived from the capability registry; run both ffmpeg and `mediamolder run --import-cli ...`; diff outputs.
4. **Capability registry coverage gate.** No PR can merge that adds a new flag to the registry without also adding either a schema
   field marking it `covered` or an explicit `out-of-scope` rationale.
5. **Quoting / escaping fuzzer.** Targeted at `pipeline/engine.go` `buildFilterSpec` and at the `compat/ffcli` lexer. Generate filter-graph specs containing every combination of `,`, `;`, `'`, `"`, `\`, `:`, `=`, `[`, `]`, and unicode whitespace; assert that `parse → spec → libavfilter → re-parse` is idempotent. The `04f1a0c7` quoting fix is the first known instance of a class of bugs that only fuzzing will surface.

## 4. Cross-cutting principles

- **Library-first.** Every feature has to be a real binding in `av/`, not a string forwarded blindly to AVDict. AVDict passthrough is a legitimate transitional state but it should be tracked in the registry as `partial` and burned down.
- **Schema-validated.** Every new schema field needs matching JSON Schema entries in `schema/v1.0.json` and `schema/v1.1.json`, and matching TS types in `frontend/src/lib/jobTypes.ts`. The existing `TestSchemaSyncWithGoStructs` is the enforcement mechanism.
- **GUI must be able to author it.** A schema field that the GUI cannot produce or edit is a schema field nobody will use. Every Phase-B/C/D feature has a GUI deliverable in Phase E.
- **The oracle is FFmpeg.** Round-trip CLI ↔ JSON conversion plus byte/SSIM/loudness comparison against ffmpeg is what defines "covered". Anything not in a regression test will regress.

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

### 6.4 Wave 4 — "expression authoring polish" (Phase D)

19. **`expression: true` AVOption flag bit** (§3.1.6.a; deferred
    from §5#7) — ✅ done. FFmpeg has no `AV_OPT_FLAG_EXPRESSION`
    bit, so the implementation has two halves: (a) the av layer
    now exposes the raw `AVOption.flags` bitfield + every
    decoded `AV_OPT_FLAG_*` bit (`IsEncodingParam` /
    `IsDecodingParam` / `IsAudioParam` / `IsVideoParam` /
    `IsSubtitleParam` / `IsExport` / `IsReadOnly` / `IsBSFParam` /
    `IsRuntimeParam` / `IsFilteringParam` / `IsDeprecated` /
    `IsChildConsts`) on every `EncoderOption` returned by
    `EncoderOptionsByName` and `FilterOptionsByName`; (b) a
    curated `(filter, option) → expression-typed` registry lives
    in [internal/gui/filter_eval.go](../internal/gui/filter_eval.go)
    (`filterExprOptions`, paired with the existing
    `filterExprVars` table that the eval-expression endpoint
    already uses, so a single source of truth feeds both the GUI
    Inspector and the validator). The
    `GET /api/filters/{name}/options` handler annotates matching
    `EncoderOption`s with `Expression: true` + `Variables: [...]`
    on the way out the door, mirrored in `frontend/src/lib/
    encoderSchema.ts`'s `EncoderOption` type. Ten well-known
    expression-typed pairs registered today (drawtext.x/.y/
    .text_x/.text_y/.box_w/.box_h/.fontsize/.alpha/.enable;
    overlay.x/.y/.enable; crop.x/.y/.w/.h/.out_w/.out_h/.enable;
    scale.w/.h/.width/.height; pad.w/.h/.x/.y/.enable; rotate.angle/
    .a/.out_w/.ow/.out_h/.oh/.enable; zoompan.zoom/.z/.x/.y/.d/
    .fps/.enable; setpts.expr; asetpts.expr; volume.volume/.enable).
20. **Syntax-highlighted GUI expression input** (§3.1.6.b, §3.5.8) —
    ✅ done. New
    [frontend/src/components/controls/ExpressionInput.tsx](../frontend/src/components/controls/ExpressionInput.tsx)
    renders a transparent `<textarea>` over a styled `<pre>`
    overlay (no Monaco / CodeMirror dependency) that
    syntax-colours functions vs. variables vs. numbers vs.
    operators by tokenising the input against the curated
    libavutil function list (`abs`, `between`, `if`, `mod`,
    `lt`, `gte`, …) and the per-filter variable list shipped on
    the option schema. Unknown identifiers get a red wavy
    underline so the user gets immediate feedback before the
    round-trip. A 250 ms-debounced background fetch hits
    `GET /api/filters/{name}/eval-expression?expr=...` (the
    endpoint shipped in §5#7) under default-zero variable
    bindings; the response's `ok` / `value` / `error` is
    surfaced inline beneath the input (green `= <value>` on
    parse-success, red message on libavutil rejection). A
    cookbook `<select>` exposes the five canonical patterns
    called out in the roadmap (between / scroll / frame-stamp /
    fade-gate / conditional) which are inserted at the cursor
    position. The control is wired into `OptionControl` on a
    per-`(filter, option)` basis: when the schema marks the
    option as `expression: true` and the form supplies the
    `filter` prop (currently `FilterForm` does so), the
    `ExpressionInput` is rendered in place of the plain text
    input — every other AVOption rendering path is unchanged.


### 6.5 Wave 5 — "input fidelity" (Phase A burn-down)

Promote every common input-side AVDict passthrough to a typed
schema field so the importer/exporter round-trip and the GUI both
have a name for it. No new orchestration — pure schema + validation.

23. **Typed input source parameters** (§2.1) — ✅ Wave 5. `Input.FrameRate`
    (replaces `-framerate`/`-r` on the input), `Input.PixelFormat`
    (`-pix_fmt`/`-pixel_format`), `Input.VideoSize` (`-video_size`,
    `WxH` or named preset), `Input.SampleRate`, `Input.Channels`,
    `Input.SampleFormat` (audio twins). Validated at config-load by
    `pipeline.validateInputDemuxerFields`; `compat/ffcli` latches the
    legacy spellings into `pendingFileOpts` and routes them to the typed
    fields at the next `-i` boundary (so the same flag can mean "input
    override" or "encoder option" depending on position).
24. **Force-demuxer for arbitrary formats** (§2.1) — ✅ Wave 5.
    `Input.Format` is now first-class. `compat/ffcli` auto-promotes
    `-f rawvideo`/`-f s16le` etc. to `Input.Kind = "raw"`, `-f lavfi`
    to `lavfi`, `-f concat` to `concat`. Other format names are kept
    as a typed `Input.Format` string and passed to
    `av.OpenInputWithFormat`.
25. **First-class raw-stream input** (§1.1, §2.7) — ✅ Wave 5.
    `Input.Kind = "raw"` is the documented composite shape and
    requires `Format` plus the matching geometry/format fields
    (`PixelFormat`+`VideoSize` for video, `SampleRate`+`Channels`+
    `SampleFormat` for audio). Locked in by
    `testdata/community-scripts/27_raw_yuv.json` (skipped when the
    fixture is absent; harness prints the `ffmpeg` command to
    generate it).
26. **`concat` demuxer as input kind** (§2.1) — ✅ Wave 5.
    `Input.Kind = "concat"` + `Input.ConcatList []ConcatEntry`
    (`{file, duration?, inpoint?, outpoint?, metadata?}`).
    `pipeline.materialiseConcatList` writes an `ffconcat 1.0`
    listfile to a temp file before `openSource`, opens it with
    `format="concat"`, and registers a cleanup func on
    `sourceResources` so the temp file is removed at input close.
    Apostrophes/newlines in filenames are rejected at validation
    time. Distinct from the concat **filter** (already supported)
    because the demuxer preserves stream copy.
27. **`-accurate_seek` / `-noaccurate_seek` / `-seek_timestamp`**
    (§2.1) — ✅ Wave 5. `Input.AccurateSeek` (`*bool`; FFmpeg's
    default of `true` is preserved by emitting `accurate_seek=0`
    only when explicitly set to `false`) and `Input.SeekTimestamp`
    (bool). Composes with the existing `-ss` plumbing.
28. **`-thread_queue_size`, `-protocol_whitelist`, `image2`
    `-pattern_type`** (§2.1) — ✅ Wave 5. `Input.ThreadQueueSize int`
    (validated `>= 0`); `Input.ProtocolWhitelist []string`
    (comma-joined into the demuxer AVDict at open time);
    `Input.PatternType ∈ {none, sequence, glob, glob_sequence}`
    (validated against the libavformat enum). Round-trip tests in
    `compat/ffcli/input_demuxer_test.go`.

### 6.6 Wave 6 — "muxer / encoder fidelity" (Phase C burn-down)

Close the remaining ⚠️/❌ items in §2.4 and §2.5 that are not hardware
and not deprecated.

29. **`-muxdelay` / `-muxpreload` / `-start_at_zero` /
    `-avoid_negative_ts`** (§2.5) — ✅ Wave 6. `Output.MuxDelay`
    + `Output.MuxPreload` (float seconds; rendered into the
    muxer AVDict as `max_delay`/`preload` in `AV_TIME_BASE`
    microseconds — mirrors `fftools/ffmpeg_mux_init.c`
    L3444/L3447), `Output.AvoidNegativeTS ∈ {auto, disabled,
    make_non_negative, make_zero}` (passed through verbatim as
    the `avoid_negative_ts` AVDict key — libavformat parses it
    against the same enum that the AVOption table uses,
    `libavformat/options_table.h` L95-99), and `Config.StartAtZero
    bool` (global; modulates `Config.CopyTS` so the demuxer
    `ts_offset` shift is re-enabled even under `-copyts` —
    mirrors `fftools/ffmpeg_demux.c` L486). `StartAtZero`
    requires `CopyTS=true` (validator). Completes the
    timestamp-policy cluster started by `Config.CopyTS` in
    Wave 1 #2.
30. **Per-stream encoder option overrides** (`-b:v:0`, `-crf:v:1`,
    `-preset:v:0`) (§2.4) ✅ Wave 6 (`Output.Streams[].Encoder
    *EncoderOverride { Codec, Options }` overlays the matching
    synthetic encoder node by media-type + index; expandImplicitEncoders
    counts edges per type in declaration order. ffcli parses
    `-<key>:<type>:<idx>` for the canonical encoder option key set
    plus `-c:<type>:<idx>` for codec).
31. **Attachments + cover art** (§2.5) ✅ — `Output.Attachments
    []Attachment` (`{path, filename?, mimetype?}`) muxes file
    attachments via `AVMEDIA_TYPE_ATTACHMENT` streams (matroska /
    mkv / webm only). New `av.OutputFormatContext.AddAttachment`
    cgo helper allocates an attachment stream, copies file content
    into `codecpar->extradata`, and sets `filename` / `mimetype`
    metadata; codec_id is guessed via `av_guess_codec` against the
    muxer's attachment table (`.ttf` → `AV_CODEC_ID_TTF`, `.otf` →
    `AV_CODEC_ID_OTF`, etc.). Importer: `-attach FILE`.
32. **`-vn` / `-an` / `-sn` / `-dn` per output** (§2.2) — ✅
    Wave 6. `Output.DisableVideo` / `DisableAudio` /
    `DisableSubtitle` / `DisableData` drop every inbound edge of
    the corresponding media type at this output's sink before
    `expandImplicitEncoders` runs, so no implicit encoder is
    synthesised and no copy stream is registered. Mirrors
    `fftools/ffmpeg_opt.c` L1977/2078/2115/2187 (the `OPT_OUTPUT`
    half of FFmpeg's dual-purpose disable bools). Validator
    rejects an output with all four flags set (would yield a
    zero-stream muxer). `compat/ffcli` round-trip latches `-vn`/
    `-an`/`-sn`/`-dn` onto the next output.
33. **Encoder colour/timing edge cases** (§2.4) ✅ Wave 6 (`Output.EncoderTimeBase`
    accepts `"demux"` / `"filter"` sentinel or `"N/D"` rational, mirrors
    `fftools/ffmpeg_mux_init.c` L1391-1417; `Output.FieldOrder` ∈ {`""`,
    `progressive`, `tt`, `bb`, `tb`, `bt`} stamps `AVCodecContext.field_order`;
    `Output.InterlacedEncode` toggles `AV_CODEC_FLAG_INTERLACED_DCT|ME`
    (avcodec.h L310/L331). Sentinels propagated through implicit-encoder
    expansion as `__enc_time_base` / `__field_order` / `__interlaced`).
34. **Subtitle: `-sub_charenc`, forced / hearing-impaired flags,
    codec-pair validation** (§2.6) ✅ Wave 6 (`Input.SubtitleCharenc`
    threaded into `av.OpenSubtitleDecoderWithOptions`; bitmap-subtitle
    streams reject the option at decoder-open time via the codec
    descriptor `AV_CODEC_PROP_TEXT_SUB` check; forced/HI continue
    to ride on the per-stream `Disposition` from Wave 1 #3; ffcli
    `-sub_charenc CODE` latches onto the next `-i` only).
35. **Dolby Vision RPU passthrough** (§2.4) ✅ — `Output.HDR.DoVi
    *DoViMetadata` (`{profile, level, rpu_present, el_present,
    bl_present, bl_compatibility_id}`). Stream-level configuration
    record muxed via `AV_PKT_DATA_DOVI_CONF` (mp4/mov/matroska);
    validator restricts to hevc/av1/h264 + mp4/mov/matroska.
    Per-frame RPU NAL injection (NAL 62 SEI) out of scope.

### 6.7 Wave 7 — "filtergraph completion" (Phase A/C burn-down)

Close remaining ⚠️/❌ items in §2.3 that are not hardware-related.

36. **Source-filter and sink-filter graph node kinds** (§2.3) —
    New `KindFilterSource` (zero inputs, e.g. `color`, `testsrc`,
    `sine`, `smptebars`, `movie`) and `KindFilterSink` (zero
    outputs, e.g. `nullsink`, `anullsink`). Today these only
    work via `Input.Kind="lavfi"` (the whole input) or trailing
    null sinks the engine inserts implicitly; first-class node
    kinds let filters appear *inside* a graph as zero-input
    intermediates (canonical use: `[0:v][color=...]overlay`).
    Broken into five sub-items, smallest to largest, each
    independently shippable. Dependencies: 36a → {36b, 36c, 36d}
    in parallel → 36e.

    **36a — enum + schema + validator (types only).** ✅ Wave 7.
    New `NodeKind` values `KindFilterSource` / `KindFilterSink`
    in `graph/graph.go` (plus `parseNodeKind` and `String()`
    arms); new `node.type` enum entries `filter_source` /
    `filter_sink` in `schema/v1.0.json` + `schema/v1.1.json`
    and the `NodeDef.type` union in
    `frontend/src/lib/jobTypes.ts`. `graph.Build` rejects
    inbound edges into `filter_source` and outbound edges from
    `filter_sink`, and classifies them into `g.Sources` /
    `g.Sinks`. New curated allow-lists
    `pipeline.knownFilterSources` (`color`, `testsrc`,
    `testsrc2`, `smptebars`, `smptehdbars`, `mandelbrot`,
    `life`, `yuvtestsrc`, `rgbtestsrc`, `sine`, `anullsrc`,
    `aevalsrc`, `movie`, `amovie`) and
    `pipeline.knownFilterSinks` (`nullsink`, `anullsink`)
    checked at config-load time alongside Wave 7 #42's
    `validateFilterAvailability`. Validator + round-trip tests
    only — no runtime execution path yet, so the new node
    kinds parse and validate but jobs that include them fail
    at runner-construction time with an explicit
    `not yet implemented` error. Unblocks Wave 8 #44 palette
    typing.

    **36b — av-layer source-only / sink-only filter graph
    constructors (cgo).** ✅ Wave 7.
    New
    `av.NewSourceFilterGraph(spec string, outputs
    []FilterOutputConfig, threads int)` builds
    `<spec> [out0]…; …; buffersink` with no buffersrc; the
    spec contains the source filter directly (e.g.
    `color=c=black:s=1920x1080:r=24:d=10[out0]`). New
    `av.NewSinkFilterGraph(inputs []FilterPadConfig, spec
    string, threads int)` builds `[in0]buffersrc; …; <spec>`
    with no buffersink. Both reuse the `Threads` plumbing
    from #38 and the AVFilterInOut linked-list builders from
    `NewComplexFilterGraph` with the empty side passed as
    `nil` to `avfilter_graph_parse_ptr`. Cgo-level tests in
    `av/filter_source_sink_test.go` exercise `color`,
    `testsrc2`, `sine`, `anullsrc` → frame; `buffersrc →
    anullsink`. Pure av-layer change — no runtime / pipeline
    wiring.

    **36c — `KindFilterSource` runtime handler.** ✅ Wave 7.
    New
    `handleFilterSource` worker in
    [pipeline/handlers.go](../pipeline/handlers.go) — owns
    an `av.FilterGraph` built via `NewSourceFilterGraph`,
    pumps frames from buffersink into N outbound channels
    until EOF or `ctx.Done()`. Optional `Params["duration"]`
    and `Params["nb_frames"]` caps so finite jobs don't run
    forever (canonical for `testsrc`/`color`); validator
    requires at least one of the two for non-`anullsrc`
    sources, otherwise the job is rejected as unbounded.
    `createFilter`/`buildFilter` dispatches on
    `node.Kind == KindFilterSource` to construct the
    source-only graph instead of the standard
    buffersrc-fronted one. Two corpus fixtures:
    [testdata/examples/51_filter_source_color_overlay.json](../testdata/examples/51_filter_source_color_overlay.json)
    (`color=black:s=1920x1080:d=10` overlaid by `[0:v]` —
    real overlay use-case) and
    [testdata/examples/52_filter_source_testsrc_only.json](../testdata/examples/52_filter_source_testsrc_only.json)
    (`testsrc2=d=5` → libx264 → mp4 with no top-level `-i`).

    **36d — `KindFilterSink` runtime handler.** ✅ Wave 7.
    New
    `handleFilterSink` worker — drains the inbound channel
    into a `NewSinkFilterGraph`-built graph, discards
    consumed frames, honours `ctx.Done()`. Validator: at
    least one `KindSink` *or* `KindFilterSink` per pipeline
    (so a pure-analyser job is legal). Fixture
    [testdata/examples/53_filter_sink_analyser.json](../testdata/examples/53_filter_sink_analyser.json)
    — `[0:a]asplit → ebur128 → ametadata=mode=print:file=…
    → anullsink` runs an analyser branch in parallel with
    the real encode and the analyser branch terminates
    cleanly without a muxer.

    **36e — `movie` / `amovie` second-asset support +
    ffcli round-trip.** ✅ Wave 7 (security validator + per-node
    `protocol_whitelist` injection + corpus fixture). `movie=filename=…` and `amovie=…`
    work as `KindFilterSource` but reference an external
    file → security review (path traversal rejected; honour
    `Config.ProtocolWhitelist`) + asset-manager hook for
    Wave 8 #51. ffcli importer detects `movie=` / `amovie=`
    inside `-filter_complex` (deferred to Wave 7 #40's
    parser if not yet built; otherwise inline mini-parser).
    Fixture
    [testdata/examples/49_movie_logo_overlay.json](../testdata/examples/49_movie_logo_overlay.json)
    — `[0:v][movie=logo.png]overlay=W-w-10:10`. Wave 8 #44
    cross-references this for the asset picker.

37. **Cross-media-type filter contract** ✅ Wave 7 (§2.3, §3.1.4) — Add
    `output_media_type` to filter node definitions so the engine
    knows `showwavespic` returns video despite consuming audio
    (and `concat=v=1:a=1` returns both). The GUI then renders
    downstream edges with the correct media type. Unblocks
    `waveform`, `showspectrum*`.
38. **Per-graph thread caps** (§2.3) — ✅ Wave 7
    `Config.FilterComplexThreads int` (pipeline-wide) and
    `NodeDef.Threads int` (per-filter override). Threaded into
    `AVFilterGraph.nb_threads` via the `__filter_threads` Params
    sentinel; ffcli `-filter_complex_threads N`; schema fields on
    both v1.0 and v1.1.
39. **Sidedata / per-frame metadata propagation** (§2.3) ✅ Wave 7 —
    `AVFrame->metadata` already propagates through libavfilter's
    `buffersrc`→filter→`buffersink` plumbing; the `metadata` and
    `ametadata` filters (vf_metadata.c / af_metadata.c) work as
    regular `KindFilter` nodes today, so no new node kind was
    required. The remaining gap was Go-side access for processors
    that want to read/write per-frame keys: added
    `Frame.Metadata()` / `Frame.GetMetadata(key)` /
    `Frame.SetMetadata(key, value)` cgo helpers (av/metadata.go);
    the values survive `av_frame_clone` / `av_frame_ref`, which is
    the same path libavfilter uses to hand frames between nodes.
    Fixture `51_metadata_filter.json` exercises an
    `add`→`print`→encode chain.
40. **Mixed labelled / unlabelled `-filter_complex` outputs**
    (§2.3, §1.1) ✅ Wave 7 — `compat/ffcli.NormalizeFilterComplex`
    (compat/ffcli/filter_complex.go) rewrites a `-filter_complex`
    spec into the canonical labelled form expected by
    `avfilter_graph_parse_ptr` (libavfilter/graphparser.c
    `parse_outputs`). Chains whose last filter has no trailing
    pad label are tagged with synthetic `[mm_fc_out_N]` labels;
    a missing leading label on the first chain becomes
    `[mm_fc_in_0]`. Internal labels (produced and consumed
    within the spec) are left untouched. The exporter half
    (Wave 8 #53) emits the same canonical form, which makes the
    round-trip idempotent. Locked in by
    `TestNormalizeFilterComplex_RoadmapExample` plus four
    edge-case tests (already-canonical, dangling input,
    multi-filter chain, empty/separator-only). Hooking the flag
    into the main `ffcli.Parse` to actually build filter nodes
    is deferred — the normalisation primitive is the
    pre-requisite that lets the parser, when added, treat every
    chain identically.
41. **Audio channel manipulation** (`pan`, `channelsplit`,
    `channelmap`, `join`, `amerge`, `amix=weights`) (§2.3, §1.1) — ✅ Wave 7
    Backend already supports these filters; landed three end-to-end
    fixtures that exercise the full path through the runtime:
    [testdata/examples/46_audio_pan.json](../testdata/examples/46_audio_pan.json)
    (downmix matrix), [testdata/examples/47_audio_channelmap.json](../testdata/examples/47_audio_channelmap.json)
    (channel-label swap), [testdata/examples/48_audio_amix_weights.json](../testdata/examples/48_audio_amix_weights.json)
    (asplit → amix with weights). `amerge` and `channelsplit`/`join`
    require distinct upstream channel layouts / per-output-pad
    routing that the typed-edges runtime cannot express today
    (libavfilter rejects two stereo → amerge with overlapping
    layouts; channelsplit emits N pads but graph edges currently
    bind to default port). GUI matrix view tracked separately in
    §3 #9.
42. **HDR `zscale` + `tonemap`** (§2.3, §1.1) ✅ — New
    `pipeline.validateFilterAvailability` walks every filter
    node and rejects unknown filters at config-load time. The
    error message includes the configure flag (e.g. `zscale` →
    `--enable-libzimg`; `tonemap_opencl` → `--enable-opencl`)
    via the `pipeline.optionalFilterLibs` registry, so the
    operator gets an actionable rebuild hint instead of a
    confusing runtime "filter not found". The palette
    (`/api/nodes` → `handleListNodes`) already lists only
    filters reported by `av.ListFilters()`, so unbuilt entries
    are absent from the GUI automatically.
43. **`minterpolate` motion-estimation parameter surface** (§2.3,
    §1.1) ✅ — The AVOption miner (Wave 4 #19) already exposes
    `mi_mode` / `mc_mode` / `me_mode` / `me` as typed `int`
    AVOptions carrying their named constants (`mci`, `bidir`,
    `epzs`, `umh`, …); the GUI Inspector renders them as enum
    dropdowns automatically. `vsbmc` lands as an `int 0..1` toggle.
    Locked in by `av/minterpolate_options_test.go`. Frame-rate /
    time-base plumbing already done in §5 #1.

### 6.8 Wave 8 — "GUI completeness" (Phase E)

Once Waves 5–7 land the schema gaps, the GUI side is unblocked. This
wave delivers every §2.8 / §3.5 GUI item that the schema can now back.

44. **Virtual-source palette** (§2.8, §3.5.1) ✅ Wave 8 —
    Drag-and-drop nodes for `color`, `testsrc`, `testsrc2`,
    `smptebars`, `smptehdbars`, `mandelbrot`, `life`, `yuvtestsrc`,
    `rgbtestsrc`, `sine`, `anullsrc`, `aevalsrc`, `movie`, `amovie`
    (all of `pipeline.knownFilterSources` from Wave 7 #36a) plus
    `nullsink` / `anullsink` discard sinks and an `Input.Kind="lavfi"`
    shorthand. Backend palette emission lives in
    [internal/gui/api.go](../internal/gui/api.go) `handleListNodes`,
    which now walks `av.ListFilters()` a second time and emits
    `Type="filter_source"` / `"filter_sink"` entries for the curated
    allow-list under the existing "Sources" / "Sinks" categories
    (mirroring `pipeline.knownFilterSources` /
    `knownFilterSinks` — only filters actually built into the
    running libavfilter appear, so e.g. `mandelbrot` is hidden when
    the binary lacks it). Frontend wiring extends
    [`spawnNodeFrom`](../frontend/src/lib/spawn.ts) so dragging a
    `filter_source` entry creates a `NodeDef{type:"filter_source",
    filter:<name>, params:{duration:"5"}}` (the default
    duration satisfies the Wave 7 #36c "must be bounded" validator;
    `anullsrc` / `aevalsrc` skip it because they are always lazy);
    [`MMNode`](../frontend/src/components/MMNode.tsx) hides the
    target handle for `filter_source` and the source handle for
    `filter_sink` so the React-Flow node looks like an input/output;
    [`Inspector`](../frontend/src/components/Inspector.tsx) routes
    the new types through the existing `FilterForm` so AVOption-typed
    parameter editing works for free. The `lavfi_input` palette
    entry materialises as a top-level `Input{kind:"lavfi",
    url:"anullsrc=r=48000:cl=stereo"}` so the existing input
    inspector form edits the filtergraph spec. Backed by Wave 7
    #36a (typing) + #36c (runtime) + #36e (`movie` / `amovie` asset
    references).
45. **Multi-output inspector with per-stream encoder tabs** (§2.8,
    §3.5.2) ✅ Wave 8 — The Inspector now renders a tab strip listing
    every non-implicit `Output` node in the current graph (when there
    are at least two), so the user can flip between outputs without
    going back to the canvas
    ([frontend/src/components/Inspector.tsx](../../frontend/src/components/Inspector.tsx)
    `OutputTabs`). Each output's form gains a new **Streams** sub-tab
    strip backed by `Output.streams[]`: `+ add` creates a new
    `StreamSpec{type:"v", index:N}`, the per-stream form edits
    `disposition` (Wave 1 #3), `metadata` key/value pairs (Wave 1 #3),
    and the `encoder` override codec + options (Wave 6 #30). All three
    surfaces were data-model complete on the backend already; this
    item is the GUI half. New `inspector-tabs` / `inspector-tab` /
    `.active` CSS rules in
    [frontend/src/styles.css](../../frontend/src/styles.css). Selection
    threading: `Inspector` gained an `onSelectNode?: (id) => void`
    prop wired in [frontend/src/app.tsx](../../frontend/src/app.tsx)
    to `setSelectedId` so clicking an output tab repaints both the
    inspector and the canvas selection halo. Subtitle disposition
    flag toggles (Wave 8 #52), the chapter/metadata table editor
    (Wave 8 #47), and the BSF chain editor (Wave 8 #46) layer on top
    of this surface in subsequent waves.
46. **BSF chain editor** (§2.8, §3.5.3) ✅ Wave 8 — Sortable list with
    add/remove/reorder of `(name, params)` entries; preview of the
    rendered `f1=k=v:k=v,f2` string. Replaces the single-field
    text input on `Output.BSFVideo` / `BSFAudio` / `BSFSubtitle`
    (Wave 3 #13). New `parseBSFChain` / `serializeBSFChain` in
    [frontend/src/lib/bsf.ts](../../frontend/src/lib/bsf.ts) handles
    the libavcodec `av_bsf_list_parse_str` syntax (comma-separated
    chain, each entry `name[=k=v[:k=v]*]`); new `BSFEditor` in
    [frontend/src/components/Inspector.tsx](../../frontend/src/components/Inspector.tsx)
    renders one section per media type with per-entry datalist
    presets (`h264_mp4toannexb`, `hevc_mp4toannexb`, `aac_adtstoasc`,
    `h264_metadata`, `dump_extra`, `extract_extradata`, `setts`, …),
    `↑`/`↓` reorder buttons, `×` remove, `+ add`, and a live
    monospace preview of the canonical chain spec.
47. **Chapter / metadata editor** (§2.8, §3.5.4) ✅ Wave 8 — Table editor
    for `(start, end, title)` chapter entries; key/value editor
    for per-output and per-stream metadata; tabs per stream.
    Backs `Output.Chapters`, `Output.Metadata`, and
    `Output.Streams[].Metadata`. New `MetadataEditor` (key/value
    wrapper around the existing `ParamsEditor` that strips empty
    keys on save) renders the per-output `Container metadata`
    section in
    [frontend/src/components/Inspector.tsx](../../frontend/src/components/Inspector.tsx);
    new `ChaptersEditor` / `ChapterRow` / `NumericField` render a
    sortable `(start, end, title)` table with `↑`/`↓` reorder, `×`
    remove, `+ add` (auto-fills `start` from the previous row's
    `end`), and a collapsible per-chapter metadata section. The
    per-stream metadata surface remained as wired by Wave 8 #45's
    `StreamSpecForm`.
48. **HLS / DASH / Tee output wizards** (§2.8, §3.5.5) ✅ Wave 8 —
    Schema-driven Inspector forms for the typed `HLS` / `DASH` /
    `Tee*` blocks landed in Waves 1 #5 and 3 #12. New
    `HLSForm` covers `hls_time` / `hls_init_time` / `hls_list_size`
    / `hls_start_number`, `playlist_type` (event/vod), `segment_type`
    (mpegts/fmp4 — fMP4-only fields shown conditionally),
    `segment_filename` / `fmp4_init_filename` / `master_pl_name`,
    a drag-and-drop `var_stream_map` ABR variant builder (serialises
    to the `a:N,v:N` space-separated token format), and a full
    `hls_flags` multi-checkbox picker (delete_segments, append_list,
    round_durations, discont_start, split_by_time, program_date_time,
    second_level_segment_* sizes, temp_file, independent_segments,
    iframes_only, single_file). New `DASHForm` covers `seg_duration`
    / `frag_duration` / `window_size` / `extra_window_size`,
    `init_seg_name` / `media_seg_name` / `adaptation_sets`, tri-state
    toggles (unset / true / false) for `use_template` and
    `use_timeline` (LL-DASH semantics), single-bit bool toggles for
    `streaming`, `ldash`, `hls_playlist`, `single_file`, and a
    `dash_flags` multi-checkbox picker (default_base_url_override,
    round_durations, single_file_name, global_sidx, write_prft,
    allow_media_loss). New `TeeForm` renders one collapsible row per
    `TeeTarget` (first row open by default) covering `url` (required,
    red asterisk when empty), `format`, `select`, `bsfs`, `onfail`
    (abort/ignore), `use_fifo` (tri-state), `fifo_options` (shown
    only when use_fifo=true), and a per-target `options` key/value
    editor. `Inspector.tsx` now renders a kind selector
    (`'' | 'file' | 'tee'`) at the top of every Output form,
    conditionally shows URL/format/codec/BSF fields only for non-tee
    outputs, and mounts `HLSForm` / `DASHForm` / `TeeForm` based on
    `output.format` or `output.kind`.
49. **Audio channel-routing UI** (§2.8, §3.5.9) ✅ — `AudioChannelForm`
    replaces the free-form params dict for six audio channel filters:
    `PanForm` renders a 2-D gain matrix (output channels × input channels)
    backed by `params._pos0`; `ChannelMapForm` offers per-output-channel
    source dropdowns backed by `params.map` + `params.channel_layout`;
    `ChannelSplitForm` exposes a single layout selector; `JoinForm`
    provides stream-index + channel pickers for each output channel;
    `AMergeForm` exposes an input-count spinner; `AMixForm` adds
    per-input weight fields plus `duration`, `normalize`, and
    `dropout_transition` options. All forms include a live spec-preview
    row so the final filter string is always visible. (Wave 8 #49)
50. **Filter expression authoring polish** (§3.5.8) ✅ — `ExpressionInput`
    gains three capabilities. **Autocomplete dropdown**: while typing
    in the expression textarea, a floating list of matching variable
    names and libavutil function names appears beneath the cursor;
    `↑`/`↓` navigate, `Tab`/`Enter` complete, `Esc` dismisses; clicking
    an item completes without losing focus. Variables are tinted teal
    (`tok-var`), functions purple (`tok-fn`), matching the existing
    syntax-highlight scheme. **Context-aware eval preview**: `Inspector`
    walks the graph edge list back from the selected filter node to the
    first probed input and extracts `{w, h, iw, ih, in_w, in_h,
    main_w, main_h, W, H, r, FR, sar, sr, nb_channels}` bindings;
    these are forwarded as extra URL query params to the
    `eval-expression` endpoint so the `= X` preview shows realistic
    values (e.g. `(main_w-tw)/2 = 880` for a 1920×p source) instead
    of the all-zero default; the status line reads `(from context)` vs
    `(vars=0)`. **Expanded cookbook** (5 → 19 patterns): timeline
    gates (`between`, `gt`, `not(between)`), PTS speed controls
    (`0.5*PTS`, `2*PTS`), `drawtext` centering / scrolling, overlay
    centering, crop/pad centering, volume fade-in and fade-in/out,
    `select` keyframes and every-N-th-frame, `zoompan` Ken Burns.
    **Backend filter registry** expanded from 10 → 16 filters:
    `select`, `aselect`, `hue`, `geq`, `trim`, `atrim`. (Wave 8 #50)
51. **Asset / model-file manager** (§3.5.10, formerly Wave 6 #22) ✅ Wave 8 —
    Symbolic asset references (fonts for `subtitles=`, RNNoise
    models for `arnndn=`, YOLO weights, ASS `fontsdir=`). Schema
    field `Pipeline.Assets map[string]AssetRef`; runtime resolves
    `$asset:<name>` params via cwd + `MEDIAMOLDER_ASSET_PATH` env
    var; `AssetManager` GUI modal manages the registry with
    add/edit/remove and file-browser integration. Schema v1.0/v1.1
    updated; `TestSchemaSyncWithGoStructs` extended to cover
    `AssetRef`.
52. **Subtitle GUI affordances** (§2.8) ✅ Wave 8 — Forced / HI flag
    toggles wired to per-stream disposition (backend done by
    Wave 6 #34); `-sub_charenc` picker on text-subtitle inputs;
    burn-in vs. soft-mux selector with codec-pair validation
    surfaced inline.
    - **Forced / HI checkboxes**: `StreamSpecForm` (Inspector.tsx) now
      branches on `spec.type === 's'` and renders dedicated `Forced`
      and `Hearing impaired (HI)` checkboxes backed by
      `hasDispFlag` / `toggleDispFlag` helpers that parse the
      `+`-separated `AV_DISPOSITION_*` string.  A free-text "Other
      disposition flags" field handles any remaining flags.
    - **`-sub_charenc` picker**: `InputForm` gains a datalist-enhanced
      text input (14 common encodings: UTF-8, ISO-8859-*, Windows-12xx,
      Shift_JIS, GB18030, etc.) mapping to `Input.SubtitleCharenc`.
      Hint text clarifies it applies to SRT/ASS/SSA only.
    - **Subtitle rendering selector + compat warning**: `OutputForm`
      now shows a **Subtitle rendering** `<select>` (`soft-mux` /
      `burn-in`) above the subtitle codec row.  In `burn-in` mode the
      codec/tag/BSF fields are hidden and a guidance banner directs
      users to add a `subtitles=` or `ass=` filter node.  In
      `soft-mux` mode an amber inline warning appears when the
      subtitle codec is known to be incompatible with the output
      container format (`SUBTITLE_CODEC_FORMATS` map covers
      `mov_text`, `webvtt`, `ass`/`ssa`, `srt`, `dvd_subtitle`,
      `hdmv_pgs_subtitle`).  CSS: `.subtitle-burnin-hint` /
      `.subtitle-compat-warn` added to `styles.css`.
53. ✅ **Live FFmpeg-CLI export** (§2.8, §3.5.7) — Round-trip oracle:
    JSON → ffmpeg command, with explicit "no equivalent CLI"
    failure modes for mediamolder-only features. Wired into the
    job-save flow as a "Show CLI" toolbar button and modal panel.
    `compat/ffcli.Export` covers all input/output options, filter
    graphs, HLS/DASH/tee, BSF chains, stream dispositions, metadata,
    two-pass, and more. Unsupported features (Assets, go_processor,
    LoudnormPass) are listed in an amber warning section.
54. ✅ **"Unsupported flag" import report** (§2.8) — `ImportResult`
    type returned by `ParseFull`/`ParseArgsFull`; `Unsupported []string`
    surfaces actionable notes for every Wave 5–7 schema-promoted flag
    (BSF, color, HDR/DoVi, attachments, muxdelay/preload, async, interlace,
    enc_time_base, field_order, filter_complex_threads, itsoffset, re,
    sub_charenc) and deprecated out-of-scope flags (`-deinterlace`,
    `-target`, `-fpre`/`-vpre`/`-spre`, `-xerror`, `-stats`/`-nostats`,
    `-dump`/`-hex`/`-debug_ts`). GUI `/api/convert-cmd` response now
    includes `unsupported`; CLI `convert-cmd` prints notes to stderr.

### 6.9 Wave 9 — "the editorial round-trip" (Phase C.8)

55. **Lossless intermediate validation harness** (FFV1/MKV,
    ProRes/MOV, DNxHR/MXF) (§3.3.8) — Single test exercising
    decode → intermediate → decode → final, asserting no quality
    loss. Catches container/encoder compatibility bugs systemically.
    Independent of GUI work; can run in parallel with Wave 8.

### 6.10 Wave 10 — "hardware everywhere" (Phase D, deferred)

**Deferred until Waves 5–9 close every common non-hardware CLI option
and GUI gap.** Hardware support is intentionally last because the
matrix in §2 still has many ⚠️/❌ entries that affect every user, while
hardware acceleration affects only users with specific devices and
already works in degraded form via per-filter spellings.

56. ✅ **`-init_hw_device` + per-node `device:` selector** (§3.4.5) —
    `Config.HardwareDevices []HardwareDevice` + `NodeDef.Device`
    string; opened at pipeline start via `av_hwdevice_ctx_create`;
    parsed/exported by `compat/ffcli`; schemas and frontend types
    updated. (Wave 10 #56, complete)
57. ✅ **Per-filter availability probe** (`scale_npp` vs `scale_cuda`)
    (§3.4.6) — `builtInFilters()` singleton (built once at first call
    via `sync.Once` + `av.ListFilters()`) provides an O(1) pure-Go
    map lookup for all subsequent `validateFilterAvailability` calls —
    same harness pattern as the codec probe. `optionalFilterLibs`
    expanded to cover CUDA (`scale_cuda`, `yadif_cuda`, `overlay_cuda`,
    …), CUDA+libnpp (`scale_npp`, `transpose_npp` — hint distinguishes
    from plain CUDA), Intel QSV (`scale_qsv`, `vpp_qsv`, …), Vulkan
    (`scale_vulkan`, `overlay_vulkan`, …), OpenCL (20+ filters), VAAPI
    (`overlay_vaapi`, `procamp_vaapi`, …), and VideoToolbox
    (`scale_vt`). All hardware filter references in a config produce
    actionable `--enable-xxx` rebuild hints. Tests: 7 new targeted
    subtests plus `TestScaleNppVsScaleCuda`. (Wave 10 #57, complete)
58. ✅ **Hardware filter auto-mapping** (`scale` ↔ `scale_cuda` /
    `scale_npp` / `scale_qsv` / `scale_vt`) (§2.3) — Opt-in
    per-node `auto_map_hw` flag promotes a sw-filter name to its hw
    equivalent based on the node's `device` type; inserts
    `hwupload` / `hwdownload` at device boundaries. 21 filter
    mappings covering CUDA, VAAPI, QSV, VideoToolbox, Vulkan, and
    OpenCL. 16 tests. (Wave 10 #58, complete)
59. ✅ **Per-input `-hwaccel`** (§2.1) — Promotes the global hwaccel
    knob to per-input granularity (`Input.HWAccel`,
    `Input.HWAccelDevice`, `Input.HWAccelOutputFormat`).
    `av.FrameDecoder` interface unifies sw/hw decoders in the source
    handler. `compat/ffcli` latches the flags per-input at the `-i`
    site (matching FFmpeg semantics). Schema + TypeScript types updated.
    12 tests. (Wave 10 #59, complete)
60. ✅ **Hardware-filter mapping indicator + multi-device picker
    (GUI)** (§2.8, §3.5.6) — Surfaces which filters will run on
    GPU once `hw_accel` is set, warns when a software filter is
    forcing a hwdownload/hwupload round-trip, and exposes a
    device picker on every encoder/filter node. Purple GPU badge
    on nodes with `NodeDef.device`; amber ⚠ sw/hw badge on SW
    filters adjacent to HW nodes; `auto_map_hw` checkbox in
    Inspector; `FlowNodeData.hwDevice` / `hwRoundTrip` injected
    by `decoratedNodes`. (Wave 10 #60, complete)

### 6.11 Wave 11 — "live capture, device palette & remaining parity"

**Targets:** the ⚠️/❌ items in §2.1, §2.2, §2.5, §2.7 not addressed by any earlier wave — device capture GUI, cover art embedding, attachment stream mapping, model-file filter assets, and network-source schema validation.

61. ✅ **`av.ListDevices` + `GET /api/devices` endpoint** (§2.1, §2.7) —
    CGO wrapper around `avdevice_list_input_sources()` in `av/list.go`
    (alongside `ListFilters`/`ListCodecs`). Returns
    `[]DeviceInfo{Name, Description}` per enumerated device. New REST
    endpoint `GET /api/devices?format=<fmt>` in
    `internal/gui/devices.go` (registered in `internal/gui/server.go`)
    dispatches per format: `dshow` on Windows, `avfoundation` on
    macOS, `v4l2` on Linux. Guards with a 2-second context timeout —
    Windows dshow enumeration can block on an in-use device.
    `avdevice_register_all()` is called once via `sync.Once`.
    (Wave 11 #61, complete)

62. ✅ **Device probe + seek guard** (§2.1) —
    `POST /api/probe` now accepts an optional `"format"` field; passes
    it to `av.OpenInputWithFormat(url, format, opts)` under a 2-second
    goroutine+context timeout (HTTP 504 on expiry). `isDeviceFormat()`
    helper added to `pipeline/handlers_source.go` with the set
    `{"dshow", "avfoundation", "v4l2", "gdigrab", "x11grab", "decklink"}`;
    the existing seek-skip guard (`timing.haveStart && formatName != "lavfi"`)
    is extended to `&& !isDeviceFormat(formatName)` so device inputs are
    never seeked (matches FFmpeg behaviour). Per-platform `device_input`
    palette entries emitted by `handleListNodes` via `runtime.GOOS`:
    Windows → `dshow` + `gdigrab`; macOS → `avfoundation`; Linux → `v4l2`.
    (Wave 11 #62, complete)

63. ✅ **Device picker + Inspector form (frontend)** (§2.1, §2.8) —
    A `DeviceInput` palette entry per platform
    (dshow / avfoundation / v4l2 / gdigrab). Inspector form:
    device-type dropdown (`video` / `audio` / `screen`); async
    device-name combobox that fetches
    `GET /api/devices?format=<fmt>` and populates the URL field with
    the platform-appropriate specifier (e.g.
    `video="Integrated Camera"` for dshow, `/dev/video0` for v4l2).
    Typed AVDict option fields for common capture knobs: `framerate`,
    `video_size`, `pixel_format`, `sample_rate`.

64. **Cover art / thumbnail embed in MP4/M4A** (§2.5) ✅ —
    `Output.CoverArt string` path field; materialised as an
    `AVMEDIA_TYPE_VIDEO` stream carrying `AV_DISPOSITION_ATTACHED_PIC`
    before `avformat_write_header`. Validator rejects containers that
    don't support attached pictures (allow-list: mp4/m4a/mov/mp3/mkv).
    GUI: file-picker in the output inspector (shown only when format
    is in the allow-list). `compat/ffcli` maps
    `-attach FILE -metadata:s:v:0 comment=Cover` to the new field.
    CGO helper in `av/cover_art.go`; validation in
    `pipeline/cover_art.go`; schemas and TypeScript types updated.
    (Wave 11 #64, commit pending)

65. **Per-input `-map` of attachment streams** (§2.2) —
    Extend `StreamSelect` with `type: "attachment"` (maps to
    `AVMEDIA_TYPE_ATTACHMENT`); `handleSource` copies the attachment
    packet unchanged (no decode/encode path). `compat/ffcli` maps
    `-map 0:t` to the new specifier. `schema/v1.x.json` and
    `frontend/src/lib/jobTypes.ts` updated.

66. **Model-file filter fixture story** (§2.3) —
    Standardise how filter parameters that reference on-disk model
    files are expressed and validated (`arnndn=model=rnnoise.rnnn`,
    `sofalizer=sofa=file.sofa`). Add a
    `filter_asset_paths []string` field to `pipeline.Config`;
    validator resolves model paths relative to the pipeline file and
    rejects missing files at `Validate()` time (mirrors the YOLO
    model-path story in `processors/`). GUI: file-picker appears in
    the Inspector when a filter option name matches known model-bearing
    suffixes (`_model`, `_sofa`, `model`, `sofa`).

67. **RTP/RTSP/RTMP/SRT/RIST URL-scheme validation + GUI affordance** (§2.7) —
    `pipeline.Validate()` inspects `Input.URL` scheme and emits a
    structured error for known-bad option combos (e.g. `rtsp://`
    without `rtsp_transport` when `Input.Options` is empty).
    GUI: URL-scheme icon in the input inspector;
    `rtsp_transport` dropdown (`tcp` / `udp` / `udp_multicast` / `http`)
    and `stimeout` field for RTSP; `listen_timeout` and `mode` for SRT.
    NDI and Decklink remain AVDict-passthrough only (§6.13).

### 6.12 Cross-cutting accelerators (parallel with all waves)

- **Capability-registry CI gate** — every PR touching
  `pipeline.Config` must update `compat/capabilities.yaml` or the
  build fails. Registry exists (§5#4); add the gate.
- **`compat/ffcli` round-trip oracle expansion** — every Wave 5–7
  schema promotion ships ≥ 3 round-trip cases against real
  `ffmpeg(1)` (codec, stream count, duration ±0.5s, SSIM ≥ 0.99).
  Harness exists; keep adding fixtures.
- **CLI export (JSON → ffmpeg command line)** (§3.5.7) — strongest
  correctness signal we can build. Sequenced at Wave 8 #53 because
  the per-stream / multi-output / tee surface from earlier waves is
  what makes the export non-trivial.

### 6.13 Suggested deprecations / out-of-scope

Mark these `out-of-scope` in the capability registry rather than chase them. Importer (`compat/ffcli`) may still accept the legacy spelling and rewrite it.

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
| Decklink / NDI **GUI** wizards | Keep the URL handlers (no work needed) but don't build dedicated inspectors until customer demand. AVDict passthrough is acceptable indefinitely for these. See Wave 11 #67 for the URL-scheme validation layer. |
| `-streamid`, `-bitexact`, `-tag` | Edge cases for spec-conformance testing. Ship as AVDict, never promote. |

#### 6.13.1 Rejected deprecations (refactor as needed)

These parameters were suggested to be deprecated, but should be supported and refactored for mediamolder.
| Flag(s) | Rationale |
|---|---|
| `-psnr`, `-ssim` (encoder side) | Tells encoder to calculate these distortion metrics while encoding (which is much more efficient than calculating after encoding) |
| `-tune <macro>` for x264/x265 when codec-specific `*-params` already covers it | Importer flattens `-tune` into the relevant `*-params` string. |
| `-dump`, `-hex`, `-debug_ts` | Pure debugging; route to MediaMolder's logging instead. |
