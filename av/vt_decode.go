// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

// VTDecoderContext provides VideoToolbox-native decoding for codecs that are
// absent from LibAV's registry (e.g. ProRes RAW / ProRes RAW HQ).  It
// satisfies the FrameDecoder interface so the pipeline can use it
// interchangeably with DecoderContext and HWDecoderContext.
//
// Design:
//   - Decode is synchronous (VTDecodeFrameFlags = 0).  The VT callback fires
//     before VTDecompressionSessionDecodeFrame returns, so no inter-goroutine
//     synchronisation is needed.
//   - Each SendPacket call produces at most one decoded frame, which is copied
//     from the CVPixelBuffer into an AVFrame and appended to a small pending
//     queue.
//   - ReceiveFrame pops from the queue; it returns AVERROR(EAGAIN) when empty
//     and ErrEOF after Flush() drains the queue.

// #cgo LDFLAGS: -framework VideoToolbox -framework CoreMedia -framework CoreFoundation -framework CoreVideo
//
// #include <VideoToolbox/VideoToolbox.h>
// #include <CoreMedia/CMFormatDescription.h>
// #include <CoreMedia/CMBlockBuffer.h>
// #include <CoreMedia/CMSampleBuffer.h>
// #include <CoreFoundation/CoreFoundation.h>
// #include <CoreVideo/CVPixelBuffer.h>
// #include "libavutil/frame.h"
// #include "libavutil/pixfmt.h"
// #include <stdint.h>
// #include <string.h>
//
// static int mm_vt_averror_eagain(void) { return AVERROR(EAGAIN); }
//
// // mm_vt_cv_pix_fmt_p010 and _nv12 return the kCVPixelFormatType constants
// // as plain uint32 so Go can compare and pass them without CGo enum issues.
// static uint32_t mm_vt_cv_pix_fmt_p010(void) {
//     return (uint32_t)kCVPixelFormatType_420YpCbCr10BiPlanarVideoRange;
// }
// static uint32_t mm_vt_cv_pix_fmt_nv12(void) {
//     return (uint32_t)kCVPixelFormatType_420YpCbCr8BiPlanarVideoRange;
// }
//
// // Per-frame decode result written by the VT output callback.
// typedef struct {
//     CVImageBufferRef image;  // retained; caller must CFRelease after use
//     CMTime           pts;
//     OSStatus         status;
// } mm_vt_dec_result;
//
// // VT output callback — fires synchronously before
// // VTDecompressionSessionDecodeFrame returns (when flags == 0).
// static void mm_vt_dec_cb(
//         void  *decompressionOutputRefCon,
//         void  *sourceFrameRefCon,
//         OSStatus status,
//         VTDecodeInfoFlags infoFlags,
//         CVImageBufferRef imageBuffer,
//         CMTime presentationTimeStamp,
//         CMTime presentationDuration)
// {
//     (void)decompressionOutputRefCon;
//     (void)infoFlags;
//     (void)presentationDuration;
//     mm_vt_dec_result *r = (mm_vt_dec_result *)sourceFrameRefCon;
//     if (!r) return;
//     r->status = status;
//     r->pts    = presentationTimeStamp;
//     if (imageBuffer) {
//         CFRetain(imageBuffer);
//         r->image = imageBuffer;
//     }
// }
//
// // mm_vt_dec_create opens a VTDecompressionSession for codec_type.
// // pref_cv_pix_fmt is a kCVPixelFormatType_* constant; VT will try to
// // output that format.  On success *session_out and *fmtdesc_out are set
// // (both owned by the caller) and noErr (0) is returned.
// static OSStatus mm_vt_dec_create(
//         uint32_t codec_type, int width, int height,
//         uint32_t pref_cv_pix_fmt,
//         void **session_out, void **fmtdesc_out)
// {
//     *session_out = NULL;
//     *fmtdesc_out = NULL;
//
//     CMVideoFormatDescriptionRef fd = NULL;
//     OSStatus st = CMVideoFormatDescriptionCreate(
//         kCFAllocatorDefault,
//         (CMVideoCodecType)codec_type,
//         (int32_t)width, (int32_t)height,
//         NULL,   // extensions — ProRes RAW frames carry their own metadata
//         &fd);
//     if (st != noErr) return st;
//
//     CFNumberRef pfmt = CFNumberCreate(kCFAllocatorDefault,
//         kCFNumberSInt32Type, &pref_cv_pix_fmt);
//     const void *dk[] = { kCVPixelBufferPixelFormatTypeKey };
//     const void *dv[] = { pfmt };
//     CFDictionaryRef destAttrs = CFDictionaryCreate(
//         kCFAllocatorDefault, dk, dv, 1,
//         &kCFTypeDictionaryKeyCallBacks,
//         &kCFTypeDictionaryValueCallBacks);
//     CFRelease(pfmt);
//
//     VTDecompressionOutputCallbackRecord cb = {
//         .decompressionOutputCallback = mm_vt_dec_cb,
//         .decompressionOutputRefCon   = NULL,
//     };
//
//     VTDecompressionSessionRef session = NULL;
//     st = VTDecompressionSessionCreate(
//         kCFAllocatorDefault,
//         fd,
//         NULL,        // videoDecoderSpecification
//         destAttrs,
//         &cb,
//         &session);
//     CFRelease(destAttrs);
//
//     if (st != noErr) {
//         CFRelease(fd);
//         return st;
//     }
//     *session_out = (void *)session;
//     *fmtdesc_out = (void *)fd;
//     return noErr;
// }
//
// // mm_vt_dec_send decodes one compressed packet synchronously.
// // data/size are the raw packet bytes (must remain valid until return).
// // pkt_pts/pkt_dts/pkt_dur are in stream time_base units; tb_num/tb_den
// // describe the stream time_base rational.
// // *result must be zero-initialised before the call; the caller is
// // responsible for CFRelease(result->image) when non-NULL.
// // Returns noErr even when the frame is dropped (result->image == NULL).
// static OSStatus mm_vt_dec_send(
//         void *session, void *fmt_desc,
//         const uint8_t *data, size_t size,
//         int64_t pkt_pts, int64_t pkt_dts, int64_t pkt_dur,
//         int32_t tb_num, int32_t tb_den,
//         mm_vt_dec_result *result)
// {
//     // Wrap compressed bytes in a CMBlockBuffer without copying.
//     // kCFAllocatorNull prevents VT from freeing data (owned by the packet).
//     CMBlockBufferRef block = NULL;
//     OSStatus st = CMBlockBufferCreateWithMemoryBlock(
//         kCFAllocatorDefault,
//         (void *)data, size,
//         kCFAllocatorNull,
//         NULL, 0, size, 0,
//         &block);
//     if (st != noErr) return st;
//
//     // Map AVPacket timestamps to CMTime.
//     // AV_NOPTS_VALUE is INT64_MIN; map it to kCMTimeInvalid.
//     const int64_t nopts = (int64_t)0x8000000000000000LL;
//     CMTime pts_cm = (pkt_pts == nopts) ? kCMTimeInvalid
//                   : CMTimeMake(pkt_pts * (int64_t)tb_num, tb_den);
//     CMTime dts_cm = (pkt_dts == nopts) ? kCMTimeInvalid
//                   : CMTimeMake(pkt_dts * (int64_t)tb_num, tb_den);
//     CMTime dur_cm = (pkt_dur == 0)     ? kCMTimeInvalid
//                   : CMTimeMake(pkt_dur * (int64_t)tb_num, tb_den);
//
//     CMSampleTimingInfo timing = {
//         .duration              = dur_cm,
//         .presentationTimeStamp = pts_cm,
//         .decodeTimeStamp       = dts_cm,
//     };
//     size_t sampleSize = size;
//
//     CMSampleBufferRef sample = NULL;
//     st = CMSampleBufferCreate(
//         kCFAllocatorDefault,
//         block, TRUE,
//         NULL, NULL,
//         (CMVideoFormatDescriptionRef)fmt_desc,
//         1, 1, &timing, 1, &sampleSize,
//         &sample);
//     CFRelease(block);
//     if (st != noErr) return st;
//
//     // Synchronous decode: callback fires before this returns.
//     VTDecodeInfoFlags info = 0;
//     st = VTDecompressionSessionDecodeFrame(
//         (VTDecompressionSessionRef)session,
//         sample,
//         0,              // flags: 0 = synchronous
//         (void *)result,
//         &info);
//     CFRelease(sample);
//     return st;
// }
//
// // mm_vt_dec_flush waits for any outstanding async frames.
// // In synchronous mode this is a no-op but is called for correctness.
// static OSStatus mm_vt_dec_flush(void *session) {
//     return VTDecompressionSessionWaitForAsynchronousFrames(
//         (VTDecompressionSessionRef)session);
// }
//
// // mm_vt_dec_close invalidates and releases the session and format desc.
// static void mm_vt_dec_close(void *session, void *fmt_desc) {
//     if (session) {
//         VTDecompressionSessionInvalidate((VTDecompressionSessionRef)session);
//         CFRelease(session);
//     }
//     if (fmt_desc) CFRelease(fmt_desc);
// }
//
// // mm_vt_cvbuf_to_avframe copies pixel data from a locked CVPixelBuffer
// // into a pre-allocated AVFrame whose data[] planes are already allocated
// // via av_frame_get_buffer.  Returns 0 on success.
// static int mm_vt_cvbuf_to_avframe(CVImageBufferRef buf, AVFrame *f) {
//     if (!buf || !f) return -1;
//     if (CVPixelBufferLockBaseAddress(buf,
//             kCVPixelBufferLock_ReadOnly) != kCVReturnSuccess)
//         return -1;
//
//     size_t nplanes = CVPixelBufferGetPlaneCount(buf);
//     if (nplanes == 0) {
//         // Packed format: single plane.
//         const uint8_t *src       = CVPixelBufferGetBaseAddress(buf);
//         size_t         src_str   = CVPixelBufferGetBytesPerRow(buf);
//         size_t         h         = CVPixelBufferGetHeight(buf);
//         uint8_t       *dst       = f->data[0];
//         int            dst_str   = f->linesize[0];
//         if (src && dst) {
//             size_t copy_w = src_str < (size_t)dst_str ? src_str : (size_t)dst_str;
//             for (size_t y = 0; y < h; y++, src += src_str, dst += dst_str)
//                 memcpy(dst, src, copy_w);
//         }
//     } else {
//         for (size_t p = 0; p < nplanes && p < AV_NUM_DATA_POINTERS; p++) {
//             const uint8_t *src   = CVPixelBufferGetBaseAddressOfPlane(buf, p);
//             size_t         src_str = CVPixelBufferGetBytesPerRowOfPlane(buf, p);
//             size_t         h       = CVPixelBufferGetHeightOfPlane(buf, p);
//             uint8_t       *dst     = f->data[p];
//             int            dst_str = f->linesize[p];
//             if (!src || !dst) continue;
//             size_t copy_w = src_str < (size_t)dst_str ? src_str : (size_t)dst_str;
//             for (size_t y = 0; y < h; y++, src += src_str, dst += dst_str)
//                 memcpy(dst, src, copy_w);
//         }
//     }
//
//     CVPixelBufferUnlockBaseAddress(buf, kCVPixelBufferLock_ReadOnly);
//     return 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// VT codec-type four-CC constants (CMFormatDescription.h).
const (
	vtCodecProResRAW   uint32 = 0x6170726E // 'aprn' kCMVideoCodecType_AppleProResRAW
	vtCodecProResRAWHQ uint32 = 0x61707268 // 'aprh' kCMVideoCodecType_AppleProResRAWHQ
)

