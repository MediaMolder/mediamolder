// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavutil/dict.h"
// #include "libavutil/mem.h"
// #include "mm_packet_guard.h"
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
	"errors"
	"fmt"
	"time"
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

	// deadline is the interrupt-callback deadline box (av_malloc'd C memory,
	// microseconds on av_gettime_relative's clock, 0 = disarmed). Non-nil only
	// for inputs opened via OpenInputResilient; freed by Close.
	deadline *C.int64_t
	// readTimeout, when the watchdog is installed, bounds each blocking
	// ReadPacket. Zero disables the per-read deadline.
	readTimeout time.Duration
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
	return openInput(url, format, options, false)
}

// Defaults for OpenInputResilient. The open deadline covers avformat_open_input
// plus avformat_find_stream_info; the read deadline bounds each ReadPacket.
// Generous on purpose: they exist to turn a demuxer wedged by damaged input
// into an error, not to police slow storage.
const (
	resilientOpenTimeout = 30 * time.Second
	resilientReadTimeout = 30 * time.Second
)

// resilientDefaults are the demuxer options OpenInputResilient applies under
// the caller's own (caller keys win):
//
//   - fflags +discardcorrupt: packets the demuxer itself flags as damaged are
//     dropped at the source instead of being handed to decoders;
//   - probesize / analyzeduration caps: stream probing on a damaged container
//     must not walk gigabytes before giving an answer. Both are above libav's
//     own defaults, so a well-formed file probes identically.
//
// Deliberately NOT set: err_detect. Aggressive error detection turns tolerable
// damage into open failures, which is the wrong trade for analysis paths that
// want whatever frames are recoverable.
//
// Caller keys replace defaults wholesale — in particular a caller-supplied
// "fflags" REPLACES "+discardcorrupt" rather than merging with it; a caller
// overriding fflags must restate it if wanted.
func resilientDefaults() map[string]string {
	return map[string]string{
		"fflags":          "+discardcorrupt",
		"probesize":       "16777216", // 16 MiB
		"analyzeduration": "10000000", // 10 s, in AV_TIME_BASE units
	}
}

// OpenInputResilient opens url for reading damaged or untrusted media — tape
// captures, truncated files, anything whose container structure cannot be
// trusted. On top of OpenInput's behaviour it:
//
//   - applies resilientDefaults (corrupt packets dropped at the demuxer,
//     probing bounded) under the caller's options;
//   - installs a watchdog on every blocking libav call: the open (including
//     stream probing) must finish within 30 s, and each subsequent ReadPacket
//     within the read deadline (default 30 s, adjustable via SetReadDeadline),
//     otherwise the call fails with an error satisfying IsInterrupted instead
//     of wedging the calling goroutine forever.
//
// Well-formed files behave identically to OpenInput. Use this for analysis and
// scanning paths; keep OpenInput for paths that must see every packet
// (e.g. remux/copy, where dropping a damaged packet would silently alter the
// output).
func OpenInputResilient(url string, options map[string]string) (*InputFormatContext, error) {
	merged := resilientDefaults()
	for k, v := range options {
		merged[k] = v
	}
	return openInput(url, "", merged, true)
}

func openInput(url, format string, options map[string]string, guarded bool) (*InputFormatContext, error) {
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

	var iformat *C.AVInputFormat
	if format != "" {
		cFmt := C.CString(format)
		iformat = C.av_find_input_format(cFmt)
		C.free(unsafe.Pointer(cFmt))
		if iformat == nil {
			return nil, fmt.Errorf("av_find_input_format(%q): unknown input format", format)
		}
	}

	// The watchdog must be installed before open: AVIO copies the format
	// context's interrupt callback when it creates the I/O context, so a
	// callback added after open would never see the low-level reads.
	var ctx *C.AVFormatContext
	var deadline *C.int64_t
	if guarded {
		ctx = C.avformat_alloc_context()
		deadline = (*C.int64_t)(C.av_mallocz(C.size_t(unsafe.Sizeof(C.int64_t(0)))))
		if ctx == nil || deadline == nil {
			C.avformat_free_context(ctx)
			C.av_free(unsafe.Pointer(deadline))
			return nil, &Err{Code: -12, Message: "openInput: out of memory"}
		}
		C.mm_install_interrupt(ctx, deadline)
		C.mm_arm_deadline(deadline, C.int64_t(resilientOpenTimeout/time.Microsecond))
	}

	// On failure avformat_open_input frees the context (caller-allocated or
	// not) and nils ctx, so only the deadline box needs cleanup below.
	ret := C.avformat_open_input(&ctx, cURL, iformat, &opts)
	if ret < 0 {
		C.av_free(unsafe.Pointer(deadline))
		return nil, newErr(ret)
	}

	ret = C.avformat_find_stream_info(ctx, nil)
	if ret < 0 {
		C.avformat_close_input(&ctx)
		C.av_free(unsafe.Pointer(deadline))
		return nil, fmt.Errorf("avformat_find_stream_info: %w", newErr(ret))
	}

	f := &InputFormatContext{p: ctx, deadline: deadline}
	if guarded {
		C.mm_arm_deadline(deadline, 0) // open survived; reads arm their own deadline
		f.readTimeout = resilientReadTimeout
	}
	return f, nil
}

