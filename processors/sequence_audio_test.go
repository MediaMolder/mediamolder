package processors

import (
	"encoding/binary"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// writeSineWAV writes a stereo s16le RIFF/WAVE file of durSec seconds at rate Hz:
// a 440 Hz tone on the left, 660 Hz on the right. Plain PCM WAV is the useful
// fixture here — libavformat's wav demuxer reports the channel COUNT but leaves
// the channel LAYOUT unspecified, which is exactly what DV/AVI tape captures
// and other PCM sources present to the resampler.
func writeSineWAV(t *testing.T, path string, rate, durSec int) {
	t.Helper()
	n := rate * durSec
	data := make([]byte, n*4)
	for i := 0; i < n; i++ {
		tt := float64(i) / float64(rate)
		l := int16(0.5 * 32767 * math.Sin(2*math.Pi*440*tt))
		r := int16(0.5 * 32767 * math.Sin(2*math.Pi*660*tt))
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
	binary.LittleEndian.PutUint32(hdr[24:], uint32(rate))
	binary.LittleEndian.PutUint32(hdr[28:], uint32(rate*4))
	binary.LittleEndian.PutUint16(hdr[32:], 4)
	binary.LittleEndian.PutUint16(hdr[34:], 16)
	copy(hdr[36:], "data")
	binary.LittleEndian.PutUint32(hdr[40:], uint32(len(data)))
	if err := os.WriteFile(path, append(hdr, data...), 0o644); err != nil {
		t.Fatal(err)
	}
}

func peakAbs(f [][]float32) float32 {
	var peak float32
	for _, ch := range f {
		for _, s := range ch {
			if s < 0 {
				s = -s
			}
			if s > peak {
				peak = s
			}
		}
	}
	return peak
}

func planes(t *testing.T, ar *audioReader, sec float64, n int) [][]float32 {
	t.Helper()
	f, err := ar.getSamples(sec, n)
	if err != nil || f == nil {
		t.Fatalf("getSamples(%v): f=%v err=%v", sec, f, err)
	}
	defer f.Close()
	out := make([][]float32, ar.outCh)
	for c := 0; c < ar.outCh; c++ {
		out[c] = append([]float32(nil), f.SamplePlaneF32(c)...)
	}
	return out
}

// A PCM source whose frames carry no channel layout must still convert. Before
// the layout was pinned, every frame was refused as INPUT_CHANGED, the FIFO
// never filled, fill() demuxed the file to EOF, and the clip's audio was
// silence — with the whole source read to produce it.
func TestAudioReaderLayoutlessPCMIsNotSilent(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "tone.wav")
	writeSineWAV(t, wav, 32000, 4)
	ar := openAudioReader(wav, 48000, 2)
	if ar == nil || ar.audIdx < 0 {
		t.Fatalf("open audio reader: %+v", ar)
	}
	defer ar.close()
	if peak := peakAbs(planes(t, ar, 0.5, 4800)); peak < 0.2 {
		t.Fatalf("audio at 0.5s is silent (peak %.3f) — the resampler refused the frames", peak)
	}
	if ar.eof {
		t.Fatal("reader hit EOF serving 0.6s of a 4s file — it read the whole source")
	}
}

// A clip placed deep into its source must seek there, not decode from the
// start. On a 60 s file the first fetch at 50 s reads a few packets, not ~1600;
// the samples it returns are the tone, not silence; and the file is not at EOF.
func TestAudioReaderSeeksToSourceIn(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "long.wav")
	writeSineWAV(t, wav, 32000, 60)
	ar := openAudioReader(wav, 32000, 2)
	if ar == nil || ar.audIdx < 0 {
		t.Fatalf("open audio reader: %+v", ar)
	}
	defer ar.close()

	out := planes(t, ar, 50.0, 3200)
	if peak := peakAbs(out); peak < 0.2 {
		t.Fatalf("audio at 50s is silent (peak %.3f)", peak)
	}
	if ar.eof {
		t.Fatal("reader hit EOF after one fetch at 50s of 60s")
	}
	// The wav demuxer hands out ~4 KiB packets: 50 s of stereo s16 @ 32 kHz is
	// ~1560 of them. A seek plus seekSlackSec of decode-and-discard is a few dozen.
	if ar.packets > 200 {
		t.Fatalf("read %d packets to reach 50s — no seek happened", ar.packets)
	}
	if ar.noSeek {
		t.Fatal("reader fell back to decode-from-start on a seekable WAV")
	}

	// The landing must be sample-exact, not merely "somewhere near 50 s": the
	// left channel is a 440 Hz sine, so the phase at the first returned sample
	// identifies the absolute position. Compare against the generator.
	want := float32(0.5 * math.Sin(2*math.Pi*440*50.0))
	got := out[0][0]
	if d := got - want; d > 0.05 || d < -0.05 {
		t.Fatalf("first sample at 50.000s = %.3f, generator says %.3f — the seek landed off-position", got, want)
	}

	// Continuation stays exact: the next fetch follows on without a second seek.
	before := ar.packets
	next := planes(t, ar, 50.1, 3200)
	want = float32(0.5 * math.Sin(2*math.Pi*440*50.1))
	if d := next[0][0] - want; d > 0.05 || d < -0.05 {
		t.Fatalf("sample at 50.100s = %.3f, want %.3f", next[0][0], want)
	}
	if ar.packets-before > 20 {
		t.Fatalf("continuation read %d packets for 0.1s", ar.packets-before)
	}
}

// A short hop stays on the decode-and-discard path (cheap, exact, decoder warm).
func TestAudioReaderNearHopDoesNotSeek(t *testing.T) {
	wav := filepath.Join(t.TempDir(), "short.wav")
	writeSineWAV(t, wav, 32000, 6)
	ar := openAudioReader(wav, 32000, 2)
	if ar == nil || ar.audIdx < 0 {
		t.Fatalf("open audio reader: %+v", ar)
	}
	defer ar.close()
	_ = planes(t, ar, 0, 320)
	_ = planes(t, ar, 1.0, 320) // 1 s ahead: under seekMinAheadSec
	if ar.landing || ar.noSeek {
		t.Fatalf("a 1 s hop must not seek: landing=%v noSeek=%v", ar.landing, ar.noSeek)
	}
	want := float32(0.5 * math.Sin(2*math.Pi*440*1.0))
	if got := planes(t, ar, 1.01, 320); got[0][0]-float32(0.5*math.Sin(2*math.Pi*440*1.01)) > 0.05 {
		t.Fatalf("position drifted after discard: %.3f vs %.3f", got[0][0], want)
	}
}
