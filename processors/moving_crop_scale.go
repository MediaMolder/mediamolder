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
//	match       — bracket | sample (default bracket). sample uses each
//	              keyframe at most once, within half the gap to the next,
//	              and Close reports a keyframe no frame used.
//	aspect      — free | output (default free)
//	align       — chroma | exact (default chroma). Accepted so older jobs
//	              still open. Both values sample at sub-pixel positions.
//	flags       — resample kernel (default bicubic)
//	emit_rect   — metadata with the window rounded half away from zero
type MovingCropScale struct {
	outW, outH  int
	pts         []int64
	times       []float64
	useT        bool
	bound       bool
	rects       []cropRect
	logSize     bool
	aspectOut   bool
	interp      string
	unmatched   string
	matchSample bool
	flags       string
	emitRect    bool
	used        []bool
	nUsed       int
	step        int64
	// store is the resample scratch. Process handles one frame at a time.
	store *av.ResampleStore
}

// Zoom is output-relative: zoom 64 maps one output pixel onto 1/64 of a
// source pixel, and zoom 1/16 maps one output pixel onto 16 source pixels.
const (
	mcsMaxZoomIn  = 64.0
	mcsMaxZoomOut = 16.0
)

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
	matchMode, err := mcsChoice(params["match"], "match", "bracket", "bracket", "sample")
	if err != nil {
		return err
	}
	if matchMode == "sample" && interp != "hold" {
		return fmt.Errorf("moving_crop_scale: match \"sample\" only allows interpolate \"hold\"")
	}
	aspect, err := mcsChoice(params["aspect"], "aspect", "free", "free", "output")
	if err != nil {
		return err
	}
	if _, err = mcsChoice(params["align"], "align", "chroma", "chroma", "exact"); err != nil {
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
	p.outW, p.outH = outW, outH
	p.pts, p.times = pts, times
	p.useT, p.bound = useT, false
	p.rects = rects
	p.logSize = logSize
	p.aspectOut = aspect == "output"
	p.interp, p.unmatched = interp, unmatched
	p.matchSample = matchMode == "sample"
	p.flags, p.emitRect = flags, emit
	p.used = make([]bool, len(rects))
	p.nUsed = 0
	p.step = keyframeStep(pts)
	return nil
}

func (p *MovingCropScale) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if frame == nil {
		return nil, nil, nil
	}
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}
	if len(p.rects) == 0 {
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
	r, err = p.limitWindow(r)
	if err != nil {
		return nil, nil, err
	}
	out, err := p.paint(frame, r)
	if err != nil {
		return nil, nil, err
	}
	var md *Metadata
	if p.emitRect {
		md = &Metadata{Custom: map[string]any{
			"pts":          frame.PTS(),
			"x":            mcsRound(r.x),
			"y":            mcsRound(r.y),
			"w":            mcsRound(r.w),
			"h":            mcsRound(r.h),
			"interpolated": interpolated,
		}}
	}
	return out, md, nil
}

// Close frees the resample scratch. Bracket matching returns nil.
// Sample matching returns an error when a keyframe was never used, which is
// how a missing copy_ts or a source that ends early fails the job instead of
// writing a short file. Close does not consult a context.
func (p *MovingCropScale) Close() error {
	if p != nil && p.store != nil {
		p.store.Close()
		p.store = nil
	}
	if p == nil || !p.matchSample || p.nUsed == len(p.rects) {
		return nil
	}
	first := 0
	for first < len(p.used) && p.used[first] {
		first++
	}
	unused := len(p.rects) - p.nUsed
	if p.useT && !p.bound {
		return fmt.Errorf("moving_crop_scale: %d of %d keyframes matched no frame (first unused keyframes[%d]); "+
			"check copy_ts, the time base before the processor, and the in/out points",
			unused, len(p.rects), first)
	}
	var pts int64
	if first < len(p.pts) {
		pts = p.pts[first]
	}
	return fmt.Errorf("moving_crop_scale: %d of %d keyframes matched no frame (first unused pts %d); "+
		"check copy_ts, the time base before the processor, and the in/out points",
		unused, len(p.rects), pts)
}

// ScaleReinits reports swscale graph builds. Sampling is done in process,
// so the count stays 0. The method remains for the node-metrics hook.
func (p *MovingCropScale) ScaleReinits() int64 {
	return 0
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
	p.step = keyframeStep(pts)
	p.bound = true
	return nil
}

func keyframeStep(pts []int64) int64 {
	if len(pts) < 2 {
		return 0
	}
	step := int64(math.MaxInt64)
	for i := 1; i < len(pts); i++ {
		if d := pts[i] - pts[i-1]; d < step {
			step = d
		}
	}
	return step
}

