// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
//
// static AVCodecParameters *get_codecpar(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->codecpar;
// }
// static AVRational get_stream_time_base(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->time_base;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// DecoderContext wraps an AVCodecContext configured for decoding.
// It must be closed via Close().
type DecoderContext struct {
	p           *C.AVCodecContext
	streamIndex int
}

// DecoderOptions configures optional decoder parameters.
type DecoderOptions struct {
	// ThreadCount sets the number of codec threads. 0 = FFmpeg auto-detect.
	ThreadCount int

	// ThreadType selects the threading model: "frame", "slice", "frame+slice",
	// or "" (let FFmpeg choose based on codec capabilities).
	ThreadType string
}

// OpenDecoder creates a decoder for the given stream index in the input format context.
// Uses FFmpeg default threading (auto-detect).
func OpenDecoder(input *InputFormatContext, streamIndex int) (*DecoderContext, error) {
	return OpenDecoderWithOptions(input, streamIndex, DecoderOptions{})
}

// OpenDecoderWithOptions creates a decoder with explicit threading configuration.
func OpenDecoderWithOptions(input *InputFormatContext, streamIndex int, opts DecoderOptions) (*DecoderContext, error) {
	if streamIndex < 0 || streamIndex >= input.NumStreams() {
		return nil, fmt.Errorf("stream index %d out of range", streamIndex)
	}

	cp := C.get_codecpar(input.raw(), C.int(streamIndex))
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

	// Set pkt_timebase so PTS/DTS values are correct.
	ctx.pkt_timebase = C.get_stream_time_base(input.raw(), C.int(streamIndex))

	// Apply threading configuration.
	if opts.ThreadCount > 0 {
		ctx.thread_count = C.int(opts.ThreadCount)
	}
	if opts.ThreadType != "" {
		ctx.thread_type = C.int(parseThreadType(opts.ThreadType))
	}

	if ret := C.avcodec_open2(ctx, codec, nil); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2: %w", newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(decoder)")
	return &DecoderContext{p: ctx, streamIndex: streamIndex}, nil
}

// Close frees the decoder context.
func (d *DecoderContext) Close() error {
	if d.p != nil {
		leakUntrack(unsafe.Pointer(d.p))
		C.avcodec_free_context(&d.p)
		d.p = nil
	}
	return nil
}

// StreamIndex returns the input stream index this decoder was opened for.
func (d *DecoderContext) StreamIndex() int { return d.streamIndex }

// SendPacket submits a packet for decoding. Pass nil to flush the decoder.
func (d *DecoderContext) SendPacket(pkt *Packet) error {
	var raw *C.AVPacket
	if pkt != nil {
		raw = pkt.raw()
	}
	ret := C.avcodec_send_packet(d.p, raw)
	return newErr(ret)
}

// ReceiveFrame receives a decoded frame. Returns ErrEAgain if more input is needed,
// ErrEOF when flushing is complete.
func (d *DecoderContext) ReceiveFrame(f *Frame) error {
	ret := C.avcodec_receive_frame(d.p, f.raw())
	return newErr(ret)
}

// Flush sends a nil packet to drain buffered frames. After Flush, call
// ReceiveFrame until it returns ErrEOF.
func (d *DecoderContext) Flush() error {
	return d.SendPacket(nil)
}

// ThreadCount returns the number of threads the decoder is using.
func (d *DecoderContext) ThreadCount() int {
	return int(d.p.thread_count)
}

// parseThreadType converts a thread type string to FFmpeg's integer constants.
// "frame" → FF_THREAD_FRAME (1), "slice" → FF_THREAD_SLICE (2),
// "frame+slice" → both (3). Returns 0 for unknown/empty (auto).
func parseThreadType(s string) int {
	switch s {
	case "frame":
		return 1 // FF_THREAD_FRAME
	case "slice":
		return 2 // FF_THREAD_SLICE
	case "frame+slice":
		return 3 // FF_THREAD_FRAME | FF_THREAD_SLICE
	default:
		return 0
	}
}
