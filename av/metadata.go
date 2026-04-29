// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// Container- and stream-level metadata + chapter IO. Used by the
// pipeline runtime to carry FFmpeg `-metadata`, `-map_metadata` and
// `-map_chapters` semantics across the av binding boundary without
// each call site having to touch AVDictionary directly.

// #include <stdint.h>
// #include "libavformat/avformat.h"
// #include "libavutil/dict.h"
// #include "libavutil/mem.h"
// #include "libavutil/opt.h"
//
// // dict_set is a thin wrapper so the Go side can set a single key
// // without juggling AVDictionary** indirection.
// static int dict_set(AVDictionary **d, const char *key, const char *val) {
//     return av_dict_set(d, key, val, 0);
// }
//
// // chapter_alloc allocates and zero-initialises a single AVChapter
// // and appends it to ctx->chapters. Returns a pointer to the new
// // chapter on success, NULL on OOM.
// static AVChapter* chapter_alloc(AVFormatContext *ctx) {
//     AVChapter **arr = av_realloc_array(ctx->chapters,
//         (size_t)(ctx->nb_chapters + 1), sizeof(*arr));
//     if (!arr) return NULL;
//     ctx->chapters = arr;
//     AVChapter *ch = av_mallocz(sizeof(*ch));
//     if (!ch) return NULL;
//     ctx->chapters[ctx->nb_chapters] = ch;
//     ctx->nb_chapters++;
//     return ch;
// }
//
// // stream_set_disposition forwards to av_opt_set on the AVStream's
// // AVClass, which parses `+`-separated AV_DISPOSITION_* flag names
// // exactly the way the FFmpeg CLI does for `-disposition:s:...`.
// static int stream_set_disposition(AVStream *st, const char *val) {
//     return av_opt_set(st, "disposition", val, 0);
// }
import "C"

import (
	"strings"
	"unsafe"
)

// ChapterInfo is the Go-side projection of an AVChapter. Start/End are
// expressed in seconds (already converted from the chapter's native
// time_base) so callers do not have to thread rationals around.
type ChapterInfo struct {
	ID       int64
	Start    float64
	End      float64
	Title    string
	Metadata map[string]string
}

// Metadata returns the container-level metadata of the input as a flat
// key/value map. Returns nil when the input has no metadata.
func (f *InputFormatContext) Metadata() map[string]string {
	if f == nil || f.p == nil {
		return nil
	}
	return dictToMap(f.p.metadata)
}

// StreamMetadata returns the per-stream metadata of stream idx as a
// flat key/value map. Returns nil when the stream is out of range or
// has no metadata.
func (f *InputFormatContext) StreamMetadata(idx int) map[string]string {
	if f == nil || f.p == nil {
		return nil
	}
	if idx < 0 || idx >= int(f.p.nb_streams) {
		return nil
	}
	streams := (*[1 << 20]*C.AVStream)(unsafe.Pointer(f.p.streams))
	return dictToMap(streams[idx].metadata)
}

// Chapters returns the chapter table of the input, with Start/End
// converted to seconds. Returns nil when the input has no chapters.
func (f *InputFormatContext) Chapters() []ChapterInfo {
	if f == nil || f.p == nil || f.p.nb_chapters == 0 {
		return nil
	}
	chapters := (*[1 << 20]*C.AVChapter)(unsafe.Pointer(f.p.chapters))
	out := make([]ChapterInfo, 0, int(f.p.nb_chapters))
	for i := 0; i < int(f.p.nb_chapters); i++ {
		ch := chapters[i]
		if ch == nil {
			continue
		}
		// time_base is num/den seconds per tick; Start/End are in ticks.
		secPerTick := float64(ch.time_base.num) / float64(ch.time_base.den)
		ci := ChapterInfo{
			ID:       int64(ch.id),
			Start:    float64(ch.start) * secPerTick,
			End:      float64(ch.end) * secPerTick,
			Metadata: dictToMap(ch.metadata),
		}
		if ci.Metadata != nil {
			ci.Title = ci.Metadata["title"]
		}
		out = append(out, ci)
	}
	return out
}

// SetMetadata replaces the container-level metadata of the output with
// the given key/value pairs. Empty maps clear existing metadata.
// Must be called after any AddStream / AddStreamFromInput and before
// WriteHeader (FFmpeg muxers typically only honour metadata set before
// the header is written).
func (f *OutputFormatContext) SetMetadata(kv map[string]string) error {
	if f == nil || f.p == nil {
		return nil
	}
	// Free any existing dict so successive calls do not accumulate
	// stale entries on the same context.
	C.av_dict_free(&f.p.metadata)
	return setDictFromMap(&f.p.metadata, kv)
}

