package av

// #include "libavfilter/avfilter.h"
// #include "libavfilter/buffersrc.h"
// #include "libavfilter/buffersink.h"
// #include "libavutil/opt.h"
// #include "libavutil/rational.h"
// #include "libavcodec/avcodec.h"
//
// // Helper: build the buffersrc args string for a video stream.
// static int make_video_src_args(char *buf, int buf_size,
//                                int width, int height,
//                                int pix_fmt,
//                                int tb_num, int tb_den,
//                                int sar_num, int sar_den) {
//     return snprintf(buf, buf_size,
//         "video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d",
//         width, height, pix_fmt, tb_num, tb_den, sar_num, sar_den);
// }
// // Helper: build the buffersrc args string for an audio stream.
// static int make_audio_src_args(char *buf, int buf_size,
//                                int sample_fmt, int sample_rate,
//                                int nb_channels) {
//     return snprintf(buf, buf_size,
//         "sample_fmt=%d:sample_rate=%d:channels=%d",
//         sample_fmt, sample_rate, nb_channels);
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// FilterGraph wraps an AVFilterGraph and the buffer source/sink contexts
// for a single-input, single-output filter chain.
type FilterGraph struct {
	graph     *C.AVFilterGraph
	bufSrc    *C.AVFilterContext
	bufSink   *C.AVFilterContext
	mediaType MediaType
}

// VideoFilterGraphConfig carries the parameters needed to build a video filter graph.
type VideoFilterGraphConfig struct {
	Width      int
	Height     int
	PixFmt     int    // AVPixelFormat
	TBNum      int    // time_base numerator
	TBDen      int    // time_base denominator
	SARNum     int    // sample_aspect_ratio numerator
	SARDen     int    // sample_aspect_ratio denominator
	FilterSpec string // e.g. "scale=1280:720"
}

// AudioFilterGraphConfig carries the parameters needed to build an audio filter graph.
type AudioFilterGraphConfig struct {
	SampleFmt  int // AVSampleFormat
	SampleRate int
	Channels   int
	FilterSpec string // e.g. "aresample=44100"
}

// NewVideoFilterGraph creates a video filter graph with a single filter chain.
// The FilterSpec is an ffmpeg filter string, e.g. "scale=1280:720,drawtext=...".
func NewVideoFilterGraph(cfg VideoFilterGraphConfig) (*FilterGraph, error) {
	graph := C.avfilter_graph_alloc()
	if graph == nil {
		return nil, &Err{Code: -12, Message: "avfilter_graph_alloc: out of memory"}
	}

	cBufName := C.CString("buffer")
	defer C.free(unsafe.Pointer(cBufName))
	cSinkName := C.CString("buffersink")
	defer C.free(unsafe.Pointer(cSinkName))

	buffersrc := C.avfilter_get_by_name(cBufName)
	buffersink := C.avfilter_get_by_name(cSinkName)
	if buffersrc == nil || buffersink == nil {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("could not find buffer/buffersink filter")
	}

	// Build args string for the buffer source.
	var argsBuf [512]C.char
	C.make_video_src_args(&argsBuf[0], 512,
		C.int(cfg.Width), C.int(cfg.Height), C.int(cfg.PixFmt),
		C.int(cfg.TBNum), C.int(cfg.TBDen),
		C.int(cfg.SARNum), C.int(cfg.SARDen))

	cIn := C.CString("in")
	defer C.free(unsafe.Pointer(cIn))
	cOut := C.CString("out")
	defer C.free(unsafe.Pointer(cOut))

	var srcCtx *C.AVFilterContext
	ret := C.avfilter_graph_create_filter(&srcCtx, buffersrc,
		cIn, &argsBuf[0], nil, graph)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("create buffer source: %w", newErr(ret))
	}

	var sinkCtx *C.AVFilterContext
	ret = C.avfilter_graph_create_filter(&sinkCtx, buffersink,
		cOut, nil, nil, graph)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("create buffersink: %w", newErr(ret))
	}

	// Wire source -> filter chain -> sink using avfilter_graph_parse_ptr.
	var inputs *C.AVFilterInOut
	var outputs *C.AVFilterInOut

	outputs = C.avfilter_inout_alloc()
	inputs = C.avfilter_inout_alloc()
	if outputs == nil || inputs == nil {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("avfilter_inout_alloc: out of memory")
	}

	outputs.name = C.av_strdup(cIn)
	outputs.filter_ctx = srcCtx
	outputs.pad_idx = 0
	outputs.next = nil

	inputs.name = C.av_strdup(cOut)
	inputs.filter_ctx = sinkCtx
	inputs.pad_idx = 0
	inputs.next = nil

	cSpec := C.CString(cfg.FilterSpec)
	defer C.free(unsafe.Pointer(cSpec))

	ret = C.avfilter_graph_parse_ptr(graph, cSpec, &inputs, &outputs, nil)
	C.avfilter_inout_free(&inputs)
	C.avfilter_inout_free(&outputs)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("avfilter_graph_parse_ptr(%q): %w", cfg.FilterSpec, newErr(ret))
	}

	ret = C.avfilter_graph_config(graph, nil)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("avfilter_graph_config: %w", newErr(ret))
	}

	return &FilterGraph{
		graph:     graph,
		bufSrc:    srcCtx,
		bufSink:   sinkCtx,
		mediaType: MediaTypeVideo,
	}, nil
}

// Close frees the filter graph.
func (fg *FilterGraph) Close() error {
	if fg.graph != nil {
		C.avfilter_graph_free(&fg.graph)
		fg.graph = nil
	}
	return nil
}

// PushFrame sends a frame into the buffer source.
func (fg *FilterGraph) PushFrame(f *Frame) error {
	ret := C.av_buffersrc_add_frame_flags(fg.bufSrc, f.raw(),
		C.AV_BUFFERSRC_FLAG_KEEP_REF)
	return newErr(ret)
}

// PullFrame receives a filtered frame from the buffer sink.
// Returns ErrEAgain if no frame is ready yet, ErrEOF when flushing is complete.
func (fg *FilterGraph) PullFrame(f *Frame) error {
	ret := C.av_buffersink_get_frame(fg.bufSink, f.raw())
	return newErr(ret)
}

// Flush signals end-of-stream to the buffer source so buffered frames drain.
func (fg *FilterGraph) Flush() error {
	ret := C.av_buffersrc_add_frame_flags(fg.bufSrc, nil, 0)
	return newErr(ret)
}
