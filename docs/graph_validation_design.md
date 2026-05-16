# Graph Validation Feature Design

Validates a MediaMolder pipeline configuration before execution and identifies every class of issue that would cause a runtime error or produce incorrect/unusable output.

---

## Goals

- Catch issues that are certain to cause a runtime error (severity: **ERROR**).
- Catch issues that will silently produce bad output (severity: **WARNING**).
- Suggest best practices (severity: **INFO**).
- Report all issues in one pass rather than stopping at the first error.
- Integrate with `mediamolder inspect`, add a `mediamolder validate` CLI command, and drive inline annotations in the GUI.
- Be fast enough to run on every GUI save and on every CI push.

## Non-Goals

- Full dry-run encoding (opening codecs and muxers) — that is a separate "preflight" feature.
- SSIM/PSNR quality regression testing.
- Runtime performance analysis.

---

## Architecture

Validation is a two-phase pipeline triggered from `pipeline.ValidateConfig`:

```
Config
  │
  ▼
Phase 1: Static Analysis
  │  No I/O. Uses only config fields, known codec/container/filter metadata,
  │  and security policy. Runs in <1ms.
  │
  ▼
Phase 2: Probe-Assisted Analysis
  │  Opens each input with avformat_find_stream_info to read StreamInfo
  │  (pixel format, interlacing, sample rate, channel layout, duration, etc.)
  │  Merges stream properties with static analysis.
  │
  ▼
ValidationReport
  { Issues []ValidationIssue, ProbeResults map[string]StreamInfo }
```

Both phases produce `[]ValidationIssue`. The report merges them and sorts by severity then by node ID.

### New packages / files

| Path | Purpose |
|---|---|
| `pipeline/validate.go` | `ValidateConfig`, `ValidateConfigStatic` entry points |
| `pipeline/validate_video.go` | Video format checks (pixel fmt, interlacing, resolution, color) |
| `pipeline/validate_audio.go` | Audio format checks (sample fmt, sample rate, channel layout) |
| `pipeline/validate_codec_container.go` | Codec↔container compatibility matrix |
| `pipeline/validate_filter.go` | Filter arity, media type, and option checks |
| `pipeline/validate_hw.go` | Hardware boundary and device-context checks |
| `pipeline/validate_security.go` | Security config constraint checks |
| `pipeline/validate_test.go` | Table-driven tests covering all issue codes |
| `cmd/mediamolder/cmd_validate.go` | `mediamolder validate` CLI command |

Existing files that grow:
- `graph/graph.go` — `Build` already checks topology; the validator adds a `graph.CheckArity` function for filter pad counts.
- `av/demux.go` — `StreamInfo` gains `FieldOrder`, `ColorSpace`, `ColorPrimaries`, `ColorTransfer`, `ColorRange`, `BitsPerRawSample`, `Profile`, `Level` fields.

---

## Data Types

```go
// Severity classifies the urgency of a validation issue.
type Severity int

const (
    SeverityError   Severity = iota // Will fail at runtime.
    SeverityWarning                  // Will produce incorrect or unusable output.
    SeverityInfo                     // Best-practice suggestion.
)

// ValidationIssue describes a single detected problem.
type ValidationIssue struct {
    Severity   Severity
    Code       string   // machine-readable, e.g. "VIDEO_INTERLACED_NO_DEINTERLACE"
    Location   string   // node ID, edge index, or input ID
    Message    string   // human-readable explanation
    Suggestion string   // how to fix it
}

// ValidationReport is the result of a full validation run.
type ValidationReport struct {
    Issues       []ValidationIssue
    ProbeResults map[string][]av.StreamInfo // keyed by input ID
    HasErrors    bool
    HasWarnings  bool
}

func (r *ValidationReport) Error() string { ... }
```

---

## Issue Taxonomy

### 1. Structural / Topology

These are already partially checked in `graph.Build`. The validator extends them.

