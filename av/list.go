// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavdevice/avdevice.h"
// #include "libavfilter/avfilter.h"
// #include "libavformat/avformat.h"
// #include <stdlib.h>
//
// // Codec iteration wrapper.
// static const AVCodec *next_codec(void **opaque) {
//     return av_codec_iterate(opaque);
// }
// // Filter iteration wrapper.
// static const AVFilter *next_filter(void **opaque) {
//     return av_filter_iterate(opaque);
// }
// // Read the media type of pad `idx` on `f`'s input (is_output=0) or output
// // (is_output=1) pad list. Returns AVMEDIA_TYPE_UNKNOWN when the index is
// // out of range or the filter has dynamic pads (NULL pad pointer).
// static int filter_pad_type(const AVFilter *f, int is_output, int idx) {
//     const AVFilterPad *pads = is_output ? f->outputs : f->inputs;
//     if (!pads) return AVMEDIA_TYPE_UNKNOWN;
//     return avfilter_pad_get_type(pads, idx);
// }
// // Muxer iteration wrapper.
// static const AVOutputFormat *next_muxer(void **opaque) {
//     return av_muxer_iterate(opaque);
// }
// // Demuxer iteration wrapper.
// static const AVInputFormat *next_demuxer(void **opaque) {
//     return av_demuxer_iterate(opaque);
// }
// // Device info accessors — isolate struct field access in C so the
// // CGO pointer chain is safe across Go GC moves.
// static AVDeviceInfo *device_info_at(AVDeviceInfoList *list, int i) {
//     return list->devices[i];
// }
// static char *device_info_name(AVDeviceInfo *d) { return d->device_name; }
// static char *device_info_desc(AVDeviceInfo *d) { return d->device_description; }
import "C"

import (
	"fmt"
	"sync"
	"unsafe"
)

// CodecInfo describes an available codec.
type CodecInfo struct {
	Name      string
	LongName  string
	IsEncoder bool
	IsDecoder bool
	Type      string // "video", "audio", "subtitle", "data", "unknown"
}

// ListCodecs returns all available codecs.
func ListCodecs() []CodecInfo {
	var out []CodecInfo
	var opaque unsafe.Pointer
	for {
		c := C.next_codec(&opaque)
		if c == nil {
			break
		}
		info := CodecInfo{
			Name:     C.GoString(c.name),
			LongName: C.GoString(c.long_name),
		}
		if C.av_codec_is_encoder(c) != 0 {
			info.IsEncoder = true
		}
		if C.av_codec_is_decoder(c) != 0 {
			info.IsDecoder = true
		}
		switch c._type {
		case C.AVMEDIA_TYPE_VIDEO:
			info.Type = "video"
		case C.AVMEDIA_TYPE_AUDIO:
			info.Type = "audio"
		case C.AVMEDIA_TYPE_SUBTITLE:
			info.Type = "subtitle"
		case C.AVMEDIA_TYPE_DATA:
			info.Type = "data"
		default:
			info.Type = "unknown"
		}
		out = append(out, info)
	}
	return out
}

// FilterInfo describes an available filter.
type FilterInfo struct {
	Name        string
	Description string
	NumInputs   int
	NumOutputs  int
	// InputTypes / OutputTypes are the media-type names ("video", "audio",
	// "subtitle", "data") of each statically-declared pad. Empty when the
	// filter has dynamic pads (e.g. concat, amix); callers should treat
	// dynamic-pad filters as media-type-agnostic.
	InputTypes  []string
	OutputTypes []string
}

// ListFilters returns all available filters.
func ListFilters() []FilterInfo {
	var out []FilterInfo
	var opaque unsafe.Pointer
	for {
		f := C.next_filter(&opaque)
		if f == nil {
			break
		}
		info := FilterInfo{
			Name:        C.GoString(f.name),
			Description: C.GoString(f.description),
		}
		// nb_inputs/nb_outputs: -1 means dynamic.
		info.NumInputs = int(C.avfilter_filter_pad_count(f, 0))
		info.NumOutputs = int(C.avfilter_filter_pad_count(f, 1))
		info.InputTypes = padTypes(f, 0, info.NumInputs)
		info.OutputTypes = padTypes(f, 1, info.NumOutputs)
		out = append(out, info)
	}
	return out
}

