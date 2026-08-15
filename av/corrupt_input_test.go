// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// These tests pin the corrupt-input contract: damaged containers may produce
// errors, truncated results, or zero packets — but never a process death. The
// poison tests reproduce the exact crash signature observed in the wild (an
// access violation inside av_packet_unref on a packet whose struct a demuxer
// left inconsistent after "successful" reads of a malformed stream); the
// fixture tests drive real demuxers over deterministically damaged mpegts and
// AVI files, the two container families that produced it.

// writeFixture encodes n synthetic mpeg2 frames into path using the given
// container format. Frame content alternates hard between checkerboard and
// flat so the encoder emits real bitrate (the deadline test needs the file to
// dwarf the AVIO buffer).
func writeFixture(t *testing.T, path, format string, frames int) {
	t.Helper()
	enc, err := OpenEncoder(EncoderOptions{
		CodecName: "mpeg2video",
		Width:     320,
		Height:    240,
		BitRate:   2_000_000,
		FrameRate: [2]int{25, 1},
		GOPSize:   12,
	})
	if err != nil {
		t.Fatalf("OpenEncoder(mpeg2video): %v", err)
	}
	defer enc.Close()

	out, err := OpenOutputWithFormat(path, format)
	if err != nil {
		t.Fatalf("OpenOutputWithFormat(%s): %v", format, err)
	}
	defer out.Close()
	idx, err := out.AddStream(enc)
	if err != nil {
		t.Fatalf("AddStream: %v", err)
	}
	if err := out.WriteHeader(); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}

	pkt, err := AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	drain := func() {
		for enc.ReceivePacket(pkt) == nil {
			pkt.SetStreamIndex(idx)
			pkt.Rescale(enc.TimeBase(), out.StreamTimeBase(idx))
			if err := out.WritePacket(pkt); err != nil {
				t.Fatalf("WritePacket: %v", err)
			}
			pkt.Unref()
		}
	}

	fr := NewTestFrame(t, 320, 240, 0 /* AV_PIX_FMT_YUV420P */)
	defer fr.Close()
	for i := 0; i < frames; i++ {
		if i%2 == 0 {
			FillTestFrameYChecker(fr)
		} else {
			FillTestFrameYFlat(fr, uint8(i*7))
		}
		fr.SetPTS(int64(i))
		if err := enc.SendFrame(fr); err != nil {
			t.Fatalf("SendFrame(%d): %v", i, err)
		}
		drain()
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	drain()
	if err := out.WriteTrailer(); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}
}

// corruptions are deterministic ways of damaging a container: truncation
// mid-GOP, scribbled header region (lying packet/PES lengths), scribbled
// middle, and spread-out bit flips.
var corruptions = []struct {
	name   string
	mutate func([]byte) []byte
}{
	{"truncated", func(b []byte) []byte { return b[:len(b)*55/100] }},
	{"scribble-early", func(b []byte) []byte {
		for i := 1024; i < 9216 && i < len(b); i++ {
			b[i] = 0xFF
		}
		return b
	}},
	{"scribble-mid", func(b []byte) []byte {
		for i := len(b) / 3; i < len(b)/3+8192 && i < len(b); i++ {
			b[i] = byte(i * 37)
		}
		return b
	}},
	{"bitflips", func(b []byte) []byte {
		for i := 512; i < len(b); i += 509 {
			b[i] ^= 0xA5
		}
		return b
	}},
}

