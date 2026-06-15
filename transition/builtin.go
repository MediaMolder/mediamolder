// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package transition

import (
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// The transitions below are ported from FFmpeg's libavfilter/vf_xfade.c (the
// *_transition functions). progress and mix() follow the same convention as the
// C code (progress 1 → a, 0 → b; mix(a,b,m) = a*m + b*(1-m)), so each formula is
// a near-verbatim translation. width/height are PLANE dimensions, so the chroma
// planes scale with luma. See transition.go for the pointwise/registry plumbing.
//
// Coverage note: this is a faithful core, not the entire xfade set. The
// scaling/blur transitions (zoomin, squeezeh/v, hblur), the crop shapes
// (circlecrop, rectcrop), the corner/bar wipes (wipetl…, vertopen…) and
// fadegrays are not yet ported; sequence_editor falls back to "fade" for any
// supported name without an entry here. distance is an approximation (see below).
func init() {
	// ---- fades ----
	registerPointwise("fade", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		return mix(a, b, p)
	})
	registerPointwise("fadeblack", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		bg, phase := blackLevel(plane), 0.2
		return mix(mix(a, bg, smoothstep(1-phase, 1, p)), mix(bg, b, smoothstep(phase, 1, p)), p)
	})
	registerPointwise("fadewhite", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		bg, phase := whiteLevel(plane), 0.2
		return mix(mix(a, bg, smoothstep(1-phase, 1, p)), mix(bg, b, smoothstep(phase, 1, p)), p)
	})

	// ---- hard wipes (a sliding boundary selects a or b) ----
	registerPointwise("wipeleft", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if x > int(float64(w)*p) {
			return b
		}
		return a
	})
	registerPointwise("wiperight", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if x > int(float64(w)*(1-p)) {
			return a
		}
		return b
	})
	registerPointwise("wipeup", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y > int(float64(h)*p) {
			return b
		}
		return a
	})
	registerPointwise("wipedown", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y > int(float64(h)*(1-p)) {
			return a
		}
		return b
	})

	// ---- smooth wipes (a soft gradient edge) ----
	registerPointwise("smoothleft", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		return mix(b, a, smoothstep(0, 1, 1+float64(x)/float64(w)-p*2))
	})
	registerPointwise("smoothright", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		return mix(b, a, smoothstep(0, 1, 1+float64(w-1-x)/float64(w)-p*2))
	})
	registerPointwise("smoothup", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		return mix(b, a, smoothstep(0, 1, 1+float64(y)/float64(h)-p*2))
	})
	registerPointwise("smoothdown", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		return mix(b, a, smoothstep(0, 1, 1+float64(h-1-y)/float64(h)-p*2))
	})

	// ---- circles ----
	registerPointwise("circleopen", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		z := math.Hypot(float64(w)/2, float64(h)/2)
		off := (p - 0.5) * 3
		smooth := math.Hypot(float64(x)-float64(w)/2, float64(y)-float64(h)/2)/z + off
		return mix(a, b, smoothstep(0, 1, smooth))
	})
	registerPointwise("circleclose", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		z := math.Hypot(float64(w)/2, float64(h)/2)
		off := (1 - p - 0.5) * 3
		smooth := math.Hypot(float64(x)-float64(w)/2, float64(y)-float64(h)/2)/z + off
		return mix(b, a, smoothstep(0, 1, smooth))
	})

	// ---- radial sweep ----
	registerPointwise("radial", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		smooth := math.Atan2(float64(x)-float64(w)/2, float64(y)-float64(h)/2) - (p-0.5)*(math.Pi*2.5)
		return mix(b, a, smoothstep(0, 1, smooth))
	})

	// ---- repeating slices ----
	registerPointwise("hlslice", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		xx := float64(x) / float64(w)
		return sliceMix(a, b, smoothstep(-0.5, 0, xx-p*1.5), fract(10*xx))
	})
	registerPointwise("hrslice", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		xx := float64(w-1-x) / float64(w)
		return sliceMix(a, b, smoothstep(-0.5, 0, xx-p*1.5), fract(10*xx))
	})
	registerPointwise("vuslice", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		yy := float64(y) / float64(h)
		return sliceMix(a, b, smoothstep(-0.5, 0, yy-p*1.5), fract(10*yy))
	})
	registerPointwise("vdslice", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		yy := float64(h-1-y) / float64(h)
		return sliceMix(a, b, smoothstep(-0.5, 0, yy-p*1.5), fract(10*yy))
	})

	// ---- distance (approximate: vf_xfade.c sums normalized squared diffs over
	// all planes at one pixel; planar YUV planes are different sizes, so here it
	// is computed per plane from that plane's own a/b difference). ----
	registerPointwise("distance", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		dist := 0.0
		if math.Abs(a-b)/255 <= p {
			dist = 1
		}
		return mix(mix(a, b, dist), b, p)
	})

	// ---- slides (a/b wrap-shifted across the frame) ----
	Register("slideleft", func(out, a, b *av.Frame, p float64) { slideHorizontal(out, a, b, p, -1) })
	Register("slideright", func(out, a, b *av.Frame, p float64) { slideHorizontal(out, a, b, p, +1) })
	Register("slideup", func(out, a, b *av.Frame, p float64) { slideVertical(out, a, b, p, -1) })
	Register("slidedown", func(out, a, b *av.Frame, p float64) { slideVertical(out, a, b, p, +1) })
}

// sliceMix is the common tail of the *slice transitions: vf_xfade.c sets
// ss = (smooth <= stripe ? 0 : 1) then mix(xf1, xf0, ss), i.e. a when the edge
// has not yet passed this stripe, b once it has.
func sliceMix(a, b, smooth, stripe float64) float64 {
	if smooth <= stripe {
		return a
	}
	return b
}

// slideHorizontal shifts both frames horizontally by dir*progress*width and lets
// the incoming frame slide in over the outgoing one (wrap-around), per
// vf_xfade.c slideleft/slideright. dir is +1 (right) or -1 (left).
func slideHorizontal(out, a, b *av.Frame, progress, dir float64) {
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		ap, bp, dp := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, dl := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		if ap == nil || bp == nil || dp == nil {
			continue
		}
		z := int(dir * progress * float64(w))
		for y := 0; y < h; y++ {
			arow, brow, drow := ap[y*al:], bp[y*bl:], dp[y*dl:]
			for x := 0; x < w; x++ {
				zx := z + x
				zz := ((zx % w) + w) % w
				if zx >= 0 && zx < w {
					drow[x] = brow[zz]
				} else {
					drow[x] = arow[zz]
				}
			}
		}
	}
}

// slideVertical is slideHorizontal's vertical twin (slideup/slidedown).
func slideVertical(out, a, b *av.Frame, progress, dir float64) {
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		ap, bp, dp := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, dl := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		if ap == nil || bp == nil || dp == nil {
			continue
		}
		z := int(dir * progress * float64(h))
		for y := 0; y < h; y++ {
			zy := z + y
			zz := ((zy % h) + h) % h
			drow := dp[y*dl:]
			if zy >= 0 && zy < h {
				copy(drow[:w], bp[zz*bl:zz*bl+w])
			} else {
				copy(drow[:w], ap[zz*al:zz*al+w])
			}
		}
	}
}
