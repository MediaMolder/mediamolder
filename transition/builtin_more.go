// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package transition

import (
	"math"

	"github.com/MediaMolder/MediaMolder/av"
)

// The remaining xfade transitions, ported from FFmpeg's libavfilter/vf_xfade.c.
// Same conventions as builtin.go (progress 1 → a, 0 → b; mix(a,b,m)=a*m+b*(1-m);
// plane dimensions in place of vf_xfade.c's out->width/out->height). Background
// values for the crops/fades come from blackLevel/whiteLevel, which match
// vf_xfade.c's s->black / s->white exactly.
func init() {
	// ---- bar opens/closes (a soft band growing from / shrinking to the centre) ----
	registerPointwise("vertopen", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		w2 := float64(w) / 2
		smooth := 2 - math.Abs((float64(x)-w2)/w2) - p*2
		return mix(b, a, smoothstep(0, 1, smooth))
	})
	registerPointwise("vertclose", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		w2 := float64(w) / 2
		smooth := 1 + math.Abs((float64(x)-w2)/w2) - p*2
		return mix(b, a, smoothstep(0, 1, smooth))
	})
	registerPointwise("horzopen", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		h2 := float64(h) / 2
		smooth := 2 - math.Abs((float64(y)-h2)/h2) - p*2
		return mix(b, a, smoothstep(0, 1, smooth))
	})
	registerPointwise("horzclose", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		h2 := float64(h) / 2
		smooth := 1 + math.Abs((float64(y)-h2)/h2) - p*2
		return mix(b, a, smoothstep(0, 1, smooth))
	})

	// ---- corner wipes (a rectangle from one corner selects a) ----
	registerPointwise("wipetl", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y <= int(float64(h)*p) && x <= int(float64(w)*p) {
			return a
		}
		return b
	})
	registerPointwise("wipetr", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y <= int(float64(h)*p) && x > int(float64(w)*(1-p)) {
			return a
		}
		return b
	})
	registerPointwise("wipebl", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y > int(float64(h)*(1-p)) && x <= int(float64(w)*p) {
			return a
		}
		return b
	})
	registerPointwise("wipebr", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		if y > int(float64(h)*(1-p)) && x > int(float64(w)*(1-p)) {
			return a
		}
		return b
	})

	// ---- crops (the frame outside a shrinking/growing shape goes to black) ----
	registerPointwise("circlecrop", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		z := math.Pow(2*math.Abs(p-0.5), 3) * math.Hypot(float64(w)/2, float64(h)/2)
		dist := math.Hypot(float64(x)-float64(w)/2, float64(y)-float64(h)/2)
		if z < dist {
			return blackLevel(plane)
		}
		if p < 0.5 {
			return b
		}
		return a
	})
	registerPointwise("rectcrop", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		zw := int(math.Abs(p-0.5) * float64(w))
		zh := int(math.Abs(p-0.5) * float64(h))
		ax := x - w/2
		if ax < 0 {
			ax = -ax
		}
		ay := y - h/2
		if ay < 0 {
			ay = -ay
		}
		if !(ax < zw && ay < zh) {
			return blackLevel(plane)
		}
		if p < 0.5 {
			return b
		}
		return a
	})

	// ---- fade through grayscale: chroma desaturates to neutral mid-transition ----
	registerPointwise("fadegrays", func(a, b float64, x, y, w, h, plane int, p float64) float64 {
		const phase = 0.2
		const mid = 128.0 // (max_value + 1) / 2
		bg0, bg1 := float64(mid), float64(mid)
		if plane == 0 {
			bg0, bg1 = a, b // luma keeps its value → a plain fade
		}
		return mix(mix(a, bg0, smoothstep(1-phase, 1, p)), mix(bg1, b, smoothstep(phase, 1, p)), p)
	})

	// ---- transitions needing neighbourhood / resample access (not pointwise) ----
	Register("distance", distanceTransition)
	Register("hblur", hblurTransition)
	Register("squeezeh", squeezehTransition)
	Register("squeezev", squeezevTransition)
	Register("zoomin", zoominTransition)
}

