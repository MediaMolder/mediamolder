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
