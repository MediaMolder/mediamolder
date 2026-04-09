package clock

import (
	"testing"
	"time"
)

func TestClockSourceDefault(t *testing.T) {
	c := New(1, 90000, false)
	if c.Source() != SourceSystem {
		t.Errorf("default source: got %v, want SourceSystem", c.Source())
	}
}

func TestClockSourceInput(t *testing.T) {
	c := New(1, 90000, false)
	c.SetSource(SourceInput)
	if c.Source() != SourceInput {
		t.Errorf("source: got %v, want SourceInput", c.Source())
	}
}

func TestPTSToDuration90kHz(t *testing.T) {
	c := New(1, 90000, false)
	// 90000 ticks = 1 second
	d := c.PTSToDuration(90000)
	if d != time.Second {
		t.Errorf("duration: got %v, want 1s", d)
	}
}

func TestPTSToDuration48kHz(t *testing.T) {
	c := New(1, 48000, false)
	// 48000 ticks = 1 second
	d := c.PTSToDuration(48000)
	if d != time.Second {
		t.Errorf("duration: got %v, want 1s", d)
	}
}

func TestPTSToDuration25fps(t *testing.T) {
	c := New(1, 25, false)
	// 25 ticks = 1 second
	d := c.PTSToDuration(25)
	if d != time.Second {
		t.Errorf("duration: got %v, want 1s", d)
	}
}

func TestPTSToDurationZeroDen(t *testing.T) {
	c := New(1, 0, false)
	d := c.PTSToDuration(1000)
	if d != 0 {
		t.Errorf("duration with zero denominator: got %v, want 0", d)
	}
}

func TestMediaTime(t *testing.T) {
	c := New(1, 90000, false)
	c.Update(180000)
	mt := c.MediaTime()
	if mt != 2*time.Second {
		t.Errorf("media time: got %v, want 2s", mt)
	}
}

func TestReset(t *testing.T) {
	c := New(1, 90000, false)
	c.Update(90000)
	c.Reset()
	if c.PTS() != 0 {
		t.Errorf("PTS after reset: got %d, want 0", c.PTS())
	}
}

func TestSyncCheckWithin(t *testing.T) {
	c := New(1, 90000, false)
	// 1800 ticks = 20ms → within tolerance
	s := c.CheckSync(90000, 90000+1800)
	if s.Exceeded {
		t.Errorf("should not exceed tolerance for 20ms drift")
	}
}

func TestSyncCheckExceeded(t *testing.T) {
	c := New(1, 90000, false)
	// 5400 ticks = 60ms → exceeds 40ms tolerance
	s := c.CheckSync(90000, 90000+5400)
	if !s.Exceeded {
		t.Errorf("should exceed tolerance for 60ms drift")
	}
}

func TestSyncCheckDriftSign(t *testing.T) {
	c := New(1, 90000, false)
	s := c.CheckSync(90000, 91800)
	// video behind audio → negative drift
	if s.Drift >= 0 {
		t.Errorf("drift should be negative when video PTS < audio PTS, got %v", s.Drift)
	}
}

func TestSyncCheckExact(t *testing.T) {
	c := New(1, 90000, false)
	s := c.CheckSync(90000, 90000)
	if s.Exceeded {
		t.Error("zero drift should not exceed tolerance")
	}
	if s.Drift != 0 {
		t.Errorf("drift should be 0, got %v", s.Drift)
	}
}

func TestRealtime(t *testing.T) {
	c := New(1, 90000, true)
	if !c.Realtime() {
		t.Error("expected realtime=true")
	}
	c2 := New(1, 90000, false)
	if c2.Realtime() {
		t.Error("expected realtime=false")
	}
}

func TestElapsed(t *testing.T) {
	c := New(1, 90000, false)
	// Elapsed should be very small since we just created the clock
	e := c.Elapsed()
	if e > time.Second {
		t.Errorf("elapsed too large: %v", e)
	}
}
