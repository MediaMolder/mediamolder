// Package clock implements the pipeline clock and A/V synchronization
// model for file-based and real-time inputs.
//
// The Pipeline clock provides PTS/DTS tracking, A/V sync tolerance checks,
// and wall-clock pacing for live outputs.
package clock

import (
	"sync"
	"time"
)

// SyncTolerance is the default A/V drift tolerance (±40ms).
const SyncTolerance = 40 * time.Millisecond

// Source identifies how a clock derives its time reference.
type Source int

const (
	// SourceSystem uses the monotonic system clock (default for file inputs).
	SourceSystem Source = iota
	// SourceInput derives time from a live input stream.
	SourceInput
)

// Pipeline is the reference clock for a media processing pipeline.
// It tracks the pipeline's current time based on processed media PTS values
// and can optionally pace output to wall-clock time for live sources.
type Pipeline struct {
	mu       sync.Mutex
	source   Source
	baseTime time.Time // wall-clock time at pipeline start
	curPTS   int64     // current media PTS in timebase units
	tbNum    int       // time_base numerator
	tbDen    int       // time_base denominator
	realtime bool      // if true, pace to wall-clock
}

// New creates a new pipeline clock.
// If realtime is true, outputs are paced to wall-clock time.
func New(tbNum, tbDen int, realtime bool) *Pipeline {
	return &Pipeline{
		source:   SourceSystem,
		baseTime: time.Now(),
		tbNum:    tbNum,
		tbDen:    tbDen,
		realtime: realtime,
	}
}

// SetSource sets the clock source.
func (c *Pipeline) SetSource(s Source) {
	c.mu.Lock()
	c.source = s
	c.mu.Unlock()
}

// Source returns the current clock source.
func (c *Pipeline) Source() Source {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.source
}

// Update advances the pipeline clock to the given PTS.
func (c *Pipeline) Update(pts int64) {
	c.mu.Lock()
	c.curPTS = pts
	c.mu.Unlock()
}

// PTS returns the current pipeline PTS.
func (c *Pipeline) PTS() int64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.curPTS
}

// PTSToDuration converts a PTS value to a Go time.Duration.
func (c *Pipeline) PTSToDuration(pts int64) time.Duration {
	if c.tbDen == 0 {
		return 0
	}
	// pts * tbNum / tbDen → seconds
	return time.Duration(pts) * time.Duration(c.tbNum) * time.Second / time.Duration(c.tbDen)
}

// MediaTime returns the current pipeline time as a Duration.
func (c *Pipeline) MediaTime() time.Duration {
	c.mu.Lock()
	pts := c.curPTS
	c.mu.Unlock()
	return c.PTSToDuration(pts)
}

// Elapsed returns the wall-clock time since the pipeline started.
func (c *Pipeline) Elapsed() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return time.Since(c.baseTime)
}

// Realtime returns whether the clock is in real-time pacing mode.
func (c *Pipeline) Realtime() bool {
	return c.realtime
}

// Reset resets the clock state (used after seek).
func (c *Pipeline) Reset() {
	c.mu.Lock()
	c.curPTS = 0
	c.baseTime = time.Now()
	c.mu.Unlock()
}

// SyncCheck reports the A/V drift between two PTS values (video and audio).
// Returns the signed drift as a Duration and whether it exceeds the tolerance.
type SyncStatus struct {
	Drift    time.Duration
	Exceeded bool
}

// CheckSync computes the A/V drift between two PTS values.
func (c *Pipeline) CheckSync(videoPTS, audioPTS int64) SyncStatus {
	vDur := c.PTSToDuration(videoPTS)
	aDur := c.PTSToDuration(audioPTS)
	drift := vDur - aDur
	if drift < 0 {
		drift = -drift
	}
	return SyncStatus{
		Drift:    vDur - aDur,
		Exceeded: drift > SyncTolerance,
	}
}