| Code | Severity | Condition |
|---|---|---|
| `TOPO_CYCLE` | ERROR | Cycle detected in the graph |
| `TOPO_UNREACHABLE_NODE` | WARNING | A processing node has no path to any sink |
| `TOPO_DANGLING_SOURCE` | ERROR | An input has no outbound edges |
| `TOPO_DANGLING_SINK` | ERROR | A sink has no inbound edges |
| `TOPO_SELF_LOOP` | ERROR | Edge from a node to itself |
| `TOPO_UNKNOWN_NODE_REF` | ERROR | Edge references a node ID that does not exist |
| `TOPO_DUPLICATE_NODE_ID` | ERROR | Two nodes share the same ID |
| `TOPO_EDGE_INTO_SOURCE` | ERROR | An edge targets a source/input node |
| `TOPO_EDGE_FROM_SINK` | ERROR | An edge originates from a sink/output node |
| `TOPO_MULTI_EDGE_SAME_INPUT_PORT` | ERROR | Multiple edges feed the same input pad of a filter |

### 2. Video Format

These require probe results to be certain but can be flagged as warnings without them when the graph structure implies the problem.

| Code | Severity | Condition |
|---|---|---|
| `VIDEO_INTERLACED_NO_DEINTERLACE` | WARNING | Source `field_order` ≠ progressive and no `yadif`, `bwdif`, `w3fdif`, `kerndeint`, `deinterlace_vaapi`, or `deinterlace_qsv` node is present in the path from that source to any encoder |
| `VIDEO_PIX_FMT_ENCODER_MISMATCH` | ERROR | Pixel format reaching an encoder is not in that encoder's accepted list (e.g. `yuv422p` → `libx264` Baseline/Main) |
| `VIDEO_BIT_DEPTH_MISMATCH` | ERROR | 10-bit pixel format entering an 8-bit profile encoder (e.g. `yuv420p10le` → `libx264` non-Hi10P) |
| `VIDEO_ODD_DIMENSION` | ERROR | Width or height is not a multiple of 2; most encoders require even dimensions |
| `VIDEO_ZERO_DIMENSION` | ERROR | Width or height is 0 |
| `VIDEO_RESOLUTION_EXCEEDS_LEVEL` | WARNING | Resolution × frame rate exceeds the maximum macroblocks-per-second for the configured H.264/HEVC level |
| `VIDEO_FRAMERATE_EXCEEDS_LEVEL` | WARNING | Frame rate exceeds the codec-level cap (e.g. H.264 Level 3.0 max 30fps at 720p) |
| `VIDEO_VFR_TO_CFR_ENCODER` | WARNING | Source has variable frame rate (avg_frame_rate ≠ r_frame_rate) and no `fps` or `mpdecimate` node precedes a CFR encoder |
| `VIDEO_ZERO_FRAMERATE` | ERROR | Frame rate numerator or denominator is 0 |
| `VIDEO_HDR_NO_TONEMAP` | WARNING | Source has BT.2020/PQ/HLG color properties and the encoder target is an SDR container (H.264 without HDR SEI) with no `zscale` or `tonemap` filter |
| `VIDEO_HDR_MISSING_METADATA` | WARNING | HDR output requested but no mastering display or max CLL/FALL metadata is present in the path |
| `VIDEO_MISSING_SAR` | INFO | No sample aspect ratio is set; some muxers will write a broken DAR |
| `VIDEO_NO_FORMAT_CONVERTER` | WARNING | Pixel format entering encoder differs from encoder default and no `format` or `scale` filter is present |

**Chroma subsampling reference** (used by `VIDEO_PIX_FMT_ENCODER_MISMATCH`):