func corruptCopy(t *testing.T, src, dst string, mutate func([]byte) []byte) {
	t.Helper()
	b, err := os.ReadFile(src)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(dst, mutate(b), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestGuardedUnrefSurvivesPoisonedPacket reproduces the observed crash
// signature: a packet whose buf/data are all-ones pointers and whose size is
// negative. A bare av_packet_unref on this dies with an access violation that
// no recover() can catch; the guarded Unref must scrub instead, count the
// scrub, and leave the packet reusable.
func TestGuardedUnrefSurvivesPoisonedPacket(t *testing.T) {
	pkt, err := AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()

	before := ScrubbedPacketCount()
	poisonPacketForTest(pkt)
	pkt.Unref() // unguarded, this line is a process death, not a test failure
	if got := ScrubbedPacketCount(); got != before+1 {
		t.Fatalf("scrubbed count = %d, want %d", got, before+1)
	}
	if pkt.Size() != 0 || pkt.Data() != nil {
		t.Fatalf("scrubbed packet not blank: size=%d", pkt.Size())
	}
	// The scrubbed packet must remain fully usable.
	pkt.Unref()
	if got := ScrubbedPacketCount(); got != before+1 {
		t.Fatalf("clean unref after scrub was counted: %d != %d", got, before+1)
	}
}

// TestGuardedCloseSurvivesPoisonedPacket: av_packet_free unrefs internally, so
// a deferred Close crashes on the same poison unless it shares the guard.
func TestGuardedCloseSurvivesPoisonedPacket(t *testing.T) {
	pkt, err := AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	before := ScrubbedPacketCount()
	poisonPacketForTest(pkt)
	if err := pkt.Close(); err != nil { // unguarded, this is a process death
		t.Fatalf("Close: %v", err)
	}
	if got := ScrubbedPacketCount(); got != before+1 {
		t.Fatalf("scrubbed count = %d, want %d", got, before+1)
	}
}

// TestCorruptContainersDoNotKillTheProcess drives real demuxers over damaged
// mpegts and AVI files with both the plain and the resilient open, using both
// the owned loop and the manual demux idiom callers use in the wild. Any
// outcome — error, fewer packets, zero packets — is acceptable except death.
func TestCorruptContainersDoNotKillTheProcess(t *testing.T) {
	dir := t.TempDir()
	for _, format := range []struct{ name, ext string }{
		{"mpegts", ".ts"},
		{"avi", ".avi"},
	} {
		clean := filepath.Join(dir, "clean-"+format.name+format.ext)
		writeFixture(t, clean, format.name, 100)
		for _, c := range corruptions {
			c := c
			t.Run(format.name+"/"+c.name, func(t *testing.T) {
				bad := filepath.Join(dir, c.name+format.ext)
				corruptCopy(t, clean, bad, c.mutate)

				// Resilient open + owned loop.
				if in, err := OpenInputResilient(bad, nil); err == nil {
					n := 0
					if err := in.ForEachPacket(func(p *Packet) error {
						n++
						return nil
					}); err != nil {
						t.Logf("ForEachPacket ended with: %v (after %d packets)", err, n)
					}
					in.Close()
				} else {
					t.Logf("resilient open refused: %v", err)
				}

				// Plain open + the manual keyframe-scan-shaped loop.
				in, err := OpenInput(bad, nil)
				if err != nil {
					t.Logf("plain open refused: %v", err)
					return
				}
				defer in.Close()
				pkt, err := AllocPacket()
				if err != nil {
					t.Fatal(err)
				}
				defer pkt.Close()
				keyframes := 0
				for {
					pkt.Unref()
					if e := in.ReadPacket(pkt); e != nil {
						break
					}
					if pkt.IsKeyFrame() {
						keyframes++
					}
				}
				t.Logf("manual loop: %d keyframes", keyframes)
			})
		}
	}
}

// TestForEachPacketOnWellFormedInput pins the owned loop's contract on a clean
// file: every packet delivered, early stop via ErrStopDemux, EOF returns nil.
func TestForEachPacketOnWellFormedInput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "clean.ts")
	writeFixture(t, path, "mpegts", 50)

	in, err := OpenInputResilient(path, nil)
	if err != nil {
		t.Fatalf("OpenInputResilient: %v", err)
	}
	defer in.Close()

	total := 0
	if err := in.ForEachPacket(func(p *Packet) error {
		if p.Size() <= 0 {
			t.Fatalf("delivered packet with size %d", p.Size())
		}
		total++
		return nil
	}); err != nil {
		t.Fatalf("ForEachPacket: %v", err)
	}
	if total < 50 {
		t.Fatalf("delivered %d packets for 50 frames", total)
	}

	in2, err := OpenInput(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer in2.Close()
	stopped := 0
	if err := in2.ForEachPacket(func(p *Packet) error {
		stopped++
		return ErrStopDemux
	}); err != nil {
		t.Fatalf("ErrStopDemux must surface as nil, got %v", err)
	}
	if stopped != 1 {
		t.Fatalf("callback ran %d times after ErrStopDemux", stopped)
	}
}

// TestReadDeadlineInterrupts proves the watchdog turns a wedged-or-slow read
// into a typed error: with an already-expired deadline, demuxing must fail
// with IsInterrupted as soon as the demuxer needs the I/O layer, long before
// EOF on a file much larger than the AVIO buffer.
func TestReadDeadlineInterrupts(t *testing.T) {
	path := filepath.Join(t.TempDir(), "big.ts")
	writeFixture(t, path, "mpegts", 300)
	if fi, err := os.Stat(path); err != nil || fi.Size() < 128*1024 {
		t.Fatalf("fixture too small to outlast the AVIO buffer: %v bytes", fi.Size())
	}

	in, err := OpenInputResilient(path, nil)
	if err != nil {
		t.Fatalf("OpenInputResilient: %v", err)
	}
	defer in.Close()
	in.SetReadDeadline(time.Microsecond)

	pkt, err := AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	for i := 0; i < 100_000; i++ {
		pkt.Unref()
		if e := in.ReadPacket(pkt); e != nil {
			if IsEOF(e) {
				t.Fatal("reached EOF without the deadline ever firing")
			}
			if !IsInterrupted(e) {
				t.Fatalf("expected an interrupted error, got %v", e)
			}
			return
		}
	}
	t.Fatal("read loop never ended")
}