// IsVTCodec reports whether codecTag is a codec that MediaMolder can decode
// through VideoToolbox without a LibAV codec.  Used by the pipeline to try a
// VT-native path before propagating an "codec not found" error.
func IsVTCodec(codecTag uint32) bool {
	return codecTag == vtCodecProResRAW || codecTag == vtCodecProResRAWHQ
}

// vtPixFmt returns the CVPixelBuffer pixel format and AVPixelFormat for a
// VT-native decode session.  ProRes RAW preserves bit depth via P010.
func vtPixFmt() (cvFmt C.uint32_t, avFmt C.int) {
	// Always request 10-bit NV12 (P010) so ProRes RAW's extended bit depth
	// is preserved in the output AVFrame.
	return C.uint32_t(C.mm_vt_cv_pix_fmt_p010()), C.int(C.AV_PIX_FMT_P010LE)
}

// VTDecoderContext implements FrameDecoder using Apple VideoToolbox.
// Obtain one via OpenVTDecoder; close with Close().
type VTDecoderContext struct {
	session  unsafe.Pointer // VTDecompressionSessionRef
	fmtDesc  unsafe.Pointer // CMVideoFormatDescriptionRef
	width    int
	height   int
	avPixFmt C.int  // AV_PIX_FMT_* for output AVFrame
	timeBase [2]int // stream time_base {num, den}

	// pending holds decoded frames produced by SendPacket awaiting
	// ReceiveFrame.  In synchronous mode there is at most one entry.
	pending []*Frame
	eof     bool
}

