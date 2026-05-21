# Threading Architecture

This document specifies how MediaMolder controls CPU threading for codec
operations (decoding, encoding, filtering). Threading directly affects
pipeline throughput and is the primary knob for exploiting multi-core hardware.

---

## Problem

FFmpeg's libavcodec supports two internal threading models (frame-level and
slice-level), but MediaMolder never configures them. All codecs default to
FFmpeg's auto-detection (`thread_count = 0` → `min(cores+1, 16)`). This has
three consequences:

1. **No per-node tuning.** A slow encoder gets the same thread count as a
   fast filter, even though the encoder is the bottleneck.
2. **No global cap.** On a 64-core machine, four independent decode + encode
   chains could spawn `4 × 2 × 16 = 128` codec threads, overwhelming the
   scheduler.
3. **Dead config.** `global_options.threads` is parsed from JSON but never
   applied. `SecurityConfig.MaxThreads` is declared but never enforced.

---

## Design

### Thread Count Resolution

Thread counts are resolved per codec context using a three-level hierarchy:

```
Per-node ("threads" in node params)
    ↓ fallback
Global ("global_options.threads" in config)
    ↓ fallback
FFmpeg auto (0 = let libavcodec decide)
```

If a node's `params` contains a `"threads"` key, that value is used for that
codec. Otherwise, if `global_options.threads` is set, that value is used.
Otherwise, 0 is passed (FFmpeg auto-detect).

### Thread Type

FFmpeg supports two threading models:

| Value | Constant | Behavior |
|-------|----------|----------|
| `"frame"` | `FF_THREAD_FRAME` | Decode/encode multiple frames concurrently. Adds latency. |
| `"slice"` | `FF_THREAD_SLICE` | Decode/encode slices of one frame concurrently. No added latency. |
| `"frame+slice"` | Both | Use both models if codec supports them. |
| `""` (default) | 0 | Let FFmpeg choose based on codec capabilities. |

Thread type can be set per node via `params.thread_type` or globally via
`global_options.thread_type`, following the same fallback hierarchy as thread
count.

### SecurityConfig.MaxThreads Enforcement

When `SecurityConfig.MaxThreads > 0`, the resolved thread count for each codec
is clamped: `min(resolved, maxThreads)`. This prevents resource exhaustion in
multi-tenant deployments.

### DecoderOptions

`OpenDecoder` currently takes `(input, streamIndex)` with no options. A new
`DecoderOptions` struct is introduced:

```go
// av/decode.go

type DecoderOptions struct {
    ThreadCount int    // 0 = FFmpeg auto
    ThreadType  string // "frame", "slice", "frame+slice", "" = auto
}

func OpenDecoderWithOptions(input *InputFormatContext, streamIndex int, opts DecoderOptions) (*DecoderContext, error)
```

The original `OpenDecoder(input, streamIndex)` remains unchanged (backward
compatible, uses FFmpeg defaults).

### EncoderOptions Extension

`EncoderOptions` gains two new fields:

```go
// av/encode.go

type EncoderOptions struct {
    // ... existing fields ...

    ThreadCount int    // 0 = FFmpeg auto
    ThreadType  string // "frame", "slice", "frame+slice", "" = auto
}
```

These are applied to the `AVCodecContext` before `avcodec_open2`. The existing
`ExtraOpts["threads"]` passthrough still works but explicit fields take
priority (they're type-safe and validated).

### Pipeline Integration

The engine resolves thread counts during resource pre-opening (step 3 of
`runGraph`):

```
For each decoder:
    threads = node.Params["threads"] ?? cfg.GlobalOptions.Threads ?? 0
    threads = min(threads, securityConfig.MaxThreads) if MaxThreads > 0
    → pass to OpenDecoderWithOptions

For each encoder:
    threads = node.Params["threads"] ?? cfg.GlobalOptions.Threads ?? 0
    threads = min(threads, securityConfig.MaxThreads) if MaxThreads > 0
    → set EncoderOptions.ThreadCount
```

### Thread Budget Awareness

The total codec thread budget is:

```
totalCodecThreads = sum(resolvedThreadCount for each decoder + encoder)
goroutineThreads  = len(graph.Order)  // one per node
totalThreads      = totalCodecThreads + goroutineThreads
```

When `SecurityConfig.MaxThreads` is set, the engine logs a warning if
`totalThreads` exceeds the cap. This is advisory — enforcement happens at the
per-codec level.

---

## Files Changed

| File | Change | Description |
|------|--------|-------------|
| `av/decode.go` | Add `DecoderOptions`, `OpenDecoderWithOptions`, `parseThreadType` | Decoder threading support |
| `av/encode.go` | Add `ThreadCount`, `ThreadType` to `EncoderOptions` | Encoder threading support |
| `av/hwdecode.go` | Add threading to `HWDecoderOptions` | HW decoder threading support |
| `av/threading_test.go` | New file | Unit tests for `parseThreadType` |
| `pipeline/config.go` | Add `ThreadType` to `Options` | Global thread type config |
| `pipeline/handlers.go` | Add `resolveThreadCount`/`resolveThreadType`, update `openSource`/`createEncoder` | Wire threading in resource creation |
| `pipeline/engine.go` | Add `maxThreads` field, `SetMaxThreads`, pass `DecoderOptions` to `openSource` | Thread resolution and capping |
| `pipeline/threading_test.go` | New file | Unit tests for thread resolution and capping |
| `schema/v1.0.json` | Add `thread_type` to `global_options` | Schema update |
| `schema/v1.1.json` | Add `thread_type` to `global_options` | Schema update |

---

## Testing Strategy

- **Unit test (`av/threading_test.go`):** `TestParseThreadType` verifies string→constant
  mapping for all thread type values.
- **Unit test (`pipeline/threading_test.go`):** `TestResolveThreadCount` verifies the
  per-node > global > auto hierarchy and `MaxThreads` clamping.
- **Unit test (`pipeline/threading_test.go`):** `TestResolveThreadType` verifies the
  per-node > global fallback for thread type.
- **Unit test (`pipeline/threading_test.go`):** `TestSetMaxThreads` verifies the
  `Pipeline.SetMaxThreads` setter.
- **Build gate:** `go vet ./...` clean.

---

## Backward Compatibility

- `OpenDecoder(input, streamIndex)` remains unchanged — all existing callers
  continue to work with FFmpeg auto-threading.
- `EncoderOptions` with `ThreadCount=0` behaves identically to before.
- JSON configs without `"threads"` are unaffected.
- `SecurityConfig` with `MaxThreads=0` means no cap (existing default).
