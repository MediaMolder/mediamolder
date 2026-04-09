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

// OpenDecoder creates a decoder for the given stream index in the input format context.
func OpenDecoder(input *InputFormatContext, streamIndex int) (*DecoderContext, error) {
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
