// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/dict.h"
//
// // Helper: get stream codec parameters for stream index i.
// static AVCodecParameters *stream_codecpar(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->codecpar;
// }
// static int64_t stream_duration(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->duration;
// }
// static AVRational stream_time_base(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->time_base;
// }
// static AVRational stream_avg_frame_rate(AVFormatContext *ctx, int i) {
//     return ctx->streams[i]->avg_frame_rate;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// MediaType mirrors AVMediaType values.
type MediaType int

const (
	MediaTypeVideo    MediaType = MediaType(C.AVMEDIA_TYPE_VIDEO)
	MediaTypeAudio    MediaType = MediaType(C.AVMEDIA_TYPE_AUDIO)
	MediaTypeSubtitle MediaType = MediaType(C.AVMEDIA_TYPE_SUBTITLE)
	MediaTypeData     MediaType = MediaType(C.AVMEDIA_TYPE_DATA)
	MediaTypeUnknown  MediaType = MediaType(C.AVMEDIA_TYPE_UNKNOWN)
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
	default:
		return "unknown"
	}
}

// StreamInfo describes a single stream in an input container.
type StreamInfo struct {
	Index      int
	Type       MediaType
	CodecID    uint32
	Width      int
	Height     int
	PixFmt     int    // AVPixelFormat (video only)
	FrameRate  [2]int // {num, den} average frame rate (video only)
	SampleRate int
	SampleFmt  int // AVSampleFormat (audio only)
	Channels   int
	TimeBase   [2]int // {num, den}
	Duration   int64  // in stream timebase units
}

// InputFormatContext opens a media file for reading and demuxing.
// It must be closed via Close().
type InputFormatContext struct {
	p *C.AVFormatContext
}

// OpenInput opens the file at url for reading. Options may be nil.
func OpenInput(url string, options map[string]string) (*InputFormatContext, error) {
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
	ret := C.avformat_open_input(&ctx, cURL, nil, &opts)
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
	return StreamInfo{
		Index:      i,
		Type:       MediaType(cp.codec_type),
		CodecID:    uint32(cp.codec_id),
		Width:      int(cp.width),
		Height:     int(cp.height),
		PixFmt:     int(cp.format),
		FrameRate:  [2]int{int(fr.num), int(fr.den)},
		SampleRate: int(cp.sample_rate),
		SampleFmt:  int(cp.format),
		Channels:   int(cp.ch_layout.nb_channels),
		TimeBase:   [2]int{int(tb.num), int(tb.den)},
		Duration:   int64(C.stream_duration(f.p, C.int(i))),
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

// raw returns the underlying pointer for use by other av package types.
func (f *InputFormatContext) raw() *C.AVFormatContext { return f.p }
