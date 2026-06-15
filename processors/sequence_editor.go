// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package processors provides the sequence_editor -- a basic NLE-style
// timeline / sequence generator implemented as a FrameSource go_processor.
//
// It allows defining a fixed output video format (resolution, pix_fmt, fps,
// continuous high-precision timebase) and multiple tracks. Each track contains
// a list of placed clips. A clip specifies:
//   - the source (url or input_id -- resolved by engine before Init)
//   - sequence_in: the time in the output sequence (seconds) where this clip begins
//   - source_in: the time in the source clip (seconds) that corresponds to sequence_in
//   - duration: how long (seconds) this placement lasts in the sequence (and how much
//     source material is consumed, at 1x speed)
//
// At any output time t the "winning" clip is the one on the highest-priority
// (highest index) track that covers t. Its corresponding source frame is decoded,
// converted to the sequence format, stamped with a continuous sequence PTS, and
// emitted. If no clip covers t a black frame is emitted.
//
// This gives a simple but powerful "video editor" model: cuts, inserts, multi-cam
// selects, and layered content via track priority (upper track replaces lower where
// present). Adjacent clips on a track can carry a transition
// (clip.transition = {"type":"wipeleft","duration":<sec>}): "dissolve" is a
// linear cross-fade via the libavfilter blend filter, and every other style
// (wipes, slides, fades, circles, …) is composited by the native Go transition
// engine — see package transition and docs/architecture/transitions.md.
//
// The sequence timebase is continuous and independent of any source timebases.
// All output frames are generated at a constant frame rate with strictly
// increasing PTS.
//
// Debugging: pass "sequence_log": "/tmp/seq.jsonl" (or any path) in the
// go_processor params. You will get one JSON object per output frame (JSON Lines
// format) describing the actual ingredients and actions the renderer performed
// for that frame (winning track/clip, exact source_t fetched, conversion,
// hold vs. fresh content, etc.). The schema is future-proof for multi-layer
// blends with per-layer opacity.

package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"os"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/transition"
)

// No direct C includes here — we use a map for the common pix_fmt names
// (the av package handles the real cgo pixfmt work). This keeps the file
// easy to build/test in isolation while still supporting the formats
// users actually specify for a sequence.

// seqFormat holds the fixed output characteristics of the entire sequence.
type seqFormat struct {
	Width     int
	Height    int
	PixFmt    string // e.g. "yuv420p", "yuvj420p"
	PixFmtInt int    // AVPixelFormat value
	FrameRate float64
	// TimeBase is the continuous timebase used for all output PTS (num/den).
	// Recommended: [1, 90000] for high precision or a multiple of the frame rate.
	TimeBase  [2]int
	LengthSec float64 // optional, from "length_sec" in format; overrides computed duration
}

// seqClip describes one placement of source material onto the timeline.
type seqClip struct {
	URL        string  // resolved url (input_id is rewritten by engine before Init)
	SeqIn      float64 // sequence time (seconds) at which this clip starts
	SourceIn   float64 // source time (seconds) that maps to SeqIn
	Duration   float64 // length of this placement in sequence time (and source material consumed)
	Transition *seqTransition
}

// seqTransition holds a transition attached to a clip (the outgoing clip in a
// pair).
type seqTransition struct {
	Type     string
	Duration float64
}

// seqSupportedTransitions is the set of transition types sequence_editor can
// render. "dissolve" is a linear cross-fade done with the blend filter (kept
// for backward compatibility; note this is NOT xfade's dithered "dissolve").
// Every other name is composited by the native Go transition engine (see
// package transition and docs/architecture/transitions.md). Unknown types are
// rejected at Init rather than silently degrading to a hard cut; a supported
// name the engine does not yet implement falls back to a linear fade. The names
// mirror FFmpeg's xfade set documented in docs/using-mediamolder.md §5.15.
var seqSupportedTransitions = map[string]bool{
	"dissolve": true, // linear blend (not xfade's dithered dissolve)
	// libavfilter xfade transitions:
	"fade": true, "fadeblack": true, "fadewhite": true, "fadegrays": true,
	"wipeleft": true, "wiperight": true, "wipeup": true, "wipedown": true,
	"wipetl": true, "wipetr": true, "wipebl": true, "wipebr": true,
	"slideleft": true, "slideright": true, "slideup": true, "slidedown": true,
	"smoothleft": true, "smoothright": true, "smoothup": true, "smoothdown": true,
	"circleopen": true, "circleclose": true, "circlecrop": true, "rectcrop": true,
	"vertopen": true, "vertclose": true, "horzopen": true, "horzclose": true,
	"distance": true, "radial": true, "zoomin": true,
	"squeezeh": true, "squeezev": true,
	"hlslice": true, "hrslice": true, "vuslice": true, "vdslice": true,
	"hblur": true,
}

// seqSupportedTransitionList is the human-readable form of the above for
// error messages.
const seqSupportedTransitionList = "dissolve (linear), or any libavfilter xfade transition (fade, wipeleft, slideright, circleopen, …)"

// seqTrack is one layer in the timeline. Higher-index tracks have priority
// (their content replaces lower tracks where they are active).
type seqTrack struct {
	ID    string // original "id" from the JSON (e.g. "V1"), for logging
	Clips []seqClip
}

// seqLogLayer and seqLogFrame describe one row in the optional sequence_log
// (JSON Lines). The goal is to record *what the code actually did* for every
// output frame, not just the high-level plan. This makes it possible to debug
// extraction, conversion, compositing, and hold/freeze behaviour.
type seqLogLayer struct {
	TrackIdx   int     `json:"track_idx,omitempty"`
	TrackID    string  `json:"track_id,omitempty"`
	ClipIdx    int     `json:"clip_idx"`
	URL        string  `json:"url"`
	SourceIn   float64 `json:"source_in"`
	SourceT    float64 `json:"source_t"` // the exact source time we requested from the reader
	TimelineIn float64 `json:"timeline_in"`
	Opacity    float64 `json:"opacity"` // 1.0 = fully opaque (current v1 behaviour)
}

