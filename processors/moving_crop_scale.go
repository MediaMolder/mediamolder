// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"math"
	"sort"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

func init() {
	Register("moving_crop_scale", func() Processor { return &MovingCropScale{} })
}

// MovingCropScale crops each video frame to a keyframed window and scales
// that window to a fixed output size. Keyframes bracket presentation time;
// they are not consumed. See docs/moving-crop-scale.md.
//
// Params:
//
//	output     — {"width": even ≥ 2, "height": even ≥ 2}
//	keyframes  — [{pts|t, x,y|cx,cy, w,h|zoom}, ...], time strictly increasing
//	interpolate — hold | linear | smooth (default linear)
//	unmatched   — hold | drop | error (default hold)
//	aspect      — free | output (default free)
//	align       — chroma | exact (default chroma)
//	flags       — swscale algorithm (default bicubic)
//	emit_rect   — metadata with the applied integer rectangle (default false)
type MovingCropScale struct {
	outW, outH int
	pts        []int64
	times      []float64
	useT       bool
	bound      bool
	rects      []cropRect
	logSize    bool
	aspectOut  bool
	interp     string
	unmatched  string
	exact      bool
	emitRect   bool
	scaler     *av.Scaler
}

// cropRect is one keyframe after cx/cy and zoom have been converted to a
// top-left origin and a size, in luma pixels. Values may be fractional.
type cropRect struct {
	x, y, w, h float64
}

func (p *MovingCropScale) Init(params map[string]any) error {
	outW, outH, err := mcsOutput(params["output"])
	if err != nil {
		return err
	}
	interp, err := mcsChoice(params["interpolate"], "interpolate", "linear", "hold", "linear", "smooth")
	if err != nil {
		return err
	}
	unmatched, err := mcsChoice(params["unmatched"], "unmatched", "hold", "hold", "drop", "error")
	if err != nil {
		return err
	}
	aspect, err := mcsChoice(params["aspect"], "aspect", "free", "free", "output")
	if err != nil {
		return err
	}
	align, err := mcsChoice(params["align"], "align", "chroma", "chroma", "exact")
	if err != nil {
		return err
	}
	flags, err := mcsChoice(params["flags"], "flags", "bicubic", "bilinear", "bicubic", "lanczos", "spline", "area", "neighbor")
	if err != nil {
		return err
	}
	emit, err := mcsBool(params["emit_rect"], "emit_rect")
	if err != nil {
		return err
	}
	pts, times, useT, rects, logSize, err := mcsKeyframes(params["keyframes"], outW, outH, aspect == "output")
	if err != nil {
		return err
	}
	sc, err := av.NewScaler(av.ScalerOptions{Width: outW, Height: outH, PixFmt: -1, Flags: flags})
	if err != nil {
		return fmt.Errorf("moving_crop_scale: %w", err)
	}
	if p.scaler != nil {
		p.scaler.Close()
	}
	p.outW, p.outH = outW, outH
	p.pts, p.times = pts, times
	p.useT, p.bound = useT, false
	p.rects = rects
	p.logSize = logSize
	p.aspectOut = aspect == "output"
	p.interp, p.unmatched = interp, unmatched
	p.exact, p.emitRect = align == "exact", emit
	p.scaler = sc
	return nil
}

func (p *MovingCropScale) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if frame == nil {
		return nil, nil, nil
	}
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}
	if len(p.rects) == 0 || p.scaler == nil {
		return nil, nil, fmt.Errorf("moving_crop_scale: not initialised")
	}
	if frame.Hardware() {
		name := av.PixFmtName(frame.PixFmt())
		if name == "" {
			name = fmt.Sprintf("pixel format %d", frame.PixFmt())
		}
		return nil, nil, fmt.Errorf("moving_crop_scale: %s frames are not supported", name)
	}
	if frame.Interlaced() {
		return nil, nil, fmt.Errorf("moving_crop_scale: interlaced frames are not supported")
	}
	if p.useT && !p.bound {
		if err := p.bindTime(ctx.TimeBase); err != nil {
			return nil, nil, err
		}
	}
	r, interpolated, drop, err := p.sample(frame.PTS())
	if err != nil {
		return nil, nil, err
	}
	if drop {
		return nil, nil, nil
	}
	// aspect: output keeps h on the output aspect after interpolation, so a
	// spline on h cannot stretch the window. A supplied h was discarded at Init.
	if p.aspectOut {
		r.h = r.w * float64(p.outH) / float64(p.outW)
	}
	x, y, w, h, err := p.quantise(frame, r)
	if err != nil {
		return nil, nil, err
	}
	var out *av.Frame
	if w == p.outW && h == p.outH {
		out, err = frame.CropRect(x, y, w, h)
		if err != nil {
			return nil, nil, fmt.Errorf("moving_crop_scale: %w", err)
		}
	} else {
		win, werr := frame.CropWindow(x, y, w, h)
		if werr != nil {
			return nil, nil, fmt.Errorf("moving_crop_scale: %w", werr)
		}
		out, err = p.scaler.Scale(win)
		win.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("moving_crop_scale: %w", err)
		}
	}
	var md *Metadata
	if p.emitRect {
		md = &Metadata{Custom: map[string]any{
			"pts":          frame.PTS(),
			"x":            x,
			"y":            y,
			"w":            w,
			"h":            h,
			"interpolated": interpolated,
		}}
	}
	return out, md, nil
}

