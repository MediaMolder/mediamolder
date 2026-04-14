// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavutil/hwcontext.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/buffer.h"
// #include "libavutil/frame.h"
//
// static AVCodecParameters *hw_get_codecpar(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->codecpar;
// }
// static AVRational hw_get_stream_time_base(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->time_base;
// }
//
// // Per-decoder hw pixel format for the get_format callback.
// // Serialized by Go-side because decoder open is sequential.
// static enum AVPixelFormat g_hw_pix_fmt = AV_PIX_FMT_NONE;
//
// static enum AVPixelFormat get_hw_format(AVCodecContext *ctx,
//                                          const enum AVPixelFormat *pix_fmts) {
//     const enum AVPixelFormat *p;
//     for (p = pix_fmts; *p != AV_PIX_FMT_NONE; p++) {
//         if (*p == g_hw_pix_fmt) return *p;
//     }
//     return pix_fmts[0];
// }
//
// static void set_hw_get_format(AVCodecContext *ctx, enum AVPixelFormat fmt) {
//     g_hw_pix_fmt = fmt;
//     ctx->get_format = get_hw_format;
// }
//
// static int codec_supports_hw_type(const AVCodec *codec, enum AVHWDeviceType type) {
//     int i;
//     for (i = 0;; i++) {
//         const AVCodecHWConfig *config = avcodec_get_hw_config(codec, i);
//         if (!config) return 0;
//         if (config->methods & AV_CODEC_HW_CONFIG_METHOD_HW_DEVICE_CTX &&
//             config->device_type == type) {
//             return 1;
//         }
//     }
// }
//
// static void set_hw_device_ctx(AVCodecContext *ctx, AVBufferRef *device_ref) {
//     ctx->hw_device_ctx = av_buffer_ref(device_ref);
// }
//
// static enum AVPixelFormat hw_dec_pix_fmt_for_type(enum AVHWDeviceType type) {
//     switch (type) {
//         case AV_HWDEVICE_TYPE_CUDA:        return AV_PIX_FMT_CUDA;
//         case AV_HWDEVICE_TYPE_VAAPI:       return AV_PIX_FMT_VAAPI;
//         case AV_HWDEVICE_TYPE_QSV:         return AV_PIX_FMT_QSV;
//         case AV_HWDEVICE_TYPE_VIDEOTOOLBOX: return AV_PIX_FMT_VIDEOTOOLBOX;
//         default:                            return AV_PIX_FMT_NONE;
//     }
// }
//
// static int hw_dec_is_hw_pix_fmt(enum AVPixelFormat fmt) {
//     const AVPixFmtDescriptor *desc = av_pix_fmt_desc_get(fmt);
//     if (!desc) return 0;
//     return (desc->flags & AV_PIX_FMT_FLAG_HWACCEL) ? 1 : 0;
// }
//
// static enum AVPixelFormat hw_dec_frame_format(const AVFrame *f) {
//     return (enum AVPixelFormat)f->format;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// HWDecoderContext wraps a hardware-accelerated decoder.
// It decodes packets using a hardware device and can optionally transfer
// frames to software automatically.
type HWDecoderContext struct {
	p            *C.AVCodecContext
	streamIndex  int
	deviceType   HWDeviceType
	autoTransfer bool // if true, hw frames are automatically transferred to sw
}

// HWDecoderOptions configures hardware decoder creation.
type HWDecoderOptions struct {
	// AutoTransfer: if true, ReceiveFrame automatically transfers hw frames to sw.
	AutoTransfer bool
}

