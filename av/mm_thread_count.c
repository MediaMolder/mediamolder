// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

#include "mm_thread_count.h"
#include <stdlib.h>

/* ---- codec execute2 callback ------------------------------------------- */

static int mm_execute2_cb(AVCodecContext *c,
                           int (*func)(AVCodecContext *, void *, int, int),
                           void *arg2, int *ret, int count)
{
    mm_codec_tctx_t *tctx = (mm_codec_tctx_t *)c->opaque;
    atomic_fetch_add_explicit(&tctx->tasks_active, count, memory_order_relaxed);
    int r = tctx->orig_execute2(c, func, arg2, ret, count);
    atomic_fetch_sub_explicit(&tctx->tasks_active, count, memory_order_relaxed);
    return r;
}

/* ---- filter execute callback -------------------------------------------- */

static int mm_filter_execute_cb(AVFilterContext *ctx,
                                 avfilter_action_func *func,
                                 void *arg, int *ret, int nb_jobs)
{
    mm_filter_tctx_t *tctx = (mm_filter_tctx_t *)ctx->graph->opaque;
    atomic_fetch_add_explicit(&tctx->tasks_active, nb_jobs, memory_order_relaxed);
    int r = tctx->orig_execute(ctx, func, arg, ret, nb_jobs);
    atomic_fetch_sub_explicit(&tctx->tasks_active, nb_jobs, memory_order_relaxed);
    return r;
}

/* ---- public install / free / query ------------------------------------- */

mm_codec_tctx_t *mm_install_codec_tracker(AVCodecContext *ctx)
{
    if (!ctx || !ctx->execute2)
        return NULL;
    mm_codec_tctx_t *tctx = (mm_codec_tctx_t *)calloc(1, sizeof(*tctx));
    if (!tctx)
        return NULL;
    tctx->orig_execute2 = ctx->execute2;
    ctx->execute2       = mm_execute2_cb;
    ctx->opaque         = tctx;
    return tctx;
}

mm_filter_tctx_t *mm_install_filter_tracker(AVFilterGraph *graph)
{
    if (!graph || !graph->execute)
        return NULL;
    mm_filter_tctx_t *tctx = (mm_filter_tctx_t *)calloc(1, sizeof(*tctx));
    if (!tctx)
        return NULL;
    tctx->orig_execute  = graph->execute;
    graph->execute      = mm_filter_execute_cb;
    graph->opaque       = tctx;
    return tctx;
}

void mm_codec_tctx_free(AVCodecContext *ctx, mm_codec_tctx_t *tctx)
{
    if (!tctx)
        return;
    /* Restore the original callback so no dangling pointer remains. */
    if (ctx && ctx->execute2 == mm_execute2_cb) {
        ctx->execute2 = tctx->orig_execute2;
        ctx->opaque   = NULL;
    }
    free(tctx);
}

void mm_filter_tctx_free(AVFilterGraph *graph, mm_filter_tctx_t *tctx)
{
    if (!tctx)
        return;
    if (graph && graph->execute == mm_filter_execute_cb) {
        graph->execute = tctx->orig_execute;
        graph->opaque  = NULL;
    }
    free(tctx);
}

int mm_codec_threads_busy(const mm_codec_tctx_t *tctx)
{
    if (!tctx)
        return -1;
    return (int)atomic_load_explicit(&tctx->tasks_active, memory_order_relaxed);
}

int mm_filter_threads_busy(const mm_filter_tctx_t *tctx)
{
    if (!tctx)
        return -1;
    return (int)atomic_load_explicit(&tctx->tasks_active, memory_order_relaxed);
}
