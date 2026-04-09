package av

// #include "libavfilter/avfilter.h"
// #include "libavfilter/buffersrc.h"
// #include "libavfilter/buffersink.h"
// #include "libavutil/opt.h"
// #include "libavutil/hwcontext.h"
// #include "libavutil/buffer.h"
// #include "libavutil/pixdesc.h"
//
// // Helper: build buffersrc args for a HW video input.
// static int make_hw_video_src_args(char *buf, int buf_size,
//                                    int width, int height,
//                                    int pix_fmt,
//                                    int tb_num, int tb_den,
//                                    int sar_num, int sar_den) {
//     return snprintf(buf, buf_size,
//         "video_size=%dx%d:pix_fmt=%d:time_base=%d/%d:pixel_aspect=%d/%d",
//         width, height, pix_fmt, tb_num, tb_den, sar_num, sar_den);
// }
//
// // Set hardware device context on all filter contexts in a graph.
// static int set_filter_hw_device(AVFilterGraph *graph, AVBufferRef *device_ref) {
//     unsigned i;
//     for (i = 0; i < graph->nb_filters; i++) {
//         graph->filters[i]->hw_device_ctx = av_buffer_ref(device_ref);
//         if (!graph->filters[i]->hw_device_ctx) return -1;
//     }
//     return 0;
// }
//
// // Set hw device ctx on a single filter context (used post-parse, pre-config).
// static int set_one_filter_hw_device(AVFilterContext *fctx, AVBufferRef *device_ref) {
//     fctx->hw_device_ctx = av_buffer_ref(device_ref);
//     if (!fctx->hw_device_ctx) return -1;
//     return 0;
// }
//
// // Get number of filters in graph.
// static unsigned get_graph_nb_filters(AVFilterGraph *graph) {
//     return graph->nb_filters;
// }
//
// // Get filter context by index.
// static AVFilterContext* get_graph_filter(AVFilterGraph *graph, unsigned idx) {
//     return graph->filters[idx];
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// HWVideoFilterGraphConfig extends VideoFilterGraphConfig with hardware device support.
type HWVideoFilterGraphConfig struct {
	Width      int
	Height     int
	PixFmt     int // Can be a hardware pixel format (e.g. AV_PIX_FMT_CUDA)
	TBNum      int
	TBDen      int
	SARNum     int
	SARDen     int
	FilterSpec string

	// HWDevice enables hardware filter acceleration (e.g. scale_cuda, scale_vaapi).
	// If nil, the filter graph runs in software mode.
	HWDevice *HWDeviceContext
}

// NewHWVideoFilterGraph creates a video filter graph that can use hardware filters.
// When HWDevice is set, the filter graph's hw_device_ctx is configured so
// hardware filters (scale_cuda, scale_vaapi, etc.) can allocate GPU frames.
func NewHWVideoFilterGraph(cfg HWVideoFilterGraphConfig) (*FilterGraph, error) {
	graph := C.avfilter_graph_alloc()
	if graph == nil {
		return nil, &Err{Code: -12, Message: "avfilter_graph_alloc: out of memory"}
	}

	// Set hardware device context on the filter graph if provided.
	// Must be done after parsing but before configuring - we store the device
	// ref to apply after avfilter_graph_parse_ptr.
	hwDevice := cfg.HWDevice

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

	var argsBuf [512]C.char
	C.make_hw_video_src_args(&argsBuf[0], 512,
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
		return nil, fmt.Errorf("create buffer source (hw): %w", newErr(ret))
	}

	var sinkCtx *C.AVFilterContext
	ret = C.avfilter_graph_create_filter(&sinkCtx, buffersink,
		cOut, nil, nil, graph)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("create buffersink (hw): %w", newErr(ret))
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

	// Apply hardware device context to all filter contexts after parse.
	if hwDevice != nil {
		nf := C.get_graph_nb_filters(graph)
		for i := C.uint(0); i < nf; i++ {
			fctx := C.get_graph_filter(graph, i)
			if retHW := C.set_one_filter_hw_device(fctx, hwDevice.raw()); retHW < 0 {
				C.avfilter_graph_free(&graph)
				return nil, fmt.Errorf("set hw device on filter %d: failed", i)
			}
		}
	}

	ret = C.avfilter_graph_config(graph, nil)
	if ret < 0 {
		C.avfilter_graph_free(&graph)
		return nil, fmt.Errorf("avfilter_graph_config (hw): %w", newErr(ret))
	}

	return &FilterGraph{
		graph:     graph,
		bufSrcs:   []*C.AVFilterContext{srcCtx},
		bufSinks:  []*C.AVFilterContext{sinkCtx},
		mediaType: MediaTypeVideo,
	}, nil
}

// HWFilterName returns the hardware-accelerated equivalent of a software filter
// for the given device type. Returns the original name if no hw variant exists.
func HWFilterName(filter string, deviceType HWDeviceType) string {
	hwMap := map[HWDeviceType]map[string]string{
		HWDeviceCUDA: {
			"scale":     "scale_cuda",
			"yadif":     "yadif_cuda",
			"transpose": "transpose_cuda",
			"overlay":   "overlay_cuda",
		},
		HWDeviceVAAPI: {
			"scale":       "scale_vaapi",
			"deinterlace": "deinterlace_vaapi",
			"transpose":   "transpose_vaapi",
			"overlay":     "overlay_vaapi",
		},
		HWDeviceQSV: {
			"scale":       "scale_qsv",
			"deinterlace": "deinterlace_qsv",
			"overlay":     "overlay_qsv",
		},
		HWDeviceVideoToolbox: {
			"scale": "scale_vt",
		},
	}

	if m, ok := hwMap[deviceType]; ok {
		if hw, ok := m[filter]; ok {
			return hw
		}
	}
	return filter
}
