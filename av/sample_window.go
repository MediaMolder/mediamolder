// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <math.h>
// #include <stddef.h>
// #include <stdlib.h>
// #include "libavutil/frame.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/pixfmt.h"
//
// enum {
//     MM_POINT = 0,
//     MM_BILINEAR = 1,
//     MM_BICUBIC = 2,
//     MM_LANCZOS = 3,
//     MM_AREA = 4,
//     MM_SPLINE = 5
// };
//
// typedef struct {
//     int plane, step, offset, shift, depth;
//     int sub_x, sub_y;
//     int black, maxv;
//     int sole; // no other component shares this component's bytes
// } MMComp;
//
// typedef struct {
//     MMComp comp[4];
//     int n;
//     int be;
// } MMFmt;
//
// static int mm_limited_black(int depth) {
//     if (depth >= 8)
//         return 16 << (depth - 8);
//     return (16 * ((1 << depth) - 1) + 127) / 255;
// }
//
// static int mm_chroma_mid(int depth) {
//     if (depth >= 8)
//         return 128 << (depth - 8);
//     return 1 << (depth - 1);
// }
//
// static int mm_load_fmt(const AVFrame *f, MMFmt *o) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)f->format);
//     if (!d)
//         return -2;
//     if (d->flags & (AV_PIX_FMT_FLAG_HWACCEL | AV_PIX_FMT_FLAG_BITSTREAM |
//                     AV_PIX_FMT_FLAG_PAL | AV_PIX_FMT_FLAG_FLOAT | AV_PIX_FMT_FLAG_BAYER))
//         return -1;
//     if (d->nb_components < 1 || d->nb_components > 4)
//         return -1;
//     int rgb = (d->flags & AV_PIX_FMT_FLAG_RGB) != 0;
//     int alpha = (d->flags & AV_PIX_FMT_FLAG_ALPHA) ? (int)d->nb_components - 1 : -1;
//     int full = f->color_range == AVCOL_RANGE_JPEG;
//     o->n = d->nb_components;
//     o->be = (d->flags & AV_PIX_FMT_FLAG_BE) != 0;
//     for (int i = 0; i < o->n; i++) {
//         const AVComponentDescriptor *cd = &d->comp[i];
//         if (cd->depth < 1 || cd->depth > 16 || cd->step < 1)
//             return -1;
//         if ((cd->depth > 8 || cd->shift + cd->depth > 8) && cd->step < 2)
//             return -1;
//         if (cd->shift < 0 || cd->shift + cd->depth > 16)
//             return -1;
//         if (cd->plane < 0 || cd->plane > 3)
//             return -1;
//         MMComp *c = &o->comp[i];
//         c->plane = cd->plane;
//         c->step = cd->step;
//         c->shift = cd->shift;
//         // Big-endian descriptors store an 8-bit component with offset -1
//         // and rely on av_read_image_line adding one byte. Apply that here
//         // so the address stays inside the plane.
//         c->offset = cd->offset;
//         if (cd->shift + cd->depth <= 8 && (d->flags & AV_PIX_FMT_FLAG_BE))
//             c->offset += 1;
//         if (c->offset < 0)
//             return -1;
//         c->depth = cd->depth;
//         c->maxv = (1 << cd->depth) - 1;
//         int chroma = !rgb && i != alpha && (i == 1 || i == 2);
//         c->sub_x = chroma ? (1 << d->log2_chroma_w) : 1;
//         c->sub_y = chroma ? (1 << d->log2_chroma_h) : 1;
//         if (i == alpha)
//             c->black = c->maxv;
//         else if (chroma)
//             c->black = mm_chroma_mid(cd->depth);
//         else if (rgb || full)
//             c->black = 0;
//         else
//             c->black = mm_limited_black(cd->depth);
//         if (c->black > c->maxv)
//             c->black = c->maxv;
//     }
//     for (int i = 0; i < o->n; i++) {
//         MMComp *c = &o->comp[i];
//         int n = (c->depth > 8 || c->shift + c->depth > 8) ? 2 : 1;
//         c->sole = 1;
//         for (int j = 0; j < o->n; j++) {
//             MMComp *b;
//             int m;
//             if (i == j)
//                 continue;
//             b = &o->comp[j];
//             if (b->plane != c->plane)
//                 continue;
//             m = (b->depth > 8 || b->shift + b->depth > 8) ? 2 : 1;
//             if (c->offset < b->offset + m && b->offset < c->offset + n)
//                 c->sole = 0;
//         }
//     }
//     return 0;
// }
//
// static const uint8_t *mm_at(const AVFrame *f, const MMComp *c, int x, int y) {
//     return f->data[c->plane] + (ptrdiff_t)y * f->linesize[c->plane] +
//            (ptrdiff_t)x * c->step + c->offset;
// }
//
// static int mm_read(const AVFrame *f, const MMComp *c, int be, int x, int y) {
//     const uint8_t *p = mm_at(f, c, x, y);
//     uint32_t v;
//     int wide = c->depth > 8 || c->shift + c->depth > 8;
//     if (!wide)
//         v = p[0];
//     else if (be)
//         v = ((uint32_t)p[0] << 8) | p[1];
//     else
//         v = (uint32_t)p[0] | ((uint32_t)p[1] << 8);
//     return (int)((v >> c->shift) & (uint32_t)c->maxv);
// }
//
// static void mm_write(AVFrame *f, const MMComp *c, int be, int x, int y, int value) {
//     uint8_t *p = (uint8_t *)mm_at(f, c, x, y);
//     uint32_t mask = ((uint32_t)c->maxv << c->shift);
//     uint32_t bits = ((uint32_t)value << c->shift) & mask;
//     // A component that owns its bytes (planar 10-bit) is written without
//     // a read, so unused high bits become zero. av_frame_get_buffer does
//     // not zero the allocation, and a 10-bit reader consumes the whole
//     // word. A packed pixel shares those bytes with the next component, so
//     // that write merges. x2rgb10le stores B at shift 0 in the same word as G.
//     int wide = c->depth > 8 || c->shift + c->depth > 8;
//     if (!wide) {
//         if (c->shift == 0 && c->depth == 8)
//             p[0] = (uint8_t)value;
//         else
//             p[0] = (uint8_t)((p[0] & ~mask) | bits);
//         return;
//     }
//     uint32_t u;
//     if (c->sole)
//         u = bits;
//     else {
//         u = be ? ((uint32_t)p[0] << 8) | p[1] : (uint32_t)p[0] | ((uint32_t)p[1] << 8);
//         u = (u & ~mask) | bits;
//     }
//     if (be) {
//         p[0] = (uint8_t)(u >> 8);
//         p[1] = (uint8_t)u;
//     } else {
//         p[0] = (uint8_t)u;
//         p[1] = (uint8_t)(u >> 8);
//     }
// }
//
// static int mm_round_comp(double v, int maxv) {
//     if (v < 0)
//         v = 0;
//     if (v > maxv)
//         v = maxv;
//     return (int)floor(v + 0.5 + 1e-10);
// }
//
// static int mm_fill_black(AVFrame *f) {
//     MMFmt fmt;
//     int ret = mm_load_fmt(f, &fmt);
//     if (ret < 0)
//         return ret;
//     if (f->width < 1 || f->height < 1)
//         return -3;
//     for (int i = 0; i < fmt.n; i++) {
//         MMComp *c = &fmt.comp[i];
//         if (!f->data[c->plane])
//             return -3;
//         int pw = (f->width + c->sub_x - 1) / c->sub_x;
//         int ph = (f->height + c->sub_y - 1) / c->sub_y;
//         for (int y = 0; y < ph; y++) {
//             for (int x = 0; x < pw; x++)
//                 mm_write(f, c, fmt.be, x, y, c->black);
//         }
//     }
//     return 0;
// }
//
// static double mm_tent(double d) {
//     if (d < 0)
//         d = -d;
//     if (d >= 1)
//         return 0;
//     return 1 - d;
// }
//
// // Catmull-Rom (cubic a = -0.5). Zero at every non-zero integer, so an
// // integer coordinate copies that pixel.
// static double mm_cubic(double d) {
//     if (d < 0)
//         d = -d;
//     if (d < 1)
//         return ((1.5 * d - 2.5) * d) * d + 1;
//     if (d < 2)
//         return (((-0.5 * d + 2.5) * d - 4) * d) + 2;
//     return 0;
// }
//
// // Uniform cubic B-spline. Support is two pixels. It is not interpolating:
// // an integer coordinate mixes in the neighbours.
// static double mm_bspline(double d) {
//     if (d < 0)
//         d = -d;
//     if (d < 1) {
//         double d2 = d * d;
//         return (3 * d2 * d - 6 * d2 + 4) / 6;
//     }
//     if (d < 2) {
//         double t = 2 - d;
//         return (t * t * t) / 6;
//     }
//     return 0;
// }
//
// static double mm_lanczos(double d) {
//     const double a = 3;
//     if (d < 0)
//         d = -d;
//     if (d == 0)
//         return 1;
//     if (d >= a)
//         return 0;
//     const double pi = 3.14159265358979323846;
//     double p = pi * d;
//     return a * sin(p) * sin(p / a) / (p * p);
// }
//
// static double mm_kern(int kernel, double d) {
//     if (kernel == MM_BILINEAR)
//         return mm_tent(d);
//     if (kernel == MM_LANCZOS)
//         return mm_lanczos(d);
//     if (kernel == MM_SPLINE)
//         return mm_bspline(d);
//     return mm_cubic(d);
// }
//
// static double mm_support(int kernel) {
//     if (kernel == MM_BILINEAR)
//         return 1;
//     if (kernel == MM_LANCZOS)
//         return 3;
//     return 2;
// }
//
// static double mm_scaled_kern(int kernel, double d, double f) {
//     if (f > 1)
//         return mm_kern(kernel, d / f);
//     return mm_kern(kernel, d);
// }
//
// // mm_keep_pixel is true when any luma pixel covered by plane pixel (i, j)
// // has its centre inside the source. A chroma sample on 4:2:0 covers a
// // 2x2 luma block. Skipping it because its own centre is outside leaves
// // picture luma with neutral chroma on an odd edge.
// static int mm_keep_pixel(double x, double y, double w, double h,
//                          int dst_w, int dst_h, int src_w, int src_h,
//                          int sub_x, int sub_y, int i, int j) {
//     int x0 = i * sub_x;
//     int x1 = x0 + sub_x;
//     int y0 = j * sub_y;
//     int y1 = y0 + sub_y;
//     if (x0 >= dst_w || y0 >= dst_h)
//         return 0;
//     if (x1 > dst_w)
//         x1 = dst_w;
//     if (y1 > dst_h)
//         y1 = dst_h;
//     for (int ly = y0; ly < y1; ly++) {
//         double sy = y + (ly + 0.5) * h / dst_h - 0.5;
//         if (sy < -0.5 || sy >= src_h - 0.5)
//             continue;
//         for (int lx = x0; lx < x1; lx++) {
//             double sx = x + (lx + 0.5) * w / dst_w - 0.5;
//             // Index 0 is the centre of the first pixel, so the picture
//             // covers [-0.5, src_w - 0.5).
//             if (sx >= -0.5 && sx < src_w - 0.5)
//                 return 1;
//         }
//     }
//     return 0;
// }
//
// static int mm_sample(const AVFrame *src, const MMComp *c, int be, int x, int y, int pw, int ph) {
//     if (x < 0 || y < 0 || x >= pw || y >= ph)
//         return c->black;
//     return mm_read(src, c, be, x, y);
// }
//
// static double mm_overlap(double left, double right, int k) {
//     double a = left > k ? left : (double)k;
//     double b = right < (k + 1) ? right : (double)(k + 1);
//     return b > a ? b - a : 0;
// }
//
// // mm_resample_window samples src into dst. Output component pixel (i, j)
// // reads source coordinate
// //   s = origin + (i + 0.5) * (window / sub) / plane - 0.5
// // so an integer 1:1 window copies source pixel origin+i for an
// // interpolating kernel. A luma centre outside src leaves dst unchanged.
// // A chroma sample is written when any luma pixel it covers is inside.
// // Taps outside src are digital black. A downscale stretches the kernel
// // by the scale so the footprint covers the source under that pixel.
// static int mm_filter_separable(AVFrame *dst, const AVFrame *src, const MMComp *c,
//                                int be, double x, double y, double w, double h, int kernel) {
//     int dw = (dst->width + c->sub_x - 1) / c->sub_x;
//     int dh = (dst->height + c->sub_y - 1) / c->sub_y;
//     int sw = (src->width + c->sub_x - 1) / c->sub_x;
//     int sh = (src->height + c->sub_y - 1) / c->sub_y;
//     if (dw < 1 || dh < 1)
//         return -3;
//     double ox = x / c->sub_x;
//     double oy = y / c->sub_y;
//     double sx_scale = (w / c->sub_x) / dw;
//     double sy_scale = (h / c->sub_y) / dh;
//     double fx = sx_scale > 1 ? sx_scale : 1;
//     double fy = sy_scale > 1 ? sy_scale : 1;
//     if (fx >= 4096 || fy >= 4096)
//         return -3;
//     double support = mm_support(kernel);
//     double radx = support * fx;
//     double rady = support * fy;
//     double sy0 = oy + 0.5 * sy_scale - 0.5;
//     double sy1 = oy + (dh - 0.5) * sy_scale - 0.5;
//     if (!isfinite(sy0) || !isfinite(sy1))
//         return -3;
//     int row0 = 0;
//     int row1 = -1;
//     if (sy1 + rady >= 0 && sy0 - rady < sh) {
//         double lo = sy0 - rady;
//         double hi = sy1 + rady;
//         if (lo < 0)
//             lo = 0;
//         if (hi > sh - 1)
//             hi = sh - 1;
//         row0 = (int)floor(lo);
//         row1 = (int)ceil(hi);
//         if (row0 < 0)
//             row0 = 0;
//         if (row1 >= sh)
//             row1 = sh - 1;
//     }
//     int nrows = row1 >= row0 ? row1 - row0 + 1 : 0;
//     double *tmp = NULL;
//     if (nrows > 0) {
//         if ((size_t)dw > SIZE_MAX / sizeof(double) / (size_t)nrows)
//             return -3;
//         tmp = malloc((size_t)nrows * (size_t)dw * sizeof(double));
//         if (!tmp)
//             return -3;
//         for (int yy = row0; yy <= row1; yy++) {
//             double *row = tmp + (size_t)(yy - row0) * (size_t)dw;
//             for (int i = 0; i < dw; i++) {
//                 double sx = ox + (i + 0.5) * sx_scale - 0.5;
//                 // A huge origin must not be cast to int. The pixel is
//                 // outside the picture, so the vertical pass will skip it.
//                 if (!isfinite(sx) || sx < -1e9 || sx > 1e9) {
//                     row[i] = c->black;
//                     continue;
//                 }
//                 int k0 = (int)floor(sx - radx);
//                 int k1 = (int)ceil(sx + radx);
//                 double acc = 0, norm = 0;
//                 for (int k = k0; k <= k1; k++) {
//                     double wx = mm_scaled_kern(kernel, sx - k, fx);
//                     if (wx == 0)
//                         continue;
//                     norm += wx;
//                     acc += wx * mm_sample(src, c, be, k, yy, sw, sh);
//                 }
//                 row[i] = norm == 0 ? c->black : acc / norm;
//             }
//         }
//     }
//     for (int j = 0; j < dh; j++) {
//         double sy = oy + (j + 0.5) * sy_scale - 0.5;
//         if (!isfinite(sy) || sy < -1e9 || sy > 1e9)
//             continue;
//         int k0 = (int)floor(sy - rady);
//         int k1 = (int)ceil(sy + rady);
//         for (int i = 0; i < dw; i++) {
//             if (!mm_keep_pixel(x, y, w, h, dst->width, dst->height, src->width, src->height,
//                                c->sub_x, c->sub_y, i, j))
//                 continue;
//             double acc = 0, norm = 0;
//             for (int k = k0; k <= k1; k++) {
//                 double wy = mm_scaled_kern(kernel, sy - k, fy);
//                 if (wy == 0)
//                     continue;
//                 norm += wy;
//                 double sample = c->black;
//                 if (tmp && k >= row0 && k <= row1)
//                     sample = tmp[(size_t)(k - row0) * (size_t)dw + (size_t)i];
//                 acc += wy * sample;
//             }
//             if (norm == 0)
//                 acc = c->black;
//             else
//                 acc /= norm;
//             mm_write(dst, c, be, i, j, mm_round_comp(acc, c->maxv));
//         }
//     }
//     free(tmp);
//     return 0;
// }
//
// static int mm_resample_window(AVFrame *dst, const AVFrame *src,
//                               double x, double y, double w, double h, int kernel) {
//     if (!isfinite(x) || !isfinite(y) || !isfinite(w) || !isfinite(h) || w <= 0 || h <= 0)
//         return -3;
//     if (kernel < MM_POINT || kernel > MM_SPLINE)
//         return -3;
//     if (dst->format != src->format || dst->width < 1 || dst->height < 1)
//         return -3;
//     MMFmt fmt;
//     int ret = mm_load_fmt(dst, &fmt);
//     if (ret < 0)
//         return ret;
//     for (int ci = 0; ci < fmt.n; ci++) {
//         MMComp *c = &fmt.comp[ci];
//         if (!dst->data[c->plane] || !src->data[c->plane])
//             return -3;
//         if (kernel != MM_POINT && kernel != MM_AREA) {
//             ret = mm_filter_separable(dst, src, c, fmt.be, x, y, w, h, kernel);
//             if (ret < 0)
//                 return ret;
//             continue;
//         }
//         int dw = (dst->width + c->sub_x - 1) / c->sub_x;
//         int dh = (dst->height + c->sub_y - 1) / c->sub_y;
//         int sw = (src->width + c->sub_x - 1) / c->sub_x;
//         int sh = (src->height + c->sub_y - 1) / c->sub_y;
//         double ox = x / c->sub_x;
//         double oy = y / c->sub_y;
//         double sx_scale = (w / c->sub_x) / dw;
//         double sy_scale = (h / c->sub_y) / dh;
//         for (int j = 0; j < dh; j++) {
//             double sy = oy + (j + 0.5) * sy_scale - 0.5;
//             for (int i = 0; i < dw; i++) {
//                 double sx = ox + (i + 0.5) * sx_scale - 0.5;
//                 if (!mm_keep_pixel(x, y, w, h, dst->width, dst->height, src->width, src->height,
//                                    c->sub_x, c->sub_y, i, j))
//                     continue;
//                 double acc;
//                 if (kernel == MM_POINT) {
//                     int ix = (int)floor(sx + 0.5);
//                     int iy = (int)floor(sy + 0.5);
//                     if (ix < 0)
//                         ix = 0;
//                     if (iy < 0)
//                         iy = 0;
//                     if (ix >= sw)
//                         ix = sw - 1;
//                     if (iy >= sh)
//                         iy = sh - 1;
//                     acc = mm_read(src, c, fmt.be, ix, iy);
//                 } else {
//                     double left = ox + i * sx_scale;
//                     double right = ox + (i + 1) * sx_scale;
//                     double top = oy + j * sy_scale;
//                     double bottom = oy + (j + 1) * sy_scale;
//                     double span = (right - left) * (bottom - top);
//                     acc = 0;
//                     double covered = 0;
//                     if (!(span > 0)) {
//                         acc = c->black;
//                         mm_write(dst, c, fmt.be, i, j, mm_round_comp(acc, c->maxv));
//                         continue;
//                     }
//                     int x0 = (int)floor(left);
//                     int x1 = (int)ceil(right) - 1;
//                     int y0 = (int)floor(top);
//                     int y1 = (int)ceil(bottom) - 1;
//                     if (x0 < 0)
//                         x0 = 0;
//                     if (y0 < 0)
//                         y0 = 0;
//                     if (x1 >= sw)
//                         x1 = sw - 1;
//                     if (y1 >= sh)
//                         y1 = sh - 1;
//                     for (int yy = y0; yy <= y1; yy++) {
//                         double ycov = mm_overlap(top, bottom, yy);
//                         if (ycov == 0)
//                             continue;
//                         for (int xx = x0; xx <= x1; xx++) {
//                             double xcov = mm_overlap(left, right, xx);
//                             if (xcov == 0)
//                                 continue;
//                             double weight = xcov * ycov / span;
//                             covered += weight;
//                             acc += weight * mm_read(src, c, fmt.be, xx, yy);
//                         }
//                     }
//                     acc += (1 - covered) * c->black;
//                 }
//                 mm_write(dst, c, fmt.be, i, j, mm_round_comp(acc, c->maxv));
//             }
//         }
//     }
//     return 0;
// }
//
// static const char *mm_sample_err(int code) {
//     if (code == -1)
//         return "unsupported pixel format";
//     if (code == -2)
//         return "unknown pixel format";
//     return "bad window";
// }
import "C"

