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

// FilterGraph wraps an AVFilterGraph and the buffer source/sink contexts.
// Simple (1-in / 1-out) graphs are created with NewVideoFilterGraph or
// NewAudioFilterGraph. Complex (N-in / M-out) graphs use NewComplexFilterGraph.
type FilterGraph struct {
	graph     *C.AVFilterGraph
	bufSrcs   []*C.AVFilterContext // one per input pad
	bufSinks  []*C.AVFilterContext // one per output pad
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
		bufSrcs:   []*C.AVFilterContext{srcCtx},
		bufSinks:  []*C.AVFilterContext{sinkCtx},
		mediaType: MediaTypeVideo,
	}, nil
}

// Close frees the filter graph.
func (fg *FilterGraph) Close() error {
	if fg.graph != nil {
		C.avfilter_graph_free(&fg.graph)
		fg.graph = nil
	}
	fg.bufSrcs = nil
	fg.bufSinks = nil
	return nil
}

// NumInputs returns the number of input pads.
func (fg *FilterGraph) NumInputs() int { return len(fg.bufSrcs) }

// NumOutputs returns the number of output pads.
func (fg *FilterGraph) NumOutputs() int { return len(fg.bufSinks) }

// PushFrame sends a frame into the first (index 0) buffer source.
func (fg *FilterGraph) PushFrame(f *Frame) error {
	return fg.PushFrameAt(0, f)
}

// PushFrameAt sends a frame into the buffer source at the given index.
func (fg *FilterGraph) PushFrameAt(idx int, f *Frame) error {
	if idx < 0 || idx >= len(fg.bufSrcs) {
		return fmt.Errorf("filter input index %d out of range [0, %d)", idx, len(fg.bufSrcs))
	}
	ret := C.av_buffersrc_add_frame_flags(fg.bufSrcs[idx], f.raw(),
		C.AV_BUFFERSRC_FLAG_KEEP_REF)
	return newErr(ret)
}

// PullFrame receives a filtered frame from the first (index 0) buffer sink.
// Returns ErrEAgain if no frame is ready yet, ErrEOF when flushing is complete.
func (fg *FilterGraph) PullFrame(f *Frame) error {
	return fg.PullFrameAt(0, f)
}

// PullFrameAt receives a filtered frame from the buffer sink at the given index.
func (fg *FilterGraph) PullFrameAt(idx int, f *Frame) error {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return fmt.Errorf("filter output index %d out of range [0, %d)", idx, len(fg.bufSinks))
	}
	ret := C.av_buffersink_get_frame(fg.bufSinks[idx], f.raw())
	return newErr(ret)
}

// Flush signals end-of-stream to the first (index 0) buffer source.
func (fg *FilterGraph) Flush() error {
	return fg.FlushAt(0)
}

// FlushAt signals end-of-stream to the buffer source at the given index.
func (fg *FilterGraph) FlushAt(idx int) error {
	if idx < 0 || idx >= len(fg.bufSrcs) {
		return fmt.Errorf("filter input index %d out of range [0, %d)", idx, len(fg.bufSrcs))
	}
	ret := C.av_buffersrc_add_frame_flags(fg.bufSrcs[idx], nil, 0)
	return newErr(ret)
}

// OutputWidth returns the output video width of the sink at the given index.
func (fg *FilterGraph) OutputWidth(idx int) int {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return 0
	}
	return int(C.av_buffersink_get_w(fg.bufSinks[idx]))
}

// OutputHeight returns the output video height of the sink at the given index.
func (fg *FilterGraph) OutputHeight(idx int) int {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return 0
	}
	return int(C.av_buffersink_get_h(fg.bufSinks[idx]))
}

// OutputPixFmt returns the output pixel format of the sink at the given index.
func (fg *FilterGraph) OutputPixFmt(idx int) int {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return -1
	}
	return int(C.av_buffersink_get_format(fg.bufSinks[idx]))
}

// OutputSampleRate returns the output sample rate of the audio sink at the given index.
func (fg *FilterGraph) OutputSampleRate(idx int) int {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return 0
	}
	return int(C.av_buffersink_get_sample_rate(fg.bufSinks[idx]))
}

// OutputChannels returns the number of audio channels of the sink at the given index.
func (fg *FilterGraph) OutputChannels(idx int) int {
	if idx < 0 || idx >= len(fg.bufSinks) {
		return 0
	}
	return int(C.av_buffersink_get_channels(fg.bufSinks[idx]))
}

