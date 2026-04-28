// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/opt.h"
// #include "libavutil/pixdesc.h"
//
// // select_supported_sample_fmt ensures ctx->sample_fmt is in the codec's
// // supported list. When it is not, the first supported format is used.
// // AVCodec.sample_fmts is deprecated in FFmpeg 8+ but still functional;
// // suppress the warning since we intentionally use it here.
// #pragma GCC diagnostic push
// #pragma GCC diagnostic ignored "-Wdeprecated-declarations"
// static void select_supported_sample_fmt(AVCodecContext *ctx, const AVCodec *codec) {
//     if (codec->sample_fmts == NULL) return;
//     for (const enum AVSampleFormat *p = codec->sample_fmts; *p != AV_SAMPLE_FMT_NONE; p++) {
//         if (*p == ctx->sample_fmt) return;
//     }
//     ctx->sample_fmt = codec->sample_fmts[0];
// }
// #pragma GCC diagnostic pop
import "C"

import (
	"fmt"
	"unsafe"
)

// EncoderOptions configures an encoder context.
type EncoderOptions struct {
	// CodecName is the encoder name, e.g. "libx264", "aac", "libx265".
	CodecName string

	// --- Video ---
	Width     int
	Height    int
	PixFmt    int // AVPixelFormat; 0 = let encoder choose (yuv420p default for x264)
	BitRate   int64
	FrameRate [2]int // {num, den}
	GOPSize   int

	// TimeBase, if set with TimeBase[1] > 0, is used as the encoder's
	// time_base instead of the default 1/FrameRate. This is required when
	// the encoder is fed by a filter graph whose buffersink advertises a
	// finer time_base than 1/framerate (e.g. demuxer TB 1/12288 propagated
	// through scale): without it, frame PTS produced in the buffersink's
	// units would be reinterpreted in the encoder's coarser units, blowing
	// up the output container duration by orders of magnitude.
	TimeBase [2]int

	// --- Audio ---
	SampleFmt  int // AVSampleFormat
	SampleRate int
	Channels   int

	// GlobalHeader instructs the encoder to place codec extradata in the global
	// header (required for MP4/MOV/MKV containers).
	GlobalHeader bool

	// ThreadCount sets the number of codec threads. 0 = FFmpeg auto-detect.
	ThreadCount int

	// ThreadType selects the threading model: "frame", "slice", "frame+slice",
	// or "" (let FFmpeg choose based on codec capabilities).
	ThreadType string

	// ExtraOpts are passed as AVDictionary options (e.g. {"preset": "medium", "crf": "23"}).
	ExtraOpts map[string]string
}

// EncoderContext wraps an AVCodecContext configured for encoding.
// It must be closed via Close().
type EncoderContext struct {
	p *C.AVCodecContext
}

// OpenEncoder creates and opens an encoder from the given options.
func OpenEncoder(opts EncoderOptions) (*EncoderContext, error) {
	cName := C.CString(opts.CodecName)
	defer C.free(unsafe.Pointer(cName))

	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil, fmt.Errorf("encoder %q not found", opts.CodecName)
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
		// An explicit TimeBase (typically the upstream buffersink's TB) takes
		// precedence over the framerate-derived default so frame PTS values
		// produced by the filter graph are interpreted correctly.
		if opts.TimeBase[1] > 0 {
			ctx.time_base = C.AVRational{
				num: C.int(opts.TimeBase[0]),
				den: C.int(opts.TimeBase[1]),
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
		// Auto-correct sample format: if the encoder doesn't support the
		// requested format (e.g. libopus rejects fltp), fall back to the
		// first format in the codec's supported list.
		C.select_supported_sample_fmt(ctx, codec)
		ctx.time_base = C.AVRational{num: 1, den: C.int(opts.SampleRate)}
		// Channel layout: use the default layout for the given channel count.
		C.av_channel_layout_default(&ctx.ch_layout, C.int(opts.Channels))
	}

	if opts.BitRate > 0 {
		ctx.bit_rate = C.int64_t(opts.BitRate)
	}

	if opts.GlobalHeader {
		ctx.flags |= C.AV_CODEC_FLAG_GLOBAL_HEADER
	}

	// Apply threading configuration.
	if opts.ThreadCount > 0 {
		ctx.thread_count = C.int(opts.ThreadCount)
	}
	if opts.ThreadType != "" {
		ctx.thread_type = C.int(parseThreadType(opts.ThreadType))
	}

	// Build AVDictionary for extra options (e.g. preset, crf).
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

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(encoder:"+opts.CodecName+")")
	return &EncoderContext{p: ctx}, nil
}

// Close frees the encoder context.
func (e *EncoderContext) Close() error {
	if e.p != nil {
		leakUntrack(unsafe.Pointer(e.p))
		C.avcodec_free_context(&e.p)
		e.p = nil
	}
	return nil
}

// SendFrame submits a frame for encoding. Pass nil to flush.
func (e *EncoderContext) SendFrame(f *Frame) error {
	var raw *C.AVFrame
	if f != nil {
		raw = f.raw()
	}
	ret := C.avcodec_send_frame(e.p, raw)
	return newErr(ret)
}

// ReceivePacket receives an encoded packet.
// Returns ErrEAgain if more frames are needed, ErrEOF when flushing is complete.
func (e *EncoderContext) ReceivePacket(pkt *Packet) error {
	ret := C.avcodec_receive_packet(e.p, pkt.raw())
	return newErr(ret)
}

// Flush signals end-of-input so buffered packets can be drained.
func (e *EncoderContext) Flush() error {
	return e.SendFrame(nil)
}

// ThreadCount returns the number of threads the encoder is using.
func (e *EncoderContext) ThreadCount() int {
	return int(e.p.thread_count)
}

// TimeBase returns the encoder context's time_base as {num, den}. Encoded
// packet PTS / DTS values are expressed in this time base; muxers that
// override the output stream's time_base during WriteHeader (notably MP4)
// require packets to be rescaled from this to the post-header stream
// time_base before WritePacket, otherwise PTS values are interpreted in
// the wrong units and the decoded video plays back at the wrong rate.
func (e *EncoderContext) TimeBase() [2]int {
	return [2]int{int(e.p.time_base.num), int(e.p.time_base.den)}
}

// raw returns the underlying C pointer. For use within the av package only.
func (e *EncoderContext) raw() *C.AVCodecContext { return e.p }