// OpenVTDecoder opens a VideoToolbox decompression session for the video
// stream at streamIndex.  Returns an error if the stream's codec tag is not
// a VT-native codec or if VT session creation fails.
func OpenVTDecoder(input *InputFormatContext, streamIndex int) (*VTDecoderContext, error) {
	si, err := input.StreamInfo(streamIndex)
	if err != nil {
		return nil, err
	}
	if !IsVTCodec(si.CodecTag) {
		return nil, fmt.Errorf("codec tag 0x%08x is not a supported VT-native codec", si.CodecTag)
	}
	if si.Width <= 0 || si.Height <= 0 {
		return nil, fmt.Errorf("stream %d: invalid dimensions %dx%d for VT decoder", streamIndex, si.Width, si.Height)
	}

	cvFmt, avFmt := vtPixFmt()

	var sessionPtr, fmtDescPtr unsafe.Pointer
	st := C.mm_vt_dec_create(
		C.uint32_t(si.CodecTag),
		C.int(si.Width), C.int(si.Height),
		cvFmt,
		&sessionPtr, &fmtDescPtr,
	)
	if st != 0 {
		return nil, fmt.Errorf("VTDecompressionSessionCreate for stream %d: OSStatus %d", streamIndex, int(st))
	}

	return &VTDecoderContext{
		session:  sessionPtr,
		fmtDesc:  fmtDescPtr,
		width:    si.Width,
		height:   si.Height,
		avPixFmt: avFmt,
		timeBase: si.TimeBase,
	}, nil
}