// planeShift returns how many times the full (luma) dimension must be halved
// (AV_CEIL_RSHIFT) to reach this plane's dimension — 0 for luma, 1 for the
// chroma planes of a 4:2:0 / 4:2:2 format. Used to map between plane and frame
// coordinates.
func planeShift(full, plane int) int {
	shift := 0
	for plane < full && shift < 4 {
		full = (full + 1) >> 1
		shift++
	}
	return shift
}

// distanceTransition is xfade's "distance": pixels whose colour in a and b is
// close enough (Euclidean distance over all planes ≤ progress) cross-fade first.
// Faithful to vf_xfade.c, which reads every plane at one pixel; planar-YUV planes
// differ in size, so coordinates are mapped through the luma plane.
func distanceTransition(out, a, b *av.Frame, progress float64) {
	const max = 255.0
	np := out.NumPlanes()
	lumaW, lumaH := out.PlaneWidth(0), out.PlaneHeight(0)

	type plane struct {
		ad, bd, od []byte
		al, bl, ol int
		pw, ph     int
		shx, shy   int
	}
	pl := make([]plane, np)
	for q := 0; q < np; q++ {
		pl[q] = plane{
			ad: a.Plane(q), bd: b.Plane(q), od: out.Plane(q),
			al: a.Linesize(q), bl: b.Linesize(q), ol: out.Linesize(q),
			pw: out.PlaneWidth(q), ph: out.PlaneHeight(q),
			shx: planeShift(lumaW, out.PlaneWidth(q)),
			shy: planeShift(lumaH, out.PlaneHeight(q)),
		}
	}

	for p := 0; p < np; p++ {
		P := pl[p]
		for y := 0; y < P.ph; y++ {
			fy := y << P.shy
			for x := 0; x < P.pw; x++ {
				fx := x << P.shx
				var d float64
				for q := 0; q < np; q++ {
					Q := pl[q]
					qx, qy := fx>>Q.shx, fy>>Q.shy
					if qx >= Q.pw {
						qx = Q.pw - 1
					}
					if qy >= Q.ph {
						qy = Q.ph - 1
					}
					diff := (float64(Q.ad[qy*Q.al+qx]) - float64(Q.bd[qy*Q.bl+qx])) / max
					d += diff * diff
				}
				var dist float64
				if math.Sqrt(d) <= progress {
					dist = 1
				}
				av := float64(P.ad[y*P.al+x])
				bv := float64(P.bd[y*P.bl+x])
				P.od[y*P.ol+x] = clip8(mix(mix(av, bv, dist), bv, progress))
			}
		}
	}
}

// hblurTransition blurs both frames horizontally with a window that grows then
// shrinks over the transition, cross-fading the two blurs. The window is a
// left-anchored running box, exactly as vf_xfade.c computes it.
func hblurTransition(out, a, b *av.Frame, progress float64) {
	prog := progress * 2
	if progress > 0.5 {
		prog = (1 - progress) * 2
	}
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		size := 1 + int(float64(w/2)*prog)
		if size > w {
			size = w
		}
		ad, bd, od := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, ol := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		for y := 0; y < h; y++ {
			arow, brow, drow := ad[y*al:], bd[y*bl:], od[y*ol:]
			var sum0, sum1 float64
			cnt := float64(size)
			for x := 0; x < size; x++ {
				sum0 += float64(arow[x])
				sum1 += float64(brow[x])
			}
			for x := 0; x < w; x++ {
				drow[x] = clip8(mix(sum0/cnt, sum1/cnt, progress))
				if x+size < w {
					sum0 += float64(arow[x+size]) - float64(arow[x])
					sum1 += float64(brow[x+size]) - float64(brow[x])
				} else {
					sum0 -= float64(arow[x])
					sum1 -= float64(brow[x])
					cnt--
				}
			}
		}
	}
}