type seqLogFrame struct {
	I      int           `json:"i"`
	T      float64       `json:"t"`
	PTS    int64         `json:"pts,omitempty"`
	Action string        `json:"action"` // "content", "hold", "black", "none"
	Layers []seqLogLayer `json:"layers"`

	// What we actually fed to the output (after any conversion)
	SrcW      int    `json:"src_w,omitempty"`
	SrcH      int    `json:"src_h,omitempty"`
	SrcPixFmt int    `json:"src_pix_fmt,omitempty"`
	DstW      int    `json:"dst_w,omitempty"`
	DstH      int    `json:"dst_h,omitempty"`
	DstPixFmt int    `json:"dst_pix_fmt,omitempty"`
	Converter string `json:"converter,omitempty"`

	// If this frame was a hold of a previous real content frame
	HeldFromI *int `json:"held_from_i,omitempty"`

	// Free-form notes about what actually happened (e.g. "get returned EOF", "convert pull EAGAIN, fell back to native")
	Notes string `json:"notes,omitempty"`

	// Whether we actually called send() for this frame
	Sent bool `json:"sent"`
}

// SequenceEditor is a FrameSource go_processor that renders a multi-track
// timeline into a single continuous video stream with user-defined format.
type SequenceEditor struct {
	format   seqFormat
	tracks   []seqTrack
	duration float64 // computed or user-supplied total sequence length in seconds

	// runtime state. Each clipReader owns its own scale+format converter (see
	// clipReader.converter); there is deliberately no SequenceEditor-level shared
	// converter, because a transition converts two sources in the same timestep
	// and a single shared graph corrupted one of the two outputs' chroma.
	readers   map[string]*clipReader
	lastFrame *av.Frame // last successfully sent frame (used for hold/freeze on timeline gaps)

	// warnedTransitions records transition names already logged as "not
	// implemented in the Go engine; using fade", so the warning fires at most
	// once per name instead of per frame.
	warnedTransitions map[string]bool

	// lastContentI remembers the output frame index (i) of the last real content
	// frame we produced (not a hold). Used by the optional sequence log.
	lastContentI int

	// Optional detailed per-frame debug log (JSON Lines).
	// Each line describes exactly what the renderer did for that output frame:
	// which source clip(s) + exact source time(s) were fetched, what modifications
	// (scale/format) were applied, whether the frame was held from a previous one,
	// opacities for future blending, etc.
	// Enabled by passing "sequence_log": "/path/to/debug.jsonl" in the processor params.
	sequenceLog     *os.File
	sequenceLogPath string
}

// Init parses "format" and "tracks".
// "clips" entries inside tracks may use "url" (after engine resolution) or "input_id"
// (which causes an explicit error so the engine knows it must resolve first).
func (se *SequenceEditor) Init(params map[string]any) error {
	se.readers = make(map[string]*clipReader)

	// --- format (required) ---
	fm, ok := params["format"].(map[string]any)
	if !ok {
		return fmt.Errorf("sequence_editor: 'format' object is required")
	}
	if v, ok := fm["width"].(float64); ok {
		se.format.Width = int(v)
	}
	if v, ok := fm["height"].(float64); ok {
		se.format.Height = int(v)
	}
	if v, ok := fm["pix_fmt"].(string); ok {
		se.format.PixFmt = v
		se.format.PixFmtInt = pixFmtFromName(v)
	}
	if v, ok := fm["frame_rate"].(float64); ok {
		se.format.FrameRate = v
	}
	if tb, ok := fm["time_base"].([]any); ok && len(tb) == 2 {
		se.format.TimeBase[0] = int(tb[0].(float64))
		se.format.TimeBase[1] = int(tb[1].(float64))
	} else {
		se.format.TimeBase = [2]int{1, 90000} // continuous high-resolution default
	}
	if v, ok := fm["length_sec"].(float64); ok {
		se.format.LengthSec = v
	}
	if v, ok := fm["length_sec"].(float64); ok {
		se.format.LengthSec = v
	}
	if se.format.Width <= 0 || se.format.Height <= 0 || se.format.FrameRate <= 0 {
		return fmt.Errorf("sequence_editor: format must include positive width, height and frame_rate")
	}
	if se.format.PixFmtInt == 0 {
		// fallback common
		se.format.PixFmt = "yuv420p"
		se.format.PixFmtInt = pixFmtFromName(se.format.PixFmt)
	}

	// --- tracks ---
	rawTracks, ok := params["tracks"].([]any)
	if !ok || len(rawTracks) == 0 {
		return fmt.Errorf("sequence_editor: 'tracks' array is required and must be non-empty")
	}
	for _, tr := range rawTracks {
		tm, ok := tr.(map[string]any)
		if !ok {
			return fmt.Errorf("sequence_editor: each track must be an object")
		}
		var track seqTrack
		if id, ok := tm["id"].(string); ok {
			track.ID = id
		}
		rawClips, _ := tm["clips"].([]any)
		for _, ci := range rawClips {
			cm, ok := ci.(map[string]any)
			if !ok {
				return fmt.Errorf("sequence_editor: each clip must be an object")
			}
			var c seqClip
			if u, has := cm["url"].(string); has && u != "" {
				c.URL = u
			} else if _, hasID := cm["input_id"]; hasID {
				return fmt.Errorf("sequence_editor: clip still contains 'input_id' — the engine must resolve input references before Init()")
			} else {
				return fmt.Errorf("sequence_editor: clip is missing 'url'")
			}
			if v, ok := cm["sequence_in"].(float64); ok {
				c.SeqIn = v
			} else if v, ok := cm["in"].(float64); ok {
				c.SeqIn = v
			} else if v, ok := cm["timeline_in"].(float64); ok {
				c.SeqIn = v
			}
			if v, ok := cm["source_in"].(float64); ok {
				c.SourceIn = v
			} else if v, ok := cm["ss"].(float64); ok {
				c.SourceIn = v
			}
			if v, ok := cm["duration"].(float64); ok {
				c.Duration = v
			} else if v, ok := cm["t"].(float64); ok {
				c.Duration = v
			} else if so, has := cm["source_out"].(float64); has {
				c.Duration = so - c.SourceIn
			}
			if c.Duration <= 0 {
				return fmt.Errorf("sequence_editor: clip duration must be > 0 (sequence_in=%v source_in=%v)", c.SeqIn, c.SourceIn)
			}
			if transm, ok := cm["transition"].(map[string]any); ok {
				// Default an absent/empty type to the only supported one so a
				// bare {"duration": ...} works; reject any other type loudly
				// instead of silently rendering a hard cut.
				st := &seqTransition{Type: "dissolve"}
				if typ, ok := transm["type"].(string); ok && typ != "" {
					st.Type = typ
				}
				if !seqSupportedTransitions[st.Type] {
					return fmt.Errorf("sequence_editor: unsupported transition type %q (supported: %s); "+
						"for wipes, slides, fades and other styles use the xfade_sequence processor", st.Type, seqSupportedTransitionList)
				}
				dur, ok := transm["duration"].(float64)
				if !ok || dur <= 0 {
					return fmt.Errorf("sequence_editor: transition %q requires a positive 'duration' in seconds", st.Type)
				}
				st.Duration = dur
				c.Transition = st
			}
			track.Clips = append(track.Clips, c)
		}
		se.tracks = append(se.tracks, track)
	}

	// compute sequence duration (max end of any placement)
	maxEnd := 0.0
	for _, tr := range se.tracks {
		for _, c := range tr.Clips {
			end := c.SeqIn + c.Duration
			if end > maxEnd {
				maxEnd = end
			}
		}
	}
	// Prefer length_sec from format (new param for exact sequence length)
	if se.format.LengthSec > 0 {
		se.duration = se.format.LengthSec
	} else if topD, ok := params["duration"].(float64); ok && topD > maxEnd {
		se.duration = topD
	} else {
		se.duration = maxEnd
	}
	if se.duration <= 0 {
		return fmt.Errorf("sequence_editor: could not determine a positive sequence duration")
	}

	// Optional sequence debug log (JSON Lines, one object per output frame).
	// Records exactly what the renderer did for debugging (source extraction,
	// which clip(s) won, srcT actually requested, conversion, hold vs content, etc.).
	if p, ok := params["sequence_log"].(string); ok && p != "" {
		f, err := os.Create(p)
		if err != nil {
			return fmt.Errorf("sequence_editor: cannot create sequence_log %q: %w", p, err)
		}
		se.sequenceLog = f
		se.sequenceLogPath = p
	}

	return nil
}

