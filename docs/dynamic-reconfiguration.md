# Dynamic Reconfiguration

MediaMolder supports live parameter changes and structural modifications
to running pipelines without dropping frames.

## Pipeline.Reconfigure() — Parameter Changes

Change filter parameters on a running pipeline:

```go
err := pipeline.Reconfigure("volume_filter", map[string]any{
    "volume": "0.5",
})
```

### How It Works

1. Validates the pipeline is in PLAYING or PAUSED state
2. Locates the filter's `FilterGraph` in the running `graphRunner`
3. Sends each parameter as an `avfilter_graph_send_command` to the named filter
4. Updates the internal parameter tracking
5. Emits a `ReconfigureComplete` event

This uses FFmpeg's built-in command interface, which processes commands
between frames — no frames are dropped or reordered.

### Supported Use Cases

- Changing `drawtext` parameters (text, position, fontsize)
- Adjusting `volume` levels
- Modifying filter parameters that support `avfilter_graph_send_command`

### JSON Config

Nodes must be declared as filters with a `filter` field:

```json
{
  "id": "text_overlay",
  "type": "filter",
  "filter": "drawtext",
  "params": {
    "text": "Hello World",
    "fontsize": 24,
    "x": 10,
    "y": 10
  }
}
```

### ReconfigureComplete Event

```go
type ReconfigureComplete struct {
    NodeID string
    Params map[string]any
    Time   time.Time
}
```

## Pipeline.AddOutput() — Structural Changes

Add a new output to a running pipeline:

```go
ack, err := pipeline.AddOutput(pipeline.Output{
    ID:         "hls_out",
    URL:        "/var/media/stream.m3u8",
    Format:     "hls",
    CodecVideo: "libx264",
})
<-ack // Wait for the change to be live.
```

### Quiesce-Drain-Apply Flow

1. **Quiesce**: Pipeline is paused to stop data flow
2. **Apply**: The new output is added to the pipeline config
3. **Resume**: Pipeline is resumed; the graph rebuilds with the new output

The returned `ack` channel is closed when the change is live.

### Constraints

- Pipeline must be in PLAYING or PAUSED state
- Output ID must be unique (no duplicates)
- Output must have a valid URL

### OutputAdded Event

```go
type OutputAdded struct {
    OutputID string
    Time     time.Time
}
```
