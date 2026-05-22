// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/opt.h"
// #include "libavutil/channel_layout.h"
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
//     // Normalize an unspecified audio channel layout to the default named
//     // layout for the channel count (e.g. 2ch → AV_CHANNEL_LAYOUT_STEREO).
//     // Some muxers (e.g. MP4/MOV) reject AV_CHANNEL_ORDER_UNSPEC and log
//     // "unsupported channel layout N channels". This mirrors what FFmpeg
//     // does in its audio encoding path when no explicit layout is given.
//     AVCodecParameters *dst_cp = out_ctx->streams[out_idx]->codecpar;
//     if (dst_cp->codec_type == AVMEDIA_TYPE_AUDIO &&
//         dst_cp->ch_layout.order == AV_CHANNEL_ORDER_UNSPEC &&
//         dst_cp->ch_layout.nb_channels > 0) {
//         av_channel_layout_default(&dst_cp->ch_layout, dst_cp->ch_layout.nb_channels);
//     }
//     return 0;
// }
// static AVRational out_stream_time_base(AVFormatContext *ctx, int idx) {
//     if (idx < 0 || idx >= (int)ctx->nb_streams) { AVRational z = {0,0}; return z; }
//     return ctx->streams[idx]->time_base;
// }
// static int set_stream_codec_tag(AVFormatContext *ctx, int idx, uint32_t tag) {
//     if (idx < 0 || idx >= (int)ctx->nb_streams) return -1;
//     ctx->streams[idx]->codecpar->codec_tag = tag;
//     return 0;
// }
// // bytes_written returns the current size of the muxed file by
// // querying avio_tell on the format context's IO. Mirrors how
// // fftools/ffmpeg_mux.c implements -fs (limit_filesize): it calls
// // avio_tell(s->pb) before every WritePacket and stops with EOF
// // when the result reaches the configured limit.
// static int64_t bytes_written(AVFormatContext *ctx) {
//     if (!ctx || !ctx->pb) return -1;
//     return avio_tell(ctx->pb);
// }
import "C"

import (
	"fmt"
	"os"
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
	return OpenOutputWithFormat(path, "")
}

// OpenOutputWithFormat creates an output container at path using the given
// format name. When format is empty the format is inferred from the file
// extension (same behaviour as OpenOutput).
func OpenOutputWithFormat(path, format string) (*OutputFormatContext, error) {
	tmpPath := path + ".tmp"
	cTmpPath := C.CString(tmpPath)
	defer C.free(unsafe.Pointer(cTmpPath))

	// Prefer an explicitly supplied format; fall back to the file extension.
	// When an explicit format is supplied, use it as the format_name argument.
	// Otherwise pass nil so FFmpeg auto-detects from the real filename (not the
	// .tmp suffix, which would confuse it). avio_open still uses cTmpPath.
	var cFmt *C.char
	if format != "" {
		cFmt = C.CString(format)
		defer C.free(unsafe.Pointer(cFmt))
	}

	cRealPath := C.CString(path)
	defer C.free(unsafe.Pointer(cRealPath))

	var ctx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&ctx, nil, cFmt, cRealPath)
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
		return &OutputFormatContext{
			p:       ctx,
			tmpPath: tmpPath,
			outPath: path,
		}, nil
	}

	// AVFMT_NOFILE muxers (HLS, DASH, segment, image2 in some modes,
	// tee) manage their own files: they write the playlist /
	// manifest / per-segment files directly to the path stored in
	// AVFormatContext.url, and the more recently authored ones
	// (hlsenc, dashenc) implement their own atomic .tmp →
	// final-name rename internally controlled by their `temp_file`
	// AVOption. Leave tmpPath/outPath empty so our wrapper's
	// Close() does not race the muxer with a second rename.
	return &OutputFormatContext{
		p: ctx,
	}, nil
}