// Process must never be called for a FrameSource.
func (se *SequenceEditor) Process(*av.Frame, ProcessorContext) (*av.Frame, *Metadata, error) {
	return nil, nil, fmt.Errorf("sequence_editor: Process() called on FrameSource node — runtime bug")
}

// Close releases all cached readers, the converter graph and black template.
func (se *SequenceEditor) Close() error {
	for _, r := range se.readers {
		if r != nil {
			r.close()
		}
	}
	se.readers = nil
	if se.lastFrame != nil {
		se.lastFrame.Close()
		se.lastFrame = nil
	}
	if se.sequenceLog != nil {
		se.sequenceLog.Close()
		se.sequenceLog = nil
	}
	return nil
}

// OutputStreamInfo reports the fixed sequence format so downstream nodes
// (scale, encoder, ...) can be configured before Run() produces the first frame.
func (se *SequenceEditor) OutputStreamInfo() (av.StreamInfo, error) {
	if se.format.Width == 0 {
		return av.StreamInfo{}, fmt.Errorf("sequence_editor: not initialised")
	}
	return av.StreamInfo{
		Type:      av.MediaTypeVideo,
		Width:     se.format.Width,
		Height:    se.format.Height,
		PixFmt:    se.format.PixFmtInt,
		FrameRate: fpsToRational(se.format.FrameRate),
		TimeBase:  se.format.TimeBase,
		BitDepth:  8, // common default; real depth is implied by PixFmt
	}, nil
}