// NewAudioFilterGraph creates an audio filter graph with a single filter chain.
// The FilterSpec is an ffmpeg audio filter string, e.g. "aresample=44100" or "anull".
func NewAudioFilterGraph(cfg AudioFilterGraphConfig) (*FilterGraph, error) {
	graph := C.avfilter_graph_alloc()
	if graph == nil {
		return nil, &Err{Code: -12, Message: "avfilter_graph_alloc: out of memory"}
	}

	cBufName := C.CString("abuffer")
	defer C.free(unsafe.Pointer(cBufName))
	cSinkName := C.CString("abuffersink")
	defer C.free(unsafe.Pointer(cSinkName))

	abuffersrc := C.avfilter_get_by_name(cBufName)
	abuffersink := C.avfilter_get_by_name(cSinkName)
	if abuffersrc == nil || abuffersink == nil {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("could not find abuffer/abuffersink filter")
	}

	var argsBuf [512]C.char
	C.make_audio_src_args(&argsBuf[0], 512,
		C.int(cfg.SampleFmt), C.int(cfg.SampleRate), C.int(cfg.Channels))

	cIn := C.CString("in")
	defer C.free(unsafe.Pointer(cIn))
	cOut := C.CString("out")
	defer C.free(unsafe.Pointer(cOut))

	var srcCtx *C.AVFilterContext
	ret := C.avfilter_graph_create_filter(&srcCtx, abuffersrc,
		cIn, &argsBuf[0], nil, graph)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("create abuffer source: %w", newErr(ret))
	}

	var sinkCtx *C.AVFilterContext
	ret = C.avfilter_graph_create_filter(&sinkCtx, abuffersink,
		cOut, nil, nil, graph)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("create abuffersink: %w", newErr(ret))
	}

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
		bufSrcs:   []*C.AVFilterContext{srcCtx},
		bufSinks:  []*C.AVFilterContext{sinkCtx},
		mediaType: MediaTypeAudio,
	}, nil
}

// MediaType returns the media type of the filter graph (video or audio).
func (fg *FilterGraph) MediaType() MediaType {
	return fg.mediaType
}

// ---------- Complex (multi-input / multi-output) filter graphs ----------

// FilterPadConfig describes one input pad of a complex filter graph.
type FilterPadConfig struct {
	Label     string    // pad label in the filter spec, e.g. "in0"
	MediaType MediaType // MediaTypeVideo or MediaTypeAudio

	// Video parameters (only when MediaType == MediaTypeVideo).
	Width, Height, PixFmt int
	TBNum, TBDen          int
	SARNum, SARDen        int

	// Audio parameters (only when MediaType == MediaTypeAudio).
	SampleFmt, SampleRate, Channels int
}

// FilterOutputConfig describes one output pad of a complex filter graph.
type FilterOutputConfig struct {
	Label     string    // pad label in the filter spec, e.g. "out0"
	MediaType MediaType // MediaTypeVideo or MediaTypeAudio
}

// ComplexFilterGraphConfig configures a multi-input / multi-output filter graph.
// The FilterSpec must reference all input/output labels, e.g.
// "[in0][in1]overlay[out0]" or "[in0]split[out0][out1]".
type ComplexFilterGraphConfig struct {
	Inputs     []FilterPadConfig
	Outputs    []FilterOutputConfig
	FilterSpec string
}

