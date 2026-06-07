// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// xfade_sequence — sequential timeline processor.
//
// Opens clips one at a time (≤2 decoders concurrent during transitions),
// applies libavfilter xfade blending at each join point, and streams output
// frames directly downstream.  Unlike the graph-level xfade chain approach,
// no upstream buffering of future clips occurs — memory is O(1 frame) regardless
// of timeline length.
//
// JSON params:
//
//	{
//	  "clips": [
//	    {"url": "/path/a.mp4", "in": 0, "out": 30},
//	    {"transition": "dissolve", "duration": 1.0},
//	    {"url": "/path/b.mp4", "in": 0, "out": 30},
//	    {"transition": "fade", "duration": 0.5},
//	    {"url": "/path/c.mp4", "in": 5, "out": 40}
//	  ]
//	}
//
// "in" / "out" are seconds.  "out" defaults to end-of-file when omitted.
// Clips and transitions must alternate; there must be exactly N-1 transitions
// for N clips.

import (
	"context"
	"fmt"
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// xfsClipSpec is one parsed clip entry.
type xfsClipSpec struct {
	url string
	in  float64 // seek point (seconds)
	out float64 // stop point (seconds); math.MaxFloat64 = EOF
}

// xfsTrans is one parsed transition entry.
type xfsTrans struct {
	name     string
	duration float64 // seconds
}

// XfadeSequence generates a video timeline from clips joined by xfade transitions.
// It implements Processor (to satisfy the registry interface) and FrameSource
// (so the runtime calls Run() directly instead of the per-frame Process() loop).
type XfadeSequence struct {
	clips []xfsClipSpec
	trans []xfsTrans // len == len(clips)-1
}

// Init parses the "clips" param array (alternating clip / transition objects).
func (xs *XfadeSequence) Init(params map[string]any) error {
	raw, ok := params["clips"].([]any)
	if !ok || len(raw) == 0 {
		return fmt.Errorf("xfade_sequence: 'clips' param required (non-empty array)")
	}
	for _, item := range raw {
		m, ok := item.(map[string]any)
		if !ok {
			return fmt.Errorf("xfade_sequence: each 'clips' entry must be an object")
		}
		if urlRaw, hasURL := m["url"]; hasURL {
			url, _ := urlRaw.(string)
			if url == "" {
				return fmt.Errorf("xfade_sequence: clip entry missing 'url'")
			}
			c := xfsClipSpec{url: url, in: 0, out: math.MaxFloat64}
			if v, ok := m["in"].(float64); ok {
				c.in = v
			}
			if v, ok := m["out"].(float64); ok {
				c.out = v
			}
			xs.clips = append(xs.clips, c)
		} else if _, hasInputID := m["input_id"]; hasInputID {
			// input_id should have been resolved to url by the engine before
			// Init() is called.  Reaching here means a bug in the caller.
			return fmt.Errorf("xfade_sequence: clip entry still has 'input_id' — engine must resolve input references before calling Init")
		} else if transRaw, hasTrans := m["transition"]; hasTrans {
			name, _ := transRaw.(string)
			if name == "" {
				return fmt.Errorf("xfade_sequence: transition entry missing 'transition' name")
			}
			dur, _ := m["duration"].(float64)
			if dur <= 0 {
				return fmt.Errorf("xfade_sequence: transition 'duration' must be > 0")
			}
			xs.trans = append(xs.trans, xfsTrans{name: name, duration: dur})
		} else {
			return fmt.Errorf("xfade_sequence: entry must have 'url' (clip) or 'transition' (transition)")
		}
	}
	if len(xs.clips) == 0 {
		return fmt.Errorf("xfade_sequence: no clips found")
	}
	if want := len(xs.clips) - 1; len(xs.trans) != want {
		return fmt.Errorf("xfade_sequence: expected %d transitions for %d clips, got %d",
			want, len(xs.clips), len(xs.trans))
	}
	return nil
}

// Process satisfies the Processor interface but must never be called;
// the runtime uses FrameSource.Run() for XfadeSequence nodes.
func (xs *XfadeSequence) Process(*av.Frame, ProcessorContext) (*av.Frame, *Metadata, error) {
	return nil, nil, fmt.Errorf("xfade_sequence: Process() called on a FrameSource node — this is a runtime bug")
}

// Close is a no-op; resources are managed within Run().
func (xs *XfadeSequence) Close() error { return nil }

// Run implements FrameSource.
// It opens clips sequentially, sends direct frames for the non-overlap portion
// of each clip, and runs an xfade filter graph for each transition window.
// At most two decoders are open simultaneously (current clip + next clip during
// the transition window).
func (xs *XfadeSequence) Run(ctx context.Context, send func(*av.Frame) error) error {
	var outPTSOffset int64 // cumulative output timeline advance (in clip timebase units)

	// Open the first clip; it stays open across phase-A and its transition.
	cd, err := xfsOpenClip(xs.clips[0])
	if err != nil {
		return fmt.Errorf("xfade_sequence clip 0 (%q): %w", xs.clips[0].url, err)
	}

	// clipBasePTS is the clip-timebase PTS that aligns with outPTSOffset.
	// For the first clip this is the PTS of the first decoded frame.
	// After each transition it advances to firstPTS_of_next + transitionPTS.
	var clipBasePTS int64

	for i, clip := range xs.clips {
		if i > 0 {
			// cd was set to nextCD at the end of the previous transition.
			// clipBasePTS was updated there.
		}

		tbNum := cd.si.TimeBase[0]
		tbDen := cd.si.TimeBase[1]

		// Determine where Phase A ends within this clip's PTS space.
		// For the last clip there is no transition: send everything to EOF.
		var splitPTS int64 = math.MaxInt64
		if i < len(xs.trans) {
			t := xs.trans[i]
			if clip.out == math.MaxFloat64 {
				return fmt.Errorf("xfade_sequence clip %d: 'out' is required when a transition follows", i)
			}
			// splitSec is seconds from clip.in to transition start.
			splitSec := (clip.out - clip.in) - t.duration
			if splitSec < 0 {
				return fmt.Errorf("xfade_sequence clip %d: transition duration (%.3f) exceeds clip duration (%.3f)",
					i, t.duration, clip.out-clip.in)
			}
			// We'll convert to PTS units once we have cd.firstPTS.
			// Use math.MaxInt64 as sentinel until firstPTS is known.
			_ = splitSec
		}

		// Phase A: decode and forward frames directly until the transition
		// split point (or EOF for the last clip).
		var heldFrame *av.Frame // first frame at or past the split point
		for {
			if ctx.Err() != nil {
				if heldFrame != nil {
					heldFrame.Close()
				}
				cd.close()
				return ctx.Err()
			}
			f, ferr := cd.nextFrame()
			if ferr != nil {
				if av.IsEOF(ferr) {
					break
				}
				cd.close()
				return fmt.Errorf("xfade_sequence clip %d: %w", i, ferr)
			}

			// Latch firstPTS and derive splitPTS on the first frame.
			if !cd.firstPTSSet {
				panic("xfade_sequence: nextFrame returned frame without setting firstPTS")
			}
			if clipBasePTS == 0 && i == 0 && !cd.clipBasePTSSet {
				clipBasePTS = cd.firstPTS
				cd.clipBasePTSSet = true
			}
			if splitPTS == math.MaxInt64 && i < len(xs.trans) {
				t := xs.trans[i]
				splitSec := (clip.out - clip.in) - t.duration
				splitPTS = cd.firstPTS + xfSecToPTS(splitSec, tbNum, tbDen)
			}
			// Check output stop for this clip (out time, excluding transition).
			if i < len(xs.trans) {
				endPTS := cd.firstPTS + xfSecToPTS(clip.out-clip.in, tbNum, tbDen)
				if f.PTS() >= endPTS {
					f.Close()
					break
				}
			}

			if splitPTS != math.MaxInt64 && f.PTS() >= splitPTS {
				heldFrame = f
				break
			}

			f.SetPTS(outPTSOffset + (f.PTS() - clipBasePTS))
			if err := send(f); err != nil {
				f.Close()
				cd.close()
				return err
			}
		}

		if i >= len(xs.trans) {
			// Last clip — no transition.
			if heldFrame != nil {
				heldFrame.Close()
			}
			break
		}

		// Phase B: xfade transition with the next clip.
		t := xs.trans[i]
		nextSpec := xs.clips[i+1]
		nextCD, err := xfsOpenClip(nextSpec)
		if err != nil {
			if heldFrame != nil {
				heldFrame.Close()
			}
			cd.close()
			return fmt.Errorf("xfade_sequence clip %d (%q): %w", i+1, nextSpec.url, err)
		}

		transPTS := xfSecToPTS(t.duration, tbNum, tbDen)
		leftEndPTS := cd.firstPTS + xfSecToPTS(clip.out-clip.in, tbNum, tbDen)

		// outPTSOffset at transition start = current offset + phase-A duration.
		phaseADur := splitPTS - clipBasePTS
		transOutOffset := outPTSOffset + phaseADur

		if err := xfsRunTransition(ctx, t, cd, heldFrame, leftEndPTS, nextCD, transPTS, transOutOffset, send); err != nil {
			cd.close()
			nextCD.close()
			return fmt.Errorf("xfade_sequence transition %d: %w", i, err)
		}
		cd.close()

		// Advance output offset by the full clip duration (phase A + transition).
		outPTSOffset += xfSecToPTS(clip.out-clip.in, tbNum, tbDen)

		// The next clip's Phase A starts after the transition.
		// clipBasePTS = firstPTS_of_nextCD + transPTS so that
		//   output PTS = outPTSOffset + (f.PTS() - clipBasePTS) is correct.
		if nextCD.firstPTSSet {
			clipBasePTS = nextCD.firstPTS + transPTS
		} else {
			// nextCD was never decoded into (shouldn't happen unless transition
			// duration > clip duration, caught by Init). Fall back gracefully.
			clipBasePTS = 0
		}
		nextCD.clipBasePTSSet = true
		cd = nextCD
	}

	cd.close()
	return nil
}

// ---- clip decoder ----

type xfsClipDecoder struct {
	demux         *av.InputFormatContext
	dec           *av.DecoderContext
	pkt           *av.Packet
	si            av.StreamInfo
	vidIdx        int
	firstPTS      int64
	firstPTSSet   bool
	clipBasePTSSet bool // set by Run() loop, not nextFrame()
	flushed       bool
}

func xfsOpenClip(spec xfsClipSpec) (*xfsClipDecoder, error) {
	opts := map[string]string{}
	demux, err := av.OpenInput(spec.url, opts)
	if err != nil {
		return nil, fmt.Errorf("open: %w", err)
	}

	// Find first video stream.
	vidIdx := -1
	var si av.StreamInfo
	for idx := 0; idx < demux.NumStreams(); idx++ {
		info, err := demux.StreamInfo(idx)
		if err != nil {
			continue
		}
		if info.Type == av.MediaTypeVideo {
			vidIdx = idx
			si = info
			break
		}
	}
	if vidIdx < 0 {
		demux.Close()
		return nil, fmt.Errorf("no video stream found")
	}

	dec, err := av.OpenDecoder(demux, vidIdx)
	if err != nil {
		demux.Close()
		return nil, fmt.Errorf("open decoder: %w", err)
	}

	// Seek to in point.
	if spec.in > 0 {
		targetUS := int64(spec.in * 1e6)
		if err := demux.SeekFile(targetUS); err != nil {
			dec.Close()
			demux.Close()
			return nil, fmt.Errorf("seek to %.3fs: %w", spec.in, err)
		}
		dec.Flush() //nolint:errcheck — flush decoder after seek
	}

	pkt, err := av.AllocPacket()
	if err != nil {
		dec.Close()
		demux.Close()
		return nil, err
	}

	return &xfsClipDecoder{
		demux:  demux,
		dec:    dec,
		pkt:    pkt,
		si:     si,
		vidIdx: vidIdx,
	}, nil
}

// nextFrame decodes and returns the next video frame.
// Sets cd.firstPTS on the first successful decode.
// Returns av.ErrEOF (via av.IsEOF) when the stream is exhausted.
func (cd *xfsClipDecoder) nextFrame() (*av.Frame, error) {
	for {
		f, err := av.AllocFrame()
		if err != nil {
			return nil, err
		}
		recvErr := cd.dec.ReceiveFrame(f)
		if recvErr == nil {
			if !cd.firstPTSSet {
				cd.firstPTS = f.PTS()
				cd.firstPTSSet = true
			}
			return f, nil
		}
		f.Close()
		if !av.IsEAgain(recvErr) {
			return nil, recvErr
		}

		// Feed more packets to the decoder.
		for {
			cd.pkt.Unref()
			if err := cd.demux.ReadPacket(cd.pkt); err != nil {
				if av.IsEOF(err) {
					if !cd.flushed {
						cd.flushed = true
						_ = cd.dec.Flush()
					}
					break
				}
				return nil, err
			}
			if cd.pkt.StreamIndex() != cd.vidIdx {
				continue
			}
			if err := cd.dec.SendPacket(cd.pkt); err != nil {
				if av.IsEAgain(err) {
					break // decoder full; try receive first
				}
				return nil, fmt.Errorf("SendPacket: %w", err)
			}
			break
		}
	}
}

func (cd *xfsClipDecoder) close() {
	if cd.pkt != nil {
		cd.pkt.Close()
		cd.pkt = nil
	}
	if cd.dec != nil {
		cd.dec.Close()
		cd.dec = nil
	}
	if cd.demux != nil {
		cd.demux.Close()
		cd.demux = nil
	}
}

// ---- xfade transition runner ----

// xfsRunTransition blends the tail of leftCD with the head of rightCD using
// libavfilter's xfade filter.  Frames are fed concurrently from two goroutines
// into a single-consumer filter loop, mirroring the multi-input pattern in
// handleFilter.
//
// leftHeld is the first frame >= splitPTS already decoded from leftCD; may be nil
// if leftCD hit EOF before the split.  leftEndPTS is the PTS at which leftCD
// should stop (clip.out in timebase units from cd.firstPTS origin).
// rightCD is a freshly-opened decoder for the next clip.
// transPTS is the transition duration in timebase units.
// outOffset is the cumulative output PTS to add to xfade output frames.
func xfsRunTransition(
	ctx context.Context,
	t xfsTrans,
	leftCD *xfsClipDecoder,
	leftHeld *av.Frame,
	leftEndPTS int64,
	rightCD *xfsClipDecoder,
	transPTS int64,
	outOffset int64,
	send func(*av.Frame) error,
) error {
	si := leftCD.si

	spec := fmt.Sprintf(
		"[in0][in1]xfade=transition=%s:duration=%.6f:offset=0[out0]",
		t.name, t.duration)

	fg, err := av.NewComplexFilterGraph(av.ComplexFilterGraphConfig{
		Inputs: []av.FilterPadConfig{
			{
				Label:     "in0",
				MediaType: av.MediaTypeVideo,
				Width:     si.Width, Height: si.Height, PixFmt: si.PixFmt,
				TBNum: si.TimeBase[0], TBDen: si.TimeBase[1],
				SARNum: 1, SARDen: 1,
				FRNum: si.FrameRate[0], FRDen: si.FrameRate[1],
			},
			{
				Label:     "in1",
				MediaType: av.MediaTypeVideo,
				Width:     si.Width, Height: si.Height, PixFmt: si.PixFmt,
				TBNum: si.TimeBase[0], TBDen: si.TimeBase[1],
				SARNum: 1, SARDen: 1,
				FRNum: si.FrameRate[0], FRDen: si.FrameRate[1],
			},
		},
		Outputs: []av.FilterOutputConfig{
			{Label: "out0", MediaType: av.MediaTypeVideo},
		},
		FilterSpec: spec,
	})
	if err != nil {
		if leftHeld != nil {
			leftHeld.Close()
		}
		return fmt.Errorf("build xfade filter: %w", err)
	}
	defer fg.Close()

	// normalPTS0 is the left-clip PTS that maps to xfade input 0 time = 0.
	// All left-side frames are shifted by -normalPTS0 before pushing.
	var normalPTS0 int64
	if leftHeld != nil {
		normalPTS0 = leftHeld.PTS()
	}

	type padMsg struct {
		padIdx int
		frame  *av.Frame // nil → EOS for this pad
		err    error
	}
	msgCh := make(chan padMsg, 32)

	// Goroutine A: feed pad 0 (left clip tail).
	go func() {
		send0 := func(f *av.Frame) bool {
			select {
			case msgCh <- padMsg{padIdx: 0, frame: f}:
				return true
			case <-ctx.Done():
				f.Close()
				return false
			}
		}
		if leftHeld != nil {
			leftHeld.SetPTS(0)
			if !send0(leftHeld) {
				return
			}
		}
		for {
			f, ferr := leftCD.nextFrame()
			if ferr != nil {
				if !av.IsEOF(ferr) {
					select {
					case msgCh <- padMsg{padIdx: 0, err: ferr}:
					case <-ctx.Done():
					}
				}
				break
			}
			if f.PTS() >= leftEndPTS {
				f.Close()
				break
			}
			f.SetPTS(f.PTS() - normalPTS0)
			if !send0(f) {
				return
			}
		}
		select {
		case msgCh <- padMsg{padIdx: 0}:
		case <-ctx.Done():
		}
	}()

	// Goroutine B: feed pad 1 (right clip head, up to transPTS).
	go func() {
		send1 := func(f *av.Frame) bool {
			select {
			case msgCh <- padMsg{padIdx: 1, frame: f}:
				return true
			case <-ctx.Done():
				f.Close()
				return false
			}
		}
		for {
			f, ferr := rightCD.nextFrame()
			if ferr != nil {
				if !av.IsEOF(ferr) {
					select {
					case msgCh <- padMsg{padIdx: 1, err: ferr}:
					case <-ctx.Done():
					}
				}
				break
			}
			relPTS := f.PTS() - rightCD.firstPTS
			if relPTS >= transPTS {
				f.Close()
				break
			}
			f.SetPTS(relPTS)
			if !send1(f) {
				return
			}
		}
		select {
		case msgCh <- padMsg{padIdx: 1}:
		case <-ctx.Done():
		}
	}()

	// pullOutput drains all available frames from xfade's output.
	pullOutput := func() error {
		for {
			outF, err := av.AllocFrame()
			if err != nil {
				return err
			}
			if err := fg.PullFrameAt(0, outF); err != nil {
				outF.Close()
				if av.IsEAgain(err) {
					return nil
				}
				return err // includes EOF
			}
			outF.SetPTS(outOffset + outF.PTS())
			if err := send(outF); err != nil {
				outF.Close()
				return err
			}
		}
	}

	pending := 2
	var transErr error
	for pending > 0 && transErr == nil {
		select {
		case <-ctx.Done():
			transErr = ctx.Err()
		case msg := <-msgCh:
			if msg.err != nil {
				transErr = msg.err
				break
			}
			if msg.frame == nil {
				// EOS for this pad.
				if err := fg.FlushAt(msg.padIdx); err != nil && !av.IsEOF(err) {
					transErr = err
					break
				}
				pending--
			} else {
				if err := fg.PushFrameAt(msg.padIdx, msg.frame); err != nil {
					msg.frame.Close()
					if !av.IsEOF(err) && !av.IsEAgain(err) {
						transErr = err
						break
					}
				} else {
					msg.frame.Close()
				}
			}
			if err := pullOutput(); err != nil && !av.IsEOF(err) {
				transErr = err
			}
		}
	}
	// Drain remaining output even after goroutines finish.
	if transErr == nil {
		for {
			if err := pullOutput(); err != nil {
				if av.IsEOF(err) || av.IsEAgain(err) {
					break
				}
				transErr = err
				break
			}
		}
	}
	// Drain msgCh to unblock any goroutine still trying to send.
	for len(msgCh) > 0 {
		msg := <-msgCh
		if msg.frame != nil {
			msg.frame.Close()
		}
	}
	return transErr
}

// ---- helpers ----

// xfSecToPTS converts seconds to PTS units for the given timebase (num/den).
func xfSecToPTS(sec float64, tbNum, tbDen int) int64 {
	if tbNum == 0 || tbDen == 0 {
		return int64(sec * 90000) // safe fallback: 1/90000 tb
	}
	return int64(sec * float64(tbDen) / float64(tbNum))
}

func init() {
	Register("xfade_sequence", func() Processor { return &XfadeSequence{} })
}