// Run renders the timeline at the exact sequence frame rate with a continuous
// timebase. It is the heart of the FrameSource behaviour.
func (se *SequenceEditor) Run(ctx context.Context, send func(*av.Frame) error) error {
	if se.duration <= 0 {
		return nil
	}
	se.lastContentI = -1
	fmt.Printf("sequence_editor: starting Run duration=%.3f tracks=%d fps=%.3f\n", se.duration, len(se.tracks), se.format.FrameRate)
	for ti, tr := range se.tracks {
		fmt.Printf("  track%d clips=%d\n", ti, len(tr.Clips))
		for ci, c := range tr.Clips {
			if ci < 3 {
				fmt.Printf("    clip%d seqIn=%.3f dur=%.3f url=%s\n", ci, c.SeqIn, c.Duration, c.URL)
			}
		}
	}
	fps := se.format.FrameRate
	if fps <= 0 {
		fps = 25.0
	}
	tbNum := se.format.TimeBase[0]
	tbDen := se.format.TimeBase[1]
	if tbNum <= 0 || tbDen <= 0 {
		tbNum, tbDen = 1, 90000
	}

	frameInterval := 1.0 / fps
	numFrames := int(math.Round(se.duration * fps))
	if numFrames < 1 {
		numFrames = 1
	}

	for i := 0; i < numFrames; i++ {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		t := float64(i) * frameInterval // sequence time for this output frame

		// activeLayer represents one contributing source (clip) for the current t,
		// with computed opacity for blending during transitions.
		type activeLayer struct {
			trackIdx int
			trackID  string
			clipIdx  int
			clip     *seqClip
			srcT     float64
			opacity  float64
		}

		var activeLayers []activeLayer
		// Transition state for this output frame (set during window detection,
		// read by the compositing branch below). transType is "" outside a
		// transition window or "dissolve" for the linear blend path; any other
		// value routes through the native Go transition engine. transProg is the
		// timeline progress through the window (0 at the start → 1 at the end).
		var transType string
		var transProg float64
		for ti := len(se.tracks) - 1; ti >= 0; ti-- {
			tr := &se.tracks[ti]
			var trackCovers []activeLayer
			for ci := range tr.Clips {
				c := &tr.Clips[ci]
				if t >= c.SeqIn && t < c.SeqIn+c.Duration {
					srcT := c.SourceIn + (t - c.SeqIn)
					trackCovers = append(trackCovers, activeLayer{
						trackIdx: ti,
						trackID:  tr.ID,
						clipIdx:  ci,
						clip:     c,
						srcT:     srcT,
						opacity:  1.0,
					})
				}
			}
			if len(trackCovers) > 0 {
				// Detect transition windows on this track. We look for
				// consecutive clips where the earlier one declares a transition
				// whose window overlaps the current t. The extra duration
				// padding in the JSON (e.g. 10.125 instead of 10) makes the
				// coverage of outgoing and incoming overlap exactly during the
				// transition. Any supported type is handled here; the
				// compositing branch below picks blend (dissolve) vs xfade.
				if len(trackCovers) >= 2 {
					// trackCovers appended in clip-list order; assume earlier SeqIn first
					outL := &trackCovers[0]
					inL := &trackCovers[len(trackCovers)-1]
					if outL.clip.Transition != nil && outL.clip.Transition.Duration > 0 {
						dDur := outL.clip.Transition.Duration
						dStart := outL.clip.SeqIn + outL.clip.Duration - dDur
						if t >= dStart {
							prog := (t - dStart) / dDur
							if prog < 0 {
								prog = 0
							}
							if prog > 1 {
								prog = 1
							}
							outL.opacity = 1 - prog
							inL.opacity = prog
							transType = outL.clip.Transition.Type
							transProg = prog
						}
					}
				}
				activeLayers = trackCovers
				break
			}
		}

		// For compatibility with the existing hold/force fallback code we still
		// synthesize a "chosen" from the dominant (highest-opacity) active layer.
		type winner struct {
			trackIdx int
			trackID  string
			clipIdx  int
			clip     *seqClip
			srcT     float64
		}
		var chosen *winner
		if len(activeLayers) > 0 {
			main := activeLayers[0]
			for _, al := range activeLayers {
				if al.opacity > main.opacity {
					main = al
				}
			}
			chosen = &winner{
				trackIdx: main.trackIdx,
				trackID:  main.trackID,
				clipIdx:  main.clipIdx,
				clip:     main.clip,
				srcT:     main.srcT,
			}
		}

		var outFrame *av.Frame
		contentThisFrame := false // set true only when we actually obtained pixels from active layer(s)
		if len(activeLayers) > 0 {
			if len(activeLayers) == 1 {
				l := activeLayers[0]
				reader := se.getOrOpenReader(l.clip.URL)
				if reader == nil {
					// fallback to black/hold for this frame (reader open failed)
				} else {
					native, err := reader.getFrameAtSeconds(l.srcT)
					if err != nil {
						if !av.IsEOF(err) {
							return fmt.Errorf("sequence_editor: source %q at %.3fs: %w", l.clip.URL, l.srcT, err)
						}
						// source ended — fall through to black
					} else if native != nil {
						outFrame = se.convertFrame(native, reader)
						contentThisFrame = true
					}
				}
			} else {
				// Transition window: fetch natives for all (typically 2) active layers,
				// convert each to target format, then composite with the per-layer opacities.
				var cframes []*av.Frame
				for _, l := range activeLayers {
					reader := se.getOrOpenReader(l.clip.URL)
					if reader != nil {
						native, err := reader.getFrameAtSeconds(l.srcT)
						if err == nil && native != nil {
							cf := se.convertFrame(native, reader)
							if cf != nil {
								cframes = append(cframes, cf)
							}
						}
					}
				}
				if len(cframes) >= 2 && transType != "" && transType != "dissolve" {
					// Non-dissolve transition (wipes, slides, fades, …): composite
					// with the native Go transition engine. cframes[0] is outgoing,
					// cframes[1] incoming; the engine uses xfade's progress
					// convention (1 → outgoing, 0 → incoming), so we pass
					// 1-transProg. renderTransition takes ownership of both frames.
					outFrame = se.renderTransition(transType, 1-transProg, cframes[0], cframes[1])
					contentThisFrame = true
				} else if len(cframes) >= 2 {
					// Cross-dissolve (transType "dissolve"), or an undeclared overlap
					// of two clips: composite A*(1-prog) + B*prog with the blend filter.
					// activeLayers[0] is the outgoing layer, so its opacity (1-prog) is
					// the value blend wants as all_opacity (see blendTwoFrames, which
					// short-circuits the trivial 0/1 endpoints without a graph and
					// builds a one-shot blend graph for intermediate frames).
					//
					// A reusable blend graph whose all_opacity was updated per frame via
					// SendCommand was tried here, but the blend filter implements no
					// process_command: every SendCommand failed with ENOSYS ("Function
					// not implemented") and fell back to this same path anyway, logging
					// an error per frame. The reusable graph is gone; this is the path.
					outFrame = se.blendTwoFrames(cframes[0], cframes[1], activeLayers[0].opacity)
					contentThisFrame = true
				} else if len(cframes) > 0 {
					outFrame = cframes[0]
					contentThisFrame = true
				}
			}
		}

		// ------------------------------------------------------------------
		// Sequence debug logging -- record exactly what we did for this frame.
		// This must reflect reality (what getFrameAt / convert / hold actually
		// produced), not just the high-level plan from the JSON.
		// ------------------------------------------------------------------
		var logLayers []seqLogLayer
		logAction := "none"
		logNotes := ""
		logHeldFrom := (*int)(nil)

		for _, al := range activeLayers {
			logLayers = append(logLayers, seqLogLayer{
				TrackIdx:   al.trackIdx,
				TrackID:    al.trackID,
				ClipIdx:    al.clipIdx,
				URL:        al.clip.URL,
				SourceIn:   al.clip.SourceIn,
				SourceT:    al.srcT,
				TimelineIn: al.clip.SeqIn,
				Opacity:    al.opacity,
			})
		}

		// Determine action based on how outFrame was obtained.
		if len(activeLayers) > 0 {
			if outFrame != nil {
				logAction = "content"
				contentThisFrame = true
			} else {
				logAction = "hold"
				logNotes = "active layer(s) produced no usable frame; holding previous"
			}
		} else if outFrame != nil {
			logAction = "hold"
			logNotes = "no clip covered t; holding previous frame (gap or start)"
		}

		if se.lastContentI >= 0 && logAction == "hold" {
			h := se.lastContentI
			logHeldFrom = &h
		}

		if outFrame != nil {
			// Use the actual sequence time t (which is i * (1/fps)) scaled into the
			// high-resolution timebase. Using raw "i" with tbDen/tbNum produced
			// PTS deltas of 90000 ticks (1 second) per frame instead of ~3003,
			// causing the encoder/muxer to treat the stream as 1 fps over ~3895 s.
			pts := int64(t*float64(tbDen)/float64(tbNum) + 0.5)
			outFrame.SetPTS(pts)
			if err := send(outFrame); err != nil {
				outFrame.Close()
				se.emitSequenceLog(i, t, pts, logAction, logLayers, logHeldFrom, logNotes, false)
				return err
			}
			// remember a clone for future hold/freeze gaps (we own the clone)
			if se.lastFrame != nil {
				se.lastFrame.Close()
			}
			if cl, cerr := outFrame.Clone(); cerr == nil {
				se.lastFrame = cl
			}

			if contentThisFrame && chosen != nil {
				se.lastContentI = i
			}

			se.emitSequenceLog(i, t, pts, logAction, logLayers, logHeldFrom, logNotes, true)
		} else {
			// No frame was emitted for this output slot (very early gap, or total failure).
			se.emitSequenceLog(i, t, 0, logAction, logLayers, logHeldFrom, logNotes, false)
		}
	}

	return nil
}

