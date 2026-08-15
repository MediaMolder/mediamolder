/* Copyright (C) 2026 Thomas Vaughan
 * SPDX-License-Identifier: LGPL-2.1-or-later
 *
 * Why this file exists: a damaged container (lying PES lengths, non-interleaved
 * AVI, truncated tape captures) can drive a demuxer into leaving an AVPacket
 * structurally inconsistent even though av_read_frame reported success. The
 * next av_packet_unref / av_packet_free on that packet dereferences the poison
 * and takes the whole process down with an access violation — a fatal fault,
 * not an error any caller can catch (in particular, a Go recover() cannot catch
 * a cgo access violation). These helpers detect the DETECTABLE corruption
 * patterns and scrub the packet — drop the references without touching them —
 * instead of freeing through them.
 *
 * Honest limits: a dangling pointer that still looks plausible (aligned,
 * non-sentinel, pointing at freed-but-mapped memory) is indistinguishable from
 * a live one without dereferencing it, which is the very crash being avoided.
 * This guard stops scribbled/poisoned structs; it is defence in depth, not a
 * proof of safety. Process isolation is the complete answer for input that
 * corrupts the demuxer's own heap.
 */

#include "mm_packet_guard.h"

#include <string.h>

#include "libavutil/avutil.h"
#include "libavutil/time.h"

/* No real packet in a probed container approaches 1 GiB; a size beyond it is
 * structural corruption, not data. */
#define MM_PACKET_SIZE_CAP (1 << 30)

/* NULL is fine; a sentinel pattern or a null-page value cannot be a live
 * allocation. Deliberately NO alignment test here: this variant is for
 * pkt->data, which is a byte offset into a buffer, not a heap object —
 * mpegts/PES demuxers routinely set data = buf->data + n with an odd header
 * length n, and flagging that as poison would scrub well-formed packets and
 * abandon healthy files. */
static int mm_data_ptr_poisoned(const void *p) {
    uintptr_t v = (uintptr_t)p;
    if (v == 0) return 0;
    if (v == UINTPTR_MAX) return 1; /* the observed fault pattern */
    if (v < 4096) return 1;         /* inside the null page */
    return 0;
}

/* For pointer-to-struct fields (buf, side_data, opaque_ref), which are heap
 * allocations and therefore also pointer-aligned. */
static int mm_ptr_poisoned(const void *p) {
    return mm_data_ptr_poisoned(p) ||
           (((uintptr_t)p & (sizeof(void *) - 1)) != 0);
}

int mm_packet_consistent(const AVPacket *pkt) {
    if (!pkt) return 0;
    if (pkt->size < 0 || pkt->size > MM_PACKET_SIZE_CAP) return 0;
    if (pkt->size > 0 && pkt->data == NULL) return 0;
    if (mm_data_ptr_poisoned(pkt->data)) return 0;
    if (mm_ptr_poisoned(pkt->buf) ||
        mm_ptr_poisoned(pkt->side_data) || mm_ptr_poisoned(pkt->opaque_ref))
        return 0;
    if (pkt->side_data_elems < 0 || pkt->side_data_elems > 1024) return 0;
    if (pkt->side_data_elems > 0 && pkt->side_data == NULL) return 0;
    return 1;
}

void mm_packet_scrub(AVPacket *pkt) {
    if (!pkt) return;
    memset(pkt, 0, sizeof(*pkt));
    pkt->pts = AV_NOPTS_VALUE;
    pkt->dts = AV_NOPTS_VALUE;
    pkt->pos = -1;
}

int mm_packet_unref_guarded(AVPacket *pkt) {
    if (!pkt) return 0;
    if (!mm_packet_consistent(pkt)) {
        mm_packet_scrub(pkt);
        return 1;
    }
    av_packet_unref(pkt);
    return 0;
}

int mm_packet_free_guarded(AVPacket **pkt) {
    int scrubbed = 0;
    if (pkt && *pkt && !mm_packet_consistent(*pkt)) {
        mm_packet_scrub(*pkt);
        scrubbed = 1;
    }
    av_packet_free(pkt);
    return scrubbed;
}

int mm_read_frame_guarded(AVFormatContext *ctx, AVPacket *pkt) {
    int ret = av_read_frame(ctx, pkt);
    if (ret < 0) {
        /* libav >= 5 documents pkt as blank on error; verifying costs less
         * than trusting, and a scrub here spares the caller's next Unref. */
        if (!mm_packet_consistent(pkt)) mm_packet_scrub(pkt);
        return ret;
    }
    if (!mm_packet_consistent(pkt) ||
        pkt->stream_index < 0 ||
        (unsigned)pkt->stream_index >= ctx->nb_streams) {
        mm_packet_scrub(pkt);
        return MM_ERR_POISONED_PACKET;
    }
    return 0;
}

int mm_deadline_expired(void *opaque) {
    const int64_t *box = opaque;
    int64_t deadline = *box;
    return deadline > 0 && av_gettime_relative() > deadline;
}

void mm_install_interrupt(AVFormatContext *ctx, int64_t *deadline_us_box) {
    ctx->interrupt_callback.callback = mm_deadline_expired;
    ctx->interrupt_callback.opaque = deadline_us_box;
}

void mm_arm_deadline(int64_t *deadline_us_box, int64_t timeout_us) {
    *deadline_us_box = timeout_us > 0 ? av_gettime_relative() + timeout_us : 0;
}

void mm_packet_poison_for_test(AVPacket *pkt) {
    pkt->buf = (AVBufferRef *)UINTPTR_MAX;
    pkt->data = (uint8_t *)UINTPTR_MAX;
    pkt->size = -1;
}

void mm_packet_offset_data_for_test(AVPacket *pkt) {
    if (!pkt || !pkt->data || pkt->size < 2) return;
    pkt->data += 1; /* the legal mpegts/PES shape: data = buf->data + odd n */
    pkt->size -= 1;
}
