// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/dict.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/samplefmt.h"
// #include "libavutil/parseutils.h"
// #include "libavutil/display.h"
// #include <math.h>
//
// // Helper: get stream codec parameters for stream index i.
// static AVCodecParameters *stream_codecpar(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->codecpar;
// }
// static int64_t stream_duration(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->duration;
// }
// static int64_t stream_start_time(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->start_time;
// }
// static AVRational stream_time_base(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->time_base;
// }
// static AVRational stream_avg_frame_rate(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->avg_frame_rate;
// }
// static AVRational stream_r_frame_rate(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->r_frame_rate;
// }
// static AVRational stream_sample_aspect_ratio(AVFormatContext *ctx, int i) {
//     AVRational sar = ctx->streams[i]->sample_aspect_ratio;
//     if (sar.num == 0) sar = ctx->streams[i]->codecpar->sample_aspect_ratio;
//     return sar;
// }
// // Bit depth from pixel format descriptor (component 0). 0 if unknown.
// static int pix_fmt_bit_depth(int pix_fmt) {
//     const AVPixFmtDescriptor *d = av_pix_fmt_desc_get((enum AVPixelFormat)pix_fmt);
//     if (!d || d->nb_components == 0) return 0;
//     return d->comp[0].depth;
// }
// // Bit depth for an audio sample format (bytes_per_sample * 8). 0 if unknown.
// static int sample_fmt_bit_depth(int sample_fmt) {
//     int b = av_get_bytes_per_sample((enum AVSampleFormat)sample_fmt);
//     return b * 8;
// }
// // Clockwise rotation (degrees) needed to display the stream upright, from its display-matrix side
// // data. Follows FFmpeg's own get_rotation() convention (theta = -av_display_rotation_get), normalized
// // to [0,360). Returns 0 when there is no display matrix.
// static int stream_display_rotation(AVFormatContext *ctx, int i) {
//     const AVCodecParameters *cp = ctx->streams[i]->codecpar;
//     const AVPacketSideData *sd = av_packet_side_data_get(
//         cp->coded_side_data, cp->nb_coded_side_data, AV_PKT_DATA_DISPLAYMATRIX);
//     if (!sd || sd->size < (int)(9 * sizeof(int32_t)))
//         return 0;
//     double theta = -av_display_rotation_get((const int32_t *)sd->data);
//     theta = fmod(theta, 360.0);
//     if (theta < 0) theta += 360.0;
//     return (int)lround(theta) % 360;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// MediaType mirrors AVMediaType values.
type MediaType int

const (
	MediaTypeVideo      MediaType = MediaType(C.AVMEDIA_TYPE_VIDEO)
	MediaTypeAudio      MediaType = MediaType(C.AVMEDIA_TYPE_AUDIO)
	MediaTypeSubtitle   MediaType = MediaType(C.AVMEDIA_TYPE_SUBTITLE)
	MediaTypeData       MediaType = MediaType(C.AVMEDIA_TYPE_DATA)
	MediaTypeAttachment MediaType = MediaType(C.AVMEDIA_TYPE_ATTACHMENT)
	MediaTypeUnknown    MediaType = MediaType(C.AVMEDIA_TYPE_UNKNOWN)
)

func (m MediaType) String() string {
	switch m {
	case MediaTypeVideo:
		return "video"
	case MediaTypeAudio:
		return "audio"
	case MediaTypeSubtitle:
		return "subtitle"
	case MediaTypeData:
		return "data"
	case MediaTypeAttachment:
		return "attachment"
	default:
		return "unknown"
	}
}

// StreamInfo describes a single stream in an input container.
//
// Grid-coded HEIF/AVIF caveat: a video stream may be one TILE of a larger canvas whose
// true geometry lives in a tile-grid stream group — decode loops that pick "the" video
// stream must consult (*InputFormatContext).TileGrids first, or a 4032x3024 smartphone
// photo decodes as a single 512x512 tile.
type StreamInfo struct {
	Index              int
	Type               MediaType
	CodecID            uint32
	CodecTag           uint32 // four-CC codec tag (FourCC)
	Width              int
	Height             int
	PixFmt             int    // AVPixelFormat (video only)
	FrameRate          [2]int // {num, den} average frame rate (video only)
	RFrameRate         [2]int // {num, den} real base frame rate (video only)
	SampleAspectRatio  [2]int // {num, den}
	FieldOrder         int    // AVFieldOrder (video only)
	ColorSpace         int    // AVColorSpace
	ColorRange         int    // AVColorRange
	ColorPrimaries     int    // AVColorPrimaries
	ColorTransfer      int    // AVColorTransferCharacteristic
	BitsPerCodedSample int    // codec-reported coded bit depth (0 = unknown)
	BitsPerRawSample   int    // codec-reported raw bit depth (0 = unknown)
	BitDepth           int    // derived from PixFmt/SampleFmt component depth
	Profile            int    // FF_PROFILE_* (codec-specific)
	Level              int    // codec-specific level
	BitRate            int64  // bits per second (0 = unknown)
	SampleRate         int
	SampleFmt          int // AVSampleFormat (audio only)
	Channels           int
	TimeBase           [2]int // {num, den}
	Duration           int64  // in stream timebase units
	StartTime          int64  // in stream timebase units (AV_NOPTS_VALUE if unknown)
	Rotation           int    // clockwise degrees to display upright, from the display matrix (0 if none)
}

