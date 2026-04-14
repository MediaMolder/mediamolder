// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/opt.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/hwcontext.h"
// #include "libavutil/buffer.h"
// #include "libavutil/frame.h"
//
// static void hw_enc_set_device_ctx(AVCodecContext *ctx, AVBufferRef *device_ref) {
//     ctx->hw_device_ctx = av_buffer_ref(device_ref);
// }
//
// static int hw_enc_set_frames_ctx(AVCodecContext *ctx, AVBufferRef *frames_ref) {
//     ctx->hw_frames_ctx = av_buffer_ref(frames_ref);
//     if (!ctx->hw_frames_ctx) return -1;
//     return 0;
// }
//
// static int hw_enc_is_hw_pix_fmt(enum AVPixelFormat fmt) {
//     const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(fmt);
//     if (!desc) return 0;
//     return (desc->flags & AV_PIX_FMT_FLAG_HWACCEL) ? 1 : 0;
// }
//
// static enum AVPixelFormat hw_enc_frame_format(const AVFrame *f) {
//     return (enum AVPixelFormat)f->format;
// }
//
// static int hw_enc_transfer_sw_to_hw(AVFrame *dst, const AVFrame *src) {
//     return av_hwframe_transfer_data(dst, src, 0);
// }
//
// static int hw_enc_alloc_frame(AVFrame *f, AVBufferRef *hw_frames_ref) {
//     f->hw_frames_ctx = av_buffer_ref(hw_frames_ref);
//     if (!f->hw_frames_ctx) return -1;
//     return av_hwframe_get_buffer(f->hw_frames_ctx, f, 0);
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// HWEncoderOptions extends EncoderOptions with hardware acceleration fields.
type HWEncoderOptions struct {
	EncoderOptions

	// HWDevice is the hardware device context to use for encoding.
	HWDevice *HWDeviceContext

	// HWFrames is an optional pre-allocated hardware frames context.
	// If nil and HWDevice is set, a frames context is created automatically.
	HWFrames *HWFramesContext

	// SWPixFmt is the software pixel format for automatic sw→hw upload.
	// Default: NV12 (common for NVENC/VAAPI/QSV).
	SWPixFmt int
}

// HWEncoderContext wraps a hardware-accelerated encoder.
// It automatically handles sw→hw frame upload when the encoder requires
// hardware frames but receives software frames.
type HWEncoderContext struct {
	p         *C.AVCodecContext
	hwFrames  *C.AVBufferRef // hw_frames_ctx for upload, may be nil
	ownFrames bool           // true if we allocated hwFrames and must free it
}

// OpenHWEncoder creates and opens a hardware-accelerated encoder.
func OpenHWEncoder(opts HWEncoderOptions) (*HWEncoderContext, error) {
	cName := C.CString(opts.CodecName)
	defer C.free(unsafe.Pointer(cName))

	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil, fmt.Errorf("hw encoder %q not found", opts.CodecName)
	}

	ctx := C.avcodec_alloc_context3(codec)
	if ctx == nil {
		return nil, &Err{Code: -12, Message: "avcodec_alloc_context3: out of memory"}
	}

	// Video fields.
	if opts.Width > 0 {
		ctx.width = C.int(opts.Width)
		ctx.height = C.int(opts.Height)
		if opts.PixFmt != 0 {
			ctx.pix_fmt = C.enum_AVPixelFormat(opts.PixFmt)
		} else if opts.HWDevice != nil {
			// Use the hardware pixel format for this device type.
			ctx.pix_fmt = C.enum_AVPixelFormat(opts.HWDevice.Type().HWPixelFormat())
		} else {
			ctx.pix_fmt = C.AV_PIX_FMT_YUV420P
		}
		if opts.FrameRate[1] > 0 {
			ctx.time_base = C.AVRational{
				num: C.int(opts.FrameRate[1]),
				den: C.int(opts.FrameRate[0]),
			}
			ctx.framerate = C.AVRational{
				num: C.int(opts.FrameRate[0]),
				den: C.int(opts.FrameRate[1]),
			}
		}
		if opts.GOPSize > 0 {
			ctx.gop_size = C.int(opts.GOPSize)
		}
	}

	// Audio fields.
	if opts.SampleRate > 0 {
		ctx.sample_rate = C.int(opts.SampleRate)
		ctx.sample_fmt = C.enum_AVSampleFormat(opts.SampleFmt)
		ctx.time_base = C.AVRational{num: 1, den: C.int(opts.SampleRate)}
		C.av_channel_layout_default(&ctx.ch_layout, C.int(opts.Channels))
	}

	if opts.BitRate > 0 {
		ctx.bit_rate = C.int64_t(opts.BitRate)
	}

	if opts.GlobalHeader {
		ctx.flags |= C.AV_CODEC_FLAG_GLOBAL_HEADER
	}

	// Set hardware device context.
	var hwFramesRef *C.AVBufferRef
	ownFrames := false

	if opts.HWDevice != nil {
		C.hw_enc_set_device_ctx(ctx, opts.HWDevice.raw())

		if opts.HWFrames != nil {
			hwFramesRef = opts.HWFrames.raw()
			if ret := C.hw_enc_set_frames_ctx(ctx, hwFramesRef); ret < 0 {
				C.avcodec_free_context(&ctx)
				return nil, fmt.Errorf("set hw frames ctx: %w", newErr(ret))
			}
		}
	}

	// Build AVDictionary for extra options.
	var dict *C.AVDictionary
	for k, v := range opts.ExtraOpts {
		ck := C.CString(k)
		cv := C.CString(v)
		C.av_dict_set(&dict, ck, cv, 0)
		C.free(unsafe.Pointer(ck))
		C.free(unsafe.Pointer(cv))
	}

	ret := C.avcodec_open2(ctx, codec, &dict)
	if dict != nil {
		C.av_dict_free(&dict)
	}
	if ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2(%s): %w", opts.CodecName, newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(hw_encoder:"+opts.CodecName+")")
	return &HWEncoderContext{
		p:         ctx,
		hwFrames:  hwFramesRef,
		ownFrames: ownFrames,
	}, nil
}

