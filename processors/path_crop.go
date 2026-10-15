// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

func init() {
	Register("path_crop", func() Processor { return &PathCrop{} })
}

// maxExactJSONInt is the largest integer a JSON number (float64) holds exactly.
const maxExactJSONInt = 1 << 53

// PathCrop crops each video frame to a full-height window whose left edge
// follows a path of (PTS, centre x) samples, and drops frames that are not on
// the path. The window math matches FFmpeg's crop filter
// (libavfilter/vf_crop.c). See docs/path-crop-processor.md.
//
// Params:
//
//	width  — window width in luma pixels; even, at least 2.
//	frames — [{"pts": int, "cx": float}, ...], PTS strictly increasing.
type PathCrop struct {
	width int
	pts   []int64
	cx    []float64
	used  []bool
	nUsed int
	half  int64
}

func (p *PathCrop) Init(params map[string]any) error {
	width, err := pathCropWidth(params["width"])
	if err != nil {
		return err
	}
	pts, cx, err := pathCropFrames(params["frames"])
	if err != nil {
		return err
	}
	minStep := int64(math.MaxInt64)
	for i := range pts {
		if math.IsNaN(cx[i]) || math.IsInf(cx[i], 0) {
			return fmt.Errorf("path_crop: sample %d: cx %v is not finite", i, cx[i])
		}
		if i > 0 {
			if pts[i] <= pts[i-1] {
				return fmt.Errorf("path_crop: sample %d: pts %d does not follow %d", i, pts[i], pts[i-1])
			}
			minStep = min(minStep, pts[i]-pts[i-1])
		}
	}
	p.width = width
	p.pts = pts
	p.cx = cx
	p.used = make([]bool, len(pts))
	p.nUsed = 0
	p.half = 1
	if len(pts) >= 2 && minStep >= 2 {
		p.half = minStep / 2
	}
	return nil
}

func (p *PathCrop) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if frame == nil {
		return nil, nil, nil
	}
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}
	i, ok := p.match(frame.PTS())
	if !ok {
		return nil, nil, nil
	}
	fw := frame.Width()
	if fw < p.width {
		return nil, nil, fmt.Errorf("path_crop: frame width %d is narrower than window %d", fw, p.width)
	}
	shift := frame.Log2ChromaW()
	if shift < 0 {
		return nil, nil, fmt.Errorf("path_crop: unknown pixel format %d", frame.PixFmt())
	}
	if p.width%(1<<shift) != 0 {
		return nil, nil, fmt.Errorf("path_crop: width %d is not a multiple of the %s chroma step %d",
			p.width, av.PixFmtName(frame.PixFmt()), 1<<shift)
	}
	out, err := frame.CropColumns(pathCropX(p.cx[i], p.width, fw, shift), p.width)
	if err != nil {
		return nil, nil, fmt.Errorf("path_crop: %w", err)
	}
	p.used[i] = true
	p.nUsed++
	return out, nil, nil
}

// Close reports path samples that no frame matched. That happens when copy_ts
// is off, when a node before the processor changed PTS or the time base, or
// when the source ends before the out point.
func (p *PathCrop) Close() error {
	if p.nUsed == len(p.pts) {
		return nil
	}
	first := 0
	for p.used[first] {
		first++
	}
	return fmt.Errorf("path_crop: %d of %d path samples matched no frame (first unused pts %d); "+
		"check copy_ts, the time base before the processor, and the in/out points",
		len(p.pts)-p.nUsed, len(p.pts), p.pts[first])
}

// match returns the unused sample for pts: an exact match, else the nearest
// within the half-frame limit, ties to the earlier sample. Steps are at least
// twice the limit, so only the samples either side of pts can be in range.
func (p *PathCrop) match(pts int64) (int, bool) {
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
		if d <= p.half && (best < 0 || d < bestD) {
			best, bestD = k, d
		}
	}
	return best, best >= 0
}

// pathCropX is the integer left edge of a width-wide window centred on cx in a
// frameWidth-wide frame: cx − width/2 rounded to the nearest integer (ties to
// the lower), clamped to [0, frameWidth−width], then aligned down to the
// chroma step like FFmpeg's crop filter (vf_crop.c: x &= ~((1<<hsub)−1)).
func pathCropX(cx float64, width, frameWidth, log2ChromaW int) int {
	e := cx - float64(width)/2
	maxX := frameWidth - width
	x := 0
	switch {
	case e <= 0:
	case e >= float64(maxX):
		x = maxX
	default:
		x = int(math.Floor(e))
		if e-math.Floor(e) > 0.5 {
			x++
		}
	}
	return x &^ (1<<log2ChromaW - 1)
}

func pathCropWidth(v any) (int, error) {
	var w int
	switch n := v.(type) {
	case nil:
		return 0, fmt.Errorf("path_crop: missing width")
	case float64:
		if n != math.Trunc(n) || math.Abs(n) > maxExactJSONInt {
			return 0, fmt.Errorf("path_crop: width %v is not an integer", n)
		}
		w = int(n)
	case int:
		w = n
	case string:
		i, err := strconv.Atoi(strings.TrimSpace(n))
		if err != nil {
			return 0, fmt.Errorf("path_crop: width: %w", err)
		}
		w = i
	default:
		return 0, fmt.Errorf("path_crop: width has type %T", v)
	}
	if w < 2 || w%2 != 0 {
		return 0, fmt.Errorf("path_crop: width %d must be even and at least 2", w)
	}
	return w, nil
}

func pathCropFrames(v any) ([]int64, []float64, error) {
	var items []map[string]any
	switch list := v.(type) {
	case nil:
		return nil, nil, fmt.Errorf("path_crop: missing frames")
	case []any:
		for i, e := range list {
			m, ok := e.(map[string]any)
			if !ok {
				return nil, nil, fmt.Errorf("path_crop: frames[%d] is %T, not an object", i, e)
			}
			items = append(items, m)
		}
	case []map[string]any:
		items = list
	default:
		return nil, nil, fmt.Errorf("path_crop: frames is %T, not an array", v)
	}
	if len(items) == 0 {
		return nil, nil, fmt.Errorf("path_crop: frames is empty")
	}
	pts := make([]int64, len(items))
	cx := make([]float64, len(items))
	for i, m := range items {
		switch n := m["pts"].(type) {
		case float64:
			if n != math.Trunc(n) || math.Abs(n) > maxExactJSONInt {
				return nil, nil, fmt.Errorf("path_crop: frames[%d].pts %v is not an integer within ±2^53", i, n)
			}
			pts[i] = int64(n)
		case int:
			pts[i] = int64(n)
		case int64:
			pts[i] = n
		default:
			return nil, nil, fmt.Errorf("path_crop: frames[%d].pts has type %T, want a number", i, m["pts"])
		}
		switch n := m["cx"].(type) {
		case float64:
			cx[i] = n
		case int:
			cx[i] = float64(n)
		default:
			return nil, nil, fmt.Errorf("path_crop: frames[%d].cx has type %T, want a number", i, m["cx"])
		}
	}
	return pts, cx, nil
}
