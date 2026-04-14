# Event Bus

MediaMolder's pipeline emits structured events for observability.

## Event types

| Event           | Fields                                  | Description                        |
|-----------------|-----------------------------------------|------------------------------------|
| `StateChanged`  | `From`, `To` (State), `Duration`        | Pipeline state transition          |
| `ErrorEvent`    | `NodeID`, `Stage`, `Err`, `Time`        | Error in a processing node         |
| `EOS`           | (none)                                  | End of stream reached              |
| `StreamStart`   | `NodeID`, `MediaType`                   | New stream started processing      |
| `BufferOverflow`| `Dropped`                               | Events dropped due to full buffer  |
| `BufferingPercent` | `NodeID`, `Percent`, `Time`          | Node buffer fill level (0.0–1.0)   |
| `MetricsSnapshotEvent` | `Snapshot`, `Time`               | Periodic metrics snapshot          |
| `ClockLost`     | `Reason`, `Time`                        | Pipeline clock source unavailable  |
| `ReconfigureComplete` | `NodeID`, `Params`, `Time`        | Live filter parameter change done  |
| `OutputAdded`   | `OutputID`, `Time`                      | New output added to running pipeline |
| `NodeRestart`   | `NodeID`, `Attempt`, `Err`, `Time`      | Node restarted after transient error |
| `ErrorPolicyApplied` | `NodeID`, `Policy`, `Err`, `Attempt`, `Backoff`, `FallbackNode` | Error policy invoked |

## Subscribing

The event bus uses a buffered Go channel. Events are non-blocking: if the consumer is slow, events are dropped and counted.

```go
p, _ := pipeline.NewPipeline(cfg)

// Get the event channel
ch := p.Events().Chan()

// Consume events in a goroutine
go func() {
    for ev := range ch {
        switch e := ev.(type) {
        case pipeline.StateChanged:
            fmt.Printf("state: %v → %v\n", e.From, e.To)
        case pipeline.ErrorEvent:
            fmt.Printf("error in %s: %v\n", e.NodeID, e.Err)
        case pipeline.EOS:
            fmt.Println("stream complete")
        case pipeline.StreamStart:
            fmt.Printf("stream started: %s (%s)\n", e.NodeID, e.MediaType)
        case pipeline.BufferingPercent:
            fmt.Printf("buffer %s: %.0f%%\n", e.NodeID, e.Percent*100)
        case pipeline.MetricsSnapshotEvent:
            fmt.Printf("metrics: %d nodes, state=%s\n", len(e.Snapshot.Nodes), e.Snapshot.State)
        case pipeline.ClockLost:
            fmt.Printf("clock lost: %s\n", e.Reason)
        case pipeline.ReconfigureComplete:
            fmt.Printf("reconfigured %s: %v\n", e.NodeID, e.Params)
        case pipeline.OutputAdded:
            fmt.Printf("output added: %s\n", e.OutputID)
        case pipeline.NodeRestart:
            fmt.Printf("node %s restart attempt %d: %v\n", e.NodeID, e.Attempt, e.Err)
        }
    }
}()

// Run pipeline
p.Run(ctx)
```

## Buffer sizing

The default buffer size is 256 events. For high-throughput pipelines, you may need to consume events promptly to avoid drops.

Check how many events were dropped:

```go
dropped := p.Events().Dropped()
```

## Complete example

```go
package main

import (
    "context"
    "fmt"
    "github.com/MediaMolder/MediaMolder/pipeline"
)

func main() {
    cfg, _ := pipeline.ParseConfigFile("config.json")
    p, _ := pipeline.NewPipeline(cfg)

    // Monitor events
    go func() {
        for ev := range p.Events().Chan() {
            switch e := ev.(type) {
            case pipeline.StateChanged:
                fmt.Printf("[%v] %v → %v\n", e.Duration, e.From, e.To)
            case pipeline.EOS:
                fmt.Println("Done!")
            case pipeline.ErrorEvent:
                fmt.Printf("ERROR [%s/%s]: %v\n", e.NodeID, e.Stage, e.Err)
            }
        }
    }()

    if err := p.Run(context.Background()); err != nil {
        fmt.Printf("pipeline error: %v\n", err)
    }
}
```
