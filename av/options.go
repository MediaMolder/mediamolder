// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavutil/opt.h"
// #include <stdlib.h>
//
// // Iterate AVOptions on a class. Returns NULL when done. The opt_class is
// // typically &AVCodecContext.av_class or codec->priv_class.
// static const AVOption *next_opt(const AVClass **cls_ptr, const AVOption *prev) {
//     return av_opt_next(cls_ptr, prev);
// }
//
// // Allocate an AVCodecContext for a given encoder name; returns NULL if the
// // encoder doesn't exist or allocation fails. Caller must avcodec_free_context.
// static AVCodecContext *alloc_encoder_ctx(const char *name) {
//     const AVCodec *c = avcodec_find_encoder_by_name(name);
//     if (!c) return NULL;
//     return avcodec_alloc_context3(c);
// }
//
// // Return the codec's private class (may be NULL for codecs with no private
// // options).
// static const AVClass *encoder_priv_class(const char *name) {
//     const AVCodec *c = avcodec_find_encoder_by_name(name);
//     if (!c) return NULL;
//     return c->priv_class;
// }
//
// // Free wrapper.
// static void free_ctx(AVCodecContext *ctx) {
//     if (ctx) avcodec_free_context(&ctx);
// }
//
// // Codec long-name lookup.
// static const char *encoder_long_name(const char *name) {
//     const AVCodec *c = avcodec_find_encoder_by_name(name);
//     if (!c) return NULL;
//     return c->long_name;
// }
//
// // Codec media type for an encoder.
// static int encoder_media_type(const char *name) {
//     const AVCodec *c = avcodec_find_encoder_by_name(name);
//     if (!c) return AVMEDIA_TYPE_UNKNOWN;
//     return c->type;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// OptionType classifies an AVOption for UI rendering.
type OptionType string

const (
	OptTypeFlags    OptionType = "flags"
	OptTypeInt      OptionType = "int"
	OptTypeInt64    OptionType = "int64"
	OptTypeDouble   OptionType = "double"
	OptTypeFloat    OptionType = "float"
	OptTypeString   OptionType = "string"
	OptTypeRational OptionType = "rational"
	OptTypeBinary   OptionType = "binary"
	OptTypeDict     OptionType = "dict"
	OptTypeUInt64   OptionType = "uint64"
	OptTypeBool     OptionType = "bool"
	OptTypeDuration OptionType = "duration"
	OptTypeColor    OptionType = "color"
	OptTypeChLayout OptionType = "channel_layout"
	OptTypePixFmt   OptionType = "pix_fmt"
	OptTypeSmpFmt   OptionType = "sample_fmt"
	OptTypeUnknown  OptionType = "unknown"
)

// EncoderOption describes a single AVOption exposed by an encoder.
type EncoderOption struct {
	Name      string             `json:"name"`
	Help      string             `json:"help,omitempty"`
	Type      OptionType         `json:"type"`
	Unit      string             `json:"unit,omitempty"`
	Min       float64            `json:"min,omitempty"`
	Max       float64            `json:"max,omitempty"`
	Default   *EncoderOptionVal  `json:"default,omitempty"`
	Constants []EncoderOptionEnum `json:"constants,omitempty"`
	IsPrivate bool               `json:"is_private"` // true => codec-specific (e.g. libx264 preset); false => generic AVCodecContext (e.g. b, g)
}

// EncoderOptionEnum is one named constant attached to an option's `unit`.
type EncoderOptionEnum struct {
	Name  string `json:"name"`
	Help  string `json:"help,omitempty"`
	Value int64  `json:"value"`
}

// EncoderOptionVal carries a typed default value. Only one field is set.
type EncoderOptionVal struct {
	Int    *int64   `json:"int,omitempty"`
	Float  *float64 `json:"float,omitempty"`
	String *string  `json:"string,omitempty"`
	NumDen *[2]int  `json:"num_den,omitempty"`
}

// EncoderInfo summarises an encoder for the GUI.
type EncoderInfo struct {
	Name      string          `json:"name"`
	LongName  string          `json:"long_name,omitempty"`
	MediaType string          `json:"media_type"` // "video" | "audio" | "subtitle" | "data" | "unknown"
	Options   []EncoderOption `json:"options"`
}

// EncoderOptionsByName enumerates every AVOption available on the named
// encoder: both the generic AVCodecContext options (bit_rate, g, threads, …)
// and the encoder's private options (preset, crf, tune, …). Returns an error
// if the encoder doesn't exist.
//
// The list is intentionally returned in libav declaration order — callers
// (typically the GUI) decide how to group/filter it.
func EncoderOptionsByName(name string) (EncoderInfo, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	ctx := C.alloc_encoder_ctx(cName)
	if ctx == nil {
		return EncoderInfo{}, fmt.Errorf("encoder %q not found", name)
	}
	defer C.free_ctx(ctx)

	info := EncoderInfo{Name: name}
	if ln := C.encoder_long_name(cName); ln != nil {
		info.LongName = C.GoString(ln)
	}
	info.MediaType = MediaType(C.encoder_media_type(cName)).String()

	// Generic AVCodecContext options. The class is exposed via the context
	// pointer's first field (av_class). av_opt_next walks them when given a
	// pointer to the class pointer.
	clsPtr := (**C.AVClass)(unsafe.Pointer(ctx))
	info.Options = append(info.Options, walkOptions(clsPtr, false)...)

	// Encoder's private options (priv_class). May be NULL (e.g. raw codecs).
	privCls := C.encoder_priv_class(cName)
	if privCls != nil {
		// av_opt_next requires a pointer to a pointer; create a local.
		local := privCls
		info.Options = append(info.Options, walkOptions(&local, true)...)
	}

	return info, nil
}