// SendPacket submits a compressed packet to the VT decompression session.
// In synchronous mode the callback fires before this returns, so the decoded
// frame (if any) is immediately available via ReceiveFrame.
func (d *VTDecoderContext) SendPacket(pkt *Packet) error {
	if pkt == nil || pkt.p == nil {
		return nil
	}

	result := (*C.mm_vt_dec_result)(C.calloc(1, C.size_t(C.sizeof_mm_vt_dec_result)))
	if result == nil {
		return fmt.Errorf("VT decode: calloc mm_vt_dec_result: out of memory")
	}
	defer C.free(unsafe.Pointer(result))

	tb := d.timeBase
	st := C.mm_vt_dec_send(
		d.session, d.fmtDesc,
		pkt.p.data, C.size_t(pkt.p.size),
		C.int64_t(pkt.p.pts),
		C.int64_t(pkt.p.dts),
		C.int64_t(pkt.p.duration),
		C.int32_t(tb[0]), C.int32_t(tb[1]),
		result,
	)
	if st != 0 {
		// VT normally passes nil imageBuffer on error, but release defensively.
		if result.image != nil {
			C.CFRelease(C.CFTypeRef(unsafe.Pointer(result.image)))
		}
		return fmt.Errorf("VTDecompressionSessionDecodeFrame: OSStatus %d", int(st))
	}
	if result.image == nil {
		// Frame dropped by VT — not an error; caller will get EAGAIN.
		return nil
	}

	// Build AVFrame from the decoded CVPixelBuffer.
	f, err := AllocFrame()
	if err != nil {
		C.CFRelease(C.CFTypeRef(unsafe.Pointer(result.image)))
		return err
	}
	f.p.format = d.avPixFmt
	f.p.width = C.int(d.width)
	f.p.height = C.int(d.height)
	if ret := C.av_frame_get_buffer(f.p, 0); ret < 0 {
		C.CFRelease(C.CFTypeRef(unsafe.Pointer(result.image)))
		f.Close()
		return fmt.Errorf("av_frame_get_buffer: %w", newErr(ret))
	}

	if rc := C.mm_vt_cvbuf_to_avframe(result.image, f.p); rc != 0 {
		C.CFRelease(C.CFTypeRef(unsafe.Pointer(result.image)))
		f.Close()
		return fmt.Errorf("VT decode: pixel copy failed (rc=%d)", int(rc))
	}
	C.CFRelease(C.CFTypeRef(unsafe.Pointer(result.image)))

	// Set PTS from the CMTime delivered by VT (which reflects the
	// presentationTimeStamp we supplied in mm_vt_dec_send).
	// kCMTimeFlags_Valid = 1 (bit 0); only convert when CMTime is valid.
	if result.pts.flags&1 != 0 && result.pts.timescale > 0 && tb[0] > 0 {
		// CMTime.value / CMTime.timescale = seconds
		// PTS = (value / timescale) / (tb_num / tb_den) = value * tb_den / (timescale * tb_num)
		pts := int64(result.pts.value) * int64(tb[1]) /
			(int64(result.pts.timescale) * int64(tb[0]))
		f.p.pts = C.int64_t(pts)
	}

	d.pending = append(d.pending, f)
	return nil
}

