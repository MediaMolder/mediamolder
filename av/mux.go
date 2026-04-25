// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/opt.h"
//
// static AVStream* new_stream(AVFormatContext *ctx) {
//     return avformat_new_stream(ctx, NULL);
// }
// static void set_stream_codecpar(AVFormatContext *ctx, int idx,
//                                  AVCodecContext *enc_ctx) {
//     avcodec_parameters_from_context(ctx->streams[idx]->codecpar, enc_ctx);
//     ctx->streams[idx]->time_base = enc_ctx->time_base;
// }
// // Copy codec parameters from an input stream to an output stream and
// // adopt the input stream's time_base. Used for stream-copy outputs.
// static int copy_stream_codecpar(AVFormatContext *out_ctx, int out_idx,
//                                  AVFormatContext *in_ctx, int in_idx) {
//     int ret = avcodec_parameters_copy(out_ctx->streams[out_idx]->codecpar,
//                                       in_ctx->streams[in_idx]->codecpar);
//     if (ret < 0) return ret;
//     // Clear codec_tag so the muxer can pick a container-appropriate one.
//     out_ctx->streams[out_idx]->codecpar->codec_tag = 0;
//     out_ctx->streams[out_idx]->time_base = in_ctx->streams[in_idx]->time_base;
//     return 0;
// }
// static AVRational out_stream_time_base(AVFormatContext *ctx, int idx) {
//     return ctx->streams[idx]->time_base;
// }
import "C"

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unsafe"
)

// OutputFormatContext wraps an AVFormatContext used for muxing.
// It must be closed via Close().
type OutputFormatContext struct {
	p       *C.AVFormatContext
	tmpPath string
	outPath string
}

// OpenOutput creates an output container at path, inferring the format from
// the file extension. The actual write happens to a .tmp file; Close() performs
// an atomic rename to the final path.
func OpenOutput(path string) (*OutputFormatContext, error) {
	tmpPath := path + ".tmp"
	cTmpPath := C.CString(tmpPath)
	defer C.free(unsafe.Pointer(cTmpPath))

	// Determine format from the real extension, not the .tmp extension.
	ext := strings.TrimPrefix(filepath.Ext(path), ".")
	var cFmt *C.char
	if ext != "" {
		cFmt = C.CString(ext)
		defer C.free(unsafe.Pointer(cFmt))
	}

	var ctx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&ctx, nil, cFmt, cTmpPath)
	if ret < 0 {
		return nil, fmt.Errorf("avformat_alloc_output_context2(%q): %w", path, newErr(ret))
	}

	// Open the output file for writing.
	if ctx.oformat.flags&C.AVFMT_NOFILE == 0 {
		ret = C.avio_open(&ctx.pb, cTmpPath, C.AVIO_FLAG_WRITE)
		if ret < 0 {
			C.avformat_free_context(ctx)
			return nil, fmt.Errorf("avio_open(%q): %w", tmpPath, newErr(ret))
		}
	}

	return &OutputFormatContext{
		p:       ctx,
		tmpPath: tmpPath,
		outPath: path,
	}, nil
}

// AddStream adds a new output stream using the given encoder context's codec/format.
// Returns the zero-based stream index assigned.
func (f *OutputFormatContext) AddStream(enc *EncoderContext) (int, error) {
	st := C.new_stream(f.p)
	if st == nil {
		return -1, fmt.Errorf("avformat_new_stream: out of memory")
	}
	C.set_stream_codecpar(f.p, C.int(st.index), enc.raw())
	return int(st.index), nil
}

// AddStreamFromInput adds a new output stream by copying codec parameters
// directly from inputStreamIndex on src (no re-encoding). Adopts the input
// stream's time_base. Used to wire stream-copy nodes to the muxer.
// Returns the zero-based stream index assigned in the output container.
func (f *OutputFormatContext) AddStreamFromInput(src *InputFormatContext, inputStreamIndex int) (int, error) {
	if src == nil || src.p == nil {
		return -1, fmt.Errorf("AddStreamFromInput: nil source")
	}
	st := C.new_stream(f.p)
	if st == nil {
		return -1, fmt.Errorf("avformat_new_stream: out of memory")
	}
	if ret := C.copy_stream_codecpar(f.p, C.int(st.index), src.p, C.int(inputStreamIndex)); ret < 0 {
		return -1, fmt.Errorf("avcodec_parameters_copy: %w", newErr(ret))
	}
	return int(st.index), nil
}

// StreamTimeBase returns the time_base of output stream idx as {num, den}.
// Valid after AddStream / AddStreamFromInput; some muxers adjust it during
// WriteHeader, so callers wanting the post-header value should re-query.
func (f *OutputFormatContext) StreamTimeBase(idx int) [2]int {
	tb := C.out_stream_time_base(f.p, C.int(idx))
	return [2]int{int(tb.num), int(tb.den)}
}

// WriteHeader writes the container header. Must be called after all streams
// have been added and before any packets are written.
func (f *OutputFormatContext) WriteHeader() error {
	ret := C.avformat_write_header(f.p, nil)
	return newErr(ret)
}

// WritePacket muxes a packet into the container. The packet's stream_index
// must match a stream added with AddStream.
func (f *OutputFormatContext) WritePacket(pkt *Packet) error {
	ret := C.av_interleaved_write_frame(f.p, pkt.raw())
	return newErr(ret)
}

// WriteTrailer flushes any buffered packets and writes the container trailer.
// Must be called before Close().
func (f *OutputFormatContext) WriteTrailer() error {
	ret := C.av_write_trailer(f.p)
	return newErr(ret)
}

// Close flushes, closes the IO context, frees the AVFormatContext, and
// atomically renames the .tmp file to the final output path.
func (f *OutputFormatContext) Close() error {
	if f.p == nil {
		return nil
	}
	if f.p.oformat.flags&C.AVFMT_NOFILE == 0 && f.p.pb != nil {
		C.avio_closep(&f.p.pb)
	}
	C.avformat_free_context(f.p)
	f.p = nil

	if f.tmpPath == "" {
		return nil
	}
	// Atomic rename: .tmp -> final path.
	if err := os.Rename(f.tmpPath, f.outPath); err != nil {
		return fmt.Errorf("rename %q -> %q: %w", f.tmpPath, f.outPath, err)
	}
	f.tmpPath = ""
	return nil
}

// Abort removes the .tmp file without renaming, discarding any partial output.
func (f *OutputFormatContext) Abort() {
	if f.p != nil {
		if f.p.oformat.flags&C.AVFMT_NOFILE == 0 && f.p.pb != nil {
			C.avio_closep(&f.p.pb)
		}
		C.avformat_free_context(f.p)
		f.p = nil
	}
	if f.tmpPath != "" {
		os.Remove(f.tmpPath)
		f.tmpPath = ""
	}
}