// SetReadDeadline adjusts the per-read watchdog on an input opened with
// OpenInputResilient: each subsequent ReadPacket must complete within d or fail
// with an error satisfying IsInterrupted. Zero disables the per-read deadline.
// No effect on inputs opened any other way — the watchdog's interrupt callback
// can only be installed before open.
func (f *InputFormatContext) SetReadDeadline(d time.Duration) {
	if f.deadline != nil {
		f.readTimeout = d
	}
}

// Close frees the format context and closes the input.
func (f *InputFormatContext) Close() error {
	if f.p != nil {
		C.avformat_close_input(&f.p)
		f.p = nil
	}
	if f.deadline != nil {
		// After avformat_close_input nothing can invoke the interrupt callback.
		C.av_free(unsafe.Pointer(f.deadline))
		f.deadline = nil
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

// ErrPoisonedPacket is returned by ReadPacket when the demuxer reported
// success but left the packet structurally inconsistent — the corrupt-input
// failure mode that, unguarded, crashes the process on the next unref (see
// mm_packet_guard.c). The packet has been scrubbed and is safe to reuse or
// Close, but the input should be abandoned: structural corruption does not
// self-heal, and continuing to demux a stream that produced it is how one bad
// file becomes a dead process.
var ErrPoisonedPacket = &Err{
	Code:    int(C.MM_ERR_POISONED_PACKET),
	Message: "demuxer returned a structurally inconsistent packet (abandon this input)",
}

// ReadPacket reads the next packet from the container into pkt.
// Returns ErrEOF at end of stream and ErrPoisonedPacket when the demuxer's
// "success" fails the packet consistency check. On an input opened with
// OpenInputResilient the read is additionally bounded by the read deadline
// (IsInterrupted on expiry).
func (f *InputFormatContext) ReadPacket(pkt *Packet) error {
	if f.deadline != nil && f.readTimeout > 0 {
		C.mm_arm_deadline(f.deadline, C.int64_t(f.readTimeout/time.Microsecond))
	}
	var scrubbed C.int
	ret := C.mm_read_frame_guarded(f.p, pkt.raw(), &scrubbed)
	if scrubbed != 0 {
		scrubbedPackets.Add(1)
	}
	if ret == C.MM_ERR_POISONED_PACKET {
		return ErrPoisonedPacket
	}
	return newErr(ret)
}

// ErrStopDemux, returned by a ForEachPacket callback, ends the iteration
// early; ForEachPacket then returns nil.
var ErrStopDemux = errors.New("av: stop demux")

// ForEachPacket runs the standard demux loop with the packet lifecycle owned
// here: one packet is allocated, unreffed between reads and closed on return,
// so callers cannot get the reuse discipline wrong. Payload-corrupt packets
// (AV_PKT_FLAG_CORRUPT) are skipped, never delivered to fn. The pointer passed
// to fn is only valid for that call — callers keeping data must copy it (e.g.
// via Packet.Data or ClonePacket).
//
// Iteration ends with nil at EOF or when fn returns ErrStopDemux; any other
// error from fn, and any read error other than EOF (including
// ErrPoisonedPacket), is returned as-is. Callers scanning damaged media and
// wanting "whatever was readable" can treat a non-nil return as end-of-usable-
// stream rather than failure.
func (f *InputFormatContext) ForEachPacket(fn func(*Packet) error) error {
	pkt, err := AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()
	for {
		pkt.Unref()
		if e := f.ReadPacket(pkt); e != nil {
			if IsEOF(e) {
				return nil
			}
			return e
		}
		if pkt.IsCorrupt() {
			continue
		}
		if e := fn(pkt); e != nil {
			if errors.Is(e, ErrStopDemux) {
				return nil
			}
			return e
		}
	}
}

// SeekFile seeks to the nearest keyframe at targetTS (in AV_TIME_BASE units).
// On an input opened with OpenInputResilient the seek is bounded by the same
// read deadline as ReadPacket — a damaged index can wedge a seek exactly like
// a read, and extract-at-timestamp paths seek before their first read.
func (f *InputFormatContext) SeekFile(targetTS int64) error {
	if f.deadline != nil && f.readTimeout > 0 {
		C.mm_arm_deadline(f.deadline, C.int64_t(f.readTimeout/time.Microsecond))
	}
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