// Close releases the scaler. It returns nil: a frame outside the keyframes
// is reported by Process, and Close does not consult a context.
func (p *MovingCropScale) Close() error {
	if p != nil && p.scaler != nil {
		p.scaler.Close()
		p.scaler = nil
	}
	return nil
}

// ScaleReinits reports how many times swscale built its graph. The first
// scaled frame counts as one. Fast-path crops do not.
func (p *MovingCropScale) ScaleReinits() int64 {
	if p == nil || p.scaler == nil {
		return 0
	}
	return int64(p.scaler.ReinitCount())
}

// TransformStreamInfo sets the output width and height and leaves the pixel
// format and sample aspect ratio as they arrived.
func (p *MovingCropScale) TransformStreamInfo(in av.StreamInfo) (av.StreamInfo, error) {
	in.Width = p.outW
	in.Height = p.outH
	return in, nil
}

// bindTime converts keyframe seconds to ticks of tb. av_rescale_q rounds
// halfway away from zero. Two times that share a tick, a tick outside ±2^53,
// or a time base that cannot represent the value is a Process error.
func (p *MovingCropScale) bindTime(tb [2]int) error {
	pts := make([]int64, len(p.times))
	var prev int64
	for i, sec := range p.times {
		tick, err := av.SecondsToTicks(sec, tb[0], tb[1])
		if err != nil {
			return fmt.Errorf("moving_crop_scale: keyframes[%d].t: %w", i, err)
		}
		if tick < -maxExactJSONInt || tick > maxExactJSONInt {
			return fmt.Errorf("moving_crop_scale: keyframes[%d].t %v is outside ±2^53 in time base %d/%d", i, sec, tb[0], tb[1])
		}
		if i > 0 && tick == prev {
			return fmt.Errorf("moving_crop_scale: keyframes[%d].t and keyframes[%d].t land on the same tick %d", i-1, i, tick)
		}
		if i > 0 && tick < prev {
			return fmt.Errorf("moving_crop_scale: keyframes[%d].t tick %d does not follow %d", i, tick, prev)
		}
		pts[i] = tick
		prev = tick
	}
	p.pts = pts
	p.bound = true
	return nil
}

// sample returns the rectangle for pts. interpolated is true only when the
// frame sits strictly between two keyframes and interpolate is linear or
// smooth. drop is a frame with no bracket under unmatched drop, or a frame
// with no PTS under hold or drop.
func (p *MovingCropScale) sample(pts int64) (cropRect, bool, bool, error) {
	if pts == av.NoPTSValue {
		if p.unmatched == "error" {
			return cropRect{}, false, false, fmt.Errorf("moving_crop_scale: frame has no PTS")
		}
		return cropRect{}, false, true, nil
	}
	i := sort.Search(len(p.pts), func(k int) bool { return p.pts[k] >= pts })
	if i < len(p.pts) && p.pts[i] == pts {
		return p.rects[i], false, false, nil
	}
	if i == 0 || i == len(p.pts) {
		switch p.unmatched {
		case "error":
			return cropRect{}, false, false, fmt.Errorf("moving_crop_scale: pts %d is outside the keyframes", pts)
		case "drop":
			return cropRect{}, false, true, nil
		default:
			idx := 0
			if i == len(p.pts) {
				idx = len(p.pts) - 1
			}
			return p.rects[idx], false, false, nil
		}
	}
	r, interpolated := p.blend(i-1, pts)
	return r, interpolated, false, nil
}