| Encoder | Accepted pixel formats |
|---|---|
| `libx264` Baseline/Main | `yuv420p`, `yuvj420p` |
| `libx264` High | `yuv420p`, `yuvj420p`, `yuv422p`, `yuvj422p`, `yuv444p`, `yuvj444p` |
| `libx264` Hi10P | `yuv420p10le` |
| `libx265` Main | `yuv420p` |
| `libx265` Main10 | `yuv420p10le` |
| `libx265` Main444 | `yuv444p`, `yuv444p10le` |
| `libvpx-vp9` Profile 0 | `yuv420p` |
| `libvpx-vp9` Profile 1 | `yuv422p`, `yuv440p`, `yuv444p` |
| `libvpx-vp9` Profile 2 | `yuv420p10le`, `yuv420p12le` |
| `prores_ks` HQ/4444 | `yuva444p10le`, `yuv444p10le` |
| `h264_nvenc` | `yuv420p`, `nv12`, `p010le`, `cuda` |
| `hevc_nvenc` | `yuv420p`, `nv12`, `p010le`, `cuda` |
| `h264_vaapi` | `vaapi` (surface) |
| `h264_videotoolbox` | `yuv420p`, `nv12`, `p010le`, `videotoolbox_vda` |

### 3. Audio Format

| Code | Severity | Condition |
|---|---|---|
| `AUDIO_SAMPLE_FMT_MISMATCH` | ERROR | Sample format reaching an encoder is not accepted (e.g. `s16` → `aac`, which requires `fltp`) |
| `AUDIO_SAMPLE_RATE_MISMATCH` | ERROR | Sample rate not in the encoder's accepted set (e.g. 22050 Hz → `aac` without an `aresample` node) |
| `AUDIO_CHANNEL_LAYOUT_INVALID` | ERROR | Channel count does not match the channel layout (e.g. 5 channels with a stereo layout) |
| `AUDIO_MISSING_RESAMPLER` | WARNING | Audio source sample rate differs from the encoder's expected rate and no `aresample` or `aformat` node is present |
| `AUDIO_MISSING_FORMAT_CONV` | WARNING | Audio source sample format differs from encoder requirement and no `aformat` node is present |
| `AUDIO_MULTICHANNEL_NO_DOWNMIX` | WARNING | Source is multichannel (e.g. 5.1) but output stream is stereo and no `pan`, `amerge`, or `adownmix` filter is present |
| `AUDIO_CLIPPING_RISK` | INFO | Lossy audio encoder present with no loudnorm, dynaudnorm, or compand filter; peaks may clip |

**Audio encoder sample format reference:**

| Encoder | Required sample format |
|---|---|
| `aac` | `fltp` |
| `libfdk_aac` | `s16` |
| `mp3` / `libmp3lame` | `s16p`, `fltp` |
| `opus` / `libopus` | `s16`, `flt` |
| `flac` | `s16`, `s32` |
| `pcm_s16le` | `s16` |
| `vorbis` / `libvorbis` | `fltp` |
| `ac3` | `fltp` |
| `eac3` | `fltp` |

### 4. Codec / Container Compatibility

The compatibility matrix is defined in `validate_codec_container.go` as a `map[containerFormat]allowedCodecs`.

| Code | Severity | Condition |
|---|---|---|
| `CONTAINER_CODEC_UNSUPPORTED` | ERROR | Codec is not valid in the target container (e.g. `libvpx-vp8` in `.mp4`, `flac` in `.mp4`) |
| `CONTAINER_NEEDS_GLOBAL_HEADER` | ERROR | Container is MP4/MOV/MKV and encoder `global_header` flag is not set |
| `CONTAINER_BSF_REQUIRED` | WARNING | Codec/container combination requires a BSF that is not configured (see table below) |
| `CONTAINER_HLS_CODEC` | ERROR | HLS output uses a codec not in the HLS allowed set (H.264 + AAC for v3; H.265 + AAC for v6+) |
| `CONTAINER_DASH_NO_FRAGMENTED` | ERROR | DASH output but `movflags` does not include `frag_keyframe+empty_moov` |
| `CONTAINER_TS_CODEC` | ERROR | MPEG-TS output with a codec not supported in MPEG-TS (e.g. VP9) |
| `CONTAINER_SUBTITLE_INCOMPATIBLE` | ERROR | Subtitle codec incompatible with container (e.g. ASS into MPEG-TS, MOV_TEXT into MKV) |
| `CONTAINER_PCM_IN_MP4` | ERROR | PCM audio codec in MP4 container |
| `CONTAINER_OPUS_IN_MP4` | WARNING | Opus in MP4 has limited player support; use MKV or WebM for maximum compatibility |
| `CONTAINER_HEVC_TAG_MISSING` | WARNING | HEVC in MP4 without `tag:v=hvc1`; some Apple devices will not play it |

