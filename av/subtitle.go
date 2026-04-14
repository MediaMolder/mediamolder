// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavutil/mem.h"
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
	if streamIndex < 0 || streamIndex >= input.NumStreams() {
		return nil, fmt.Errorf("stream index %d out of range", streamIndex)
	}

	cp := C.sub_get_codecpar(input.raw(), C.int(streamIndex))
	if cp.codec_type != C.AVMEDIA_TYPE_SUBTITLE {
		return nil, fmt.Errorf("stream %d is not subtitle (type=%d)", streamIndex, cp.codec_type)
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

	if ret := C.avcodec_open2(ctx, codec, nil); ret < 0 {
		C.avcodec_free_context(&ctx)
		return nil, fmt.Errorf("avcodec_open2 (subtitle): %w", newErr(ret))
	}

	leakTrack(unsafe.Pointer(ctx), "AVCodecContext(subtitle_decoder)")
	return &SubtitleDecoderContext{p: ctx, streamIndex: streamIndex}, nil
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
