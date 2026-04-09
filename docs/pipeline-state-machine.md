# Pipeline State Machine

MediaMolder pipelines follow a strict state machine model inspired by GStreamer.

## States

| State     | Value | Description                                    |
|-----------|-------|------------------------------------------------|
| `NULL`    | 0     | Initial/terminal state. No resources allocated.|
| `READY`   | 1     | Resources allocated but not processing.        |
| `PAUSED`  | 2     | Ready to process, data flow suspended.         |
| `PLAYING` | 3     | Actively processing media data.                |

## State diagram

```
NULL ──→ READY ──→ PAUSED ──→ PLAYING
  ↑                  ↑          │
  │                  └──────────┘
  │         (PLAYING → PAUSED)
  │
  └──────── any state (teardown)
```

## Transition rules

1. **Forward auto-walk**: `SetState(target)` walks through all intermediate states. For example, `SetState(PLAYING)` from `NULL` transitions through `READY` → `PAUSED` → `PLAYING`.
2. **Any → NULL**: Always allowed. Tears down all resources.
3. **PLAYING → PAUSED**: Allowed (pause).
4. **Other backward transitions**: Return `ErrInvalidStateTransition`.

## Go API

```go
// Create pipeline
p, err := pipeline.NewPipeline(cfg)

// Manual state transitions
p.SetState(pipeline.StatePaused)   // NULL → READY → PAUSED
p.SetState(pipeline.StatePlaying)  // PAUSED → PLAYING
p.SetState(pipeline.StateNull)     // teardown

// Convenience methods
p.Start(ctx)   // NULL → PLAYING
p.Pause()      // PLAYING → PAUSED
p.Resume()     // PAUSED → PLAYING
p.Close()      // any → NULL

// Simple run-to-completion
err = p.Run(ctx)  // Start + Wait

// Wait for completion
err = p.Wait()
```

## Events

State transitions emit `StateChanged` events on the event bus:

```go
ch := p.Events().Chan()
for ev := range ch {
    if sc, ok := ev.(pipeline.StateChanged); ok {
        fmt.Printf("State: %v → %v (took %v)\n", sc.From, sc.To, sc.Duration)
    }
}
```

## Error handling

Invalid transitions return `*ErrInvalidStateTransition`:

```go
err := p.SetState(pipeline.StateReady) // from PAUSED → error
if iste, ok := err.(*pipeline.ErrInvalidStateTransition); ok {
    fmt.Printf("cannot go from %v to %v\n", iste.From, iste.To)
}
```