**BSF requirements reference:**

| Codec | Container | Required BSF |
|---|---|---|
| `h264` (Annex B) | MP4, MOV, MKV | `h264_mp4toannexb` (when copying; inverse needed when muxing from Annex B into MP4) |
| `hevc` (Annex B) | MP4, MOV, MKV, MPEG-TS | `hevc_mp4toannexb` |
| `aac` (ADTS) | MP4, MOV, MKV | `aac_adtstoasc` |
| `dts` | MP4 | `dca_core` |

### 5. Hardware Acceleration

| Code | Severity | Condition |
|---|---|---|
| `HW_DEVICE_MISMATCH` | ERROR | Decoder and encoder are on different hardware device contexts and no `hwdownload`/`hwupload` pair exists between them |
| `HW_MISSING_HWUPLOAD` | ERROR | Software-decoded frames enter a hardware encoder without a `hwupload` (or `hwupload_cuda`) node |
| `HW_MISSING_HWDOWNLOAD` | ERROR | Hardware-decoded frames enter a software filter without a `hwdownload` node |
| `HW_FILTER_DEVICE_MISMATCH` | ERROR | A hardware filter (e.g. `scale_cuda`) is used but the upstream device is different (e.g. VAAPI) |
| `HW_CODEC_UNAVAILABLE` | ERROR | The requested hardware codec (e.g. `hevc_nvenc`) is not reported as available by `av.FindEncoder` / `list-hw-devices` |
| `HW_PLATFORM_MISMATCH` | ERROR | Hardware codec is not available on the current OS (e.g. `h264_vaapi` on Windows, `h264_amf` on macOS) |
| `HW_ZERO_COPY_BROKEN` | WARNING | `auto_map_hw` is enabled but a software filter is in the path between hardware decoder and encoder, breaking the zero-copy path |

### 6. Stream Selection / Mapping

| Code | Severity | Condition |
|---|---|---|
| `STREAM_INDEX_OUT_OF_RANGE` | ERROR | Selected stream track index does not exist in the probed input |
| `STREAM_TYPE_MISMATCH` | ERROR | Selected stream type does not match the edge type (e.g. audio stream selected for a video edge) |
| `STREAM_COPY_CODEC_MISMATCH` | ERROR | Stream copy into a container that does not support the source codec |
| `STREAM_NO_SPLIT_MULTI_OUTPUT` | WARNING | The same source stream feeds multiple output branches without a `split` / `asplit` node (frames are consumed by the first consumer) |
| `STREAM_MISSING_SUBTITLE` | WARNING | Output codec is subtitle but no subtitle stream was selected from any input |
| `STREAM_ALL_STREAMS_UNMAPPED` | WARNING | One or more input streams are selected but have no outbound edges |

### 7. Timing and Synchronization

| Code | Severity | Condition |
|---|---|---|
| `TIMING_NO_PTS` | ERROR | A source configured as raw input (`kind=raw`) has no explicit time base set |
| `TIMING_CONCAT_FRAMERATE_MISMATCH` | WARNING | Concat demuxer inputs have different frame rates and no `fps` filter follows |
| `TIMING_VFR_AUDIO_SYNC_RISK` | WARNING | Variable frame rate video and audio from different inputs merged into a single mux without explicit PTS fixup |
| `TIMING_ITSOFFSET_NO_AUDIO` | INFO | `-itsoffset` is set on a video-only input; if the intention is A/V sync it has no effect |
| `TIMING_START_AT_ZERO_WITH_TRIM` | INFO | `copy_ts=false` and `start_at_zero=true` combined with input `-ss` may cause timestamp discontinuities |

