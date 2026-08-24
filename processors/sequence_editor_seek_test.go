package processors

import (
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

const fixFPS = 25

// writeVideoFixture encodes frames synthetic pictures at 25 fps into path
// with the given codec and container. Content is a slowly brightening flat
// field: cheap to encode, and P-frames stay P-frames, so GOP size 12 gives
// the real long-GOP shape a seek must handle (land on a keyframe before the
// target, decode forward the rest). High-motion content here makes the
// encoder promote frames to intra and the GOP collapses to nothing.
func writeVideoFixture(t *testing.T, path, format, codec string, frames int) {
	t.Helper()
	enc, err := av.OpenEncoder(av.EncoderOptions{
		CodecName: codec,
		Width:     160,
		Height:    120,
		BitRate:   500_000,
		FrameRate: [2]int{fixFPS, 1},
		GOPSize:   12,
	})
	if err != nil {
		t.Fatalf("OpenEncoder(%s): %v", codec, err)
	}
	defer enc.Close()

	out, err := av.OpenOutputWithFormat(path, format)
	if err != nil {
		t.Fatalf("OpenOutputWithFormat(%s): %v", format, err)
	}
	idx, err := out.AddStream(enc)
	if err != nil {
		out.Close()
		t.Fatalf("AddStream: %v", err)
	}
	if err := out.WriteHeader(); err != nil {
		out.Close()
		t.Fatalf("WriteHeader: %v", err)
	}
	pkt, err := av.AllocPacket()
	if err != nil {
		out.Close()
		t.Fatal(err)
	}
	defer pkt.Close()
	drain := func() {
		for enc.ReceivePacket(pkt) == nil {
			pkt.SetStreamIndex(idx)
			pkt.Rescale(enc.TimeBase(), out.StreamTimeBase(idx))
			if err := out.WritePacket(pkt); err != nil {
				out.Close()
				t.Fatalf("WritePacket: %v", err)
			}
			pkt.Unref()
		}
	}
	fr := av.NewTestFrame(t, 160, 120, 0 /* AV_PIX_FMT_YUV420P */)
	defer fr.Close()
	for i := 0; i < frames; i++ {
		av.FillTestFrameYFlat(fr, uint8(i))
		fr.SetPTS(int64(i))
		if err := enc.SendFrame(fr); err != nil {
			out.Close()
			t.Fatalf("SendFrame(%d): %v", i, err)
		}
		drain()
	}
	if err := enc.Flush(); err != nil {
		out.Close()
		t.Fatalf("Flush: %v", err)
	}
	drain()
	if err := out.WriteTrailer(); err != nil {
		out.Close()
		t.Fatalf("WriteTrailer: %v", err)
	}
	// The muxer writes to path+".tmp"; Close performs the rename to path.
	if err := out.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func openFixtureReader(t *testing.T, ext, format, codec string, frames int) *clipReader {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture."+ext)
	writeVideoFixture(t, path, format, codec, frames)
	r, err := openClipReader(path)
	if err != nil {
		t.Fatalf("openClipReader: %v", err)
	}
	t.Cleanup(r.close)
	return r
}

// frameSec places a decoded frame on the source timeline from its timestamp —
// the position proof, not merely "a frame came back". best_effort_timestamp,
// not pts: AVI stores only decode timestamps.
func frameSec(t *testing.T, r *clipReader, f *av.Frame) float64 {
	t.Helper()
	if f.BestEffortTimestamp() < 0 {
		t.Fatal("returned frame has no timestamp; fixture cannot prove position")
	}
	return float64(f.BestEffortTimestamp()) * float64(r.si.TimeBase[0]) / float64(r.si.TimeBase[1])
}

// frameAt fetches the frame for sec and asserts its timestamp says the same time.
func frameAt(t *testing.T, r *clipReader, sec float64) {
	t.Helper()
	f, err := r.getFrameAtSeconds(sec)
	if err != nil {
		t.Fatalf("getFrameAtSeconds(%v): %v", sec, err)
	}
	defer f.Close()
	if got := frameSec(t, r, f); got < sec-0.03 || got > sec+0.03 {
		t.Fatalf("asked for %.2fs, frame PTS says %.3fs", sec, got)
	}
}

// A clip placed deep into its source must seek there, not decode from the
// start: the first fetch at 50 s of a 60 s long-GOP file decodes a few dozen
// frames (the slack second plus the landing), not the ~1250-frame prefix,
// and the frame it returns is the one at 50 s. mpeg2video in AVI is the
// dts-only shape tape captures present: decoded frames carry pts == NOPTS
// and the position lives in best_effort_timestamp — anchoring must still
// work there.
func TestClipReaderSeeksToSourceIn(t *testing.T) {
	r := openFixtureReader(t, "avi", "avi", "mpeg2video", 60*fixFPS)

	frameAt(t, r, 50)
	if r.frames > 200 {
		t.Fatalf("decoded %d frames to reach 50s — no seek happened", r.frames)
	}
	if r.noSeek || r.landing {
		t.Fatalf("seek state after landing: noSeek=%v landing=%v", r.noSeek, r.landing)
	}

	// Continuation follows on from the hot decoder without a second seek.
	before := r.frames
	frameAt(t, r, 50+1.0/fixFPS)
	if r.frames-before > 2 {
		t.Fatalf("continuation decoded %d frames for one frame step", r.frames-before)
	}
}

// A short hop stays on the decode-and-discard path (cheap, exact, decoder
// warm); a hop past the threshold seeks. Both checked in both directions:
// what happened, and what did not.
func TestClipReaderNearHopDoesNotSeek(t *testing.T) {
	r := openFixtureReader(t, "avi", "avi", "mpeg4", 10*fixFPS)

	frameAt(t, r, 0)
	before := r.frames
	frameAt(t, r, 1.0) // ~1 s ahead: under seekMinAheadSec
	if r.landing || r.noSeek {
		t.Fatalf("a 1 s hop must not seek: landing=%v noSeek=%v", r.landing, r.noSeek)
	}
	// Decode-and-discard of ~1 s is ~25 frames; a seek's landing would be fewer.
	if d := r.frames - before; d < 20 {
		t.Fatalf("a 1 s hop decoded only %d frames — it seeked instead of discarding", d)
	}

	before = r.frames
	frameAt(t, r, 8.0) // ~7 s ahead: over the threshold
	if d := r.frames - before; d > 100 {
		t.Fatalf("a 7 s hop decoded %d frames — it discarded instead of seeking", d)
	}
}

// A seek that lands on frames without a timestamp must not decode the rest
// of the file waiting for one: the wait is bounded and position-less frames
// are never kept. No demuxer here hands back a file whose post-seek frames
// stay NOPTS, so the landing state machine is driven directly, as the audio
// test does.
func TestClipReaderLandingIsBounded(t *testing.T) {
	r := openFixtureReader(t, "avi", "avi", "mpeg4", 4*fixFPS)
	if !r.reposition(1.0) {
		t.Fatal("fixture refused the seek")
	}
	// av.AllocFrame leaves best_effort_timestamp at AV_NOPTS_VALUE (negative).
	f, err := av.AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	for i := 0; i < maxLandingFrames+5; i++ {
		if r.anchor(f) {
			t.Fatal("anchored on a frame with no timestamp")
		}
	}
	if r.landingFrames < maxLandingFrames {
		t.Fatalf("landingFrames = %d, want ≥ %d", r.landingFrames, maxLandingFrames)
	}
	if !r.landing {
		t.Fatal("landing cleared without an anchor")
	}
}

// A source that cannot anchor a seek at all (no usable time base to place a
// decoded frame) degrades to exactly the old behaviour: decode-and-discard
// the prefix from the start, position counted at the nominal rate.
func TestClipReaderUnanchorableSourceFallsBack(t *testing.T) {
	r := openFixtureReader(t, "avi", "avi", "mpeg4", 10*fixFPS)
	r.si.TimeBase = [2]int{0, 0} // a stream that cannot place frames cannot anchor

	f, err := r.getFrameAtSeconds(4.0)
	if err != nil {
		t.Fatalf("getFrameAtSeconds(4.0): %v", err)
	}
	defer f.Close()
	if r.landing {
		t.Fatal("reader left in the landing state")
	}
	// The mpeg4/AVI fixture stamps pts in 1/25 ticks: frame index directly.
	const wantIdx = int64(4 * fixFPS)
	if got := f.BestEffortTimestamp(); got != wantIdx {
		t.Fatalf("asked for 4.0s, got frame %d (want %d)", got, wantIdx)
	}
	if r.frames != wantIdx+1 {
		t.Fatalf("decoded %d frames, want exactly the %d-frame prefix", r.frames, wantIdx+1)
	}
}
