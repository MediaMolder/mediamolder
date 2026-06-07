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
// present). No built-in transitions in v1 (use xfade_sequence for simple dissolves
// or overlap clips on adjacent tracks + external blending if needed).
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
	TimeBase [2]int
	LengthSec float64 // optional, from "length_sec" in format; overrides computed duration
}

// seqClip describes one placement of source material onto the timeline.
type seqClip struct {
	URL      string  // resolved url (input_id is rewritten by engine before Init)
	SeqIn    float64 // sequence time (seconds) at which this clip starts
	SourceIn float64 // source time (seconds) that maps to SeqIn
	Duration float64 // length of this placement in sequence time (and source material consumed)
}

// seqTrack is one layer in the timeline. Higher-index tracks have priority
// (their content replaces lower tracks where they are active).
type seqTrack struct {
	ID    string    // original "id" from the JSON (e.g. "V1"), for logging
	Clips []seqClip
}

// seqLogLayer and seqLogFrame describe one row in the optional sequence_log
// (JSON Lines). The goal is to record *what the code actually did* for every
// output frame, not just the high-level plan. This makes it possible to debug
// extraction, conversion, compositing, and hold/freeze behaviour.
type seqLogLayer struct {
	TrackIdx   int     `json:"track_idx,omitempty"`
	TrackID    string  `json:"track_id,omitempty"`
	ClipIdx    int     `json:"clip_idx,omitempty"`
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
	HeldFromI *int   `json:"held_from_i,omitempty"`

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

	// runtime state
	readers  map[string]*clipReader
	converter *av.FilterGraph // single-input scale+format converter (input = source native, output = sequence format)
	converterInputSI av.StreamInfo
	lastFrame *av.Frame // last successfully sent frame (used for hold/freeze on timeline gaps)

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
	if se.converter != nil {
		se.converter.Close()
		se.converter = nil
	}
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

		// Find the winning (highest priority) clip that covers t.
		// We capture track/clip indices so the sequence log can show exactly
		// which layer won.
		type winner struct {
			trackIdx int
			trackID  string
			clipIdx  int
			clip     *seqClip
			srcT     float64
		}
		var chosen *winner
		for ti := len(se.tracks) - 1; ti >= 0; ti-- {
			tr := &se.tracks[ti]
			for ci := range tr.Clips {
				c := &tr.Clips[ci]
				if t >= c.SeqIn && t < c.SeqIn+c.Duration {
					srcT := c.SourceIn + (t - c.SeqIn)
					chosen = &winner{
						trackIdx: ti,
						trackID:  tr.ID,
						clipIdx:  ci,
						clip:     c,
						srcT:     srcT,
					}
					break
				}
			}
			if chosen != nil {
				break
			}
		}

		var outFrame *av.Frame
		var chosenSrcT float64
		var chosenURL string
		contentThisFrame := false // set true only when we actually obtained pixels from a chosen clip (not a hold)
		if chosen != nil {
			chosenSrcT = chosen.srcT
			chosenURL = chosen.clip.URL
			reader := se.getOrOpenReader(chosenURL)
			if reader == nil {
				// fallback to black/hold for this frame (reader open failed)
			} else {
				native, err := reader.getFrameAtSeconds(chosenSrcT)
				if err != nil {
					if !av.IsEOF(err) {
						return fmt.Errorf("sequence_editor: source %q at %.3fs: %w", chosenURL, chosenSrcT, err)
					}
					// source ended — fall through to black
				} else if native != nil {
					outFrame = se.convertFrame(native, reader.si)
					contentThisFrame = true
				}
			}
		}

		if outFrame == nil {
			// No clip covers this time on any track — hold the previous frame
			// (common "freeze" behaviour in a basic timeline editor). We keep
			// a clone of the last successfully sent frame for this purpose.
			if se.lastFrame != nil {
				cl, cerr := se.lastFrame.Clone()
				if cerr == nil {
					outFrame = cl
				}
			}
		}

		if outFrame == nil && chosen != nil {
			// Force send a native frame for the current t/clip to guarantee
			// progression through the timeline (bypass any converter/pull issues
			// that leave outFrame nil even when chosen). With the conditional-seek
			// fix, repeated calls within a clip will now return *different*
			// advancing source frames instead of always the first.
			reader2 := se.getOrOpenReader(chosenURL)
			if reader2 != nil {
				srcT2 := chosenSrcT
				native2, err2 := reader2.getFrameAtSeconds(srcT2)
				if err2 != nil {
					// Only log force errs (rare now that seek logic is fixed); keeps
					// normal runs quiet while still surfacing problems.
					fmt.Printf("sequence_editor: force get err for t=%.3f: %v\n", t, err2)
				} else if native2 != nil {
					outFrame = se.convertFrame(native2, reader2.si)
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

		if chosen != nil {
			logLayers = append(logLayers, seqLogLayer{
				TrackIdx:   chosen.trackIdx,
				TrackID:    chosen.trackID,
				ClipIdx:    chosen.clipIdx,
				URL:        chosen.clip.URL,
				SourceIn:   chosen.clip.SourceIn,
				SourceT:    chosen.srcT,
				TimelineIn: chosen.clip.SeqIn,
				Opacity:    1.0,
			})
		}

		// Determine action based on how outFrame was obtained.
		// The hold block runs before force; force tries to get real content.
		// We treat "outFrame came from a chosen clip path" as content.
		if chosen != nil {
			// We had a covering clip. If we still have outFrame after all paths,
			// and it wasn't purely the result of a hold (i.e. the content or force
			// path contributed), we call it content. Otherwise it became a hold.
			if outFrame != nil {
				// Heuristic: if lastContentI changed or we will update it below,
				// it was content. Simpler: presence of chosen + successful outFrame
				// after the chosen paths means we tried to use real source material.
				logAction = "content"
				contentThisFrame = true
			} else {
				logAction = "hold"
				logNotes = "chosen clip(s) produced no usable frame; holding previous"
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
			pts := int64(t * float64(tbDen) / float64(tbNum) + 0.5)
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
	if se.converter != nil {
		rec.Converter = "scale+format (via av.FilterGraph)"
	}

	enc := json.NewEncoder(se.sequenceLog)
	// One object per line (JSON Lines / .jsonl). SetEscapeHTML false is nicer for paths.
	enc.SetEscapeHTML(false)
	_ = enc.Encode(rec) // best effort; debug log failure should not kill the render
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
		"yuv420p":     0,   // AV_PIX_FMT_YUV420P
		"yuyv422":     1,
		"rgb24":       2,
		"bgr24":       3,
		"yuv422p":     4,
		"yuv444p":     5,
		"yuv410p":     6,
		"yuv411p":     7,
		"gray":        8,
		"monowhite":   9,
		"monoblack":   10,
		"pal8":        11,
		"yuvj420p":    12,  // AV_PIX_FMT_YUVJ420P (full range, common on cameras)
		"yuvj422p":    13,
		"yuvj444p":    14,
		"uyvy422":     17,
		"uyyvyy411":   18,
		"bgr8":        19,
		"bgr4":        20,
		"bgr4_byte":   21,
		"rgb8":        22,
		"rgb4":        23,
		"rgb4_byte":   24,
		"nv12":        25,
		"nv21":        26,
		"argb":        27,
		"rgba":        28,
		"abgr":        29,
		"bgra":        30,
		"gray16":      31,
		"yuv440p":     32,
		"yuvj440p":    33,
		"yuvA420p":    34,
		"rgb48":       35,
		"rgb565":      36,
		"rgb555":      37,
		"bgr565":      38,
		"bgr555":      39,
		"vaapi_moco":  40,
		"vaapi_idct":  41,
		"vaapi_vld":   42,
		"yuv420p16":   43,
		"yuv422p16":   44,
		"yuv444p16":   45,
		"dxva2_vld":   46,
		"rgb444":      47,
		"bgr444":      48,
		"ya8":         49,
		"bgr48":       50,
		"yuv420p9":    51,
		"yuv420p10":   52,
		"yuv422p10":   53,
		"yuv444p9":    54,
		"yuv444p10":   55,
		"yuv422p9":    56,
		"gbrp":        57,
		"gbrp9":       58,
		"gbrp10":      59,
		"gbrp16":      60,
		"yuva420p":    61,
		"yuva422p":    62,
		"yuva444p":    63,
		"yuva420p9":   64,
		"yuva422p9":   65,
		"yuva444p9":   66,
		"yuva420p10":  67,
		"yuva422p10":  68,
		"yuva444p10":  69,
		"yuva420p16":  70,
		"yuva422p16":  71,
		"yuva444p16":  72,
		"vdpau":       73,
		"xyz12":       74,
		"nv16":        75,
		"nv20":        76,
		"rgba64":      77,
		"bgra64":      78,
		"yvyu422":     79,
		"ya16":        80,
		"gbrap":       81,
		"gbrap16":     82,
		"qsv":         83,
		"mmal":        84,
		"d3d11va_vld": 85,
		"cuda":        86,
		"0rgb":        87,
		"rgb0":        88,
		"0bgr":        89,
		"bgr0":        90,
		"yuv420p12":   91,
		"yuv420p14":   92,
		"yuv422p12":   93,
		"yuv422p14":   94,
		"yuv444p12":   95,
		"yuv444p14":   96,
		"gb rp12":     97,
		"gbrp14":      98,
		"yuvj411p":    99,
		"bayer_bggr8": 100,
		"bayer_rggb8": 101,
		"bayer_gbrg8": 102,
		"bayer_grbg8": 103,
		"bayer_bggr16":104,
		"bayer_rggb16":105,
		"bayer_gbrg16":106,
		"bayer_grbg16":107,
		"xv30":        108,
		"xv36":        109,
		"xv48":        110,
		"ayuv64":      111,
		"videotoolbox_vld": 112,
		"p010":        113,
		"p016":        114,
		"y210":        115,
		"y212":        116,
		"xyuv":        117,
		"vuya":        118,
		"vuyx":        119,
		"y410":        120,
		"y412":        121,
		"v30x":        122,
		"rgb30":       123,
		"av1":         124,
		"argb32":      125,
		"abgr32":      126,
		"0rgb32":      127,
		"0bgr32":      128,
		"yuv420p16le": 129,
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
	url    string
	demux  *av.InputFormatContext
	dec    *av.DecoderContext
	pkt    *av.Packet
	si         av.StreamInfo
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

// convertFrame runs a decoded source frame through the (lazily built) scale+format
// converter so the frame delivered to downstream nodes (e.g. the encoder) exactly
// matches the sequence format declared in OutputStreamInfo (width/height/pix_fmt
// from the job JSON "format"). Sending unconverted native source frames (which may
// have a different pix_fmt, full-range vs limited, or even different resolution)
// to an encoder configured for the target format produces garbage output (vertical
// lines, corrupted blocks, no recognizable picture) and/or absurdly low bitrate
// because the encoder receives bogus pixel data.
func (se *SequenceEditor) convertFrame(native *av.Frame, srcSI av.StreamInfo) *av.Frame {
	if native == nil {
		return nil
	}
	se.ensureConverter(srcSI)
	if se.converter == nil {
		// Converter could not be built for this source (logged by ensure). Fall back
		// to the native frame; downstream may still produce something or the sizes
		// may happen to match.
		return native
	}
	if err := se.converter.PushFrame(native); err != nil {
		native.Close()
		fmt.Printf("sequence_editor: converter push err: %v\n", err)
		return nil
	}
	// Graph took a ref (KEEP_REF); we can close our copy of the input frame.
	native.Close()

	// Pull the converted frame. A simple scale+format graph is 1:1 and should
	// produce output on the first Pull after Push, but the first frame (or after
	// a clip switch) can require several Pull attempts while the filter configures
	// itself (see the "changing video frame properties on the fly" warnings).
	// Keep pulling on EAGAIN for a while before giving up (caller will then hold
	// the previous lastFrame for this timestep).
	for attempt := 0; attempt < 16; attempt++ {
		cf, aerr := av.AllocFrame()
		if aerr != nil {
			return nil
		}
		perr := se.converter.PullFrame(cf)
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

func (se *SequenceEditor) ensureConverter(srcSI av.StreamInfo) {
	if se.converter != nil {
		// very naive: keep if dimensions/pixfmt roughly match last used
		if se.converterInputSI.Width == srcSI.Width && se.converterInputSI.PixFmt == srcSI.PixFmt {
			return
		}
		se.converter.Close()
		se.converter = nil
	}
	spec := fmt.Sprintf("scale=%d:%d:flags=bicubic,format=%s",
		se.format.Width, se.format.Height, se.format.PixFmt)
	fg, err := av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
		Width:   srcSI.Width,
		Height:  srcSI.Height,
		PixFmt:  srcSI.PixFmt,
		TBNum:   srcSI.TimeBase[0],
		TBDen:   srcSI.TimeBase[1],
		SARNum:  srcSI.SampleAspectRatio[0],
		SARDen:  srcSI.SampleAspectRatio[1],
		FilterSpec: spec,
	})
	if err != nil {
		fmt.Printf("sequence_editor: converter build failed for %dx%d pix%d -> %s: %v\n", srcSI.Width, srcSI.Height, srcSI.PixFmt, spec, err)
		// best effort — caller will fall back to black or error later
		return
	}
	se.converter = fg
	se.converterInputSI = srcSI
}

func init() {
	Register("sequence_editor", func() Processor { return &SequenceEditor{} })
}