func (p *MovingCropScale) blend(i int, pts int64) (cropRect, bool) {
	if p.interp == "hold" {
		return p.rects[i], false
	}
	t := float64(pts-p.pts[i]) / float64(p.pts[i+1]-p.pts[i])
	if p.interp == "smooth" {
		return p.smooth(i, t), true
	}
	a, b := p.rects[i], p.rects[i+1]
	return cropRect{
		x: lerp(a.x, b.x, t),
		y: lerp(a.y, b.y, t),
		w: lerpDim(a.w, b.w, t, p.logSize),
		h: lerpDim(a.h, b.h, t, p.logSize),
	}, true
}

// smooth is Catmull-Rom on segment i. Indices outside the list duplicate the
// first and last keyframes, so a two-keyframe path meets the linear midpoint.
func (p *MovingCropScale) smooth(i int, t float64) cropRect {
	at := func(k int, f func(cropRect) float64) float64 {
		if k < 0 {
			k = 0
		} else if k >= len(p.rects) {
			k = len(p.rects) - 1
		}
		return f(p.rects[k])
	}
	comp := func(f func(cropRect) float64) float64 {
		return catmull(at(i-1, f), at(i, f), at(i+1, f), at(i+2, f), t)
	}
	wOf := func(r cropRect) float64 { return r.w }
	hOf := func(r cropRect) float64 { return r.h }
	if p.logSize {
		wOf = func(r cropRect) float64 { return math.Log(r.w) }
		hOf = func(r cropRect) float64 { return math.Log(r.h) }
	}
	r := cropRect{
		x: comp(func(r cropRect) float64 { return r.x }),
		y: comp(func(r cropRect) float64 { return r.y }),
		w: comp(wOf),
		h: comp(hOf),
	}
	if p.logSize {
		r.w = math.Exp(r.w)
		r.h = math.Exp(r.h)
	}
	return r
}

func lerp(a, b, t float64) float64 { return a + (b-a)*t }

func lerpDim(a, b, t float64, logSpace bool) float64 {
	if logSpace {
		return math.Exp(lerp(math.Log(a), math.Log(b), t))
	}
	return lerp(a, b, t)
}

// catmull is the uniform Catmull-Rom segment from p1 to p2, t in [0,1].
func catmull(p0, p1, p2, p3, t float64) float64 {
	t2 := t * t
	t3 := t2 * t
	return 0.5 * ((2 * p1) + (-p0+p2)*t + (2*p0-5*p1+4*p2-p3)*t2 + (-p0+3*p1-3*p2+p3)*t3)
}

// quantise applies the crop order: raise a negative size to 0, clamp to the
// visible picture, round to even, align to the chroma step, and clamp again
// when rounding stepped past the picture. Position is clamped, rounded with
// ties down, then aligned unless align is exact.
func (p *MovingCropScale) quantise(frame *av.Frame, r cropRect) (x, y, w, h int, err error) {
	vw, vh := frame.VisibleWidth(), frame.VisibleHeight()
	logW, logH := frame.Log2ChromaW(), frame.Log2ChromaH()
	if logW < 0 || logH < 0 {
		return 0, 0, 0, 0, fmt.Errorf("moving_crop_scale: unknown pixel format %d", frame.PixFmt())
	}
	w = fitSide(r.w, vw, logW)
	h = fitSide(r.h, vh, logH)
	if w < 1 || h < 1 {
		return 0, 0, 0, 0, fmt.Errorf("moving_crop_scale: crop side collapsed to 0")
	}
	x = place(r.x, vw-w, logW, p.exact)
	y = place(r.y, vh-h, logH, p.exact)
	return x, y, w, h, nil
}

func fitSide(v float64, visible, log2 int) int {
	if visible < 1 || math.IsNaN(v) || v <= 0 {
		return 0
	}
	if v > float64(visible) {
		v = float64(visible)
	}
	n := cropAlignDown(cropRoundEven(v), log2)
	if n > visible {
		// 1919 rounds to 1920. Align the visible size down and keep it even.
		n = cropAlignDown(visible, log2)
		n &^= 1
	}
	if n > visible || n < 1 {
		return 0
	}
	return n
}