// emitSequenceLog writes one JSON object (JSON Lines) describing exactly what
// the renderer did for output frame i. It is a no-op if no sequence_log was
// requested. The record tries to capture reality (which source frames were
// actually fetched at which times, what the converter did, whether we held a
// previous frame, etc.).
func (se *SequenceEditor) emitSequenceLog(i int, t float64, pts int64, action string, layers []seqLogLayer, heldFrom *int, notes string, sent bool) {
	if se.sequenceLog == nil {
		return
	}

	rec := seqLogFrame{
		I:         i,
		T:         t,
		PTS:       pts,
		Action:    action,
		Layers:    layers,
		HeldFromI: heldFrom,
		Notes:     notes,
		Sent:      sent,
	}

	// Fill destination format from what we declared (the encoder was configured from this)
	rec.DstW = se.format.Width
	rec.DstH = se.format.Height
	rec.DstPixFmt = se.format.PixFmtInt
	if len(se.readers) > 0 {
		rec.Converter = "scale+format (per-source av.FilterGraph)"
	}

	enc := json.NewEncoder(se.sequenceLog)
	// One object per line (JSON Lines / .jsonl). SetEscapeHTML false is nicer for paths.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rec) // best effort; debug log failure should not kill the render
}

// blendTwoFrames composites frameA (bottom/outgoing) and frameB (top/incoming)
// using the passed value as all_opacity for the blend filter (see call site for
// why we pass out.opacity = 1-prog instead of in.opacity = prog).
// The input frames are assumed to already be in the sequence's target format/size.
func (se *SequenceEditor) blendTwoFrames(frameA, frameB *av.Frame, opacityB float64) *av.Frame {
	if frameA == nil {
		if frameB != nil {
			return frameB
		}
		return nil
	}
	// Note: the passed 'opacityB' here is actually the value for the blend
	// filter's all_opacity that achieves the desired crossfade (see call site).
	// With the filter's apparent formula A*X + B*(1-X), we pass X = out.opacity
	// (1-prog) to get the correct A fading out, B fading in.
	if frameB == nil || opacityB >= 1 {
		// Fully the outgoing frame; the incoming one is discarded — close it so it
		// is not leaked (this trivial endpoint is hit every overlap frame at op=1).
		if frameB != nil {
			frameB.Close()
		}
		return frameA
	}
	if opacityB <= 0 {
		// Fully the incoming frame; discard the outgoing one.
		frameA.Close()
		return frameB
	}

	// Clear PTS on inputs (we overwrite on the output). Avoids TB mismatch
	// with the graph's declared timebase.
	if frameA != nil {
		frameA.SetPTS(0)
	}
	if frameB != nil {
		frameB.SetPTS(0)
	}

	cfg := av.ComplexFilterGraphConfig{
		Inputs: []av.FilterPadConfig{
			{
				Label:     "bottom",
				MediaType: av.MediaTypeVideo,
				Width:     se.format.Width,
				Height:    se.format.Height,
				PixFmt:    se.format.PixFmtInt,
				TBNum:     se.format.TimeBase[0],
				TBDen:     se.format.TimeBase[1],
				SARNum:    1,
				SARDen:    1,
			},
			{
				Label:     "top",
				MediaType: av.MediaTypeVideo,
				Width:     se.format.Width,
				Height:    se.format.Height,
				PixFmt:    se.format.PixFmtInt,
				TBNum:     se.format.TimeBase[0],
				TBDen:     se.format.TimeBase[1],
				SARNum:    1,
				SARDen:    1,
			},
		},
		Outputs: []av.FilterOutputConfig{
			{
				Label:     "out",
				MediaType: av.MediaTypeVideo,
			},
		},
		// The FilterSpec must explicitly connect the labeled buffer sources
		// created from Inputs to the blend filter and then to the output pad.
		// Without the [bottom][top]...[out] syntax, the graph config fails with
		// "output pad ... not connected".
		// We also force format+range right after each buffer to normalize any
		// lingering range/csp differences from the source clips (GoPro yuvj vs
		// our target yuv420p). This reduces "Changing video frame properties
		// on the fly" warnings on the [bottom] and [top] buffer instances.
		FilterSpec: fmt.Sprintf(
			"[bottom]setrange=range=tv,format=%s[b]; [top]setrange=range=tv,format=%s[t]; [b][t]blend=all_opacity=%.6f[out]",
			se.format.PixFmt, se.format.PixFmt, opacityB,
		),
	}

	fg, err := av.NewComplexFilterGraph(cfg)
	if err != nil {
		fmt.Printf("sequence_editor: failed to build blend graph (opacity=%.3f): %v\n", opacityB, err)
		return frameA
	}
	defer fg.Close()

	if err := fg.PushFrameAt(0, frameA); err != nil {
		fmt.Printf("sequence_editor: blend push bottom failed: %v\n", err)
		frameA.Close()
		frameB.Close()
		return nil
	}
	frameA.Close()

	if err := fg.PushFrameAt(1, frameB); err != nil {
		fmt.Printf("sequence_editor: blend push top failed: %v\n", err)
		frameB.Close()
		return nil
	}
	frameB.Close()

	blended, err := av.AllocFrame()
	if err != nil {
		return nil
	}
	if perr := fg.PullFrameAt(0, blended); perr != nil {
		blended.Close()
		if !av.IsEAgain(perr) && !av.IsEOF(perr) {
			fmt.Printf("sequence_editor: blend pull failed: %v\n", perr)
		}
		return nil
	}
	return blended
}