// SetStreamMetadata replaces the metadata of output stream idx with the
// given key/value pairs. Must be called between AddStream and
// WriteHeader. Returns nil for an empty / nil map (no-op).
func (f *OutputFormatContext) SetStreamMetadata(idx int, kv map[string]string) error {
	if f == nil || f.p == nil {
		return nil
	}
	if idx < 0 || idx >= int(f.p.nb_streams) {
		return nil
	}
	streams := (*[1 << 20]*C.AVStream)(unsafe.Pointer(f.p.streams))
	st := streams[idx]
	C.av_dict_free(&st.metadata)
	return setDictFromMap(&st.metadata, kv)
}

// SetStreamDisposition sets the disposition flags of output stream idx
// from a `+`-separated list of AV_DISPOSITION_* flag names (e.g.
// `"default"`, `"default+forced"`, `"hearing_impaired"`). Mirrors
// `-disposition:s:<type>:<idx>` in the FFmpeg CLI by forwarding to
// av_opt_set on the AVStream's AVClass, which is the same code path
// fftools/ffmpeg_mux_init.c::set_dispositions uses to parse the value.
// Empty / whitespace-only values are no-ops. Must be called between
// AddStream and WriteHeader.
func (f *OutputFormatContext) SetStreamDisposition(idx int, disp string) error {
	if f == nil || f.p == nil {
		return nil
	}
	if idx < 0 || idx >= int(f.p.nb_streams) {
		return nil
	}
	disp = strings.TrimSpace(disp)
	if disp == "" {
		return nil
	}
	streams := (*[1 << 20]*C.AVStream)(unsafe.Pointer(f.p.streams))
	st := streams[idx]
	cVal := C.CString(disp)
	defer C.free(unsafe.Pointer(cVal))
	if rc := C.stream_set_disposition(st, cVal); rc < 0 {
		return newErr(rc)
	}
	return nil
}

// AddChapter appends a chapter to the output with start/end expressed in
// seconds. The chapter time_base is fixed at 1/1_000_000 (microseconds)
// to match AV_TIME_BASE so the values round-trip cleanly through any
// muxer that supports chapters (matroska, mp4, ogg, ffmetadata, ...).
// title is a convenience for the canonical "title" metadata key; extra
// per-chapter metadata may be passed via meta. Must be called before
// WriteHeader.
func (f *OutputFormatContext) AddChapter(id int64, startSec, endSec float64, title string, meta map[string]string) error {
	if f == nil || f.p == nil {
		return nil
	}
	ch := C.chapter_alloc(f.p)
	if ch == nil {
		return &Err{Code: -12, Message: "av_realloc_array/av_mallocz: out of memory"}
	}
	ch.id = C.int64_t(id)
	ch.time_base = C.AVRational{num: 1, den: 1_000_000}
	ch.start = C.int64_t(startSec * 1_000_000)
	ch.end = C.int64_t(endSec * 1_000_000)
	if title != "" {
		cKey := C.CString("title")
		cVal := C.CString(title)
		C.dict_set(&ch.metadata, cKey, cVal)
		C.free(unsafe.Pointer(cKey))
		C.free(unsafe.Pointer(cVal))
	}
	for k, v := range meta {
		if k == "" || k == "title" { // title already applied above
			continue
		}
		cKey := C.CString(k)
		cVal := C.CString(v)
		C.dict_set(&ch.metadata, cKey, cVal)
		C.free(unsafe.Pointer(cKey))
		C.free(unsafe.Pointer(cVal))
	}
	return nil
}

// dictToMap copies an AVDictionary into a Go map. Returns nil for an
// empty / nil dictionary.
func dictToMap(d *C.AVDictionary) map[string]string {
	if d == nil {
		return nil
	}
	out := make(map[string]string)
	var entry *C.AVDictionaryEntry
	emptyKey := C.CString("")
	defer C.free(unsafe.Pointer(emptyKey))
	for {
		entry = C.av_dict_iterate(d, entry)
		if entry == nil {
			break
		}
		out[C.GoString(entry.key)] = C.GoString(entry.value)
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// setDictFromMap inserts every (k, v) from kv into *dp. Empty keys are
// skipped. Returns the first non-zero AVERROR encountered.
func setDictFromMap(dp **C.AVDictionary, kv map[string]string) error {
	for k, v := range kv {
		if k == "" {
			continue
		}
		cKey := C.CString(k)
		cVal := C.CString(v)
		ret := C.dict_set(dp, cKey, cVal)
		C.free(unsafe.Pointer(cKey))
		C.free(unsafe.Pointer(cVal))
		if ret < 0 {
			return newErr(ret)
		}
	}
	return nil
}
