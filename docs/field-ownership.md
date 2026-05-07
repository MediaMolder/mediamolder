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
| `FilterComplexThreads` | authoring shorthand | filter-node `Internal.Threads` (default) | Default for `AVFilterGraph.nb_threads`. Per-node `NodeDef.Threads` already wins. Normalization stamps the default onto every filter node lacking an explicit value. After Milestone C, runtime reads filter-node `Internal.Threads`, never `Config.FilterComplexThreads`. |
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
| `EncoderParamsVideo` / `EncoderParamsAudio` / `EncoderParamsSubtitle` | authoring shorthand | encoder node `Params` (AVOptions) + `Internal.*` (typed fields) | Today flattened into `NodeDef.Params` at lowering. After Milestone B, the `__*` sentinels disappear; typed fields go to `Internal`, real AVOptions stay in `Params`. |
| `FPSMode` | authoring shorthand | encoder node `Internal.FPSMode` | Currently lowered as `Params["__fps_mode"]`. Replace with typed `Internal.FPSMode` in Milestone B. |
| `AudioSync` | authoring shorthand | generated `aresample` filter node | Already lowered by `spliceAudioSyncForOutputs`. After Milestone C, sinks/encoders never read `Output.AudioSync`. |
| `Pass` | authoring shorthand | encoder node `Internal.Pass` | Currently `Params["__pass"]`. |
| `PassLogFile` | authoring shorthand | encoder node `Internal.PassLogFile` | Currently `Params["__pass_log_file"]`. |
| `LoudnormPass` | authoring shorthand | loudnorm filter node `Internal.LoudnormPass` | Currently stamped onto loudnorm filter node by `applyLoudnormShuttle`. |
| `LoudnormStatsFile` | authoring shorthand | loudnorm filter node `Internal.LoudnormStatsFile` | Same path as `LoudnormPass`. |
| `ForceKeyFrames` | authoring shorthand | encoder node `Internal.ForceKeyFrames` | Currently `Params["__force_key_frames"]`. |
| `SAR` | authoring shorthand | encoder node `Internal.SAR` | Currently `Params["__sar"]`. |
| `DAR` | authoring shorthand | encoder node `Internal.DAR` | Currently `Params["__dar"]`. Mutually exclusive with `SAR` (validator). |
| `EncoderTimeBase` | authoring shorthand | encoder node `Internal.EncoderTimeBase` | Currently `Params["__enc_time_base"]`. |
| `FieldOrder` | authoring shorthand | encoder node `Internal.FieldOrder` | Currently `Params["__field_order"]`. |
| `InterlacedEncode` | authoring shorthand | encoder node `Internal.Interlaced` | Currently `Params["__interlaced"]`. |
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
| `Threads` | node-local | filter node | Already correctly scoped; wins over `Config.FilterComplexThreads`. After Milestone B, the default lives in `Internal.Threads`, set by normalization. |
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

The invariant test clears every "authoring shorthand" field on the
executable copy of `Output` and asserts byte-identical output. The
grep audit ensures runtime code (`graphRunner`, `createEncoder`, sink
open) reads only "muxer-owned" fields from `Output` and only
"node-local" fields from graph nodes.