// renderTransition composites a non-dissolve transition with the native Go
// transition engine (see package transition). a is the outgoing frame, b the
// incoming; progress follows the engine's xfade convention (1 → a, 0 → b). Both
// inputs are already converted to the sequence format, so the result is a fresh
// frame of that format. Takes ownership of a and b (both are closed here) and
// returns the composited frame, or nil on alloc failure (caller holds).
//
// A supported transition name that the engine has not yet implemented falls
// back to a linear fade (logged once per name) so the timeline still renders.
func (se *SequenceEditor) renderTransition(name string, progress float64, a, b *av.Frame) *av.Frame {
	fn, ok := transition.Lookup(name)
	if !ok {
		if se.warnedTransitions == nil {
			se.warnedTransitions = map[string]bool{}
		}
		if !se.warnedTransitions[name] {
			se.warnedTransitions[name] = true
			fmt.Printf("sequence_editor: transition %q not implemented in the Go engine; using fade\n", name)
		}
		fn, _ = transition.Lookup("fade")
	}
	if fn == nil { // should not happen — "fade" is always registered
		a.Close()
		return b
	}
	out, err := av.NewVideoFrame(se.format.Width, se.format.Height, se.format.PixFmtInt)
	if err != nil {
		fmt.Printf("sequence_editor: transition output alloc failed: %v\n", err)
		a.Close()
		b.Close()
		return nil
	}
	_ = out.CopyPropsFrom(a) // colorimetry/SAR; PTS is restamped by the caller
	fn(out, a, b, progress)
	a.Close()
	b.Close()
	return out
}

// ---------- helpers ----------