// NewComplexFilterGraph creates a filter graph with an arbitrary number of
// buffer sources and buffer sinks, connected by the given FilterSpec.
func NewComplexFilterGraph(cfg ComplexFilterGraphConfig) (*FilterGraph, error) {
	if len(cfg.Inputs) == 0 {
		return nil, fmt.Errorf("complex filter graph requires at least one input")
	}
	if len(cfg.Outputs) == 0 {
		return nil, fmt.Errorf("complex filter graph requires at least one output")
	}

	graph := C.avfilter_graph_alloc()
	if graph == nil {
		return nil, &Err{Code: -12, Message: "avfilter_graph_alloc: out of memory"}
	}

	// --- Create buffer sources for each input pad ---
	bufSrcs := make([]*C.AVFilterContext, len(cfg.Inputs))
	for i, inp := range cfg.Inputs {
		var srcFilter *C.AVFilter
		var argsBuf [512]C.char

		switch inp.MediaType {
		case MediaTypeVideo:
			cName := C.CString("buffer")
			srcFilter = C.avfilter_get_by_name(cName)
			C.free(unsafe.Pointer(cName))
			if srcFilter == nil {
				C.avfilter_graph_free(&graph)
				return nil, fmt.Errorf("buffer filter not found")
			}
			C.make_video_src_args(&argsBuf[0], 512,
				C.int(inp.Width), C.int(inp.Height), C.int(inp.PixFmt),
				C.int(inp.TBNum), C.int(inp.TBDen),
				C.int(inp.SARNum), C.int(inp.SARDen))

		case MediaTypeAudio:
			cName := C.CString("abuffer")
			srcFilter = C.avfilter_get_by_name(cName)
			C.free(unsafe.Pointer(cName))
			if srcFilter == nil {
				C.avfilter_graph_free(&graph)
				return nil, fmt.Errorf("abuffer filter not found")
			}
			C.make_audio_src_args(&argsBuf[0], 512,
				C.int(inp.SampleFmt), C.int(inp.SampleRate), C.int(inp.Channels))

		default:
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("unsupported input media type for pad %q", inp.Label)
		}

		cLabel := C.CString(inp.Label)
		ret := C.avfilter_graph_create_filter(&bufSrcs[i], srcFilter,
			cLabel, &argsBuf[0], nil, graph)
		C.free(unsafe.Pointer(cLabel))
		if ret < 0 {
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("create buffer source %q: %w", inp.Label, newErr(ret))
		}
	}

	// --- Create buffer sinks for each output pad ---
	bufSinks := make([]*C.AVFilterContext, len(cfg.Outputs))
	for i, out := range cfg.Outputs {
		var sinkFilter *C.AVFilter

		switch out.MediaType {
		case MediaTypeVideo:
			cName := C.CString("buffersink")
			sinkFilter = C.avfilter_get_by_name(cName)
			C.free(unsafe.Pointer(cName))
		case MediaTypeAudio:
			cName := C.CString("abuffersink")
			sinkFilter = C.avfilter_get_by_name(cName)
			C.free(unsafe.Pointer(cName))
		default:
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("unsupported output media type for pad %q", out.Label)
		}

		if sinkFilter == nil {
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("buffersink filter not found for %q", out.Label)
		}

		cLabel := C.CString(out.Label)
		ret := C.avfilter_graph_create_filter(&bufSinks[i], sinkFilter,
			cLabel, nil, nil, graph)
		C.free(unsafe.Pointer(cLabel))
		if ret < 0 {
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("create buffer sink %q: %w", out.Label, newErr(ret))
		}
	}

	// --- Build AVFilterInOut linked lists ---
	// "outputs" = graph open output pads = buffer sources.
	// "inputs"  = graph open input pads  = buffer sinks.
	// Linked lists are built in reverse order so index 0 is the head.
	var outputs *C.AVFilterInOut
	for i := len(cfg.Inputs) - 1; i >= 0; i-- {
		io := C.avfilter_inout_alloc()
		if io == nil {
			C.avfilter_inout_free(&outputs)
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("avfilter_inout_alloc: out of memory")
		}
		cLabel := C.CString(cfg.Inputs[i].Label)
		io.name = C.av_strdup(cLabel)
		C.free(unsafe.Pointer(cLabel))
		io.filter_ctx = bufSrcs[i]
		io.pad_idx = 0
		io.next = outputs
		outputs = io
	}

	var inputs *C.AVFilterInOut
	for i := len(cfg.Outputs) - 1; i >= 0; i-- {
		io := C.avfilter_inout_alloc()
		if io == nil {
			C.avfilter_inout_free(&inputs)
			C.avfilter_inout_free(&outputs)
			C.avfilter_graph_free(&graph)
			return nil, fmt.Errorf("avfilter_inout_alloc: out of memory")
		}
		cLabel := C.CString(cfg.Outputs[i].Label)
		io.name = C.av_strdup(cLabel)
		C.free(unsafe.Pointer(cLabel))
		io.filter_ctx = bufSinks[i]
		io.pad_idx = 0
		io.next = inputs
		inputs = io
	}

	// --- Parse, link, and configure ---
	cSpec := C.CString(cfg.FilterSpec)
	defer C.free(unsafe.Pointer(cSpec))

	ret := C.avfilter_graph_parse_ptr(graph, cSpec, &inputs, &outputs, nil)
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
		bufSrcs:   bufSrcs,
		bufSinks:  bufSinks,
		mediaType: cfg.Inputs[0].MediaType,
	}, nil
}
