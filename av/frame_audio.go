// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavutil/frame.h"
// #include "libavutil/samplefmt.h"
// #include "libavutil/channel_layout.h"
//
// static int mm_audio_new(AVFrame *f, int sample_fmt, int channels, int nb_samples, int sample_rate) {
//     f->format = sample_fmt;
//     f->nb_samples = nb_samples;
//     f->sample_rate = sample_rate;
//     av_channel_layout_default(&f->ch_layout, channels);
//     return av_frame_get_buffer(f, 0);
// }
// static void  mm_audio_set_params(AVFrame *f, int sample_fmt, int channels, int sample_rate) {
//     f->format = sample_fmt;
//     f->sample_rate = sample_rate;
//     av_channel_layout_uninit(&f->ch_layout);
//     av_channel_layout_default(&f->ch_layout, channels);
// }
// static int   mm_frame_nb_samples(const AVFrame *f)      { return f->nb_samples; }
// static void  mm_frame_set_nb_samples(AVFrame *f, int n) { f->nb_samples = n; }
// static int   mm_frame_sample_rate(const AVFrame *f)     { return f->sample_rate; }
// static void  mm_frame_set_sample_rate(AVFrame *f, int r){ f->sample_rate = r; }
// static int   mm_frame_channels(const AVFrame *f)        { return f->ch_layout.nb_channels; }
// static int   mm_frame_sample_fmt(const AVFrame *f)      { return f->format; }
// static int   mm_sample_fmt_from_name(const char *name)  { return av_get_sample_fmt(name); }
// static int   mm_bytes_per_sample(int fmt)               { return av_get_bytes_per_sample((enum AVSampleFormat)fmt); }
// static int   mm_sample_fmt_is_planar(int fmt)           { return av_sample_fmt_is_planar((enum AVSampleFormat)fmt); }
// static uint8_t *mm_frame_extended_data(const AVFrame *f, int ch) { return f->extended_data[ch]; }
import "C"

import "unsafe"

// AV_SAMPLE_FMT_FLTP — planar 32-bit float, the working format the sequence
// editor's audio mixer/crossfade engine operates in. Exposed so callers don't
// have to hard-code the libavutil enum value.
const SampleFmtFLTP = int(C.AV_SAMPLE_FMT_FLTP)

// SampleFormatFromName maps a libavutil sample-format name ("fltp", "s16",
// "flt", …) to its AVSampleFormat value. Returns -1 (AV_SAMPLE_FMT_NONE) for an
// unknown name.
func SampleFormatFromName(name string) int {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return int(C.mm_sample_fmt_from_name(cName))
}

// BytesPerSample returns the number of bytes one sample of the given
// AVSampleFormat occupies in a single channel (e.g. 4 for fltp/flt/s32, 2 for
// s16). Returns 0 for an invalid format.
func BytesPerSample(sampleFmt int) int {
	return int(C.mm_bytes_per_sample(C.int(sampleFmt)))
}

// SampleFmtIsPlanar reports whether the given AVSampleFormat stores each channel
// in its own plane (e.g. fltp, s16p) rather than interleaved.
func SampleFmtIsPlanar(sampleFmt int) bool {
	return C.mm_sample_fmt_is_planar(C.int(sampleFmt)) != 0
}

// NewAudioFrame allocates a writable audio frame with freshly allocated,
// reference-counted sample buffers for nbSamples samples of the given
// AVSampleFormat, channel count and sample rate. The channel layout is the
// libavutil default for the channel count. The caller must Close() it.
func NewAudioFrame(sampleFmt, channels, nbSamples, sampleRate int) (*Frame, error) {
	f, err := AllocFrame()
	if err != nil {
		return nil, err
	}
	if ret := C.mm_audio_new(f.p, C.int(sampleFmt), C.int(channels), C.int(nbSamples), C.int(sampleRate)); ret < 0 {
		f.Close()
		return nil, newErr(ret)
	}
	return f, nil
}

// SetAudioParams sets an audio frame's sample format, channel layout (the
// libavutil default for the channel count) and sample rate WITHOUT allocating
// sample buffers. Use it before Resampler.ConvertFrame / Flush: those allocate
// the destination buffer themselves but require these fields to be set on the
// destination frame first (a bare AllocFrame would otherwise be rejected).
func (f *Frame) SetAudioParams(sampleFmt, channels, sampleRate int) {
	if f == nil || f.p == nil {
		return
	}
	C.mm_audio_set_params(f.p, C.int(sampleFmt), C.int(channels), C.int(sampleRate))
}

// NbSamples returns the number of audio samples per channel in the frame
// (audio only; 0 for video/format-less frames).
func (f *Frame) NbSamples() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_nb_samples(f.p))
}

// SetNbSamples sets the per-channel sample count. Only shrinks the logical
// length; the backing buffer must already be large enough (allocated by
// NewAudioFrame for the original count).
func (f *Frame) SetNbSamples(n int) {
	if f == nil || f.p == nil {
		return
	}
	C.mm_frame_set_nb_samples(f.p, C.int(n))
}

// SampleRate returns the audio sample rate in Hz (audio only).
func (f *Frame) SampleRate() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_sample_rate(f.p))
}

// SetSampleRate sets the audio sample rate in Hz.
func (f *Frame) SetSampleRate(r int) {
	if f == nil || f.p == nil {
		return
	}
	C.mm_frame_set_sample_rate(f.p, C.int(r))
}

// Channels returns the number of audio channels (audio only).
func (f *Frame) Channels() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_channels(f.p))
}

// SampleFmt returns the frame's AVSampleFormat (audio only).
func (f *Frame) SampleFmt() int {
	if f == nil || f.p == nil {
		return 0
	}
	return int(C.mm_frame_sample_fmt(f.p))
}

// SamplePlaneF32 returns channel ch's samples as a float32 slice aliasing the
// frame's C buffer (length = NbSamples()). Writes through the slice edit the
// frame in place. Valid only for planar 32-bit float (fltp) frames; returns nil
// otherwise. The slice is invalidated when the frame is closed or unref'd.
func (f *Frame) SamplePlaneF32(ch int) []float32 {
	if f == nil || f.p == nil {
		return nil
	}
	if C.mm_frame_sample_fmt(f.p) != C.AV_SAMPLE_FMT_FLTP {
		return nil
	}
	if ch < 0 || ch >= int(C.mm_frame_channels(f.p)) {
		return nil
	}
	data := C.mm_frame_extended_data(f.p, C.int(ch))
	if data == nil {
		return nil
	}
	n := int(C.mm_frame_nb_samples(f.p))
	if n <= 0 {
		return nil
	}
	return unsafe.Slice((*float32)(unsafe.Pointer(data)), n)
}
