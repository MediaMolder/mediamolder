/* Copyright (C) 2026 Thomas Vaughan
 * SPDX-License-Identifier: LGPL-2.1-or-later
 *
 * Last-line-of-defence checks between libav demuxers and process death.
 * See mm_packet_guard.c for the rationale and honest limits.
 */
#ifndef MM_PACKET_GUARD_H
#define MM_PACKET_GUARD_H

#include <stdint.h>
#include "libavcodec/packet.h"
#include "libavformat/avformat.h"

/* Returned by mm_read_frame_guarded when av_read_frame reported success but
 * left the packet structurally inconsistent. A distinct negative value in the
 * AVERROR style ('POIS' tag) so it cannot collide with errno-based AVERRORs. */
#define MM_ERR_POISONED_PACKET (-0x504F4953)

/* 1 when pkt's fields are mutually consistent, 0 when the struct has been
 * scribbled on (poisoned pointers, impossible size, side-data disagreement). */
int mm_packet_consistent(const AVPacket *pkt);

/* Forget the packet's references WITHOUT touching them, leaving a blank packet
 * (as if freshly allocated). Leaks whatever the references held — on a
 * poisoned packet that is the point: one leaked buffer per damaged file beats
 * a dead process. */
void mm_packet_scrub(AVPacket *pkt);

/* av_packet_unref, unless the packet is inconsistent, in which case it is
 * scrubbed instead. Returns 1 when a scrub happened, 0 on a clean unref. */
int mm_packet_unref_guarded(AVPacket *pkt);

/* av_packet_free with the same protection: av_packet_free unrefs internally,
 * so freeing a poisoned packet dies exactly like unreffing it. Returns 1 when
 * the packet had to be scrubbed first. */
int mm_packet_free_guarded(AVPacket **pkt);

/* av_read_frame plus a post-read consistency check: on success the packet must
 * be internally consistent and its stream_index must name a real stream.
 * Returns MM_ERR_POISONED_PACKET (and scrubs the packet, leaving it safe to
 * reuse or free) when the demuxer's "success" fails those checks. *scrubbed
 * (optional) is set to 1 whenever a scrub happened — on the poisoned-success
 * path AND on an error return that left the packet inconsistent — so callers
 * counting scrubs see both. */
int mm_read_frame_guarded(AVFormatContext *ctx, AVPacket *pkt, int *scrubbed);

/* AVIO interrupt callback keyed on a monotonic deadline (av_gettime_relative
 * microseconds) stored in the opaque int64_t box. 0 in the box = disarmed.
 * The box is written and read on the thread performing the blocking call —
 * libav invokes the callback synchronously from inside that call — so no
 * atomics are needed. */
int mm_deadline_expired(void *opaque);
void mm_install_interrupt(AVFormatContext *ctx, int64_t *deadline_us_box);
void mm_arm_deadline(int64_t *deadline_us_box, int64_t timeout_us);

/* Test hook: stamp the packet with the exact poison pattern observed in the
 * wild (all-ones pointers, negative size) so tests can prove a guarded
 * unref/free survives what a bare av_packet_unref does not. */
void mm_packet_poison_for_test(AVPacket *pkt);

/* Test hook: shift pkt->data one byte into its buffer — the legal shape
 * mpegts/PES demuxers produce with odd header lengths — so tests can prove
 * an odd-offset data pointer is never mistaken for poison. */
void mm_packet_offset_data_for_test(AVPacket *pkt);

#endif /* MM_PACKET_GUARD_H */