### 8. Two-Pass Encoding

| Code | Severity | Condition |
|---|---|---|
| `TWOPASS_MISSING_PASS1` | ERROR | A node specifies `pass=2` but no corresponding `pass=1` node targeting the same stats file exists in the config |
| `TWOPASS_STATS_PATH_MISMATCH` | ERROR | The `passlogfile` param differs between pass 1 and pass 2 |
| `TWOPASS_CODEC_MISMATCH` | ERROR | Pass 1 and pass 2 nodes use different codecs |
| `TWOPASS_RESOLUTION_MISMATCH` | WARNING | Pass 1 and pass 2 nodes have different width/height params |

### 9. Filter Arity and Compatibility

| Code | Severity | Condition |
|---|---|---|
| `FILTER_WRONG_MEDIA_TYPE` | ERROR | A video filter (e.g. `scale`, `hflip`, `yadif`) receives an audio edge, or vice versa |
| `FILTER_TOO_FEW_INPUTS` | ERROR | A multi-input filter (e.g. `overlay`, `hstack`, `amix`) has fewer inbound edges than required |
| `FILTER_TOO_MANY_INPUTS` | ERROR | A filter has more inbound edges than it accepts |
| `FILTER_OUTPUT_COUNT_MISMATCH` | WARNING | A `split`/`asplit` node declares `outputs=N` but has fewer than N outbound edges (unused outputs waste memory) |
| `FILTER_UNKNOWN_NAME` | ERROR | Filter name is not present in the libavfilter registry (`av.FindFilter`) |
| `FILTER_OPTION_INVALID_TYPE` | WARNING | A filter option value cannot be parsed as the required type (numeric, rational, flags) |
| `FILTER_ZERO_FPS` | ERROR | An `fps` or `framerate` filter has `fps=0` or `rate=0` |
| `FILTER_EXPR_INVALID` | ERROR | A filter option uses an expression (e.g. `w=iw/2`) that fails libavfilter expression validation |

**Known filter input arities:**

| Filter | Min inputs | Max inputs | Media type |
|---|---|---|---|
| `overlay` | 2 | 2 | video |
| `hstack` | 2 | N (via `inputs=`) | video |
| `vstack` | 2 | N (via `inputs=`) | video |
| `xstack` | 2 | N (via `inputs=`) | video |
| `concat` | 2 | N (via `n=`, `v=`, `a=`) | video+audio |
| `amix` | 2 | N (via `inputs=`) | audio |
| `amerge` | 2 | N | audio |
| `split` | 1 | 1 | video |
| `asplit` | 1 | 1 | audio |
| `scale` | 1 | 1 | video |
| `yadif` | 1 | 1 | video |
| `bwdif` | 1 | 1 | video |
| `loudnorm` | 1 | 1 | audio |

### 10. Security / Resource Limits

These map directly onto `pipeline.SecurityConfig`.

| Code | Severity | Condition |
|---|---|---|
| `SEC_DISALLOWED_SCHEME` | ERROR | Input URL scheme is not in `AllowedSchemes` |
| `SEC_PATH_TRAVERSAL` | ERROR | Input URL resolves outside `BaseDir` |
| `SEC_MAX_DIMENSIONS_EXCEEDED` | ERROR | Output resolution exceeds `MaxDimensions` |
| `SEC_MAX_STREAMS_EXCEEDED` | ERROR | Number of output streams exceeds `MaxStreams` |
| `SEC_MAX_THREADS_EXCEEDED` | WARNING | Encoder `threads` option exceeds `MaxThreads` |

### 11. Processor Nodes

| Code | Severity | Condition |
|---|---|---|
| `PROC_NOT_REGISTERED` | ERROR | Processor node `type` is not present in `processors.Registry` |
| `PROC_WRONG_MEDIA_TYPE` | ERROR | A video-only processor receives an audio edge |
| `PROC_MISSING_REQUIRED_PARAM` | ERROR | A required processor param (e.g. `model_path` for `yolov8`) is absent |

---

