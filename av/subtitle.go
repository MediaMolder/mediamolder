// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavutil/mem.h"
// #include <stdlib.h>
//
// static AVCodecParameters *sub_get_codecpar(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->codecpar;
// }
// static AVRational sub_get_stream_time_base(AVFormatContext *ctx, int stream_index) {
//     return ctx->streams[stream_index]->time_base;
// }
//
// // Decode a subtitle from a packet.
// static int decode_subtitle(AVCodecContext *ctx, AVSubtitle *sub,
//                             int *got_sub, AVPacket *pkt) {
//     return avcodec_decode_subtitle2(ctx, sub, got_sub, pkt);
// }
//
// // Encode a subtitle to a packet buffer.
// static int encode_subtitle(AVCodecContext *ctx, uint8_t *buf, int buf_size,
//                             const AVSubtitle *sub) {
//     return avcodec_encode_subtitle(ctx, buf, buf_size, sub);
// }
//
// // Get subtitle rect count.
// static unsigned sub_num_rects(const AVSubtitle *sub) {
//     return sub->num_rects;
// }
//
// // Get subtitle rect type.
// static int sub_rect_type(const AVSubtitle *sub, unsigned idx) {
//     if (idx >= sub->num_rects) return -1;
//     return sub->rects[idx]->type;
// }
//
// // Get subtitle rect ASS text.
// static const char* sub_rect_ass(const AVSubtitle *sub, unsigned idx) {
//     if (idx >= sub->num_rects || !sub->rects[idx]->ass) return "";
//     return sub->rects[idx]->ass;
// }
//
// // Get subtitle rect text.
// static const char* sub_rect_text(const AVSubtitle *sub, unsigned idx) {
//     if (idx >= sub->num_rects || !sub->rects[idx]->text) return "";
//     return sub->rects[idx]->text;
// }
//
// // Get subtitle timing info.
// static int64_t sub_pts(const AVSubtitle *sub) { return sub->pts; }
// static uint32_t sub_start_display(const AVSubtitle *sub) { return sub->start_display_time; }
// static uint32_t sub_end_display(const AVSubtitle *sub) { return sub->end_display_time; }
//
// // Free subtitle data.
// static void sub_free(AVSubtitle *sub) { avsubtitle_free(sub); }
//
// // Get subtitle format (0=graphics, 1=text).
// static int sub_codec_format(AVCodecContext *ctx) {
//     return ctx->codec->type == AVMEDIA_TYPE_SUBTITLE ?
//         (ctx->codec_descriptor ? ctx->codec_descriptor->props : 0) : 0;
// }
//
// // Probe the codec descriptor properties for a subtitle stream so the
// // caller can distinguish text-subtitle codecs (srt, ass, mov_text, ...)
// // from bitmap-subtitle codecs (dvbsub, dvdsub, hdmv_pgs_subtitle, ...).
// // Returns AV_CODEC_PROP_TEXT_SUB / AV_CODEC_PROP_BITMAP_SUB OR'd in;
// // 0 if no descriptor found or the stream is not a subtitle stream.
// static unsigned sub_stream_props(AVFormatContext *ctx, int stream_index) {
//     AVCodecParameters *cp = ctx->streams[stream_index]->codecpar;
//     if (cp->codec_type != AVMEDIA_TYPE_SUBTITLE) return 0;
//     const AVCodecDescriptor *d = avcodec_descriptor_get(cp->codec_id);
//     return d ? d->props : 0;
// }
//
// // Same as above but probes by AVCodecID directly (used to validate the
// // output encoder before it's been constructed).
// static unsigned sub_codec_id_props(int codec_id) {
//     const AVCodecDescriptor *d = avcodec_descriptor_get(codec_id);
//     return d ? d->props : 0;
// }
//
// // Look up an encoder by name and return its codec_id (0 = not found).
// static int sub_encoder_id_by_name(const char *name) {
//     const AVCodec *c = avcodec_find_encoder_by_name(name);
//     return c ? c->id : 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// SubtitleType classifies subtitle rect types.
type SubtitleType int

