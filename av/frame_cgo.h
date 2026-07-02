// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// frame_cgo.h — static C helpers called from frame_image.go via CGO.
//
// Keeping them in a separate header (rather than in the CGO preamble block)
// lets gopls resolve them correctly when analysing frame_image.go.

#ifndef MEDIAMOLDER_FRAME_CGO_H
#define MEDIAMOLDER_FRAME_CGO_H

#include <stddef.h>
#include <stdint.h>
#include "libavutil/frame.h"
#include "libavutil/pixfmt.h"
#include "libavutil/pixdesc.h"
#include "libavutil/imgutils.h"
#include "libswscale/swscale.h"

// frame_to_rgba converts an AVFrame of any pixel format to packed RGBA.
// The caller must av_free(*out_data) when done. Returns 0 on success.
static int frame_to_rgba(const AVFrame *frame, uint8_t **out_data, int *out_linesize) {
    if (!frame || frame->width <= 0 || frame->height <= 0)
        return -1;

    struct SwsContext *sws = sws_getContext(
        frame->width, frame->height, frame->format,
        frame->width, frame->height, AV_PIX_FMT_RGBA,
        SWS_BILINEAR, NULL, NULL, NULL);
    if (!sws) return AVERROR(ENOMEM);

    *out_linesize = frame->width * 4;
    // Zero-initialize: sws_scale does not necessarily write every byte of the destination
    // (e.g. an unaligned right edge), so an av_malloc buffer would leave those bytes as
    // uninitialized heap memory, making the conversion non-deterministic.
    *out_data = (uint8_t *)av_mallocz((size_t)(*out_linesize) * frame->height);
    if (!*out_data) {
        sws_freeContext(sws);
        return AVERROR(ENOMEM);
    }

    uint8_t *dst[4] = { *out_data, NULL, NULL, NULL };
    int dst_linesize[4] = { *out_linesize, 0, 0, 0 };

    sws_scale(sws, (const uint8_t *const *)frame->data, frame->linesize,
              0, frame->height, dst, dst_linesize);

    sws_freeContext(sws);
    return 0;
}

// frame_to_bgr24 converts an AVFrame of any pixel format to packed BGR24.
// The caller must av_free(*out_data) when done. Returns 0 on success.
static int frame_to_bgr24(const AVFrame *frame, uint8_t **out_data) {
    if (!frame || frame->width <= 0 || frame->height <= 0)
        return -1;

    struct SwsContext *sws = sws_getContext(
        frame->width, frame->height, frame->format,
        frame->width, frame->height, AV_PIX_FMT_BGR24,
        SWS_BILINEAR, NULL, NULL, NULL);
    if (!sws) return AVERROR(ENOMEM);

    int linesize = frame->width * 3;
    // Zero-initialize for determinism (see frame_to_rgba): sws_scale may leave some
    // destination bytes untouched.
    *out_data = (uint8_t *)av_mallocz((size_t)linesize * frame->height);
    if (!*out_data) { sws_freeContext(sws); return AVERROR(ENOMEM); }

    uint8_t *dst[4] = { *out_data, NULL, NULL, NULL };
    int dst_ls[4] = { linesize, 0, 0, 0 };

    sws_scale(sws, (const uint8_t *const *)frame->data, frame->linesize,
              0, frame->height, dst, dst_ls);
    sws_freeContext(sws);
    return 0;
}

// sad_8bit computes the Sum of Absolute Differences between two 8-bit
// planes, respecting stride. This simple loop auto-vectorizes well with
// -O2 on both ARM64 (NEON) and x86_64 (SSE/AVX).
static uint64_t sad_8bit(const uint8_t *src1, ptrdiff_t stride1,
                         const uint8_t *src2, ptrdiff_t stride2,
                         int width, int height) {
    uint64_t sad = 0;
    for (int y = 0; y < height; y++) {
        for (int x = 0; x < width; x++) {
            int d = (int)src1[x] - (int)src2[x];
            sad += (d < 0) ? -d : d;
        }
        src1 += stride1;
        src2 += stride2;
    }
    return sad;
}

// is_yuv_planar returns 1 if the pixel format is a planar YUV format with
// at least 3 components (so plane 0 is the full-resolution luma plane).
static int is_yuv_planar(int pix_fmt) {
    const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(pix_fmt);
    if (!desc) return 0;
    return !(desc->flags & AV_PIX_FMT_FLAG_RGB) &&
            (desc->flags & AV_PIX_FMT_FLAG_PLANAR) &&
            desc->nb_components >= 3;
}

