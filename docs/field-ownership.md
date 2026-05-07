# Field Ownership

**Status:** authoritative classification (Milestone A of
[private_local/normalization_plan_revised.md](../private_local/normalization_plan_revised.md))

This document classifies every field on `pipeline.Config`,
`pipeline.Config.GlobalOptions`, `pipeline.Output`,
`pipeline.Output.Streams[i]`, and `pipeline.Input` into one of five
ownership classes. It is the authoritative answer to *"how much further
can we push runtime parameters from global / output-scoped to
node-local, and what stays global for principled reasons?"*

The classification drives Milestones B and C of the normalization plan:
the "node-local" and "authoring shorthand" rows are what
`NormalizeConfig` must lower; the "muxer-owned" rows stay where they
are; the "true global" rows survive as top-level fields on
`graph.Def`; the "defer" rows have a named owner and a question to
answer before they can be reclassified.

## Classes

| Class | Definition | Post-normalization home |
|---|---|---|
| **node-local** | Runtime-affecting media param owned by exactly one node | Stamped onto the encoder/filter/source/sink node; runtime read paths only see node fields |
| **authoring shorthand** | Convenience for human / FFmpeg-CLI authors | Lives only on `Config`; lowered into a node-local field by `NormalizeConfig`; runtime never reads it |
| **muxer-owned** | Genuinely belongs on a sink/output | Stays on the sink node (`Output` becomes a sink-node typed config) |
| **true global** | Cross-cutting policy that no single node owns | Stays on `graph.Def` top-level (assets, security, observability, timestamp policy) |
| **defer** | Currently unclear or contested | Open question; named owner |

---