const (
	SubtitleTypeNone   SubtitleType = SubtitleType(C.SUBTITLE_NONE)
	SubtitleTypeBitmap SubtitleType = SubtitleType(C.SUBTITLE_BITMAP)
	SubtitleTypeText   SubtitleType = SubtitleType(C.SUBTITLE_TEXT)
	SubtitleTypeASS    SubtitleType = SubtitleType(C.SUBTITLE_ASS)
)

func (t SubtitleType) String() string {
	switch t {
	case SubtitleTypeBitmap:
		return "bitmap"
	case SubtitleTypeText:
		return "text"
	case SubtitleTypeASS:
		return "ass"
	default:
		return "none"
	}
}

// Subtitle wraps an AVSubtitle. Must be freed with Close().
type Subtitle struct {
	s C.AVSubtitle
}

// PTS returns the subtitle presentation timestamp.
func (s *Subtitle) PTS() int64 { return int64(C.sub_pts(&s.s)) }

// StartDisplayTime returns the display start time in milliseconds (relative to PTS).
func (s *Subtitle) StartDisplayTime() uint32 { return uint32(C.sub_start_display(&s.s)) }

// EndDisplayTime returns the display end time in milliseconds (relative to PTS).
func (s *Subtitle) EndDisplayTime() uint32 { return uint32(C.sub_end_display(&s.s)) }

// NumRects returns the number of subtitle rectangles.
func (s *Subtitle) NumRects() int { return int(C.sub_num_rects(&s.s)) }

// RectType returns the type of subtitle rectangle at index i.
func (s *Subtitle) RectType(i int) SubtitleType {
	return SubtitleType(C.sub_rect_type(&s.s, C.uint(i)))
}

// RectASS returns the ASS/SSA markup for rectangle at index i.
func (s *Subtitle) RectASS(i int) string {
	return C.GoString(C.sub_rect_ass(&s.s, C.uint(i)))
}

// RectText returns the plain text for rectangle at index i.
func (s *Subtitle) RectText(i int) string {
	return C.GoString(C.sub_rect_text(&s.s, C.uint(i)))
}

// Close frees the subtitle data.
func (s *Subtitle) Close() {
	C.sub_free(&s.s)
}

// raw returns a pointer to the underlying AVSubtitle.
func (s *Subtitle) raw() *C.AVSubtitle { return &s.s }

// SubtitleDecoderContext wraps an AVCodecContext for subtitle decoding.
type SubtitleDecoderContext struct {
	p           *C.AVCodecContext
	streamIndex int
}

// OpenSubtitleDecoder creates a subtitle decoder for the given stream index.
func OpenSubtitleDecoder(input *InputFormatContext, streamIndex int) (*SubtitleDecoderContext, error) {
	return OpenSubtitleDecoderWithOptions(input, streamIndex, SubtitleDecoderOptions{})
}

// SubtitleDecoderOptions configures optional subtitle-decoder parameters.
type SubtitleDecoderOptions struct {
	// Charenc is the source character encoding for text-subtitle decoders
	// (mirrors AVCodecContext.sub_charenc / FFmpeg `-sub_charenc`). When
	// non-empty, libavcodec routes packets through iconv from Charenc to
	// UTF-8 before handing them to the decoder (see libavcodec/decode.c
	// L2014). Rejected here when applied to a bitmap-subtitle stream
	// because the conversion is meaningless on graphics frames (mirrors
	// the runtime branch at libavcodec/decode.c L2023 that forces
	// sub_charenc_mode=DO_NOTHING for non-text codecs).
	Charenc string
}