// Close frees the hw encoder context.
func (e *HWEncoderContext) Close() error {
	if e.ownFrames && e.hwFrames != nil {
		C.av_buffer_unref(&e.hwFrames)
		e.hwFrames = nil
	}
	if e.p != nil {
		leakUntrack(unsafe.Pointer(e.p))
		C.avcodec_free_context(&e.p)
		e.p = nil
	}
	return nil
}

// SendFrame submits a frame for encoding. If the frame is in software format and
// the encoder expects hardware frames, it is automatically uploaded.
// Pass nil to flush.
func (e *HWEncoderContext) SendFrame(f *Frame) error {
	if f == nil {
		ret := C.avcodec_send_frame(e.p, nil)
		return newErr(ret)
	}

	// If the encoder expects hw frames but we got a sw frame, upload it.
	fmtIsHW := C.hw_enc_is_hw_pix_fmt(C.hw_enc_frame_format(f.raw())) != 0
	encoderWantsHW := C.hw_enc_is_hw_pix_fmt(C.enum_AVPixelFormat(e.p.pix_fmt)) != 0

	if encoderWantsHW && !fmtIsHW && e.p.hw_frames_ctx != nil {
		// Upload sw → hw.
		hwFrame := C.av_frame_alloc()
		if hwFrame == nil {
			return &Err{Code: -12, Message: "av_frame_alloc for hw upload"}
		}
		defer C.av_frame_free(&hwFrame)

		retAlloc := C.hw_enc_alloc_frame(hwFrame, e.p.hw_frames_ctx)
		if retAlloc < 0 {
			return fmt.Errorf("alloc hw frame for upload: %w", newErr(retAlloc))
		}

		retTransfer := C.hw_enc_transfer_sw_to_hw(hwFrame, f.raw())
		if retTransfer < 0 {
			return fmt.Errorf("sw→hw upload: %w", newErr(retTransfer))
		}
		hwFrame.pts = f.raw().pts
		ret := C.avcodec_send_frame(e.p, hwFrame)
		return newErr(ret)
	}

	ret := C.avcodec_send_frame(e.p, f.raw())
	return newErr(ret)
}

// ReceivePacket receives an encoded packet.
// Returns ErrEAgain if more frames are needed, ErrEOF when flushing is complete.
func (e *HWEncoderContext) ReceivePacket(pkt *Packet) error {
	ret := C.avcodec_receive_packet(e.p, pkt.raw())
	return newErr(ret)
}

// Flush signals end-of-input so buffered packets can be drained.
func (e *HWEncoderContext) Flush() error {
	return e.SendFrame(nil)
}
