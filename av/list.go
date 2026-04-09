package av

// #include "libavcodec/avcodec.h"
// #include "libavfilter/avfilter.h"
// #include "libavformat/avformat.h"
//
// // Codec iteration wrapper.
// static const AVCodec *next_codec(void **opaque) {
//     return av_codec_iterate(opaque);
// }
// // Filter iteration wrapper.
// static const AVFilter *next_filter(void **opaque) {
//     return av_filter_iterate(opaque);
// }
// // Muxer iteration wrapper.
// static const AVOutputFormat *next_muxer(void **opaque) {
//     return av_muxer_iterate(opaque);
// }
// // Demuxer iteration wrapper.
// static const AVInputFormat *next_demuxer(void **opaque) {
//     return av_demuxer_iterate(opaque);
// }
import "C"

import "unsafe"

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
		out = append(out, info)
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
