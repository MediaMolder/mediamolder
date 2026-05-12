// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavfilter/avfilter.h"
// #include "libavutil/opt.h"
// #include <stdlib.h>
//
// // Look up an AVFilter by name. Returns NULL when not found.
// static const AVFilter *find_filter(const char *name) {
//     return avfilter_get_by_name(name);
// }
//
// // Return the filter's private AVClass (the class whose AVOptions
// // describe the filter-specific knobs like trim's start/end). May be
// // NULL for filters with no options.
// static const AVClass *filter_priv_class(const AVFilter *f) {
//     if (!f) return NULL;
//     return f->priv_class;
// }
//
// // Iterate AVOptions on a class (same semantics as next_opt in options.go
// // but declared here so filter_options.go can call it independently).
// static const AVOption *filter_next_opt(const AVClass **cls_ptr, const AVOption *prev) {
//     return av_opt_next(cls_ptr, prev);
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// FilterOptionsInfo describes an FFmpeg filter and its tunable options for
// the GUI Inspector. Mirrors EncoderInfo but for libavfilter.
type FilterOptionsInfo struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Options     []EncoderOption `json:"options"`
}

// FilterOptionsByName enumerates every AVOption exposed by the named
// filter's private AVClass. Returns an error when the filter doesn't
// exist. The list is returned in libav declaration order; the GUI
// decides how to group/render it.
func FilterOptionsByName(name string) (FilterOptionsInfo, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	f := C.find_filter(cName)
	if f == nil {
		return FilterOptionsInfo{}, fmt.Errorf("filter %q not found", name)
	}

	info := FilterOptionsInfo{
		Name:        C.GoString(f.name),
		Description: C.GoString(f.description),
	}

	priv := C.filter_priv_class(f)
	if priv != nil {
		local := priv
		info.Options = filterWalkOptions(&local)
	}

	return info, nil
}

// filterWalkOptions is like walkOptions but deduplicates AVOption entries
// that share the same struct offset. libavfilter sometimes registers both a
// short alias (e.g. "w") and a longer canonical name (e.g. "width") that
// both point to the same private-context field via identical OFFSET()
// values. Keeping only the first occurrence (declaration order) avoids
// showing duplicate rows in the Inspector for filters like scale.
func filterWalkOptions(clsPtr **C.AVClass) []EncoderOption {
	// First pass: collect the name of the first (canonical) option seen at
	// each AVOption.offset value. Options with offset == 0 are not
	// deduplicated (some flags options legitimately use offset 0).
	seenOffset := make(map[int]struct{})
	keepName := make(map[string]struct{})
	var prev *C.AVOption
	for {
		opt := C.filter_next_opt(clsPtr, prev)
		if opt == nil {
			break
		}
		prev = opt
		if optionType(opt) == "const" {
			continue
		}
		off := int(opt.offset)
		if _, seen := seenOffset[off]; !seen || off == 0 {
			seenOffset[off] = struct{}{}
			keepName[C.GoString(opt.name)] = struct{}{}
		}
	}

	// Second pass: use the shared walkOptions to build fully-populated
	// EncoderOption values, then drop any alias names not in keepName.
	all := walkOptions(clsPtr, true)
	out := all[:0:0]
	for _, o := range all {
		if _, ok := keepName[o.Name]; ok {
			out = append(out, o)
		}
	}
	return out
}