// sample returns the rectangle for pts. interpolated is true only when the
// frame sits strictly between two keyframes and interpolate is linear or
// smooth. drop is a frame with no bracket under unmatched drop, or a frame
// with no PTS under hold or drop.
func (p *MovingCropScale) sample(pts int64) (cropRect, bool, bool, error) {
	if p.matchSample {
		return p.sampleOnce(pts)
	}
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

// sampleOnce is the trim match. A keyframe is used at most once: an exact
// PTS wins, otherwise the nearest unused keyframe strictly less than half
// the smallest gap away. One keyframe allows a single tick. A frame exactly
// halfway, or with no PTS, matches nothing.
func (p *MovingCropScale) sampleOnce(pts int64) (cropRect, bool, bool, error) {
	if pts == av.NoPTSValue {
		if p.unmatched == "error" {
			return cropRect{}, false, false, fmt.Errorf("moving_crop_scale: frame has no PTS")
		}
		return cropRect{}, false, true, nil
	}
	if pts >= -2*maxExactJSONInt && pts <= 2*maxExactJSONInt {
		if i, ok := p.take(pts); ok {
			p.used[i] = true
			p.nUsed++
			return p.rects[i], false, false, nil
		}
	}
	switch p.unmatched {
	case "error":
		return cropRect{}, false, false, fmt.Errorf("moving_crop_scale: pts %d is outside the keyframes", pts)
	case "drop":
		return cropRect{}, false, true, nil
	default:
		return p.rects[p.nearest(pts)], false, false, nil
	}
}

// take returns the unused keyframe for pts. Only the keyframes either side
// of pts can be in range.
func (p *MovingCropScale) take(pts int64) (int, bool) {
	i := sort.Search(len(p.pts), func(k int) bool { return p.pts[k] >= pts })
	if i < len(p.pts) && p.pts[i] == pts {
		return i, !p.used[i]
	}
	best, bestD := -1, int64(0)
	for _, k := range [2]int{i - 1, i} {
		if k < 0 || k >= len(p.pts) || p.used[k] {
			continue
		}
		d := p.pts[k] - pts
		if d < 0 {
			d = -d
		}
		if p.within(d) && (best < 0 || d < bestD) {
			best, bestD = k, d
		}
	}
	return best, best >= 0
}

// within reports whether a frame d ticks from a keyframe may take it.
func (p *MovingCropScale) within(d int64) bool {
	if p.step == 0 {
		return d <= 1
	}
	return 2*d < p.step
}

// nearest is the keyframe closest to pts. A tie goes to the earlier one.
// A pts outside the list is the first or the last keyframe.
func (p *MovingCropScale) nearest(pts int64) int {
	last := len(p.pts) - 1
	if pts <= p.pts[0] {
		return 0
	}
	if pts >= p.pts[last] {
		return last
	}
	i := sort.Search(len(p.pts), func(k int) bool { return p.pts[k] >= pts })
	if p.pts[i]-pts < pts-p.pts[i-1] {
		return i
	}
	return i - 1
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

// limitWindow rejects a non-positive size and clamps a positive size into
// [output/64, output*16]. A Catmull-Rom segment can leave that range even
// when every keyframe is inside it. The origin is not clamped: a window
// past the picture is painted with digital black.
func (p *MovingCropScale) limitWindow(r cropRect) (cropRect, error) {
	if !mcsFinite(r.w) || !mcsFinite(r.h) || r.w <= 0 || r.h <= 0 {
		return r, fmt.Errorf("moving_crop_scale: crop side collapsed to 0")
	}
	r.w = mcsClamp(r.w, float64(p.outW)/mcsMaxZoomIn, float64(p.outW)*mcsMaxZoomOut)
	r.h = mcsClamp(r.h, float64(p.outH)/mcsMaxZoomIn, float64(p.outH)*mcsMaxZoomOut)
	if !mcsFinite(r.x) || !mcsFinite(r.y) {
		return r, fmt.Errorf("moving_crop_scale: crop origin is not finite")
	}
	return r, nil
}

// paint copies an integer 1:1 window that sits on the chroma grid and inside
// the picture, when the kernel reproduces that pixel. spline is a cubic
// B-spline and blurs an integer coordinate, so it always resamples. Every
// other window is resampled too, which is what lets a fractional position
// generate the in-between pixels and lets a window hang off the picture.
func (p *MovingCropScale) paint(frame *av.Frame, r cropRect) (*av.Frame, error) {
	vw, vh := frame.VisibleWidth(), frame.VisibleHeight()
	if vw < 1 || vh < 1 {
		return nil, fmt.Errorf("moving_crop_scale: empty picture")
	}
	if x, y, w, h, ok := copyWindow(frame, r, vw, vh); ok && w == p.outW && h == p.outH && p.flags != "spline" {
		out, err := frame.CropRect(x, y, w, h)
		if err != nil {
			return nil, fmt.Errorf("moving_crop_scale: %w", err)
		}
		return out, nil
	}
	src, err := frame.CropWindow(0, 0, vw, vh)
	if err != nil {
		return nil, fmt.Errorf("moving_crop_scale: %w", err)
	}
	defer src.Close()
	dst, err := av.NewVideoFrame(p.outW, p.outH, frame.PixFmt())
	if err != nil {
		return nil, fmt.Errorf("moving_crop_scale: %w", err)
	}
	if err := dst.CopyPropsFrom(frame); err != nil {
		dst.Close()
		return nil, fmt.Errorf("moving_crop_scale: %w", err)
	}
	dst.SetCrop(0, 0, 0, 0)
	// A window whose luma centres all lie inside the picture overwrites every
	// output pixel, so the fresh buffer does not need a black fill. A window
	// that hangs off leaves the uncovered pixels as digital black.
	if !av.SampleCentresInside(dst.Width(), dst.Height(), src.Width(), src.Height(), r.x, r.y, r.w, r.h) {
		if err := dst.FillBlack(); err != nil {
			dst.Close()
			return nil, fmt.Errorf("moving_crop_scale: %w", err)
		}
	}
	if p.store == nil {
		p.store, err = av.NewResampleStore()
		if err != nil {
			dst.Close()
			return nil, fmt.Errorf("moving_crop_scale: %w", err)
		}
	}
	if err := p.store.Apply(dst, src, r.x, r.y, r.w, r.h, p.flags); err != nil {
		dst.Close()
		return nil, fmt.Errorf("moving_crop_scale: %w", err)
	}
	return dst, nil
}

// copyWindow reports the integer window when a CropRect of it is the same
// picture the resampler would produce: every edge is an integer, the window
// is inside the visible picture, and the origin is on the chroma grid.
func copyWindow(frame *av.Frame, r cropRect, vw, vh int) (x, y, w, h int, ok bool) {
	if !mcsNearlyInt(r.x) || !mcsNearlyInt(r.y) || !mcsNearlyInt(r.w) || !mcsNearlyInt(r.h) {
		return 0, 0, 0, 0, false
	}
	x = int(math.Round(r.x))
	y = int(math.Round(r.y))
	w = int(math.Round(r.w))
	h = int(math.Round(r.h))
	if w < 1 || h < 1 || x < 0 || y < 0 || x+w > vw || y+h > vh {
		return 0, 0, 0, 0, false
	}
	logW, logH := frame.Log2ChromaW(), frame.Log2ChromaH()
	if !chromaAligned(x, logW) || !chromaAligned(y, logH) {
		return 0, 0, 0, 0, false
	}
	return x, y, w, h, true
}

func chromaAligned(v, log2 int) bool {
	if log2 <= 0 {
		return true
	}
	return v&((1<<log2)-1) == 0
}

func mcsFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }

func mcsClamp(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func mcsNearlyInt(v float64) bool {
	return mcsFinite(v) && math.Abs(v-math.Round(v)) <= 1e-6
}

// mcsRound rounds half away from zero. math.Round does that for finite values.
func mcsRound(v float64) int {
	if !mcsFinite(v) {
		return 0
	}
	r := math.Round(v)
	if r >= float64(math.MaxInt) {
		return math.MaxInt
	}
	if r <= float64(math.MinInt) {
		return math.MinInt
	}
	return int(r)
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
		if z > mcsMaxZoomIn || z < 1/mcsMaxZoomOut {
			return cropRect{}, fmt.Errorf("moving_crop_scale: keyframes[%d].zoom %v must be from 1/16 to 64", i, z)
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
	if err := mcsCheckSide(w, outW, i, "w"); err != nil {
		return cropRect{}, err
	}
	if err := mcsCheckSide(h, outH, i, "h"); err != nil {
		return cropRect{}, err
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

// mcsCheckSide rejects a side below 1 pixel, a side smaller than output/64
// (more than 64× in), and a side larger than output×16 (more than 16× out).
func mcsCheckSide(side float64, out, index int, name string) error {
	if !mcsFinite(side) || side < 1 {
		return fmt.Errorf("moving_crop_scale: keyframes[%d].%s %v is below 1", index, name, side)
	}
	minS := float64(out) / mcsMaxZoomIn
	maxS := float64(out) * mcsMaxZoomOut
	const eps = 1e-9
	if side < minS*(1-eps) {
		return fmt.Errorf("moving_crop_scale: keyframes[%d].%s %v zooms in more than 64x", index, name, side)
	}
	if side > maxS*(1+eps) {
		return fmt.Errorf("moving_crop_scale: keyframes[%d].%s %v zooms out more than 16x", index, name, side)
	}
	return nil
}