// OpenSubtitleDecoderWithOptions creates a subtitle decoder with a typed
// options struct. The empty-options form is equivalent to OpenSubtitleDecoder.
func OpenSubtitleDecoderWithOptions(input *InputFormatContext, streamIndex int, opts SubtitleDecoderOptions) (*SubtitleDecoderContext, error) {
	if streamIndex < 0 || streamIndex >= input.NumStreams() {
		return nil, fmt.Errorf("stream index %d out of range", streamIndex)
	}

	cp := C.sub_get_codecpar(input.raw(), C.int(streamIndex))
	if cp.codec_type != C.AVMEDIA_TYPE_SUBTITLE {
		return nil, fmt.Errorf("stream %d is not subtitle (type=%d)", streamIndex, cp.codec_type)
	}

	if opts.Charenc != "" {
		props := uint32(C.sub_codec_id_props(C.int(cp.codec_id)))
		if props&C.AV_CODEC_PROP_TEXT_SUB == 0 {
			return nil, fmt.Errorf("stream %d: sub_charenc=%q only valid for text-subtitle codecs (codec_id=%d is bitmap or unknown)",
				streamIndex, opts.Charenc, cp.codec_id)
		}
	}

	codec := C.avcodec_find_decoder(cp.codec_id)
	if codec == nil {
		return nil, fmt.Errorf("no subtitle decoder for codec_id %d", cp.codec_id)
	}

	ctx := C.avcodec_alloc_context3(codec)
	if ctx == nil {
		return nil, &Err{Code: -12, Message: "avcodec_alloc_context3: out of memory"}
	}

	if ret := C.avcodec_parameters_to_context(ctx, cp); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_parameters_to_context: %w", newErr(ret))
	}

	ctx.pkt_timebase = C.sub_get_stream_time_base(input.raw(), C.int(streamIndex))

	if opts.Charenc != "" {
		// libavcodec frees sub_charenc via av_free; allocate with av_strdup
		// so the AVCodecContext owns the buffer.
		cstr := C.CString(opts.Charenc)
		ctx.sub_charenc = C.av_strdup(cstr)
		C.free(unsafe.Pointer(cstr))
	}

	if ret := C.avcodec_open2(ctx, codec, nil); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2 (subtitle): %w", newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(subtitle_decoder)")
	return &SubtitleDecoderContext{p: ctx, streamIndex: streamIndex}, nil
}

// SubtitleStreamProps returns the codec descriptor properties bitmask for
// the subtitle stream at streamIndex. Use IsBitmapSubtitleProps /
// IsTextSubtitleProps to decode. Returns 0 if streamIndex is out of range
// or the stream is not a subtitle stream.
func SubtitleStreamProps(input *InputFormatContext, streamIndex int) uint32 {
	if streamIndex < 0 || streamIndex >= input.NumStreams() {
		return 0
	}
	return uint32(C.sub_stream_props(input.raw(), C.int(streamIndex)))
}

// IsTextSubtitleProps reports whether the codec descriptor properties bitmask
// indicates a text-subtitle codec (AV_CODEC_PROP_TEXT_SUB).
func IsTextSubtitleProps(props uint32) bool { return props&uint32(C.AV_CODEC_PROP_TEXT_SUB) != 0 }

// IsBitmapSubtitleProps reports whether the codec descriptor properties
// bitmask indicates a bitmap-subtitle codec (AV_CODEC_PROP_BITMAP_SUB).
func IsBitmapSubtitleProps(props uint32) bool {
	return props&uint32(C.AV_CODEC_PROP_BITMAP_SUB) != 0
}

// SubtitleEncoderProps looks up the encoder by name and returns its codec
// descriptor properties bitmask. Returns 0 when the encoder is unknown.
func SubtitleEncoderProps(name string) uint32 {
	if name == "" {
		return 0
	}
	cname := C.CString(name)
	defer C.free(unsafe.Pointer(cname))
	id := C.sub_encoder_id_by_name(cname)
	if id == 0 {
		return 0
	}
	return uint32(C.sub_codec_id_props(id))
}

// Close frees the subtitle decoder context.
func (d *SubtitleDecoderContext) Close() error {
	if d.p != nil {
		leakUntrack(unsafe.Pointer(d.p))
		C.avcodec_free_context(&d.p)
		d.p = nil
	}
	return nil
}

// StreamIndex returns the stream index this decoder was opened for.
func (d *SubtitleDecoderContext) StreamIndex() int { return d.streamIndex }

// Decode decodes a subtitle from a packet. Returns the decoded subtitle and
// whether a subtitle was produced. The caller must call sub.Close() when done.
func (d *SubtitleDecoderContext) Decode(pkt *Packet) (*Subtitle, bool, error) {
	var sub Subtitle
	var gotSub C.int
	ret := C.decode_subtitle(d.p, &sub.s, &gotSub, pkt.raw())
	if ret < 0 {
		return nil, false, fmt.Errorf("decode subtitle: %w", newErr(ret))
	}
	if gotSub == 0 {
		return nil, false, nil
	}
	return &sub, true, nil
}