import "fmt"

// FillBlack paints digital black into f. Limited and unspecified YUV use
// luma 16<<(depth-8) and chroma at mid level. Full-range YUV uses luma 0
// and the same chroma. RGB is 0. Alpha is opaque. The frame's color range
// selects limited or full; unspecified is limited.
func (f *Frame) FillBlack() error {
	if f == nil || f.p == nil {
		return fmt.Errorf("av: fill black: nil frame")
	}
	if ret := C.mm_fill_black(f.p); ret < 0 {
		return fmt.Errorf("av: fill black: %s", C.GoString(C.mm_sample_err(ret)))
	}
	return nil
}

// ResampleWindow writes dst by sampling the window (x, y, w, h) of src.
// Both frames are the same pixel format. Output pixel (i, j) reads source
// luma sx = x + (i+0.5)*w/dstWidth - 0.5, and the same mapping on each
// chroma plane, so an integer window at 1:1 copies source pixel (x+i, y+j).
// A luma sample centre outside src leaves that destination pixel as it was.
// A chroma sample is written when any luma pixel it covers has its centre
// inside, so a 4:2:0 edge does not show picture luma with neutral chroma.
// Samples the kernel needs from outside src are digital black, the same
// values FillBlack writes, and those taps stay in the weight sum. When the
// window is larger than dst on an axis, bilinear, bicubic, lanczos, and
// spline stretch by that scale. neighbor stays nearest. kernel is neighbor,
// bilinear, bicubic, lanczos, spline, or area. bicubic is Catmull-Rom.
// spline is a cubic B-spline. area averages the source rectangle each
// output pixel covers.
func ResampleWindow(dst, src *Frame, x, y, w, h float64, kernel string) error {
	if dst == nil || dst.p == nil || src == nil || src.p == nil {
		return fmt.Errorf("av: resample: nil frame")
	}
	k, err := sampleKernel(kernel)
	if err != nil {
		return err
	}
	if ret := C.mm_resample_window(dst.p, src.p, C.double(x), C.double(y), C.double(w), C.double(h), k); ret < 0 {
		return fmt.Errorf("av: resample: %s", C.GoString(C.mm_sample_err(ret)))
	}
	return nil
}

func sampleKernel(name string) (C.int, error) {
	switch name {
	case "neighbor":
		return C.MM_POINT, nil
	case "bilinear":
		return C.MM_BILINEAR, nil
	case "bicubic":
		return C.MM_BICUBIC, nil
	case "spline":
		return C.MM_SPLINE, nil
	case "lanczos":
		return C.MM_LANCZOS, nil
	case "area":
		return C.MM_AREA, nil
	default:
		return 0, fmt.Errorf("av: resample: unknown flags %q", name)
	}
}
