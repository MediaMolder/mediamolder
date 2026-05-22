# Clock & Sync

MediaMolder's `clock` package tracks pipeline time and enforces A/V synchronization.

## Clock model

The pipeline clock tracks media time via PTS (Presentation Time Stamp) values. It supports two modes:

| Mode     | Behavior                                                             |
|----------|----------------------------------------------------------------------|
| File     | Clock advances as fast as processing allows. No wall-clock pacing.   |
| Realtime | Output is paced to wall-clock time. Used for live streaming sources. |

## Time base

The clock uses a configurable time base (numerator/denominator). Common values:

| Use case    | tbNum | tbDen  | 1 second =             |
|-------------|-------|--------|------------------------|
| 90kHz (MPEG)| 1     | 90000  | 90000 ticks            |
| 48kHz audio | 1     | 48000  | 48000 ticks            |
| 25fps video | 1     | 25     | 25 ticks               |

## A/V sync tolerance

MediaMolder enforces a default sync tolerance of **±40ms** between video and audio PTS values. When drift exceeds this threshold, `CheckSync` reports `Exceeded: true`.

The 40ms threshold is based on human perception research — most viewers notice A/V desync above ~45ms.

## Go API

```go
import "github.com/MediaMolder/MediaMolder/clock"

// Create a clock with 90kHz time base
c := clock.New(1, 90000, false)  // false = file mode

// Update with decoded frame PTS
c.Update(pts)

// Query current state
mediaTime := c.MediaTime()    // time.Duration
elapsed := c.Elapsed()        // wall-clock duration
currentPTS := c.PTS()         // raw PTS value

// Convert PTS to duration
dur := c.PTSToDuration(90000) // → 1s

// Check A/V sync
status := c.CheckSync(videoPTS, audioPTS)
if status.Exceeded {
    log.Printf("A/V drift: %v", status.Drift)
}
```

## Seek

After a seek operation:

1. Pipeline pauses
2. `clock.Reset()` zeroes the PTS and resets the wall-clock base
3. New frames update the clock from the seek target
4. Resume continues processing

```go
p.Seek(targetPTS)  // pauses + stores target
p.Resume()         // resumes from new position
```

## Clock source

| Source        | Description                              |
|---------------|------------------------------------------|
| `SourceSystem`| Monotonic system clock (default)         |
| `SourceInput` | Clock derived from live input timestamps |

```go
c.SetSource(clock.SourceInput) // for live sources
```