// squeezehTransition squeezes a vertically into a shrinking band while b fills
// the rest (vf_xfade.c squeezeh).
func squeezehTransition(out, a, b *av.Frame, progress float64) {
	if progress <= 0 {
		copyFrame(out, b)
		return
	}
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		hf := float64(h)
		ad, bd, od := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, ol := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		for y := 0; y < h; y++ {
			drow := od[y*ol:]
			z := 0.5 + (float64(y)/hf-0.5)/progress
			if z < 0 || z > 1 {
				copy(drow[:w], bd[y*bl:y*bl+w])
				continue
			}
			yy := int(math.Round(z * (hf - 1)))
			if yy < 0 {
				yy = 0
			}
			if yy >= h {
				yy = h - 1
			}
			copy(drow[:w], ad[yy*al:yy*al+w])
		}
	}
}

// squeezevTransition squeezes a horizontally into a shrinking band while b fills
// the rest (vf_xfade.c squeezev).
func squeezevTransition(out, a, b *av.Frame, progress float64) {
	if progress <= 0 {
		copyFrame(out, b)
		return
	}
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		wf := float64(w)
		ad, bd, od := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, ol := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		for y := 0; y < h; y++ {
			arow, brow, drow := ad[y*al:], bd[y*bl:], od[y*ol:]
			for x := 0; x < w; x++ {
				z := 0.5 + (float64(x)/wf-0.5)/progress
				if z < 0 || z > 1 {
					drow[x] = brow[x]
					continue
				}
				xx := int(math.Round(z * (wf - 1)))
				if xx < 0 {
					xx = 0
				}
				if xx >= w {
					xx = w - 1
				}
				drow[x] = arow[xx]
			}
		}
	}
}

// zoominTransition zooms a in toward the frame centre (nearest-neighbour) and
// cross-fades with b (vf_xfade.c zoomin).
func zoominTransition(out, a, b *av.Frame, progress float64) {
	zf := smoothstep(0.5, 1, progress)
	amt := smoothstep(0, 0.5, progress)
	for plane := 0; plane < out.NumPlanes(); plane++ {
		w, h := out.PlaneWidth(plane), out.PlaneHeight(plane)
		wf, hf := float64(w), float64(h)
		ad, bd, od := a.Plane(plane), b.Plane(plane), out.Plane(plane)
		al, bl, ol := a.Linesize(plane), b.Linesize(plane), out.Linesize(plane)
		for y := 0; y < h; y++ {
			brow, drow := bd[y*bl:], od[y*ol:]
			for x := 0; x < w; x++ {
				u := 0.5 + (float64(x)/wf-0.5)*zf
				v := 0.5 + (float64(y)/hf-0.5)*zf
				iu := int(math.Ceil(u * (wf - 1)))
				iv := int(math.Ceil(v * (hf - 1)))
				if iu < 0 {
					iu = 0
				}
				if iu >= w {
					iu = w - 1
				}
				if iv < 0 {
					iv = 0
				}
				if iv >= h {
					iv = h - 1
				}
				zv := float64(ad[iv*al+iu])
				drow[x] = clip8(mix(zv, float64(brow[x]), amt))
			}
		}
	}
}

// copyFrame copies every plane of src into dst (same format/dimensions).
func copyFrame(dst, src *av.Frame) {
	for plane := 0; plane < dst.NumPlanes(); plane++ {
		w, h := dst.PlaneWidth(plane), dst.PlaneHeight(plane)
		sd, dd := src.Plane(plane), dst.Plane(plane)
		sl, dl := src.Linesize(plane), dst.Linesize(plane)
		for y := 0; y < h; y++ {
			copy(dd[y*dl:y*dl+w], sd[y*sl:y*sl+w])
		}
	}
}
