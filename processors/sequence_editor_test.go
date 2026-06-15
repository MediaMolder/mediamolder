// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// initWithTransition builds a minimal two-clip sequence_editor params object
// whose first clip carries the given transition JSON, and runs Init. The
// transition snippet is the value of the clip's "transition" key (or "" for
// no transition).
func initWithTransition(t *testing.T, transition string) error {
	t.Helper()
	trans := ""
	if transition != "" {
		trans = `, "transition": ` + transition
	}
	raw := `{
      "format": { "width": 320, "height": 240, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000] },
      "tracks": [
        { "id": "V1", "type": "video", "clips": [
          { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0` + trans + ` },
          { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 3 }
        ]}
      ]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	return (&SequenceEditor{}).Init(params)
}

// An unsupported transition type must be rejected at Init (not silently
// rendered as a hard cut), pointing the user at xfade_sequence.
func TestSequenceEditor_RejectsUnsupportedTransition(t *testing.T) {
	err := initWithTransition(t, `{ "type": "notarealtransition", "duration": 0.5 }`)
	if err == nil {
		t.Fatal("expected error for unsupported transition type")
	}
	if !strings.Contains(err.Error(), "notarealtransition") {
		t.Errorf("error %q should name the bad type", err.Error())
	}
}

// A transition block without a positive duration is meaningless and must error.
func TestSequenceEditor_RejectsTransitionWithoutDuration(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve" }`); err == nil {
		t.Fatal("expected error for transition with no duration")
	}
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0 }`); err == nil {
		t.Fatal("expected error for transition with zero duration")
	}
}

// dissolve is accepted; a bare {duration} defaults to dissolve.
func TestSequenceEditor_AcceptsDissolve(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0.5 }`); err != nil {
		t.Fatalf("dissolve rejected: %v", err)
	}
	if err := initWithTransition(t, `{ "duration": 0.5 }`); err != nil {
		t.Fatalf("bare-duration transition rejected: %v", err)
	}
	// No transition at all is fine (hard cut between clips).
	if err := initWithTransition(t, ""); err != nil {
		t.Fatalf("no-transition sequence rejected: %v", err)
	}
}

// xfade transition names are now accepted at Init (rendered via the xfade
// graph path).
func TestSequenceEditor_AcceptsXfadeTransitions(t *testing.T) {
	for _, typ := range []string{"fade", "wipeleft", "slideright", "circleopen", "fadeblack", "hblur"} {
		if err := initWithTransition(t, `{ "type": "`+typ+`", "duration": 0.5 }`); err != nil {
			t.Errorf("xfade transition %q rejected: %v", typ, err)
		}
	}
}