func place(e float64, maxPos, log2 int, exact bool) int {
	if maxPos < 0 {
		maxPos = 0
	}
	if math.IsNaN(e) || e < 0 {
		e = 0
	}
	if e > float64(maxPos) {
		e = float64(maxPos)
	}
	x := cropRoundTiesDown(e)
	if x > maxPos {
		x = maxPos
	}
	if !exact {
		x = cropAlignDown(x, log2)
	}
	if x < 0 {
		return 0
	}
	return x
}

func mcsOutput(v any) (int, int, error) {
	m, ok := v.(map[string]any)
	if !ok || m == nil {
		if v == nil {
			return 0, 0, fmt.Errorf("moving_crop_scale: missing output")
		}
		return 0, 0, fmt.Errorf("moving_crop_scale: output is %T, not an object", v)
	}
	w, err := mcsEvenSide(m["width"], "output.width")
	if err != nil {
		return 0, 0, err
	}
	h, err := mcsEvenSide(m["height"], "output.height")
	if err != nil {
		return 0, 0, err
	}
	return w, h, nil
}

func mcsEvenSide(v any, name string) (int, error) {
	if v == nil {
		return 0, fmt.Errorf("moving_crop_scale: missing %s", name)
	}
	n, err := cropInt(v)
	if err != nil {
		return 0, fmt.Errorf("moving_crop_scale: %s %w", name, err)
	}
	if n < 2 || n%2 != 0 || n > math.MaxInt32 {
		return 0, fmt.Errorf("moving_crop_scale: %s %d must be even and at least 2", name, n)
	}
	return int(n), nil
}

func mcsChoice(v any, name, def string, allowed ...string) (string, error) {
	if v == nil {
		return def, nil
	}
	s, ok := v.(string)
	if !ok {
		return "", fmt.Errorf("moving_crop_scale: %s has type %T, want a string", name, v)
	}
	for _, a := range allowed {
		if s == a {
			return s, nil
		}
	}
	return "", fmt.Errorf("moving_crop_scale: %s %q is not one of %s", name, s, strings.Join(allowed, ", "))
}

func mcsBool(v any, name string) (bool, error) {
	if v == nil {
		return false, nil
	}
	b, ok := v.(bool)
	if !ok {
		return false, fmt.Errorf("moving_crop_scale: %s has type %T, want a bool", name, v)
	}
	return b, nil
}

func mcsKeyframes(v any, outW, outH int, aspectOut bool) (pts []int64, times []float64, useT bool, rects []cropRect, logSize bool, err error) {
	items, err := mcsObjects(v)
	if err != nil {
		return nil, nil, false, nil, false, err
	}
	pts = make([]int64, len(items))
	times = make([]float64, len(items))
	rects = make([]cropRect, len(items))
	var haveTime, haveSize bool
	for i, m := range items {
		pt, sec, isT, err := mcsTime(m, i, &haveTime, &useT)
		if err != nil {
			return nil, nil, false, nil, false, err
		}
		if isT {
			times[i] = sec
			if i > 0 && sec <= times[i-1] {
				return nil, nil, false, nil, false, fmt.Errorf("moving_crop_scale: keyframes[%d].t %v does not follow %v", i, sec, times[i-1])
			}
		} else {
			pts[i] = pt
			if i > 0 && pt <= pts[i-1] {
				return nil, nil, false, nil, false, fmt.Errorf("moving_crop_scale: keyframes[%d].pts %d does not follow %d", i, pt, pts[i-1])
			}
		}
		r, err := mcsRect(m, i, outW, outH, aspectOut, &haveSize, &logSize)
		if err != nil {
			return nil, nil, false, nil, false, err
		}
		rects[i] = r
	}
	if useT {
		pts = nil
	} else {
		times = nil
	}
	return pts, times, useT, rects, logSize, nil
}

func mcsObjects(v any) ([]map[string]any, error) {
	var items []map[string]any
	switch list := v.(type) {
	case nil:
		return nil, fmt.Errorf("moving_crop_scale: missing keyframes")
	case []any:
		for i, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, fmt.Errorf("moving_crop_scale: keyframes[%d] is %T, not an object", i, e)
			}
			items = append(items, m)
		}
	case []map[string]any:
		items = list
	default:
		return nil, fmt.Errorf("moving_crop_scale: keyframes is %T, not an array", v)
	}
	if len(items) == 0 {
		return nil, fmt.Errorf("moving_crop_scale: keyframes is empty")
	}
	return items, nil
}