// OpenHWDecoder creates a hardware-accelerated decoder for the given stream.
// If the codec does not support the given device type, it falls back to software decoding.
func OpenHWDecoder(input *InputFormatContext, streamIndex int, device *HWDeviceContext, opts HWDecoderOptions) (*HWDecoderContext, error) {
	if streamIndex < 0 || streamIndex >= input.NumStreams() {
		return nil, fmt.Errorf("stream index %d out of range", streamIndex)
	}

	cp := C.hw_get_codecpar(input.raw(), C.int(streamIndex))
	codec := C.avcodec_find_decoder(cp.codec_id)
	if codec == nil {
		return nil, fmt.Errorf("no decoder found for codec_id %d", cp.codec_id)
	}

	ctx := C.avcodec_alloc_context3(codec)
	if ctx == nil {
		return nil, &Err{Code: -12, Message: "avcodec_alloc_context3: out of memory"}
	}

	if ret := C.avcodec_parameters_to_context(ctx, cp); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_parameters_to_context: %w", newErr(ret))
	}

	ctx.pkt_timebase = C.hw_get_stream_time_base(input.raw(), C.int(streamIndex))

	// Check if codec supports this hardware device type.
	hwSupported := C.codec_supports_hw_type(codec, C.enum_AVHWDeviceType(device.Type())) != 0

	if hwSupported {
		// Set hw device context and get_format callback.
		hwFmt := C.hw_dec_pix_fmt_for_type(C.enum_AVHWDeviceType(device.Type()))
		C.set_hw_get_format(ctx, hwFmt)
		C.set_hw_device_ctx(ctx, device.raw())
	}

	if ret := C.avcodec_open2(ctx, codec, nil); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2 (hw): %w", newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(hw_decoder)")
	return &HWDecoderContext{
		p:            ctx,
		streamIndex:  streamIndex,
		deviceType:   device.Type(),
		autoTransfer: opts.AutoTransfer,
	}, nil
}

// Close frees the hw decoder context.
func (d *HWDecoderContext) Close() error {
	if d.p != nil {
		leakUntrack(unsafe.Pointer(d.p))
		C.avcodec_free_context(&d.p)
		d.p = nil
	}
	return nil
}

// StreamIndex returns the input stream index.
func (d *HWDecoderContext) StreamIndex() int { return d.streamIndex }

// SendPacket submits a packet for decoding. Pass nil to flush.
func (d *HWDecoderContext) SendPacket(pkt *Packet) error {
	var raw *C.AVPacket
	if pkt != nil {
		raw = pkt.raw()
	}
	ret := C.avcodec_send_packet(d.p, raw)
	return newErr(ret)
}

// ReceiveFrame receives a decoded frame. If AutoTransfer is enabled and the
// frame is in hardware format, it is automatically transferred to software memory.
// Returns ErrEAgain if more input is needed, ErrEOF when flushing is complete.
func (d *HWDecoderContext) ReceiveFrame(f *Frame) error {
	ret := C.avcodec_receive_frame(d.p, f.raw())
	if err := newErr(ret); err != nil {
		return err
	}

	// Auto-transfer hw→sw if needed.
	if d.autoTransfer && C.hw_dec_is_hw_pix_fmt(C.hw_dec_frame_format(f.raw())) != 0 {
		swFrame, err := AllocFrame()
		if err != nil {
			return fmt.Errorf("alloc sw frame for transfer: %w", err)
		}
		if err := TransferToSW(swFrame, f); err != nil {
			swFrame.Close()
			return fmt.Errorf("auto hw→sw transfer: %w", err)
		}
		// Copy metadata.
		swFrame.raw().pts = f.raw().pts
		// Swap: put sw data into f, then free the temp frame.
		C.av_frame_unref(f.raw())
		C.av_frame_move_ref(f.raw(), swFrame.raw())
		swFrame.Close()
	}

	return nil
}

// Flush sends a nil packet to drain buffered frames.
func (d *HWDecoderContext) Flush() error {
	return d.SendPacket(nil)
}

// SupportsHWDecode checks if a given codec ID supports hardware decoding with
// the specified device type.
func SupportsHWDecode(codecID uint32, deviceType HWDeviceType) bool {
	codec := C.avcodec_find_decoder(C.enum_AVCodecID(codecID))
	if codec == nil {
		return false
	}
	return C.codec_supports_hw_type(codec, C.enum_AVHWDeviceType(deviceType)) != 0
}
