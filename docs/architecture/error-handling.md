# Error Handling

MediaMolder uses a per-node error policy model that lets each processing node
declare how errors should be handled. Policies are configured in the JSON
pipeline config and evaluated at runtime by the `ErrorPolicyEngine`.

## PipelineError

Every error surfaced during pipeline execution is wrapped in a `PipelineError`:

```go
type PipelineError struct {
    NodeID    string // which node produced the error
    Stage     string // "demux", "decode", "filter", "encode", "mux"
    Err       error  // the underlying error
    Transient bool   // true if the error is likely temporary
}
```

The `Transient` flag is critical — it controls whether automatic restart
logic (see below) will attempt recovery.

## Error Policies

Each node in the graph may specify an `error_policy` in its JSON definition:

```json
{
  "id": "scale",
  "type": "filter",
  "filter": "scale",
  "params": { "width": 1280, "height": 720 },
  "error_policy": {
    "policy": "retry",
    "max_retries": 5,
    "fallback_node": "passthrough"
  }
}
```

Four policies are available:

### abort (default)

Cancels the pipeline context immediately. All goroutines drain and the
pipeline transitions to an error state.

```json
{ "policy": "abort" }
```

### skip

Drops the current packet or frame, logs a warning, and continues processing.
An `ErrorPolicyApplied` event is emitted.

```json
{ "policy": "skip" }
```

### retry

Retries the operation with exponential backoff:

- **Base delay**: 100ms
- **Multiplier**: 2× per attempt
- **Maximum delay**: 5 seconds
- **Default max retries**: 3

On exhaustion, the engine checks for a `fallback_node`. If one is configured,
a `FallbackRequested` error is returned. Otherwise the pipeline aborts.

```json
{
  "policy": "retry",
  "max_retries": 5
}
```

Backoff sequence: 100ms → 200ms → 400ms → 800ms → 1600ms → 3200ms → 5000ms (capped).

### fallback

Routes the stream to an alternate node. If no `fallback_node` is configured,
escalates to abort.

```json
{
  "policy": "fallback",
  "fallback_node": "passthrough_filter"
}
```

## Automatic Node Restart

When a node returns a `PipelineError` with `Transient == true`, the
`NodeRestarter` wraps the node's work function and automatically:

1. Emits a `NodeRestart` event with the attempt number
2. Delegates to the `ErrorPolicyEngine` (which applies backoff for retry policies)
3. Re-invokes the node function if the policy permits

Non-transient errors and context cancellation propagate immediately.

## Events

The error system emits the following events:

| Event | When |
|-------|------|
| `ErrorEvent` | Every error, before policy evaluation |
| `ErrorPolicyApplied` | After a policy is invoked (skip, retry, fallback) |
| `NodeRestart` | When a node goroutine is restarted after a transient error |

## Crash Reports

On unrecoverable errors or panics, a `CrashReporter` captures:

- Graph snapshot and per-node state
- Buffer levels and last N events (ring buffer)
- Full Go stack traces
- Build information
- Pipeline metrics at time of crash

Reports are written as JSON files to a configurable directory.