func mcsTime(m map[string]any, i int, have, useT *bool) (int64, float64, bool, error) {
	hasPTS, hasT := mcsHas(m, "pts"), mcsHas(m, "t")
	if hasPTS && hasT {
		return 0, 0, false, fmt.Errorf("moving_crop_scale: keyframes[%d] mixes t and pts", i)
	}
	if !hasPTS && !hasT {
		return 0, 0, false, fmt.Errorf("moving_crop_scale: keyframes[%d] needs pts or t", i)
	}
	if *have && *useT != hasT {
		return 0, 0, false, fmt.Errorf("moving_crop_scale: keyframes mix t and pts")
	}
	*have = true
	*useT = hasT
	if hasT {
		sec, err := cropFloat(m["t"])
		if err != nil {
			return 0, 0, true, fmt.Errorf("moving_crop_scale: keyframes[%d].t %w", i, err)
		}
		if sec < 0 {
			return 0, 0, true, fmt.Errorf("moving_crop_scale: keyframes[%d].t %v is negative", i, sec)
		}
		return 0, sec, true, nil
	}
	pt, err := cropInt(m["pts"])
	if err != nil {
		return 0, 0, false, fmt.Errorf("moving_crop_scale: keyframes[%d].pts %w", i, err)
	}
	return pt, 0, false, nil
}

// mcsRect converts one keyframe to x, y, w, h. Under aspect output the real
// h is derived from w before a centre becomes a top-left corner, and a
// supplied h is ignored. A zoom list sets logSize so later interpolation
// runs on log(w) and log(h).
func mcsRect(m map[string]any, i, outW, outH int, aspectOut bool, haveSize, logSize *bool) (cropRect, error) {
	hasZoom := mcsHas(m, "zoom")
	hasW, hasH := mcsHas(m, "w"), mcsHas(m, "h")
	if hasZoom && (hasW || hasH) {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] mixes zoom with w/h", i)
	}
	if !hasZoom && !hasW {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] needs w and h, or zoom", i)
	}
	if *haveSize && *logSize != hasZoom {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes mix zoom with w/h")
	}
	*haveSize = true
	*logSize = hasZoom

	var w, h float64
	if hasZoom {
		z, err := cropFloat(m["zoom"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].zoom %w", i, err)
		}
		if z <= 0 {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].zoom %v must be greater than 0", i, z)
		}
		w = float64(outW) / z
		h = float64(outH) / z
	} else {
		var err error
		w, err = cropFloat(m["w"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].w %w", i, err)
		}
		// A supplied h under aspect output is discarded. h is derived below,
		// before cx/cy become a top-left corner.
		if !aspectOut {
			if !hasH {
				return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] needs h", i)
			}
			h, err = cropFloat(m["h"])
			if err != nil {
				return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].h %w", i, err)
			}
		}
	}
	if aspectOut {
		h = w * float64(outH) / float64(outW)
	}
	if w < 2 {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].w %v is below 2", i, w)
	}
	if h < 2 {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].h %v is below 2", i, h)
	}

	hasXY := mcsHas(m, "x") || mcsHas(m, "y")
	hasC := mcsHas(m, "cx") || mcsHas(m, "cy")
	if hasXY && hasC {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] mixes x/y with cx/cy", i)
	}
	var x, y float64
	if hasC {
		if !mcsHas(m, "cx") || !mcsHas(m, "cy") {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] needs both cx and cy", i)
		}
		cx, err := cropFloat(m["cx"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].cx %w", i, err)
		}
		cy, err := cropFloat(m["cy"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].cy %w", i, err)
		}
		x = cx - w/2
		y = cy - h/2
	} else if hasXY {
		if !mcsHas(m, "x") || !mcsHas(m, "y") {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] needs both x and y", i)
		}
		var err error
		x, err = cropFloat(m["x"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].x %w", i, err)
		}
		y, err = cropFloat(m["y"])
		if err != nil {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].y %w", i, err)
		}
	} else {
		return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d] needs x/y or cx/cy", i)
	}
	return cropRect{x: x, y: y, w: w, h: h}, nil
}

func mcsHas(m map[string]any, k string) bool {
	v, ok := m[k]
	return ok && v != nil
}