// InputFormatContext opens a media file for reading and demuxing.
// It must be closed via Close().
type InputFormatContext struct {
	p *C.AVFormatContext
}

// OpenInput opens the file at url for reading. Options may be nil.
func OpenInput(url string, options map[string]string) (*InputFormatContext, error) {
	return OpenInputWithFormat(url, "", options)
}

// OpenInputWithFormat opens the input at url forcing the given input format
// (e.g. "lavfi" for libavfilter virtual sources, "rawvideo" for raw streams).
// When format is empty libavformat probes the URL to detect the demuxer
// (matching OpenInput). For lavfi inputs the URL is the filtergraph spec
// (e.g. "anullsrc=r=48000:cl=stereo", "color=black:s=1920x1080:r=30").
func OpenInputWithFormat(url, format string, options map[string]string) (*InputFormatContext, error) {
	cURL := C.CString(url)
	defer C.free(unsafe.Pointer(cURL))

	// Build AVDictionary from options map.
	var opts *C.AVDictionary
	for k, v := range options {
		ck := C.CString(k)
		cv := C.CString(v)
		C.av_dict_set(&opts, ck, cv, 0)
		C.free(unsafe.Pointer(ck))
		C.free(unsafe.Pointer(cv))
	}
	if opts != nil {
		defer C.av_dict_free(&opts)
	}

	var ctx *C.AVFormatContext
	var iformat *C.AVInputFormat
	if format != "" {
		cFmt := C.CString(format)
		iformat = C.av_find_input_format(cFmt)
		C.free(unsafe.Pointer(cFmt))
		if iformat == nil {
			return nil, fmt.Errorf("av_find_input_format(%q): unknown input format", format)
		}
	}
	ret := C.avformat_open_input(&ctx, cURL, iformat, &opts)
	if ret < 0 {
		return nil, newErr(ret)
	}

	ret = C.avformat_find_stream_info(ctx, nil)
	if ret < 0 {
		C.avformat_close_input(&ctx)
		return nil, fmt.Errorf("avformat_find_stream_info: %w", newErr(ret))
	}

	return &InputFormatContext{p: ctx}, nil
}

// Close frees the format context and closes the input.
func (f *InputFormatContext) Close() error {
	if f.p != nil {
		C.avformat_close_input(&f.p)
		f.p = nil
	}
	return nil
}

// NumStreams returns the number of streams in the container.
func (f *InputFormatContext) NumStreams() int {
	return int(f.p.nb_streams)
}

// StreamInfo returns metadata about stream index i.
func (f *InputFormatContext) StreamInfo(i int) (StreamInfo, error) {
	if i < 0 || i >= f.NumStreams() {
		return StreamInfo{}, fmt.Errorf("stream index %d out of range [0, %d)", i, f.NumStreams())
	}
	cp := C.stream_codecpar(f.p, C.int(i))
	tb := C.stream_time_base(f.p, C.int(i))
	fr := C.stream_avg_frame_rate(f.p, C.int(i))
	rfr := C.stream_r_frame_rate(f.p, C.int(i))
	sar := C.stream_sample_aspect_ratio(f.p, C.int(i))
	mediaType := MediaType(cp.codec_type)
	bitDepth := 0
	switch mediaType {
	case MediaTypeVideo:
		bitDepth = int(C.pix_fmt_bit_depth(C.int(cp.format)))
	case MediaTypeAudio:
		bitDepth = int(C.sample_fmt_bit_depth(C.int(cp.format)))
	}
	return StreamInfo{
		Index:              i,
		Type:               mediaType,
		CodecID:            uint32(cp.codec_id),
		CodecTag:           uint32(cp.codec_tag),
		Width:              int(cp.width),
		Height:             int(cp.height),
		PixFmt:             int(cp.format),
		FrameRate:          [2]int{int(fr.num), int(fr.den)},
		RFrameRate:         [2]int{int(rfr.num), int(rfr.den)},
		SampleAspectRatio:  [2]int{int(sar.num), int(sar.den)},
		FieldOrder:         int(cp.field_order),
		ColorSpace:         int(cp.color_space),
		ColorRange:         int(cp.color_range),
		ColorPrimaries:     int(cp.color_primaries),
		ColorTransfer:      int(cp.color_trc),
		BitsPerCodedSample: int(cp.bits_per_coded_sample),
		BitsPerRawSample:   int(cp.bits_per_raw_sample),
		BitDepth:           bitDepth,
		Profile:            int(cp.profile),
		Level:              int(cp.level),
		BitRate:            int64(cp.bit_rate),
		SampleRate:         int(cp.sample_rate),
		SampleFmt:          int(cp.format),
		Channels:           int(cp.ch_layout.nb_channels),
		TimeBase:           [2]int{int(tb.num), int(tb.den)},
		Duration:           int64(C.stream_duration(f.p, C.int(i))),
		StartTime:          int64(C.stream_start_time(f.p, C.int(i))),
		Rotation:           int(C.stream_display_rotation(f.p, C.int(i))),
	}, nil
}