## Phase 1: Static Analysis

Runs against `pipeline.Config` alone, no file I/O.

**Checks performed:**
- All topology issues (§1)
- `CONTAINER_*` issues using the codec/container matrix
- `CONTAINER_NEEDS_GLOBAL_HEADER`, `CONTAINER_BSF_REQUIRED`
- `FILTER_WRONG_MEDIA_TYPE`, `FILTER_UNKNOWN_NAME`, `FILTER_ZERO_FPS`, `FILTER_TOO_FEW_INPUTS`, `FILTER_TOO_MANY_INPUTS`, `FILTER_OUTPUT_COUNT_MISMATCH`
- `TWOPASS_*` issues
- `SEC_DISALLOWED_SCHEME`, `SEC_PATH_TRAVERSAL`, `SEC_MAX_DIMENSIONS_EXCEEDED`
- `PROC_NOT_REGISTERED`, `PROC_MISSING_REQUIRED_PARAM`
- `VIDEO_ZERO_DIMENSION`, `VIDEO_ZERO_FRAMERATE`
- `AUDIO_SAMPLE_RATE_MISMATCH` and `AUDIO_SAMPLE_FMT_MISMATCH` when encoder and source params are both explicit in the config
- `HW_CODEC_UNAVAILABLE` (calls `av.FindEncoder`)
- `HW_PLATFORM_MISMATCH`

**API:**
```go
func ValidateConfigStatic(cfg *Config, sec *SecurityConfig) *ValidationReport
```

## Phase 2: Probe-Assisted Analysis

Calls `av.OpenInput` + `avformat_find_stream_info` for each input. Augments `StreamInfo` with:
- `FieldOrder int` — `AV_FIELD_*` constant
- `ColorSpace int` — `AVCOL_SPC_*`
- `ColorPrimaries int` — `AVCOL_PRI_*`
- `ColorTransfer int` — `AVCOL_TRC_*`
- `ColorRange int` — `AVCOL_RANGE_*`
- `BitsPerRawSample int`
- `Profile int`
- `Level int`

**Checks that require probe data:**
- `VIDEO_INTERLACED_NO_DEINTERLACE` — `FieldOrder` ∉ {`AV_FIELD_PROGRESSIVE`, `AV_FIELD_UNKNOWN`} and no deinterlace filter in path
- `VIDEO_PIX_FMT_ENCODER_MISMATCH` — probed `PixFmt` of the source stream vs. encoder accepted list
- `VIDEO_BIT_DEPTH_MISMATCH` — `BitsPerRawSample` > 8 and encoder is 8-bit-only
- `VIDEO_HDR_NO_TONEMAP` — `ColorPrimaries == BT2020` or `ColorTransfer ∈ {SMPTE2084, ARIB_STD_B67}` and no tonemap filter
- `VIDEO_RESOLUTION_EXCEEDS_LEVEL`, `VIDEO_FRAMERATE_EXCEEDS_LEVEL` — probed dimensions + frame rate vs. codec level table
- `AUDIO_SAMPLE_FMT_MISMATCH`, `AUDIO_SAMPLE_RATE_MISMATCH`, `AUDIO_CHANNEL_LAYOUT_INVALID` — probed vs. encoder requirements
- `AUDIO_MULTICHANNEL_NO_DOWNMIX` — probed channels > 2, output streams ≤ 2
- `STREAM_INDEX_OUT_OF_RANGE` — track index vs. probed stream count
- `STREAM_TYPE_MISMATCH` — probed stream type vs. edge type
- `VIDEO_VFR_TO_CFR_ENCODER` — probed `avg_frame_rate` ≠ `r_frame_rate` and no `fps` filter

**API:**
```go
func ValidateConfig(cfg *Config, sec *SecurityConfig) (*ValidationReport, error)
// Opens each input, probes stream info, runs both phases, returns merged report.
```

---

## Path-Tracing Algorithm

For checks that require "is filter X present between source S and encoder E", the validator walks the graph:

