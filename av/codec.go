// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/samplefmt.h"
//
// // Return the pixel format at index i from the encoder's supported list.
// // Uses avcodec_get_supported_config (FFmpeg 8+) to avoid accessing the
// // deprecated AVCodec.pix_fmts field directly.
// static enum AVPixelFormat encoder_pix_fmt_at(const AVCodec *codec, int i) {
//     const void *configs = NULL;
//     int num = 0;
//     if (avcodec_get_supported_config(NULL, codec, AV_CODEC_CONFIG_PIX_FORMAT, 0, &configs, &num) < 0)
//         return AV_PIX_FMT_NONE;
//     if (configs == NULL || i >= num) return AV_PIX_FMT_NONE;
//     return ((const enum AVPixelFormat*)configs)[i];
// }
// // Return the sample format at index i from the encoder's supported list.
// static enum AVSampleFormat encoder_sample_fmt_at(const AVCodec *codec, int i) {
//     const void *configs = NULL;
//     int num = 0;
//     if (avcodec_get_supported_config(NULL, codec, AV_CODEC_CONFIG_SAMPLE_FORMAT, 0, &configs, &num) < 0)
//         return AV_SAMPLE_FMT_NONE;
//     if (configs == NULL || i >= num) return AV_SAMPLE_FMT_NONE;
//     return ((const enum AVSampleFormat*)configs)[i];
// }
// // Return the supported sample rate count and fill *out with the pointer.
// static int encoder_sample_rate_count(const AVCodec *codec) {
//     const void *configs = NULL;
//     int num = 0;
//     if (avcodec_get_supported_config(NULL, codec, AV_CODEC_CONFIG_SAMPLE_RATE, 0, &configs, &num) < 0)
//         return 0;
//     return num;
// }
// static int encoder_sample_rate_at(const AVCodec *codec, int i) {
//     const void *configs = NULL;
//     int num = 0;
//     if (avcodec_get_supported_config(NULL, codec, AV_CODEC_CONFIG_SAMPLE_RATE, 0, &configs, &num) < 0)
//         return 0;
//     if (configs == NULL || i >= num) return 0;
//     return ((const int*)configs)[i];
// }
import "C"

import "unsafe"

// FindEncoder reports whether the named encoder is available in this build.
func FindEncoder(name string) bool {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return C.avcodec_find_encoder_by_name(cName) != nil
}

// EncoderPixFmts returns the AVPixelFormat values accepted by the named encoder.
// Returns nil if the encoder is not found or accepts any pixel format.
func EncoderPixFmts(name string) []int {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil
	}
	var out []int
	for i := 0; ; i++ {
		pf := C.encoder_pix_fmt_at(codec, C.int(i))
		if pf == C.AV_PIX_FMT_NONE {
			break
		}
		out = append(out, int(pf))
	}
	return out
}

// EncoderSampleFmts returns the AVSampleFormat values accepted by the named encoder.
// Returns nil if the encoder is not found or accepts any sample format.
func EncoderSampleFmts(name string) []int {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil
	}
	var out []int
	for i := 0; ; i++ {
		sf := C.encoder_sample_fmt_at(codec, C.int(i))
		if sf == C.AV_SAMPLE_FMT_NONE {
			break
		}
		out = append(out, int(sf))
	}
	return out
}

// EncoderSampleRates returns the supported sample rates for the named encoder.
// Returns nil if the encoder is not found or accepts any sample rate.
func EncoderSampleRates(name string) []int {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	codec := C.avcodec_find_encoder_by_name(cName)
	if codec == nil {
		return nil
	}
	n := int(C.encoder_sample_rate_count(codec))
	if n == 0 {
		return nil
	}
	out := make([]int, 0, n)
	for i := 0; i < n; i++ {
		r := int(C.encoder_sample_rate_at(codec, C.int(i)))
		if r == 0 {
			break
		}
		out = append(out, r)
	}
	return out
}