// frame_luma_sad computes the Mean Absolute Frame Difference (MAFD) between
// two frames on the luma channel, matching FFmpeg's scdet filter algorithm.
//
// For YUV planar formats: operates directly on plane 0 (the Y luma plane)
// without any pixel format conversion — zero-copy, zero-allocation.
//
// For all other formats (RGB, packed, etc.): falls back to swscale GRAY8
// conversion.
//
// Returns MAFD on a 0–100 scale (matching scdet), or -1.0 on error.
static double frame_luma_sad(const AVFrame *a, const AVFrame *b) {
    if (!a || !b || a->width <= 0 || a->height <= 0)
        return -1.0;
    if (a->width != b->width || a->height != b->height)
        return -1.0;

    int w = a->width;
    int h = a->height;
    uint64_t sad;
    uint64_t count;

    // Fast path: YUV planar — use Y plane directly (no conversion).
    if (is_yuv_planar(a->format) && is_yuv_planar(b->format)) {
        // av_image_get_linesize gives the actual data width (w/o padding).
        int luma_w = av_image_get_linesize(a->format, w, 0);
        if (luma_w <= 0) return -1.0;
        sad = sad_8bit(a->data[0], a->linesize[0],
                       b->data[0], b->linesize[0],
                       luma_w, h);
        count = (uint64_t)luma_w * h;
    } else {
        // Slow path: convert both frames to GRAY8 via swscale.
        struct SwsContext *sws_a = sws_getContext(
            w, h, a->format, w, h, AV_PIX_FMT_GRAY8,
            SWS_BILINEAR, NULL, NULL, NULL);
        if (!sws_a) return -1.0;

        struct SwsContext *sws_b = sws_getContext(
            w, h, b->format, w, h, AV_PIX_FMT_GRAY8,
            SWS_BILINEAR, NULL, NULL, NULL);
        if (!sws_b) { sws_freeContext(sws_a); return -1.0; }

        uint8_t *buf_a = (uint8_t *)av_malloc(w * h);
        uint8_t *buf_b = (uint8_t *)av_malloc(w * h);
        if (!buf_a || !buf_b) {
            av_free(buf_a); av_free(buf_b);
            sws_freeContext(sws_a); sws_freeContext(sws_b);
            return -1.0;
        }

        uint8_t *dst_a[4] = { buf_a, NULL, NULL, NULL };
        int dst_ls_a[4] = { w, 0, 0, 0 };
        sws_scale(sws_a, (const uint8_t *const *)a->data, a->linesize,
                  0, h, dst_a, dst_ls_a);

        uint8_t *dst_b[4] = { buf_b, NULL, NULL, NULL };
        int dst_ls_b[4] = { w, 0, 0, 0 };
        sws_scale(sws_b, (const uint8_t *const *)b->data, b->linesize,
                  0, h, dst_b, dst_ls_b);

        sad = sad_8bit(buf_a, w, buf_b, w, w, h);
        count = (uint64_t)w * h;

        av_free(buf_a); av_free(buf_b);
        sws_freeContext(sws_a); sws_freeContext(sws_b);
    }

    // MAFD on 0–100 scale, matching FFmpeg scdet.
    return (double)sad * 100.0 / (double)count / 255.0;
}

// lapvar_8bit computes the variance of the discrete 4-neighbour Laplacian over an 8-bit
// single-channel image — the classic focus/sharpness measure (high = sharp/high-frequency,
// low = motion-blurred/defocused). Interior pixels only (a 1-px border is skipped). Returns
// the variance (>= 0); a flat or too-small image returns 0.
static double lapvar_8bit(const uint8_t *src, ptrdiff_t stride, int width, int height) {
    if (width < 3 || height < 3) return 0.0;
    double sum = 0.0, sumsq = 0.0;
    uint64_t n = 0;
    for (int y = 1; y < height - 1; y++) {
        const uint8_t *row = src + (ptrdiff_t)y * stride;
        const uint8_t *up  = row - stride;
        const uint8_t *dn  = row + stride;
        for (int x = 1; x < width - 1; x++) {
            int lap = 4 * (int)row[x] - (int)row[x - 1] - (int)row[x + 1] - (int)up[x] - (int)dn[x];
            double l = (double)lap;
            sum += l;
            sumsq += l * l;
            n++;
        }
    }
    if (n == 0) return 0.0;
    double mean = sum / (double)n;
    double var = sumsq / (double)n - mean * mean;
    return var < 0.0 ? 0.0 : var; // guard tiny negatives from FP rounding
}

// frame_luma_lapvar returns the variance of the Laplacian of the frame's luma (Y) plane — a
// focus/sharpness measure. Higher is sharper; a motion-blurred or defocused frame scores low.
// YUV planar frames use plane 0 directly (zero conversion); other formats fall back to a GRAY8
// swscale. Returns -1.0 on error.
static double frame_luma_lapvar(const AVFrame *f) {
    if (!f || f->width <= 0 || f->height <= 0)
        return -1.0;
    int w = f->width, h = f->height;

    if (is_yuv_planar(f->format)) {
        int luma_w = av_image_get_linesize(f->format, w, 0);
        if (luma_w <= 0) return -1.0;
        return lapvar_8bit(f->data[0], f->linesize[0], luma_w, h);
    }

    struct SwsContext *sws = sws_getContext(
        w, h, f->format, w, h, AV_PIX_FMT_GRAY8, SWS_BILINEAR, NULL, NULL, NULL);
    if (!sws) return -1.0;
    uint8_t *buf = (uint8_t *)av_malloc((size_t)w * h);
    if (!buf) { sws_freeContext(sws); return -1.0; }
    uint8_t *dst[4] = { buf, NULL, NULL, NULL };
    int dst_ls[4] = { w, 0, 0, 0 };
    sws_scale(sws, (const uint8_t *const *)f->data, f->linesize, 0, h, dst, dst_ls);
    double v = lapvar_8bit(buf, w, w, h);
    av_free(buf);
    sws_freeContext(sws);
    return v;
}

static int get_frame_pix_fmt(const AVFrame *frame) {
    return frame->format;
}

static int pix_fmt_rgba(void)  { return AV_PIX_FMT_RGBA;  }
static int pix_fmt_bgr24(void) { return AV_PIX_FMT_BGR24; }

#endif /* MEDIAMOLDER_FRAME_CGO_H */