```go
// pathContainsFilter returns true if any node on any path from 'from' to 'to'
// has Kind==KindFilter and Filter==filterName (or matches a set of names).
func pathContainsFilter(g *graph.Graph, from, to *graph.Node, names map[string]bool) bool {
    // DFS from 'from', stop at 'to', check each visited filter node.
}
```

This runs in O(V+E) per check. Since validation is not on the hot path, this is acceptable.

---

## CLI Integration

### `mediamolder validate`

```
mediamolder validate [--probe] [--json] [--strict] <config.json>
```

- `--probe`: enable Phase 2 (probe inputs). Default: enabled unless `--no-probe`.
- `--json`: emit `ValidationReport` as JSON (for CI integration).
- `--strict`: exit 1 on any WARNING, not just ERROR (for CI pipelines).

Output without `--json`:
```
ERROR  [node:enc_video]   VIDEO_PIX_FMT_ENCODER_MISMATCH
       Pixel format yuv422p is not accepted by libx264 (Baseline profile requires yuv420p).
       Fix: add a format=pix_fmts=yuv420p node before enc_video.

WARNING [node:enc_video]   VIDEO_INTERLACED_NO_DEINTERLACE
       Input "main" stream 0 is interlaced (TFF) but enc_video targets progressive output.
       Fix: add a yadif=mode=send_frame:parity=tff node before enc_video.

2 error(s), 1 warning(s). Pipeline will not run correctly.
```

Exit codes: `0` = clean, `1` = errors, `2` = warnings only (suppressed with `--strict`).

### `mediamolder inspect`

Extend the existing `inspect` command to run Phase 1 static validation and append a `"validation"` section to its JSON output.

---

## GUI Integration

1. **Validate button**: Triggers `ValidateConfig` against the current graph JSON. Results rendered as a panel below the canvas.
2. **Inline annotations**: Each node and edge with issues gets a coloured badge (red = error, amber = warning). Hovering shows the issue message and suggestion.
3. **Auto-validate on save**: Phase 1 (static) runs synchronously on every graph change with <1ms overhead. Phase 2 (probe) runs asynchronously, triggered 500ms after the last edit.
4. **Fix suggestions**: For common issues (missing format converter, missing deinterlace node), the GUI offers a one-click "Insert fix" that adds the required node and wires it into the graph.

---

## Testing Strategy

1. **Unit tests** (`pipeline/validate_test.go`): Table-driven tests with minimal `Config` structs covering every issue code, verifying both that the issue is raised and that fixing the config clears it.
2. **Golden tests**: A set of deliberately broken `.json` fixtures in `testdata/validation/` — each paired with a `_expected.json` `ValidationReport`. Run with `go test -run TestValidationGolden`.
3. **Corpus tests**: Run Phase 1 validation against all 84 existing `testdata/` fixtures and assert zero errors (they are all known-good configs).
4. **Probe tests** (integration, requires media files): Run Phase 2 against the `testdata/production-patterns/` inputs and assert no unexpected issues.
5. **Fuzz target** (`fuzz_validate_test.go`): Feed random `Config` JSON to `ValidateConfigStatic` and assert it never panics.

---

## Implementation Phases

### Phase A — Static validation + CLI command (foundational) ✅ IMPLEMENTED

**Status**: Complete. All items below are merged on the `validate_graph` branch.

- ✅ Add `ValidationIssue`, `ValidationReport`, `Severity` types (`pipeline/validate.go`)
- ✅ Implement topology checks (`pipeline/validate_topology.go`):
  - `TOPO_CYCLE`, `TOPO_DUPLICATE_NODE_ID`, `TOPO_SELF_LOOP`, `TOPO_UNKNOWN_NODE_REF` (via `graph.Build`)
  - `TOPO_DANGLING_SOURCE`, `TOPO_DANGLING_SINK`
  - `TOPO_EDGE_INTO_SOURCE`, `TOPO_EDGE_FROM_SINK`
  - `TOPO_MULTI_EDGE_SAME_INPUT_PORT`
  - `TOPO_UNREACHABLE_NODE` (reverse DFS from sinks)