// ReceiveFrame fills f with the next decoded frame.
// Returns AVERROR(EAGAIN) when no frame is ready, ErrEOF after Flush().
func (d *VTDecoderContext) ReceiveFrame(f *Frame) error {
	if len(d.pending) == 0 {
		if d.eof {
			return ErrEOF
		}
		return newErr(C.int(C.mm_vt_averror_eagain()))
	}
	src := d.pending[0]
	d.pending = d.pending[1:]
	C.av_frame_move_ref(f.p, src.p)
	src.Close()
	return nil
}

// Flush signals end-of-stream. Any remaining frames in the pending queue
// are still available via ReceiveFrame; once the queue is empty ReceiveFrame
// returns ErrEOF.
func (d *VTDecoderContext) Flush() error {
	if d.session != nil {
		C.mm_vt_dec_flush(d.session)
	}
	d.eof = true
	return nil
}

// Close invalidates the VT session and releases all resources.
func (d *VTDecoderContext) Close() error {
	for _, f := range d.pending {
		f.Close()
	}
	d.pending = nil
	C.mm_vt_dec_close(d.session, d.fmtDesc)
	d.session = nil
	d.fmtDesc = nil
	return nil
}

// newVTDecoderForCodec creates a VTDecoderContext from a raw codec type and
// dimensions, bypassing InputFormatContext.  Used only by package tests.
func newVTDecoderForCodec(codecTag uint32, width, height int) (*VTDecoderContext, error) {
	if width <= 0 || height <= 0 {
		return nil, fmt.Errorf("invalid dimensions %dx%d", width, height)
	}
	cvFmt, avFmt := vtPixFmt()
	var sessionPtr, fmtDescPtr unsafe.Pointer
	st := C.mm_vt_dec_create(
		C.uint32_t(codecTag),
		C.int(width), C.int(height),
		cvFmt,
		&sessionPtr, &fmtDescPtr,
	)
	if st != 0 {
		return nil, &Err{Code: int(st), Message: "mm_vt_dec_create"}
	}
	return &VTDecoderContext{
		session:  sessionPtr,
		fmtDesc:  fmtDescPtr,
		width:    width,
		height:   height,
		avPixFmt: avFmt,
		timeBase: [2]int{1, 30000},
	}, nil
}
