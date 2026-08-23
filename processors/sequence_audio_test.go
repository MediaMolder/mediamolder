package processors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

const (
	toneRate  = 32000 // Hz
	toneLeft  = 440.0 // Hz, left channel
	toneRight = 660.0 // Hz, right channel
	toneAmp   = 0.5
)

// toneAt is the generator: the left-channel sample at absolute sample index i.
// Tests compare returned samples against it to prove the reader's absolute
// position, not merely that "some tone" came back.
func toneAt(i int64) float32 {
	return float32(toneAmp * math.Sin(2*math.Pi*toneLeft*float64(i)/toneRate))
}

// writeSineWAV writes a stereo s16le RIFF/WAVE file of durSec seconds: the left
// channel a 440 Hz tone, the right 660 Hz. Plain PCM WAV is the useful fixture
// here — libavformat's wav demuxer reports the channel COUNT but leaves the
// channel LAYOUT unspecified, which is exactly what DV/AVI tape captures and
// other PCM sources present to the resampler (TestAudioReaderLayoutlessPCMIsNotSilent
// asserts that property rather than assuming it).
func writeSineWAV(t *testing.T, path string, durSec int) {
	t.Helper()
	n := toneRate * durSec
	data := make([]byte, n*4)
	for i := 0; i < n; i++ {
		tt := float64(i) / toneRate
		l := int16(toneAmp * 32767 * math.Sin(2*math.Pi*toneLeft*tt))
		r := int16(toneAmp * 32767 * math.Sin(2*math.Pi*toneRight*tt))
		binary.LittleEndian.PutUint16(data[i*4:], uint16(l))
		binary.LittleEndian.PutUint16(data[i*4+2:], uint16(r))
	}
	hdr := make([]byte, 44)
	copy(hdr[0:], "RIFF")
	binary.LittleEndian.PutUint32(hdr[4:], uint32(36+len(data)))
	copy(hdr[8:], "WAVE")
	copy(hdr[12:], "fmt ")
	binary.LittleEndian.PutUint32(hdr[16:], 16)
	binary.LittleEndian.PutUint16(hdr[20:], 1) // PCM
	binary.LittleEndian.PutUint16(hdr[22:], 2) // stereo
	binary.LittleEndian.PutUint32(hdr[24:], toneRate)
	binary.LittleEndian.PutUint32(hdr[28:], toneRate*4)
	binary.LittleEndian.PutUint16(hdr[32:], 4)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))
	if err := os.WriteFile(path, append(hdr, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func openToneReader(t *testing.T, durSec int) *audioReader {
	t.Helper()
	wav := filepath.Join(t.TempDir(), "tone.wav")
	writeSineWAV(t, wav, durSec)
	ar := openAudioReader(wav, toneRate, 2)
	if ar == nil || ar.audIdx < 0 {
		t.Fatalf("open audio reader: %+v", ar)
	}
	t.Cleanup(ar.close)
	return ar
}

// left returns the left-channel samples the reader serves for [sec, sec+n).
func left(t *testing.T, ar *audioReader, sec float64, n int) []float32 {
	t.Helper()
	f, err := ar.getSamples(sec, n)
	if err != nil || f == nil {
		t.Fatalf("getSamples(%v): f=%v err=%v", sec, f, err)
	}
	defer f.Close()
	return append([]float32(nil), f.SamplePlaneF32(0)...)
}

// expectAt checks the reader's first returned sample for time sec against the
// generator at that absolute sample index. The request time is chosen by the
// caller so the generator is NOT near zero there — 440 Hz crosses zero at
// every integer second, where an off-by-one-second landing would pass.
func expectAt(t *testing.T, ar *audioReader, idx int64, n int) []float32 {
	t.Helper()
	sec := float64(idx) / toneRate
	want := toneAt(idx)
	if w := want; w > -0.2 && w < 0.2 {
		t.Fatalf("test bug: generator is %.3f at sample %d — pick an index where it is not near zero", w, idx)
	}
	got := left(t, ar, sec, n)
	if d := got[0] - want; d > 0.05 || d < -0.05 {
		t.Fatalf("sample at %.5fs (index %d) = %.3f, generator says %.3f", sec, idx, got[0], want)
	}
	return got
}

// sampleAt selects an absolute sample index near sec seconds where the
// generator is far from zero: 8 samples past the second puts 440 Hz at
// sin(2π·0.11) ≈ 0.64 × amplitude.
func sampleAt(sec int64) int64 { return sec*toneRate + 8 }

// A PCM source whose frames carry no channel layout must still convert. Before
// the layout was pinned, every frame was refused as INPUT_CHANGED, the FIFO
// never filled, fill() demuxed the file to EOF, and the clip's audio was
// silence — with the whole source read to produce it.
func TestAudioReaderLayoutlessPCMIsNotSilent(t *testing.T) {
	ar := openToneReader(t, 4)

	// The fixture must really be layout-less, or this test proves nothing: decode
	// one frame straight from the reader's decoder and look at its layout.
	for {
		ar.pkt.Unref()
		if err := ar.demux.ReadPacket(ar.pkt); err != nil {
			t.Fatalf("read fixture: %v", err)
		}
		if ar.pkt.StreamIndex() != ar.audIdx {
			continue
		}
		if err := ar.dec.SendPacket(ar.pkt); err != nil {
			t.Fatalf("send: %v", err)
		}
		f, _ := av.AllocFrame()
		if err := ar.dec.ReceiveFrame(f); err != nil {
			f.Close()
			continue
		}
		specified := f.ChannelLayoutSpecified()
		f.Close()
		if specified {
			t.Fatal("fixture's decoded frames carry a channel layout on this libav; the test needs a layout-less source")
		}
		break
	}
	ar.reposition(0) // hand the stream back to the reader at a known position
	ar.landing = false

	got := expectAt(t, ar, sampleAt(0), 4800)
	if ar.eof {
		t.Fatal("reader hit EOF serving 0.15s of a 4s file — it read the whole source")
	}
	if got[0] == 0 && got[len(got)-1] == 0 {
		t.Fatal("returned block is silent")
	}
}

// A clip placed deep into its source must seek there, not decode from the
// start: the first fetch at 50 s of a 60 s file reads a few dozen packets,
// lands sample-exactly (the slack second is discarded), and continues exactly.
func TestAudioReaderSeeksToSourceIn(t *testing.T) {
	ar := openToneReader(t, 60)

	expectAt(t, ar, sampleAt(50), 3200)
	if ar.eof {
		t.Fatal("reader hit EOF after one fetch at 50s of 60s")
	}
	// The wav demuxer hands out ~4 KiB packets: 50 s of stereo s16 @ 32 kHz is
	// ~1560 of them. A seek plus one second of decode-and-discard is a few dozen.
	if ar.packets > 200 {
		t.Fatalf("read %d packets to reach 50s — no seek happened", ar.packets)
	}
	if ar.noSeek || ar.landing {
		t.Fatalf("seek state after landing: noSeek=%v landing=%v", ar.noSeek, ar.landing)
	}

	// Continuation follows on without a second seek and stays exact.
	before := ar.packets
	expectAt(t, ar, sampleAt(50)+3200, 3200)
	if ar.packets-before > 20 {
		t.Fatalf("continuation read %d packets for 0.1s", ar.packets-before)
	}
}

// A short hop stays on the decode-and-discard path (cheap, exact, decoder
// warm) and lands exactly; a hop past the threshold seeks. Both checked in
// both directions: what happened, and what did not.
func TestAudioReaderNearHopDoesNotSeek(t *testing.T) {
	ar := openToneReader(t, 10)

	expectAt(t, ar, sampleAt(0), 320)
	before := ar.packets
	expectAt(t, ar, sampleAt(1), 320) // ~1 s ahead: under seekMinAheadSec
	if ar.landing || ar.noSeek {
		t.Fatalf("a 1 s hop must not seek: landing=%v noSeek=%v", ar.landing, ar.noSeek)
	}
	// Decode-and-discard of ~1 s is ~30 packets; a seek would be fewer than 5.
	if read := ar.packets - before; read < 10 {
		t.Fatalf("a 1 s hop read only %d packets — it seeked instead of discarding", read)
	}

	before = ar.packets
	expectAt(t, ar, sampleAt(8), 320) // ~7 s ahead: over the threshold
	if read := ar.packets - before; read > 60 {
		t.Fatalf("a 7 s hop read %d packets — it discarded instead of seeking", read)
	}
}

// A seek that lands on frames without a PTS must not decode the rest of the
// file waiting for one: the wait is bounded and those samples are not kept.
func TestAudioReaderLandingIsBounded(t *testing.T) {
	ar := openToneReader(t, 10)
	// Simulate a source that never stamps a PTS by keeping the reader in the
	// landing state and feeding frames through ingest directly.
	ar.reposition(0)
	f, err := av.NewAudioFrame(av.SampleFormatFromName("s16"), 2, 1024, toneRate)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	// av.NewAudioFrame leaves pts at AV_NOPTS_VALUE (negative).
	for i := 0; i < maxLandingFrames+5; i++ {
		ar.ingest(f)
	}
	if ar.landingFrames < maxLandingFrames {
		t.Fatalf("landingFrames = %d, want ≥ %d", ar.landingFrames, maxLandingFrames)
	}
	if ar.avail() != 0 {
		t.Fatalf("%d samples were appended while landing — position-less audio must be dropped", ar.avail())
	}
}
