package av

// #include "libswresample/swresample.h"
// #include "libavutil/channel_layout.h"
// #include "libavutil/samplefmt.h"
// #include "libavutil/frame.h"
// #include "libavutil/opt.h"
//
// static int swr_setup(SwrContext *swr,
//                      int out_sample_rate, int out_sample_fmt, int out_channels,
//                      int in_sample_rate, int in_sample_fmt, int in_channels) {
//     AVChannelLayout in_chl = {0}, out_chl = {0};
//     av_channel_layout_default(&in_chl, in_channels);
//     av_channel_layout_default(&out_chl, out_channels);
//     int ret;
//     ret = av_opt_set_chlayout(swr, "in_chlayout", &in_chl, 0);
//     if (ret < 0) return ret;
//     ret = av_opt_set_chlayout(swr, "out_chlayout", &out_chl, 0);
//     if (ret < 0) return ret;
//     ret = av_opt_set_int(swr, "in_sample_rate", in_sample_rate, 0);
//     if (ret < 0) return ret;
//     ret = av_opt_set_int(swr, "out_sample_rate", out_sample_rate, 0);
//     if (ret < 0) return ret;
//     ret = av_opt_set_sample_fmt(swr, "in_sample_fmt", (enum AVSampleFormat)in_sample_fmt, 0);
//     if (ret < 0) return ret;
//     ret = av_opt_set_sample_fmt(swr, "out_sample_fmt", (enum AVSampleFormat)out_sample_fmt, 0);
//     if (ret < 0) return ret;
//     av_channel_layout_uninit(&in_chl);
//     av_channel_layout_uninit(&out_chl);
//     return swr_init(swr);
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// ResamplerOptions configures an audio resampler.
type ResamplerOptions struct {
	InSampleRate  int
	InSampleFmt  int // AVSampleFormat
	InChannels   int
	OutSampleRate int
	OutSampleFmt  int // AVSampleFormat
	OutChannels   int
}

// Resampler wraps a libswresample SwrContext for audio format conversion.
// It must be closed via Close().
type Resampler struct {
	p   *C.SwrContext
	opts ResamplerOptions
}

// NewResampler creates and initializes an audio resampler.
func NewResampler(opts ResamplerOptions) (*Resampler, error) {
	swr := C.swr_alloc()
	if swr == nil {
		return nil, &Err{Code: -12, Message: "swr_alloc: out of memory"}
	}

	ret := C.swr_setup(swr,
		C.int(opts.OutSampleRate), C.int(opts.OutSampleFmt), C.int(opts.OutChannels),
		C.int(opts.InSampleRate), C.int(opts.InSampleFmt), C.int(opts.InChannels))
	if ret < 0 {
		C.swr_free(&swr)
		return nil, fmt.Errorf("swr_init: %w", newErr(ret))
	}

	leakTrack(unsafe.Pointer(swr), "SwrContext")
	return &Resampler{p: swr, opts: opts}, nil
}

// ConvertFrame resamples the input frame and writes the result into out.
// The caller should pre-allocate out via AllocFrame; the resampler fills
// sample data, format, rate, and layout fields.
func (r *Resampler) ConvertFrame(out, in *Frame) error {
	ret := C.swr_convert_frame(r.p, out.raw(), in.raw())
	return newErr(ret)
}

// Flush drains any buffered samples. Call with out frame to receive remaining samples.
// Returns ErrEOF when no more samples are available.
func (r *Resampler) Flush(out *Frame) error {
	ret := C.swr_convert_frame(r.p, out.raw(), nil)
	return newErr(ret)
}

// Close frees the resampler.
func (r *Resampler) Close() error {
	if r.p != nil {
		leakUntrack(unsafe.Pointer(r.p))
		C.swr_free(&r.p)
		r.p = nil
	}
	return nil
}
