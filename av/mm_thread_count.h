// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// mm_thread_count.h – per-node active-thread counting via execute2/execute hooks.
//
// For libavcodec slice-threaded contexts we replace the execute2 function
// pointer (set by avcodec_open2) with a thin wrapper that atomically tracks
// how many slice jobs are in flight.  For filter graphs we do the same with
// the AVFilterGraph.execute pointer (set by avfilter_graph_config).
//
// Both tracker structs are heap-allocated by mm_install_*_tracker and stored
// in the application-owned opaque fields:
//   • AVCodecContext.opaque  →  mm_codec_tctx_t *
//   • AVFilterGraph.opaque   →  mm_filter_tctx_t *
//
// Usage:
//   1. Call mm_install_codec_tracker(ctx)  AFTER avcodec_open2().
//   2. Call mm_install_filter_tracker(g)  AFTER avfilter_graph_config().
//   3. Poll mm_codec_threads_busy() / mm_filter_threads_busy() at any time.
//   4. Call mm_codec_tctx_free() / mm_filter_tctx_free() before freeing the
//      AV context, to avoid dangling-pointer callbacks.
//
// Thread safety: the atomic counter is manipulated with relaxed ordering and
// is only read through the mm_*_threads_busy() accessors which use
// memory_order_relaxed.  This is sufficient for a metric sampler that does
// not need strict synchronisation with encoding/filtering state.

#ifndef MM_THREAD_COUNT_H
#define MM_THREAD_COUNT_H

#include <stdatomic.h>
#include "libavcodec/avcodec.h"
#include "libavfilter/avfilter.h"

/* Per-encoder/decoder thread-activity tracker.
 * Stored in AVCodecContext.opaque.  Wraps the execute2 callback installed by
 * avcodec_open2 so we can count in-flight slice tasks. */
typedef struct {
    atomic_int    tasks_active;
    int (*orig_execute2)(AVCodecContext *c,
                         int (*func)(AVCodecContext *, void *, int, int),
                         void *arg2, int *ret, int count);
} mm_codec_tctx_t;

/* Per-filter-graph thread-activity tracker.
 * Stored in AVFilterGraph.opaque.  Wraps the execute callback installed by
 * avfilter_graph_config so we can count in-flight filter jobs. */
typedef struct {
    atomic_int            tasks_active;
    avfilter_execute_func *orig_execute;
} mm_filter_tctx_t;

/* Install the execute2 wrapper on ctx.
 * Returns a pointer to the allocated mm_codec_tctx_t, or NULL if execute2 is
 * not set (codec does not use slice threading) or allocation fails.
 * MUST be called after avcodec_open2(). */
mm_codec_tctx_t  *mm_install_codec_tracker(AVCodecContext *ctx);

/* Install the execute wrapper on graph.
 * Returns a pointer to the allocated mm_filter_tctx_t, or NULL if execute is
 * not set or allocation fails.
 * MUST be called after avfilter_graph_config(). */
mm_filter_tctx_t *mm_install_filter_tracker(AVFilterGraph *graph);

/* Free a codec tracker previously allocated by mm_install_codec_tracker.
 * Restores the original execute2 pointer on ctx before freeing, so it is safe
 * to call even if encoding is still active (ctx will revert to direct calls).
 * Safe to call with NULL tctx or NULL ctx. */
void mm_codec_tctx_free(AVCodecContext *ctx, mm_codec_tctx_t *tctx);

/* Free a filter tracker previously allocated by mm_install_filter_tracker.
 * Restores the original execute pointer on graph before freeing.
 * Safe to call with NULL tctx or NULL graph. */
void mm_filter_tctx_free(AVFilterGraph *graph, mm_filter_tctx_t *tctx);

/* Return the number of codec slice tasks currently executing inside
 * execute2, or -1 if tctx is NULL (execute2 was not available). */
int mm_codec_threads_busy(const mm_codec_tctx_t *tctx);

/* Return the number of filter jobs currently executing inside execute,
 * or -1 if tctx is NULL. */
int mm_filter_threads_busy(const mm_filter_tctx_t *tctx);

#endif /* MM_THREAD_COUNT_H */
