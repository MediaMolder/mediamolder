# Reverse export: `Config` → FFmpeg CLI

**Status:** authoritative reference for the F1 reverse-lowering
exporter (sequencing in
[private_local/followups_roadmap.md](../../private_local/followups_roadmap.md),
F1.1 – F1.6).

This page documents the two public entry points that turn a
`job.Config` (the JSON job format) back into an `ffmpeg ...`
command line, when to use each, and which fields round-trip cleanly
versus which are reported on `ExportResult.Unsupported` because
they have no CLI inverse.

The exporter lives in [`compat/ffcli/export.go`](../../compat/ffcli/export.go);
the per-output / per-stream view layer lives in
[`compat/ffcli/encoder_view.go`](../../compat/ffcli/encoder_view.go).

## Entry points

### `compat/ffcli.Export(cfg *job.Config) ExportResult`

Source-of-truth: the *authoring* `job.Config`. Reads
`Output.CodecVideo`, `Output.EncoderParamsVideo`, `Output.FPSMode`,
`Output.AudioSync`, `Output.Pass`/`PassLogFile`,
`Output.ForceKeyFrames`, `Output.SAR`/`DAR`,
`Output.EncoderTimeBase`, `Output.FieldOrder`,
`Output.InterlacedEncode` directly off the `job.Output`
shorthand fields. Explicit encoder/copy/filter nodes in
`cfg.Graph` overlay this view (codec wins, AVOptions emit via
`buildEncoderNodes`).

Use this when you want a CLI from the JSON the user wrote, without
running the pipeline normaliser.

### `compat/ffcli.ExportGraph(cfg *job.Config, def *graph.Def, warnings []job.NormalizeWarning) ExportResult`

Source-of-truth: the *normalised* `*graph.Def` produced by
`job.NormalizeConfig(cfg)`. Reads codec, encoder AVOptions,
encoder shorthand (`FPSMode`, `ForceKeyFrames`, `SAR`/`DAR`,
`EncoderTimeBase`, `FieldOrder`, `Interlaced`, `Pass`,
`PassLogFile`) and audio sync from the lowered graph instead of
the `Output.*` shorthand fields. The `cfg` argument is still
required for the output URLs, muxer flags, per-stream
`Output.Streams[i]` overrides, and global options that are not
node-bound (these are the **muxer-owned** and **true-global**
classes in [field-ownership.md](field-ownership.md)).

Use this when you want a CLI that proves the round-trip through
`NormalizeConfig` is lossless — i.e. when validating that a future
schema bump can drop the `Output.*` shorthand fields safely.

## How the round-trip works

`ExportGraph` walks `def.Edges` backward from each output's sink
ports and resolves a per-output `outputView` (per-type Codec /
Params / FPSMode / ForceKeyFrames / SAR / DAR / EncTimeBase /
FieldOrder / Pass / PassLogFile / AudioSync / Interlaced) by:

1. **Synthesised `__enc__*` encoders** (`Internal.Generated.By ==
   "expandImplicitEncoders"`): `view.Codec` from `n.Params["codec"]`,
   `view.Params` from `n.Params`, shorthand from typed
   `n.Internal.Encoder`. The `(*exporter).buildEncoderNodes` pass
   skips them because synthesised encoders never appear in
   `cfg.Graph.Nodes` — only in `def.Nodes`.
2. **User-authored encoder nodes** (`Internal.Generated == nil`):
   `view.Codec` already populated by `graphCodecsForOutput`
   reading `cfg.Graph`; `view.Params` left at the shorthand
   default; `(*exporter).buildEncoderNodes` walks `cfg.Graph.Nodes`
   in declaration order and emits `-<key>:<stream> <val>` (or the
   packed `-<codec>-params:<stream>` payload for the encoders in
   [`codecToParamsFlag`](../../compat/ffcli/encoder_view.go) —
   libx264, libx264rgb, libx265, libsvtav1, librav1e, libxavs2).
3. **Copy nodes**: `view.Codec = "copy"`, `view.Params = nil`.
4. **`__async__*` audio-sync resamplers**:
   `recoverAudioSyncFromGraph` walks edges back from the output
   and parses the `aresample=async=N[:first_pts=0]` spec on any
   reachable `__async__` node back to `N`, surfaced as
   `-async:a:<idx> N`.

Slots the graph does not fill inherit the shorthand value, which
keeps round-trip parity even for configs that have only partial
graph topology (a common case today: `Output.CodecVideo` set but
no explicit encoder node in the graph). This "graph-as-overlay"
strategy is intentional pre-F2; the overlay layer disappears once
the schema deprecation lands.

## What does *not* round-trip

`Output.Streams[i].Encoder` per-stream override maps are not
lowered into graph nodes today, so they are still read off `cfg`
in both modes. Filter `Internal.Generated` shuttle nodes that
have no FFmpeg-CLI form (loudnorm two-pass) collapse silently —
the lowering pass is a graph-internal expansion of a single
authoring field. Mediamolder-only constructs surface in
`ExportResult.Unsupported`:

- `Config.Assets` and `$asset:<name>` references — resolve
  manually before running the CLI.
- Processor (`go_processor`) nodes — not expressible in FFmpeg.
- Per-node `Threads` overrides on filter nodes — the CLI has only
  pipeline-wide `-filter_complex_threads`.
- `LoudnormPass` shuttle stamping — expects a multi-pass driver
  outside FFmpeg.
- `Input.ConcatList` entries with inpoints/metadata — write a
  concat listfile and use `-f concat -i <listfile>` instead.
- `ErrorPolicy` declarations on graph nodes — no CLI equivalent.

## CLI

```sh
mediamolder export job.json
mediamolder export --from-graph job.json
```

Both forms write the FFmpeg command to stdout; `Unsupported` notes
and (for `--from-graph`) `NormalizeConfig` warnings go to stderr
prefixed with `note:` and `normalize warning:` respectively. Pipe
stdout straight to `sh` to execute:

```sh
mediamolder export --from-graph job.json | sh
```

See `cmd/mediamolder/export_test.go::TestCmdExport_FromGraph` for
the round-trip identity assertion.

## Test gates

- `TestExport_*` (compat/ffcli/export_test.go): the wide
  shorthand-source acceptance battery the F1.1 refactor must keep
  green.
- `TestExportGraph_RoundTrip` (compat/ffcli/export_graph_test.go):
  for each representative config,
  `Export(cfg).Command == ExportGraph(cfg, NormalizeConfig(cfg)).Command`.
- `TestExportGraph_ExplicitEncoderCoverage`: same identity assertion
  for user-authored encoder nodes that the implicit pass cannot
  produce on its own.
- `TestCmdExport_FromGraph`: CLI dispatch + round-trip identity end
  to end.