// walkOptions iterates an AVClass via av_opt_next, returning everything
// except CONST entries (which are folded into the parent option's Constants).
func walkOptions(clsPtr **C.AVClass, private bool) []EncoderOption {
	var out []EncoderOption
	// First pass: collect non-CONST options.
	var prev *C.AVOption
	for {
		opt := C.next_opt(clsPtr, prev)
		if opt == nil {
			break
		}
		prev = opt
		if optionType(opt) == "const" {
			continue
		}
		out = append(out, optionToEncoderOption(opt, private))
	}
	// Second pass: attach CONST children for each option that has a unit.
	for i := range out {
		if out[i].Unit == "" {
			continue
		}
		out[i].Constants = collectConstants(clsPtr, out[i].Unit)
	}
	return out
}

// collectConstants returns every AV_OPT_TYPE_CONST entry on this class whose
// unit matches the given unit string.
func collectConstants(clsPtr **C.AVClass, unit string) []EncoderOptionEnum {
	var out []EncoderOptionEnum
	var prev *C.AVOption
	for {
		opt := C.next_opt(clsPtr, prev)
		if opt == nil {
			break
		}
		prev = opt
		if optionType(opt) != "const" {
			continue
		}
		u := goStr(opt.unit)
		if u != unit {
			continue
		}
		entry := EncoderOptionEnum{
			Name:  goStr(opt.name),
			Help:  goStr(opt.help),
			Value: int64(*(*C.int64_t)(unsafe.Pointer(&opt.default_val))),
		}
		out = append(out, entry)
	}
	return out
}

func optionToEncoderOption(opt *C.AVOption, private bool) EncoderOption {
	t := optionType(opt)
	o := EncoderOption{
		Name:      goStr(opt.name),
		Help:      goStr(opt.help),
		Type:      OptionType(t),
		Unit:      goStr(opt.unit),
		Min:       float64(opt.min),
		Max:       float64(opt.max),
		IsPrivate: private,
	}
	o.Default = optionDefault(opt, t)
	return o
}

// optionType maps AV_OPT_TYPE_* to a string.
func optionType(opt *C.AVOption) string {
	switch opt._type {
	case C.AV_OPT_TYPE_FLAGS:
		return "flags"
	case C.AV_OPT_TYPE_INT:
		return "int"
	case C.AV_OPT_TYPE_INT64:
		return "int64"
	case C.AV_OPT_TYPE_DOUBLE:
		return "double"
	case C.AV_OPT_TYPE_FLOAT:
		return "float"
	case C.AV_OPT_TYPE_STRING:
		return "string"
	case C.AV_OPT_TYPE_RATIONAL:
		return "rational"
	case C.AV_OPT_TYPE_BINARY:
		return "binary"
	case C.AV_OPT_TYPE_DICT:
		return "dict"
	case C.AV_OPT_TYPE_UINT64:
		return "uint64"
	case C.AV_OPT_TYPE_CONST:
		return "const"
	case C.AV_OPT_TYPE_IMAGE_SIZE:
		return "image_size"
	case C.AV_OPT_TYPE_PIXEL_FMT:
		return "pix_fmt"
	case C.AV_OPT_TYPE_SAMPLE_FMT:
		return "sample_fmt"
	case C.AV_OPT_TYPE_VIDEO_RATE:
		return "rational"
	case C.AV_OPT_TYPE_DURATION:
		return "duration"
	case C.AV_OPT_TYPE_COLOR:
		return "color"
	case C.AV_OPT_TYPE_BOOL:
		return "bool"
	case C.AV_OPT_TYPE_CHLAYOUT:
		return "channel_layout"
	default:
		return "unknown"
	}
}

func optionDefault(opt *C.AVOption, t string) *EncoderOptionVal {
	v := &EncoderOptionVal{}
	switch t {
	case "flags", "int", "int64", "uint64", "duration":
		i := int64(*(*C.int64_t)(unsafe.Pointer(&opt.default_val)))
		v.Int = &i
	case "bool":
		i := int64(*(*C.int64_t)(unsafe.Pointer(&opt.default_val)))
		v.Int = &i
	case "double", "float":
		f := float64(*(*C.double)(unsafe.Pointer(&opt.default_val)))
		v.Float = &f
	case "string", "color", "image_size", "channel_layout":
		// default_val is a char* in the union for string-like types.
		ptr := *(**C.char)(unsafe.Pointer(&opt.default_val))
		if ptr == nil {
			return nil
		}
		s := C.GoString(ptr)
		v.String = &s
	case "rational":
		// default_val is AVRational packed in the union; libav stores it as a
		// single 64-bit value via av_d2q at runtime, so reading directly is
		// unreliable across versions. Skip for now.
		return nil
	case "pix_fmt", "sample_fmt":
		i := int64(*(*C.int64_t)(unsafe.Pointer(&opt.default_val)))
		v.Int = &i
	default:
		return nil
	}
	return v
}

func goStr(c *C.char) string {
	if c == nil {
		return ""
	}
	return C.GoString(c)
}