func pixFmtFromName(name string) int {
	if name == "" {
		return 0
	}
	// Common AVPixelFormat values (from libavutil/pixfmt.h). This is a pragmatic
	// subset sufficient for real-world sequence_editor usage. For exotic formats
	// the user can fall back to the integer value or extend the map.
	common := map[string]int{
		"yuv420p":          0, // AV_PIX_FMT_YUV420P
		"yuyv422":          1,
		"rgb24":            2,
		"bgr24":            3,
		"yuv422p":          4,
		"yuv444p":          5,
		"yuv410p":          6,
		"yuv411p":          7,
		"gray":             8,
		"monowhite":        9,
		"monoblack":        10,
		"pal8":             11,
		"yuvj420p":         12, // AV_PIX_FMT_YUVJ420P (full range, common on cameras)
		"yuvj422p":         13,
		"yuvj444p":         14,
		"uyvy422":          17,
		"uyyvyy411":        18,
		"bgr8":             19,
		"bgr4":             20,
		"bgr4_byte":        21,
		"rgb8":             22,
		"rgb4":             23,
		"rgb4_byte":        24,
		"nv12":             25,
		"nv21":             26,
		"argb":             27,
		"rgba":             28,
		"abgr":             29,
		"bgra":             30,
		"gray16":           31,
		"yuv440p":          32,
		"yuvj440p":         33,
		"yuvA420p":         34,
		"rgb48":            35,
		"rgb565":           36,
		"rgb555":           37,
		"bgr565":           38,
		"bgr555":           39,
		"vaapi_moco":       40,
		"vaapi_idct":       41,
		"vaapi_vld":        42,
		"yuv420p16":        43,
		"yuv422p16":        44,
		"yuv444p16":        45,
		"dxva2_vld":        46,
		"rgb444":           47,
		"bgr444":           48,
		"ya8":              49,
		"bgr48":            50,
		"yuv420p9":         51,
		"yuv420p10":        52,
		"yuv422p10":        53,
		"yuv444p9":         54,
		"yuv444p10":        55,
		"yuv422p9":         56,
		"gbrp":             57,
		"gbrp9":            58,
		"gbrp10":           59,
		"gbrp16":           60,
		"yuva420p":         61,
		"yuva422p":         62,
		"yuva444p":         63,
		"yuva420p9":        64,
		"yuva422p9":        65,
		"yuva444p9":        66,
		"yuva420p10":       67,
		"yuva422p10":       68,
		"yuva444p10":       69,
		"yuva420p16":       70,
		"yuva422p16":       71,
		"yuva444p16":       72,
		"vdpau":            73,
		"xyz12":            74,
		"nv16":             75,
		"nv20":             76,
		"rgba64":           77,
		"bgra64":           78,
		"yvyu422":          79,
		"ya16":             80,
		"gbrap":            81,
		"gbrap16":          82,
		"qsv":              83,
		"mmal":             84,
		"d3d11va_vld":      85,
		"cuda":             86,
		"0rgb":             87,
		"rgb0":             88,
		"0bgr":             89,
		"bgr0":             90,
		"yuv420p12":        91,
		"yuv420p14":        92,
		"yuv422p12":        93,
		"yuv422p14":        94,
		"yuv444p12":        95,
		"yuv444p14":        96,
		"gb rp12":          97,
		"gbrp14":           98,
		"yuvj411p":         99,
		"bayer_bggr8":      100,
		"bayer_rggb8":      101,
		"bayer_gbrg8":      102,
		"bayer_grbg8":      103,
		"bayer_bggr16":     104,
		"bayer_rggb16":     105,
		"bayer_gbrg16":     106,
		"bayer_grbg16":     107,
		"xv30":             108,
		"xv36":             109,
		"xv48":             110,
		"ayuv64":           111,
		"videotoolbox_vld": 112,
		"p010":             113,
		"p016":             114,
		"y210":             115,
		"y212":             116,
		"xyuv":             117,
		"vuya":             118,
		"vuyx":             119,
		"y410":             120,
		"y412":             121,
		"v30x":             122,
		"rgb30":            123,
		"av1":              124,
		"argb32":           125,
		"abgr32":           126,
		"0rgb32":           127,
		"0bgr32":           128,
		"yuv420p16le":      129,
		// add more as needed; the important ones for real media are covered above
	}
	if v, ok := common[name]; ok {
		return v
	}
	// Unknown name — return 0 (caller should have provided a known format).
	return 0
}

func fpsToRational(fps float64) [2]int {
	if fps <= 0 {
		return [2]int{25, 1}
	}
	// simple rational approximation
	for den := 1; den <= 1001; den++ {
		num := int(math.Round(fps * float64(den)))
		if math.Abs(fps-float64(num)/float64(den)) < 1e-6 {
			return [2]int{num, den}
		}
	}
	return [2]int{int(math.Round(fps)), 1}
}

func (se *SequenceEditor) getOrOpenReader(url string) *clipReader {
	if r, ok := se.readers[url]; ok && r != nil {
		return r
	}
	r, err := openClipReader(url)
	if err != nil {
		return nil
	}
	se.readers[url] = r
	return r
}

type clipReader struct {
	url        string
	demux      *av.InputFormatContext
	dec        *av.DecoderContext
	pkt        *av.Packet
	si         av.StreamInfo
	converter  *av.FilterGraph // per-source scale+format normalizer to the sequence format (lazily built)
	vidIdx     int
	lastSrcSec float64
	sourceFPS  float64 // nominal frame rate for this source (from r_frame_rate or avg), used for reliable "skip N frames to reach source_in" without depending on PTS or broken seeks
}

func openClipReader(url string) (*clipReader, error) {
	demux, err := av.OpenInput(url, nil)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", url, err)
	}
	vidIdx := -1
	var si av.StreamInfo
	for i := 0; i < demux.NumStreams(); i++ {
		info, e := demux.StreamInfo(i)
		if e == nil && info.Type == av.MediaTypeVideo {
			vidIdx = i
			si = info
			break
		}
	}
	if vidIdx < 0 {
		demux.Close()
		return nil, fmt.Errorf("%s: no video stream", url)
	}
	dec, err := av.OpenDecoder(demux, vidIdx)
	if err != nil {
		demux.Close()
		return nil, fmt.Errorf("open decoder %s: %w", url, err)
	}

	pkt, err := av.AllocPacket()
	if err != nil {
		dec.Close()
		demux.Close()
		return nil, err
	}
	r := &clipReader{
		url:    url,
		demux:  demux,
		dec:    dec,
		pkt:    pkt,
		si:     si,
		vidIdx: vidIdx,
	}
	if si.RFrameRate[1] > 0 {
		r.sourceFPS = float64(si.RFrameRate[0]) / float64(si.RFrameRate[1])
	} else if si.FrameRate[1] > 0 {
		r.sourceFPS = float64(si.FrameRate[0]) / float64(si.FrameRate[1])
	} else {
		r.sourceFPS = 29.97
	}
	return r, nil
}

func (r *clipReader) close() {
	if r.converter != nil {
		r.converter.Close()
		r.converter = nil
	}
	if r.pkt != nil {
		r.pkt.Close()
		r.pkt = nil
	}
	if r.dec != nil {
		r.dec.Close()
		r.dec = nil
	}
	if r.demux != nil {
		r.demux.Close()
		r.demux = nil
	}
}

