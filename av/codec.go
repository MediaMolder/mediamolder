// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
import "C"

import "unsafe"

// FindEncoder reports whether the named encoder is available in this build.
func FindEncoder(name string) bool {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))
	return C.avcodec_find_encoder_by_name(cName) != nil
}