// SubtitleEncoderContext wraps an AVCodecContext for subtitle encoding.
type SubtitleEncoderContext struct {
	p *C.AVCodecContext
}

// SubtitleEncoderOptions configures a subtitle encoder.
type SubtitleEncoderOptions struct {
	// CodecName is the subtitle encoder name (e.g. "srt", "ass", "dvdsub").
	CodecName string
}

// OpenSubtitleEncoder creates a subtitle encoder.
func OpenSubtitleEncoder(opts SubtitleEncoderOptions) (*SubtitleEncoderContext, error) {
	cName := C.CString(opts.CodecName)
	defer C.free(unsafe.Pointer(cName))

	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil, fmt.Errorf("subtitle encoder %q not found", opts.CodecName)
	}

	ctx := C.avcodec_alloc_context3(codec)
	if ctx == nil {
		return nil, &Err{Code: -12, Message: "avcodec_alloc_context3: out of memory"}
	}

	if ret := C.avcodec_open2(ctx, codec, nil); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2(%s): %w", opts.CodecName, newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(subtitle_encoder:"+opts.CodecName+")")
	return &SubtitleEncoderContext{p: ctx}, nil
}

// Close frees the subtitle encoder context.
func (e *SubtitleEncoderContext) Close() error {
	if e.p != nil {
		leakUntrack(unsafe.Pointer(e.p))
		C.avcodec_free_context(&e.p)
		e.p = nil
	}
	return nil
}

// Encode encodes a subtitle into a byte buffer. Returns the encoded data.
func (e *SubtitleEncoderContext) Encode(sub *Subtitle) ([]byte, error) {
	const maxBufSize = 1024 * 1024 // 1MB max subtitle
	buf := C.av_malloc(C.size_t(maxBufSize))
	if buf == nil {
		return nil, &Err{Code: -12, Message: "av_malloc: out of memory"}
	}
	defer C.av_free(buf)

	ret := C.encode_subtitle(e.p, (*C.uint8_t)(buf), C.int(maxBufSize), sub.raw())
	if ret < 0 {
		return nil, fmt.Errorf("encode subtitle: %w", newErr(ret))
	}
	return C.GoBytes(buf, ret), nil
}

// SubtitleBurnInFilter returns a filter spec string to burn subtitles into video.
// path is the subtitle file path, charenc is the character encoding (e.g. "UTF-8", "" for default).
// The returned string can be used as a video filter spec.
func SubtitleBurnInFilter(path string, charenc string) string {
	// Use the 'subtitles' filter for text-based subs (SRT/ASS).
	spec := fmt.Sprintf("subtitles=filename='%s'", escapeFiltPath(path))
	if charenc != "" {
		spec += fmt.Sprintf(":charenc=%s", charenc)
	}
	return spec
}

// ASSBurnInFilter returns a filter spec string to burn ASS/SSA subtitles into video.
func ASSBurnInFilter(path string) string {
	return fmt.Sprintf("ass=filename='%s'", escapeFiltPath(path))
}

// escapeFiltPath escapes special characters in a file path for use in filter strings.
func escapeFiltPath(path string) string {
	// Escape characters that are special in FFmpeg filter strings.
	replacer := []struct{ old, new string }{
		{"\\", "\\\\"},
		{"'", "'\\''"},
		{":", "\\:"},
		{";", "\\;"},
		{"[", "\\["},
		{"]", "\\]"},
	}
	result := path
	for _, r := range replacer {
		result = replaceAll(result, r.old, r.new)
	}
	return result
}

// replaceAll replaces all occurrences of old with new in s.
func replaceAll(s, old, new string) string {
	if old == "" {
		return s
	}
	var result []byte
	for i := 0; i < len(s); {
		if i+len(old) <= len(s) && s[i:i+len(old)] == old {
			result = append(result, new...)
			i += len(old)
		} else {
			result = append(result, s[i])
			i++
		}
	}
	return string(result)
}
