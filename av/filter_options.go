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
		info.Options = walkOptions(&local, true)
	}

	return info, nil
}