// OpenTeeOutput opens libavformat's built-in tee muxer with the given
// `slavesURL` (the FFmpeg `[opt=val:opt=val]url|[opt=val]url` slaves
// grammar parsed by libavformat/tee.c::tee_write_header). Each slave's
// output file is created and managed by libavformat itself; the parent
// tee context is `AVFMT_NOFILE` (no avio_open, no `.tmp` shadow file,
// no atomic rename on close). Use this for `Output.Kind == "tee"`.
func OpenTeeOutput(slavesURL string) (*OutputFormatContext, error) {
	cSlaves := C.CString(slavesURL)
	defer C.free(unsafe.Pointer(cSlaves))

	cFmt := C.CString("tee")
	defer C.free(unsafe.Pointer(cFmt))

	var ctx *C.AVFormatContext
	ret := C.avformat_alloc_output_context2(&ctx, nil, cFmt, cSlaves)
	if ret < 0 {
		return nil, fmt.Errorf("avformat_alloc_output_context2(tee, %q): %w", slavesURL, newErr(ret))
	}
	// The tee muxer is AVFMT_NOFILE — its slaves manage their own IO.
	// Leave tmpPath/outPath empty so Close() does not attempt a rename.
	return &OutputFormatContext{p: ctx}, nil
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

// SetStreamCodecTag sets the codecpar.codec_tag (FourCC) on output stream
// idx. tag must be a 4-byte ASCII string (e.g. "hvc1", "hev1", "avc1").
// Must be called after AddStream / AddStreamFromInput and before
// WriteHeader. Equivalent to ffmpeg's -tag:v / -tag:a CLI option.
func (f *OutputFormatContext) SetStreamCodecTag(idx int, tag string) error {
	if len(tag) != 4 {
		return fmt.Errorf("SetStreamCodecTag: tag %q must be exactly 4 ASCII characters", tag)
	}
	for _, b := range []byte(tag) {
		if b > 0x7f {
			return fmt.Errorf("SetStreamCodecTag: tag %q must be ASCII", tag)
		}
	}
	b := []byte(tag)
	v := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	if ret := C.set_stream_codec_tag(f.p, C.int(idx), C.uint32_t(v)); ret < 0 {
		return fmt.Errorf("set_stream_codec_tag: invalid stream index %d", idx)
	}
	return nil
}

// WriteHeader writes the container header. Must be called after all streams
// have been added and before any packets are written.
func (f *OutputFormatContext) WriteHeader() error {
	return f.WriteHeaderWithOptions(nil)
}

// WriteHeaderWithOptions writes the container header, forwarding the
// given key/value pairs to libavformat as an AVDictionary. This is the
// hook for muxer-specific AVOptions (e.g. HLS `hls_time`,
// `hls_playlist_type`, `hls_segment_filename`; DASH `seg_duration`,
// `init_seg_name`, `adaptation_sets`). Mirrors FFmpeg's
// `fftools/ffmpeg_mux_init.c::mux_open` which builds an `AVDictionary
// **opts` from `-muxer_opts` and per-output options before calling
// `avformat_write_header`.
//
// Unconsumed entries in opts after WriteHeader returns indicate the
// muxer did not recognise an option; callers may inspect the returned
// `unconsumed` map to surface a warning. A non-nil error means the
// header write itself failed.
func (f *OutputFormatContext) WriteHeaderWithOptions(opts map[string]string) error {
	var dict *C.AVDictionary
	if len(opts) > 0 {
		if err := setDictFromMap(&dict, opts); err != nil {
			if dict != nil {
				C.av_dict_free(&dict)
			}
			return fmt.Errorf("WriteHeaderWithOptions: build dict: %w", err)
		}
	}
	ret := C.avformat_write_header(f.p, &dict)
	if dict != nil {
		C.av_dict_free(&dict)
	}
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

// BytesWritten returns the current size in bytes of the muxed
// container as reported by libavformat's IO context (avio_tell).
// Returns -1 when the context has no attached IO (e.g. AVFMT_NOFILE
// muxers). Used by the runtime to enforce `-fs` (Output.MaxFileSize)
// the same way fftools/ffmpeg_mux.c does: query before each
// WritePacket and stop with EOF once the limit is reached.
func (f *OutputFormatContext) BytesWritten() int64 {
	if f == nil || f.p == nil {
		return -1
	}
	return int64(C.bytes_written(f.p))
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