- ✅ Implement video static checks (`pipeline/validate_video.go`):
  - `VIDEO_ZERO_DIMENSION` — literal `0` width/height on encoder and scale nodes
  - `VIDEO_ZERO_FRAMERATE` — `fps=0` on fps filter nodes
- ✅ Implement audio static checks (`pipeline/validate_audio.go`):
  - `AUDIO_SAMPLE_FMT_MISMATCH` — explicit `sample_fmt` not in encoder's required set
  - `AUDIO_SAMPLE_RATE_MISMATCH` — explicit `sample_rate` not in encoder's allowed set
- ✅ Implement codec/container matrix checks (`pipeline/validate_codec_container.go`):
  - `CONTAINER_CODEC_UNSUPPORTED` — video/audio codec not supported by output container
  - `CONTAINER_HLS_CODEC` — non-H.264/H.265 video or non-AAC/MP3 audio in HLS
  - `CONTAINER_DASH_NO_FRAGMENTED` — missing `movflags=frag_keyframe+empty_moov` for DASH
  - `CONTAINER_PCM_IN_MP4` — uncompressed PCM audio in MP4 container
  - `CONTAINER_OPUS_IN_MP4` — Opus in MP4 (warning: limited player support)
  - `CONTAINER_HEVC_TAG_MISSING` — HEVC/H.265 in MP4 without `tag:v=hvc1` (warning)
  - `CONTAINER_BSF_REQUIRED` — stream-copy path likely requires a BSF (warning)
- ✅ Implement two-pass consistency checks (in `validate_codec_container.go`):
  - `TWOPASS_MISSING_PASS1`, `TWOPASS_CODEC_MISMATCH`
- ✅ Implement filter arity + type checks (`pipeline/validate_filter.go`):
  - `FILTER_UNKNOWN_NAME` — filter not registered in this libavfilter build (`av.FindFilter`)
  - `FILTER_WRONG_MEDIA_TYPE` — video/audio filter receiving wrong edge type
  - `FILTER_TOO_FEW_INPUTS`, `FILTER_TOO_MANY_INPUTS`
  - `FILTER_OUTPUT_COUNT_MISMATCH` — split/asplit output count vs wired edges
- ✅ Implement hardware checks (`pipeline/validate_hw.go`):
  - `HW_CODEC_UNAVAILABLE` — HW encoder not in `av.FindEncoder` registry
  - `HW_PLATFORM_MISMATCH` — HW encoder used on incompatible OS
- ✅ Implement security checks (`pipeline/validate_security.go`):
  - `SEC_DISALLOWED_SCHEME`, `SEC_PATH_TRAVERSAL` (via `SecurityConfig.ValidateURL`)
  - `SEC_MAX_STREAMS_EXCEEDED`, `SEC_MAX_THREADS_EXCEEDED` (warning), `SEC_MAX_DIMENSIONS_EXCEEDED`
- ✅ Add `av.FindFilter(name string) bool` to `av/list.go`
- ✅ Add `mediamolder validate [--json] [--strict] <config.json>` CLI command
  (`cmd/mediamolder/cmd_validate.go`, `cmd/mediamolder/main.go`)
- ✅ Unit tests for all static checks (`pipeline/validate_test.go` — 25+ table-driven tests)

### Phase B — Probe-assisted checks
- Extend `av.StreamInfo` with `FieldOrder`, color metadata, `Profile`, `Level`
- Implement `ValidateConfig` with probe phase
- Add interlacing, pixel format, sample format/rate, HDR checks
- Add stream index/type existence checks
- Async probe in GUI

### Phase C — GUI integration
- Inline node/edge annotations
- Validate panel
- One-click fix suggestions for common issues

### Phase D — Advanced checks
- Expression validation (call `av_expr_parse` for filter option expressions)
- Hardware boundary zero-copy path analysis
- VFR detection using probed `avg_frame_rate` vs `r_frame_rate`
- Two-pass cross-node consistency