// OutputFrameCount reports duration × frame rate so the runner can show render
// progress (frame X of N).
func TestOutputFrameCount(t *testing.T) {
	raw := `{
      "format": { "width": 64, "height": 64, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000], "length_sec": 5 },
      "tracks": [ { "id": "V1", "type": "video", "clips": [
        { "url": "a.mp4", "source_in": 0, "source_out": 5, "timeline_in": 0 }
      ]}]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	se := &SequenceEditor{}
	if err := se.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if got := se.OutputFrameCount(); got != 150 { // 5 s × 30 fps
		t.Errorf("OutputFrameCount = %d, want 150", got)
	}
}

// Audio is opt-in: without format.sample_rate the sequence emits only video;
// with it, OutputStreams gains a second (audio) stream carrying the sequence's
// sample rate / channels / planar-float working format.
func TestOutputStreamsAudioOptIn(t *testing.T) {
	base := `{
      "format": { "width": 64, "height": 64, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000], "length_sec": 2 %s },
      "tracks": [ { "id": "V1", "type": "video", "clips": [
        { "url": "a.mp4", "source_in": 0, "source_out": 2, "timeline_in": 0 }
      ]}]
    }`
	initSeq := func(extra string) *SequenceEditor {
		var params map[string]any
		if err := json.Unmarshal([]byte(fmt.Sprintf(base, extra)), &params); err != nil {
			t.Fatalf("test JSON invalid: %v", err)
		}
		se := &SequenceEditor{}
		if err := se.Init(params); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return se
	}

	// Video-only: one stream.
	vo := initSeq("")
	if vo.audioEnabled() {
		t.Error("audio should be disabled without sample_rate")
	}
	if got := vo.OutputStreams(); len(got) != 1 || got[0].Type != av.MediaTypeVideo {
		t.Fatalf("video-only OutputStreams=%v, want one video stream", got)
	}

	// Audio enabled: two streams; audio carries fltp + sample rate + default 2ch.
	au := initSeq(`, "sample_rate": 48000`)
	if !au.audioEnabled() {
		t.Fatal("audio should be enabled with sample_rate")
	}
	streams := au.OutputStreams()
	if len(streams) != 2 {
		t.Fatalf("OutputStreams len=%d, want 2", len(streams))
	}
	a := streams[1]
	if a.Type != av.MediaTypeAudio || a.SampleRate != 48000 || a.Channels != 2 || a.SampleFmt != av.SampleFmtFLTP {
		t.Errorf("audio stream = %+v, want audio/48000/2ch/fltp", a)
	}
	if a.TimeBase != [2]int{1, 48000} {
		t.Errorf("audio time_base = %v, want {1,48000}", a.TimeBase)
	}

	// channels override is honoured.
	mono := initSeq(`, "sample_rate": 44100, "channels": 1`)
	if got := mono.OutputStreams()[1]; got.Channels != 1 || got.SampleRate != 44100 {
		t.Errorf("mono audio stream = %+v, want 1ch/44100", got)
	}
}

// The per-clip transition.audio override is parsed into the transition's Audio*
// fields; absent, audio auto-couples (empty curve, zero duration, not off).
func TestTransitionAudioOverrideParsing(t *testing.T) {
	mk := func(audioJSON string) *seqTransition {
		audio := ""
		if audioJSON != "" {
			audio = `, "audio": ` + audioJSON
		}
		raw := fmt.Sprintf(`{
          "format": { "width": 64, "height": 64, "pix_fmt": "yuv420p", "frame_rate": 30, "sample_rate": 48000 },
          "tracks": [ { "id": "V1", "type": "video", "clips": [
            { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0,
              "transition": { "type": "wipeleft", "duration": 1.0%s } },
            { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 2 }
          ]}]
        }`, audio)
		var params map[string]any
		if err := json.Unmarshal([]byte(raw), &params); err != nil {
			t.Fatalf("test JSON invalid: %v", err)
		}
		se := &SequenceEditor{}
		if err := se.Init(params); err != nil {
			t.Fatalf("Init: %v", err)
		}
		return se.tracks[0].Clips[0].Transition
	}

	def := mk("")
	if def.AudioCurve != "" || def.AudioDuration != 0 || def.AudioOff {
		t.Errorf("default coupling: got curve=%q dur=%v off=%v", def.AudioCurve, def.AudioDuration, def.AudioOff)
	}

	ov := mk(`{ "curve": "qsin", "duration": 0.3, "off": false }`)
	if ov.AudioCurve != "qsin" || ov.AudioDuration != 0.3 || ov.AudioOff {
		t.Errorf("override: got curve=%q dur=%v off=%v", ov.AudioCurve, ov.AudioDuration, ov.AudioOff)
	}

	off := mk(`{ "off": true }`)
	if !off.AudioOff {
		t.Errorf("audio off override not parsed")
	}
}

// SupportedAudioTransitions backs the GUI audio crossfade picker; it must be
// sorted, non-empty, and include the default curve.
func TestSupportedAudioTransitions(t *testing.T) {
	ts := SupportedAudioTransitions()
	if len(ts) == 0 {
		t.Fatal("SupportedAudioTransitions returned empty")
	}
	for i := 1; i < len(ts); i++ {
		if ts[i-1] > ts[i] {
			t.Fatalf("not sorted at %d: %q > %q", i, ts[i-1], ts[i])
		}
	}
	have := map[string]bool{}
	for _, n := range ts {
		have[n] = true
	}
	for _, n := range []string{"tri", "qsin", "exp", "log"} {
		if !have[n] {
			t.Errorf("SupportedAudioTransitions missing %q", n)
		}
	}
}

// SupportedTransitions backs the GUI transition picker, so it must be sorted,
// non-empty, and contain the names the engine renders.
func TestSupportedTransitions(t *testing.T) {
	ts := SupportedTransitions()
	if len(ts) == 0 {
		t.Fatal("SupportedTransitions returned empty")
	}
	for i := 1; i < len(ts); i++ {
		if ts[i-1] > ts[i] {
			t.Fatalf("not sorted at %d: %q > %q", i, ts[i-1], ts[i])
		}
	}
	have := map[string]bool{}
	for _, n := range ts {
		have[n] = true
	}
	for _, n := range []string{"dissolve", "wipeleft", "zoomin", "circleclose", "hblur", "vuslice"} {
		if !have[n] {
			t.Errorf("SupportedTransitions missing %q", n)
		}
	}
}

// Regression: a non-dissolve transition converts two sources to the sequence
// format in the same timestep, then composites them with xfade. Each clipReader
// must own an independent scale+format converter. A single SequenceEditor-level
// converter shared across the two sources corrupted one of the two converted
// frames' chroma, producing garbage colour-difference data on the composited
// transition frame. This asserts converters are built per-source and never
// shared, so a regression back to a shared converter is caught here.
func TestSequenceEditor_PerSourceConverterIsolation(t *testing.T) {
	raw := `{
      "format": { "width": 64, "height": 64, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000] },
      "tracks": [
        { "id": "V1", "type": "video", "clips": [
          { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0 },
          { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 3 }
        ]}
      ]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	se := &SequenceEditor{}
	if err := se.Init(params); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// Two sources with identical StreamInfo, mirroring the two same-format clips
	// that triggered the bug. ensureReaderConverter only reads r.si, so no demux
	// or decoder is needed.
	mkReader := func(url string) *clipReader {
		return &clipReader{
			url: url,
			si: av.StreamInfo{
				Type:              av.MediaTypeVideo,
				Width:             128,
				Height:            128,
				PixFmt:            se.format.PixFmtInt,
				TimeBase:          [2]int{1, 90000},
				SampleAspectRatio: [2]int{1, 1},
			},
		}
	}
	rA := mkReader("a.mp4")
	rB := mkReader("b.mp4")
	defer rA.close()
	defer rB.close()

	se.ensureReaderConverter(rA)
	se.ensureReaderConverter(rB)

	if rA.converter == nil || rB.converter == nil {
		t.Fatalf("per-source converters not built: rA=%v rB=%v", rA.converter, rB.converter)
	}
	if rA.converter == rB.converter {
		t.Fatal("the two sources share one converter graph; a transition will corrupt chroma — each clipReader must own its converter")
	}

	// Idempotent: re-ensuring keeps the same per-source instance (no rebuild).
	prev := rA.converter
	se.ensureReaderConverter(rA)
	if rA.converter != prev {
		t.Fatal("ensureReaderConverter rebuilt an existing converter instead of reusing it")
	}
}