## `Config` (top-level)

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `SchemaVersion` | true global | `graph.Def.SchemaVersion` | Versioning of the executable graph itself. |
| `Description` | true global | `graph.Def.Description` | Free-form. |
| `Inputs` | authoring shorthand | source nodes in `graph.Def` | Each `Input` already lowers into a source node (`handlers_source.go`). After normalization, runtime reads source-node fields, not `Config.Inputs`. |
| `Graph` | n/a (the graph) | `graph.Def` | The graph itself; not a parameter. |
| `Outputs` | muxer-owned | sink nodes in `graph.Def` | Today `Output` is a hybrid (muxer + encoder + stream config). After Milestone C, only the **muxer-owned** rows below remain on the sink node; the rest is lowered. |
| `GlobalOptions` | mixed — see [§GlobalOptions](#globaloptions) | per-row | The single biggest source of "global parameters." |
| `CopyTS` | true global | `graph.Def.TimestampPolicy.CopyTS` | Affects both source-side `ts_offset` and sink-side `-ss`/`-to` semantics in correlated ways. Splitting per-node would re-create the coupling. Promote to a `TimestampPolicy` struct rather than a loose bool. |
| `StartAtZero` | true global | `graph.Def.TimestampPolicy.StartAtZero` | Modulator on `CopyTS`; same justification. |
| `FilterComplexThreads` | authoring shorthand | filter-node `Internal.Filter.Threads` (default) | **DONE** (B.4-B.5). Default for `AVFilterGraph.nb_threads`; per-node `NodeDef.Threads` wins. Normalization stamps the resolved value onto every filter node. |
| `Assets` | true global | `graph.Def.Assets` | Name-keyed lookup table referenced by ID from filter nodes. The table itself is global by definition. |

## GlobalOptions

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `Threads` | authoring shorthand | encoder-node `Internal.Threads` (default) | Already overridden by `NodeDef.Params["threads"]` in `createEncoder`. Normalization stamps the default onto every encoder node; runtime reads only the node field. |
| `ThreadType` | authoring shorthand | encoder-node `Internal.ThreadType` (default) | Same pattern as `Threads`. |
| `HardwareAccel` | authoring shorthand | source/decoder-node `Internal.HwAccel` (default) | Hw-accel is genuinely per-decoder (different sources can need different accelerators). Keep `GlobalOptions.HardwareAccel` as an authoring default; normalization stamps it onto each source node that does not specify its own. |
| `HardwareDevice` | authoring shorthand | source/decoder-node `Internal.HwDevice` (default) | Same pattern as `HardwareAccel`. |
| `Realtime` | defer | TBD | Owner: scheduler. Open question: is this a scheduler-pacing flag (true global) or a per-source pacer (`Input.ReadRate` already covers per-source). Recommend deletion in favour of `Input.ReadRate=1.0` after Milestone C; until then, classify as defer. |

## Output (sink-owned vs lowered)

`Output` is the largest concentration of mis-classified fields. After
Milestone C, only the rows marked **muxer-owned** below remain on the
sink node; everything else is lowered into encoder/filter nodes by
`NormalizeConfig`.

### Identity / muxer

| Field | Class | Owner / Lowered to |
|---|---|---|
| `ID` | muxer-owned | sink node ID |
| `URL` | muxer-owned | sink node |
| `Format` | muxer-owned | sink node |
| `Kind` | muxer-owned | sink node (file vs tee discriminator) |
| `Targets` | muxer-owned | sink node (tee slaves) |
| `Options` | muxer-owned | sink node (`avformat_write_header` AVDict) |
| `MuxDelay` | muxer-owned | sink node |
| `MuxPreload` | muxer-owned | sink node |
| `AvoidNegativeTS` | muxer-owned | sink node |
| `MaxFileSize` | muxer-owned | sink node |
| `Shortest` | muxer-owned | sink node (per-output sync-queue policy) |
| `HLS` | muxer-owned | sink node (typed HLS muxer options) |
| `DASH` | muxer-owned | sink node (typed DASH muxer options) |
| `Attachments` | muxer-owned | sink node |
| `Chapters` | muxer-owned | sink node |
| `Metadata` | muxer-owned | sink node |
| `MaxFramesVideo` | muxer-owned | sink node (per-stream packet cap at mux time) |
| `MaxFramesAudio` | muxer-owned | sink node |
| `DisableVideo` / `DisableAudio` / `DisableSubtitle` / `DisableData` | muxer-owned | sink node (drops inbound edges of given type) |

### Stream-copy / bitstream filters

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `BSFVideo` / `BSFAudio` / `BSFSubtitle` | muxer-owned | sink node (per-stream BSF chain) | BSFs run between encoder/decoder and muxer; conceptually muxer-edge, owned by the sink. |
| `CodecTagVideo` / `CodecTagAudio` / `CodecTagSubtitle` | muxer-owned | sink node | FourCC stamped on AVStream by the muxer. |

### Encoder shorthand (lowered to encoder nodes)

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `CodecVideo` / `CodecAudio` / `CodecSubtitle` | authoring shorthand | encoder node `Internal.Codec` | Lowered today by `expandImplicitEncoders`. After Milestone C, runtime never reads these. |
| `EncoderParamsVideo` / `EncoderParamsAudio` / `EncoderParamsSubtitle` | authoring shorthand | encoder node `Params` (AVOptions) + `Internal.Encoder.*` (typed fields) | Lowered by `expandImplicitEncoders`. Milestone B (B.4-B.6) **DONE**: `__*` sentinels replaced by typed fields on `NodeDef.Internal.Encoder`; real AVOptions stay in `Params`. |
| `FPSMode` | authoring shorthand | encoder node `Internal.Encoder.FPSMode` | **DONE** (B.4-B.5). |
| `AudioSync` | authoring shorthand | generated `aresample` filter node | Already lowered by `spliceAudioSyncForOutputs`. After Milestone C, sinks/encoders never read `Output.AudioSync`. |
| `Pass` | authoring shorthand | encoder node `Internal.Encoder.Pass` | **DONE** (B.4-B.5). |
| `PassLogFile` | authoring shorthand | encoder node `Internal.Encoder.PassLogFile` | **DONE** (B.4-B.5). `Internal.Encoder.PassIndex` carries the per-pass output index. |
| `LoudnormPass` | authoring shorthand | loudnorm filter node `Internal.Filter.LoudnormPass` | **DONE** (B.4-B.5). |
| `LoudnormStatsFile` | authoring shorthand | loudnorm filter node `Internal.Filter.LoudnormStatsFile` | **DONE** (B.4-B.5). |
| `ForceKeyFrames` | authoring shorthand | encoder node `Internal.Encoder.ForceKeyFrames` | **DONE** (B.4-B.5). |
| `SAR` | authoring shorthand | encoder node `Internal.Encoder.SAR` | **DONE** (B.4-B.5). |
| `DAR` | authoring shorthand | encoder node `Internal.Encoder.DAR` | **DONE** (B.4-B.5). Mutually exclusive with `SAR` (validator). |
| `EncoderTimeBase` | authoring shorthand | encoder node `Internal.Encoder.EncoderTimeBase` | **DONE** (B.4-B.5). |
| `FieldOrder` | authoring shorthand | encoder node `Internal.Encoder.FieldOrder` | **DONE** (B.4-B.5). |
| `InterlacedEncode` | authoring shorthand | encoder node `Internal.Encoder.Interlaced` | **DONE** (B.4-B.5). |
| `Color` | node-local (encoder) | encoder node `Internal.Color` | ColorMetadata is stamped onto the encoder's codecpar before WriteHeader. Move from sink to encoder node ownership during lowering. |
| `HDR` | node-local (encoder) | encoder node `Internal.HDR` | Same justification as `Color` — written to coded_side_data on the encoded stream. |

### Per-stream metadata / disposition / overrides

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `Streams[i].Type` | muxer-owned | sink node (per-stream metadata table key) | Identifies which stream the row applies to. |
| `Streams[i].Index` | muxer-owned | sink node (per-stream metadata table key) | |
| `Streams[i].Metadata` | muxer-owned | sink node (per-stream `av_dict_set` on AVStream) | Container-level per-stream tags (language, title). |
| `Streams[i].Disposition` | muxer-owned | sink node (per-stream `disposition` AVOption) | |
| `Streams[i].Encoder` | authoring shorthand | encoder node `Internal.Codec` + `Params` for the matching stream | Currently merged into the synthetic encoder by `expandImplicitEncoders`. After Milestone C, this lowering is part of `NormalizeConfig` and runtime sees only the resolved encoder node. |

---

## Input (source-owned vs lowered)

`Input` is closer to ownership-correct than `Output`: most fields are
already source-owned. The full `Input` becomes a source-node typed
config after normalization.

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `ID` | source-owned | source node ID | |
| `URL` | source-owned | source node | |
| `Kind` | source-owned | source node (file/lavfi/raw/concat) | |
| `Streams` | source-owned | source node (`StreamSelect` list) | |
| `Options` | source-owned | source node (demuxer AVDict) | |
| `Format` | source-owned | source node (forced demuxer) | |
| `FrameRate`, `PixelFormat`, `VideoSize`, `SampleRate`, `Channels`, `SampleFormat` | source-owned | source node (raw demuxer geometry) | |
| `ConcatList` | source-owned | source node (concat playlist) | |
| `AccurateSeek`, `SeekTimestamp` | source-owned | source node | |
| `ThreadQueueSize` | source-owned | source node (demuxer queue depth) | |
| `ProtocolWhitelist` | source-owned | source node (protocol allow-list) | |
| `PatternType` | source-owned | source node (image2 demuxer) | |
| `SubtitleCharenc` | source-owned | source node (per-decoder sub_charenc) | |
| `MapMetadata` | authoring shorthand | sink node (per-output metadata) | Currently a per-input bool that selects which input's container metadata to copy onto outputs that lack their own `Metadata`. Lowering: resolve at normalization time and stamp the merged map onto each affected sink node. |
| `MapChapters` | authoring shorthand | sink node (per-output chapter table) | Same lowering pattern as `MapMetadata`. |
| `StreamLoop` | source-owned | source node (demuxer loop count) | |
| `ITSOffset` | source-owned | source node (per-input PTS shift) | |
| `ReadRate`, `ReadRateInitialBurst`, `ReadRateCatchup` | source-owned | source node (read pacer) | |

---

## NodeDef (already node-local, but typed Internal needed)

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `ID`, `Type`, `Filter`, `Processor` | n/a | identity | |
| `Params` | node-local | node | After Milestone B, `Params` contains **only** real AVOptions / user filter args. The `__*` sentinel keys move to `NodeDef.Internal`. `encoderReservedParams` filtering disappears. |
| `Internal` (new) | node-local | node | Typed sub-struct introduced in Milestone B. Per-kind variants (encoder, filter, source, sink) hold the lowered shorthand fields and the `Generated` provenance metadata. |
| `ErrorPolicy` | node-local | node | Already correctly scoped. |
| `Threads` | node-local | filter node `Internal.Filter.Threads` | **DONE** (B.4-B.5). Already correctly scoped; wins over `Config.FilterComplexThreads`. |
| `OutputMediaType` | node-local | node | Already correctly scoped. |

---

## SecurityConfig

`SecurityConfig` is **not** a parameter of any single node and is not
part of `Config`. It is a constructor-time policy passed to the
pipeline by the embedding application.

| Field | Class | Owner / Lowered to | Notes |
|---|---|---|---|
| `AllowedSchemes`, `BaseDir` | true global | `graph.Def.Security` (or pipeline option) | URL allow-listing applies uniformly to every source/sink. |
| `MaxWidth`, `MaxHeight` | true global | pipeline policy | Decoded-frame caps applied at decoder open. |
| `MaxStreams` | true global | pipeline policy | Per-input stream count cap. |
| `ProbeTimeout` | true global | pipeline policy | Currently unenforced (see Milestone B follow-ups). |
| `MaxConcurrentPipelines` | true global | scheduler policy | Cross-pipeline; outside `graph.Def`. |
| `MemoryCapMB`, `MaxThreads` | true global | scheduler policy | Resource caps; outside `graph.Def`. |

---

## True globals (justification)

Not everything should become node-local. The following remain
cross-cutting after Milestone C and are argued for individually:

- **Assets** (`Config.Assets`): name-keyed lookup table. Nodes
  reference assets by ID; the table itself is global.
- **Security policy** (`SecurityConfig`): caller-imposed limits, not
  per-node knobs.
- **Observability** (Prometheus registry, OTel provider):
  infrastructure, not media.
- **Timestamp policy** (`CopyTS`, `StartAtZero`): affects both source
  reading and sink writing semantics in correlated ways. Splitting
  per-node would re-create the coupling under a different name.
  Promote to a `TimestampPolicy` struct on `graph.Def`.
- **Scheduler defaults** (channel depths, backpressure thresholds):
  runtime tuning, not media config. Outside `graph.Def`.

---

## Cross-checks

### Against `compat/capabilities.yaml`

Every FFmpeg CLI flag tracked in `compat/capabilities.yaml` lands on a
row in this table:

- `-i`, `-ss`, `-t`, `-to`, `-itsoffset`, `-stream_loop`,
  `-thread_queue_size`, `-protocol_whitelist`, `-pattern_type`,
  `-sub_charenc`, `-map_metadata`, `-map_chapters`, `-re` /
  `-readrate*` → **Input** (source-owned or authoring shorthand).
- `-c:v` / `-c:a` / `-c:s`, `-b:v`, `-crf`, `-preset`, `-tune`,
  `-profile`, `-level`, `-g`, `-maxrate`, `-bufsize`, `-pass`,
  `-passlogfile`, `-force_key_frames`, `-fps_mode` / `-vsync`,
  `-aspect` / `-sar` / `-dar`, `-enc_time_base`, `-field_order`,
  `-flags +ildct+ilme`, `-color_*`, `-mastering_display_metadata`,
  `-content_light_level`, `-tag:v` etc. → **Output encoder shorthand**
  (lowered to encoder node).
- `-async N` / `-af aresample=async=N` → **Output.AudioSync**
  (lowered to `aresample` filter node).
- `-f`, `-muxdelay`, `-muxpreload`, `-avoid_negative_ts`, `-fs`,
  `-shortest`, `-vn`/`-an`/`-sn`/`-dn`, `-metadata`,
  `-disposition:s:*`, `-attach`, `-chapter*`, `-hls_*`, `-dash_*`,
  tee `[opt=val]url|...`, `-bsf:*` → **Output muxer-owned**.
- `-threads`, `-thread_type`, `-hwaccel`, `-hwaccel_device`,
  `-filter_complex_threads` → **GlobalOptions** authoring shorthand
  (lowered to encoder/filter/source-node defaults).
- `-copyts`, `-start_at_zero` → **TimestampPolicy** (true global).

### Against `schema/v1.1.json`

Every JSON property in `schema/v1.1.json` corresponds to a row above.
Schema changes during Milestone B (introducing `NodeDef.Internal` and
`Generated`) are additive; no existing property is removed.

---

## Defer rows (open questions)

| Field | Owner | Open question |
|---|---|---|
| `GlobalOptions.Realtime` | scheduler maintainer | Is this distinct from `Input.ReadRate=1.0`? If not, deprecate after Milestone C. If yes, what does it mean for non-input nodes? |
| `EncoderOverride.Codec` collision rules | encoder maintainer | When `Output.CodecVideo`, `Output.Streams[i].Encoder.Codec`, and an explicit upstream encoder node disagree, which wins? Document as part of Milestone B's snapshot tests; today the precedence is implicit in `expandImplicitEncoders`. |
| Per-output `LoudnormPass` for outputs with multiple loudnorm filter nodes | filter maintainer | The current single `LoudnormPass` field cannot distinguish two loudnorm nodes feeding the same output. Defer until a job actually needs it. |

---

## What changes in Milestone B

The "authoring shorthand" rows above are precisely the set
`NormalizeConfig` must lower. The "node-local" / "encoder node
`Internal.*`" right-hand column is the schema for `NodeDef.Internal`'s
encoder variant. The `Params["__*"]` lowering targets become typed
fields, and `encoderReservedParams` disappears.

## What changes in Milestone C

**Status: DONE.** Three slices land the architectural promise that
runtime code never reads authoring shorthand off `Output`.

- **C.1 — Ambiguity warnings.** `NormalizeConfig` emits
  `compat.output_encoder_shorthand_ignored` whenever an `Output`
  carries any of the "authoring shorthand" rows above alongside an
  explicit encoder or `copy` node that already feeds the same edge.
  The explicit node wins; the warning surfaces the silently-dropped
  shorthand. Synthetic encoders inserted by `expandImplicitEncoders`
  are skipped via `Internal.Generated.By` provenance. Path is the
  JSON pointer of the offending field (e.g.
  `outputs[0].codec_video`, `outputs[0].streams[1].encoder`).
  Warnings flow to the GUI via the existing `EventBus` `ErrorEvent`
  path in [pipeline/engine.go](../pipeline/engine.go) `runGraph`.
- **C.2 — Invariant.**
  [pipeline/normalize_invariant_test.go](../pipeline/normalize_invariant_test.go)
  exercises every shorthand row, verifies the resulting `*graph.Def`
  carries typed `Internal.Encoder` / `Internal.Filter` /
  `Internal.Generated`, and runs a control with all shorthand
  cleared (zero ambiguity warnings expected). The legacy
  `pipeline/engine.go::runLinear` is the **single intentional
  exemption**: it bypasses `NormalizeConfig` and reads
  `Output.CodecVideo` / `GlobalOptions.Threads` directly. The
  retire-or-keep decision is tracked as F7 in the followups
  roadmap.
- **C.3 — Drop runtime fallback + grep audit.**
  `graphRunner.createEncoder` no longer falls back to scanning
  sinks for `Output.CodecVideo / CodecAudio` when
  `node.Params["codec"]` is empty; it fails fast. After Milestone B
  every synthesised encoder node has `codec` in `Params`. The
  audit grep
  ```
  grep -rn -E '\bout\.(CodecVideo|CodecAudio|CodecSubtitle|EncoderParams[A-Z]\w+|FPSMode|AudioSync|ForceKeyFrames|PassLogFile|EncoderTimeBase|FieldOrder|InterlacedEncode)\b|outCfg\.(CodecVideo|CodecAudio|FPSMode)' \
    pipeline/ runtime/ graph/ | grep -v _test.go
  ```
  shows only the two documented `runLinear` exemptions in
  [pipeline/engine.go](../pipeline/engine.go). Validation helpers
  in [pipeline/encoder_timing.go](../pipeline/encoder_timing.go)
  and [pipeline/color_hdr.go](../pipeline/color_hdr.go) also read
  these fields, but they run at config-parse time, before
  `NormalizeConfig`, so they are not runtime reads.