// AllStreams returns info for all streams.
func (f *InputFormatContext) AllStreams() ([]StreamInfo, error) {
	out := make([]StreamInfo, f.NumStreams())
	for i := range out {
		var err error
		out[i], err = f.StreamInfo(i)
		if err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ProgramInfo describes a single AVProgram entry as seen by
// libavformat. MPEG-TS captures and HLS playlists with multiple
// rendition groups expose programs; most other containers do not.
// Mirrors the subset of `AVProgram` (libavformat/avformat.h) the
// `-map 0:p:N` selector needs.
type ProgramInfo struct {
	// ID is the program identifier (NOT the array index). For
	// MPEG-TS this is the PMT-assigned program number; FFmpeg's
	// `cmdutils.c::check_stream_specifier` matches `p:N` against
	// this field.
	ID int
	// StreamIndices lists the stream indices that belong to this
	// program (`AVProgram.stream_index` array).
	StreamIndices []int
}

// Programs returns the AVProgram table of the input. Empty for
// containers that don't expose programs (most non-MPEG-TS files).
func (f *InputFormatContext) Programs() []ProgramInfo {
	n := int(f.p.nb_programs)
	if n <= 0 {
		return nil
	}
	out := make([]ProgramInfo, 0, n)
	progs := (*[1 << 20]*C.AVProgram)(unsafe.Pointer(f.p.programs))[:n:n]
	for _, prog := range progs {
		if prog == nil {
			continue
		}
		ns := int(prog.nb_stream_indexes)
		idxs := make([]int, 0, ns)
		if ns > 0 {
			arr := (*[1 << 20]C.uint)(unsafe.Pointer(prog.stream_index))[:ns:ns]
			for _, si := range arr {
				idxs = append(idxs, int(si))
			}
		}
		out = append(out, ProgramInfo{ID: int(prog.id), StreamIndices: idxs})
	}
	return out
}

// ReadPacket reads the next packet from the container into pkt.
// Returns ErrEOF at end of stream.
func (f *InputFormatContext) ReadPacket(pkt *Packet) error {
	ret := C.av_read_frame(f.p, pkt.raw())
	return newErr(ret)
}

// SeekFile seeks to the nearest keyframe at targetTS (in AV_TIME_BASE units).
func (f *InputFormatContext) SeekFile(targetTS int64) error {
	ret := C.avformat_seek_file(f.p, -1, C.INT64_MIN, C.int64_t(targetTS), C.INT64_MAX, 0)
	return newErr(ret)
}

// StartTime returns the container's reported AVFormatContext.start_time in
// AV_TIME_BASE units (microseconds), or AV_NOPTS_VALUE when the demuxer
// could not determine it. Used by the runtime when computing FFmpeg-style
// `-ss` seek targets so the seek is biased by the container's own start
// (e.g. MPEG-TS streams whose first PTS is non-zero).
func (f *InputFormatContext) StartTime() int64 {
	return int64(f.p.start_time)
}

// NoPTSValue is the FFmpeg sentinel for "timestamp unknown"
// (AV_NOPTS_VALUE).
const NoPTSValue int64 = C.AV_NOPTS_VALUE

// ParseTime is a Go wrapper around av_parse_time(). It accepts the same
// duration / timestamp grammar as the FFmpeg CLI's `-ss`, `-t` and `-to`
// flags: bare seconds ("30", "5.5"), `[-][HH:]MM:SS[.m…]` ("00:30",
// "1:23:45.250"), or `[-]S+[.m…][s|ms|us]`. When `duration` is true the
// value is interpreted as a duration (no `now` keyword, may be
// negative); otherwise it is interpreted as an absolute timestamp.
// Returns the parsed value in microseconds (AV_TIME_BASE units).
func ParseTime(s string, duration bool) (int64, error) {
	if s == "" {
		return 0, fmt.Errorf("av.ParseTime: empty time spec")
	}
	cs := C.CString(s)
	defer C.free(unsafe.Pointer(cs))
	var out C.int64_t
	durFlag := C.int(0)
	if duration {
		durFlag = 1
	}
	if ret := C.av_parse_time(&out, cs, durFlag); ret < 0 {
		return 0, fmt.Errorf("av.ParseTime(%q): %w", s, newErr(ret))
	}
	return int64(out), nil
}

// raw returns the underlying pointer for use by other av package types.
func (f *InputFormatContext) raw() *C.AVFormatContext { return f.p }