// padTypes returns the media-type names of each pad on the given side. n is
// the pad count returned by avfilter_filter_pad_count; a non-positive count
// (dynamic pads) yields nil so the catalog can mark the filter as
// media-type-agnostic.
func padTypes(f *C.AVFilter, isOutput, n int) []string {
	if n <= 0 {
		return nil
	}
	out := make([]string, 0, n)
	for i := 0; i < n; i++ {
		t := MediaType(C.filter_pad_type(f, C.int(isOutput), C.int(i))).String()
		if t == "unknown" {
			continue
		}
		out = append(out, t)
	}
	return out
}

// FormatInfo describes an available muxer or demuxer.
type FormatInfo struct {
	Name      string
	LongName  string
	IsMuxer   bool
	IsDemuxer bool
}

// ListFormats returns all available muxers and demuxers.
func ListFormats() []FormatInfo {
	seen := make(map[string]*FormatInfo)

	var opaque unsafe.Pointer
	for {
		m := C.next_muxer(&opaque)
		if m == nil {
			break
		}
		name := C.GoString(m.name)
		if info, ok := seen[name]; ok {
			info.IsMuxer = true
		} else {
			seen[name] = &FormatInfo{
				Name:     name,
				LongName: C.GoString(m.long_name),
				IsMuxer:  true,
			}
		}
	}

	opaque = nil
	for {
		d := C.next_demuxer(&opaque)
		if d == nil {
			break
		}
		name := C.GoString(d.name)
		if info, ok := seen[name]; ok {
			info.IsDemuxer = true
		} else {
			seen[name] = &FormatInfo{
				Name:      name,
				LongName:  C.GoString(d.long_name),
				IsDemuxer: true,
			}
		}
	}

	out := make([]FormatInfo, 0, len(seen))
	for _, info := range seen {
		out = append(out, *info)
	}
	return out
}

// DeviceInfo describes a capture or playback device enumerated by libavdevice.
type DeviceInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
}

// avdeviceOnce ensures avdevice_register_all is called exactly once.
// The function is a no-op in FFmpeg 4.0+ (devices auto-register) but calling
// it on older builds prevents "no such device" failures.
var avdeviceOnce sync.Once

// ListDevices enumerates the capture devices available for the given input
// format name (e.g. "dshow" on Windows, "avfoundation" on macOS, "v4l2" on
// Linux). Returns an empty slice when the format reports no devices.
//
// This call can block for several seconds on Windows (dshow enumerates COM
// objects which may be locked by other processes). The caller is responsible
// for applying a deadline — typically by running ListDevices in a goroutine
// and selecting on a time.After.
func ListDevices(format string) ([]DeviceInfo, error) {
	avdeviceOnce.Do(func() { C.avdevice_register_all() })

	cfmt := C.CString(format)
	defer C.free(unsafe.Pointer(cfmt))

	ifmt := C.av_find_input_format(cfmt)
	if ifmt == nil {
		return nil, fmt.Errorf("av: unknown input format %q", format)
	}

	var list *C.AVDeviceInfoList
	ret := C.avdevice_list_input_sources(ifmt, nil, nil, &list)
	if ret < 0 {
		return nil, newErr(ret)
	}
	if list == nil {
		return nil, nil
	}
	defer C.avdevice_free_list_devices(&list)

	n := int(list.nb_devices)
	out := make([]DeviceInfo, n)
	for i := range out {
		d := C.device_info_at(list, C.int(i))
		out[i] = DeviceInfo{
			Name:        C.GoString(C.device_info_name(d)),
			Description: C.GoString(C.device_info_desc(d)),
		}
	}
	return out, nil
}