// getFrameAtSeconds advances the decoder from its current position (or the
// beginning of the source for a fresh reader) and returns a decoded frame
// corresponding to the requested source time. We count decoded frames using
// the source's nominal rate rather than relying on PTS matching or demux seeks
// (both of which were unreliable for these files and caused get failures for any
// clip after the first, leading to the hold-last logic freezing the output on the
// last good frame from clip 0).
func (r *clipReader) getFrameAtSeconds(sec float64) (*av.Frame, error) {
	// Compute how many frames to skip from the "start of this access" to reach the
	// requested source time. We use the source's nominal frame rate (r_frame_rate
	// preferred) rather than trying to match decoded f.PTS() against a computed
	// targetPTS. This is reliable, works for any source_in on fresh or cached
	// readers, and completely avoids the SeekFile path (which was causing immediate
	// ReadPacket EOF for clips after the first).
	// On a small-advance continuation (sec close to lastSrcSec) we skip 0 in *this call*
	// and just return the next decoded picture the decoder produces.
	skip := 0
	if r.sourceFPS <= 0 {
		r.sourceFPS = 29.97
	}
	if r.lastSrcSec > 0 && sec <= r.lastSrcSec+0.5 {
		// cheap continuation within a clip: take the very next frame(s) from the
		// hot decoder
		skip = 0
	} else {
		// (re)start or jump for this srcT on this reader: skip the prefix
		skip = int(sec*r.sourceFPS + 0.5)
	}

	framesSeenThisCall := 0
	for {
		f, err := av.AllocFrame()
		if err != nil {
			return nil, err
		}
		recvErr := r.dec.ReceiveFrame(f)
		if recvErr == nil {
			framesSeenThisCall++
			if framesSeenThisCall > skip {
				r.lastSrcSec = sec
				return f, nil
			}
			// still in the skip prefix for this access — drop and continue
			f.Close()
			continue
		}
		f.Close()
		if !av.IsEAgain(recvErr) {
			return nil, recvErr
		}

		// feed packets
		for {
			r.pkt.Unref()
			if rerr := r.demux.ReadPacket(r.pkt); rerr != nil {
				if av.IsEOF(rerr) {
					r.dec.Flush()
					return nil, av.ErrEOF
				}
				return nil, rerr
			}
			if r.pkt.StreamIndex() != r.vidIdx {
				continue
			}
			if serr := r.dec.SendPacket(r.pkt); serr != nil {
				if av.IsEAgain(serr) {
					break
				}
				return nil, fmt.Errorf("SendPacket: %w", serr)
			}
			break
		}
	}
}

// convertFrame runs a decoded source frame through the reader's (lazily built)
// scale+format converter so the frame delivered to downstream nodes (e.g. the
// encoder) exactly matches the sequence format declared in OutputStreamInfo
// (width/height/pix_fmt from the job JSON "format"). Sending unconverted native
// source frames (which may have a different pix_fmt, full-range vs limited, or
// even different resolution) to an encoder configured for the target format
// produces garbage output (vertical lines, corrupted blocks, no recognizable
// picture) and/or absurdly low bitrate because the encoder receives bogus data.
//
// The converter is owned by the clipReader, not the SequenceEditor: a transition
// converts two sources in the same timestep, and feeding both through one shared
// scale+format graph corrupted one of the two outputs' chroma (the composited
// frame showed garbage colour-difference data). Per-source graphs keep the two
// conversions independent.
func (se *SequenceEditor) convertFrame(native *av.Frame, r *clipReader) *av.Frame {
	if native == nil {
		return nil
	}
	if r == nil {
		return native
	}
	se.ensureReaderConverter(r)
	if r.converter == nil {
		// Converter could not be built for this source (logged by ensure). Fall back
		// to the native frame; downstream may still produce something or the sizes
		// may happen to match.
		return native
	}
	if err := r.converter.PushFrame(native); err != nil {
		native.Close()
		fmt.Printf("sequence_editor: converter push err: %v\n", err)
		return nil
	}
	// Graph took a ref (KEEP_REF); we can close our copy of the input frame.
	native.Close()

	// Pull the converted frame. A simple scale+format graph is 1:1 and should
	// produce output on the first Pull after Push, but the first frame can require
	// several Pull attempts while the filter configures itself (see the "changing
	// video frame properties on the fly" warnings). Keep pulling on EAGAIN for a
	// while before giving up (caller will then hold the previous lastFrame).
	for attempt := 0; attempt < 16; attempt++ {
		cf, aerr := av.AllocFrame()
		if aerr != nil {
			return nil
		}
		perr := r.converter.PullFrame(cf)
		if perr == nil {
			return cf
		}
		cf.Close()
		if !av.IsEAgain(perr) {
			if !av.IsEOF(perr) {
				fmt.Printf("sequence_editor: converter pull err: %v\n", perr)
			}
			return nil
		}
	}
	return nil
}

// ensureReaderConverter lazily builds the per-source scale+format converter that
// normalizes this reader's native frames to the sequence format. Built once per
// reader from its fixed StreamInfo; never shared across readers.
func (se *SequenceEditor) ensureReaderConverter(r *clipReader) {
	if r == nil || r.converter != nil {
		return
	}
	srcSI := r.si
	// Use scale's out_range=tv (limited range) + format to produce frames with the
	// conventional properties of yuv420p (instead of inheriting full-range "pc" /
	// yuvj from camera sources), so the frames passed to the blend/xfade graphs
	// have consistent metadata.
	spec := fmt.Sprintf("scale=%d:%d:flags=bicubic:out_range=tv,format=%s",
		se.format.Width, se.format.Height, se.format.PixFmt)
	fg, err := av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
		Width:      srcSI.Width,
		Height:     srcSI.Height,
		PixFmt:     srcSI.PixFmt,
		TBNum:      srcSI.TimeBase[0],
		TBDen:      srcSI.TimeBase[1],
		SARNum:     srcSI.SampleAspectRatio[0],
		SARDen:     srcSI.SampleAspectRatio[1],
		FilterSpec: spec,
	})
	if err != nil {
		fmt.Printf("sequence_editor: converter build failed for %dx%d pix%d -> %s: %v\n", srcSI.Width, srcSI.Height, srcSI.PixFmt, spec, err)
		// best effort — caller will fall back to black or error later
		return
	}
	r.converter = fg
}

func init() {
	Register("sequence_editor", func() Processor { return &SequenceEditor{} })
}
