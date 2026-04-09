package clock

import (
	"testing"
	"time"
)

func TestPTSToDuration(t *testing.T) {
	c := New(1, 90000, false)

	dur := c.PTSToDuration(90000)
	if dur != time.Second {
		t.Fatalf("expected 1s, got %v", dur)
	}

	dur = c.PTSToDuration(45000)
	if dur != 500*time.Millisecond {
		t.Fatalf("expected 500ms, got %v", dur)
	}
}

func TestSyncCheck(t *testing.T) {
	c := New(1, 1000, false)

	status := c.CheckSync(1030, 1000)
	if status.Exceeded {
		t.Fatal("30ms drift should not exceed 40ms tolerance")
	}

	status = c.CheckSync(1050, 1000)
	if !status.Exceeded {
		t.Fatal("50ms drift should exceed 40ms tolerance")
	}

	status = c.CheckSync(1000, 1050)
	if !status.Exceeded {
		t.Fatal("50ms negative drift should exceed tolerance")
	}
}

func TestClockUpdateAndMediaTime(t *testing.T) {
	c := New(1, 25, false)

	c.Update(50)
	if c.PTS() != 50 {
		t.Fatalf("expected PTS=50, got %d", c.PTS())
	}
	mt := c.MediaTime()
	if mt != 2*time.Second {
		t.Fatalf("expected 2s media time, got %v", mt)
	}
}

func TestClockReset(t *testing.T) {
	c := New(1, 1000, false)
	c.Update(5000)
	c.Reset()
	if c.PTS() != 0 {
		t.Fatalf("expected PTS=0 after reset, got %d", c.PTS())
	}
}
