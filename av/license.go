// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
import "C"

import "strings"

// LicenseLevel describes the effective license of the linked FFmpeg libraries.
type LicenseLevel int

const (
	// LicenseLGPL21 is the default FFmpeg license (LGPL-2.1-or-later).
	LicenseLGPL21 LicenseLevel = iota
	// LicenseLGPL3 applies when FFmpeg was built with --enable-version3.
	LicenseLGPL3
	// LicenseGPL2 applies when FFmpeg was built with --enable-gpl.
	LicenseGPL2
	// LicenseGPL3 applies when FFmpeg was built with --enable-gpl --enable-version3.
	LicenseGPL3
	// LicenseNonfree applies when FFmpeg was built with --enable-nonfree.
	LicenseNonfree
)

// String returns the SPDX-style license identifier.
func (l LicenseLevel) String() string {
	switch l {
	case LicenseLGPL3:
		return "LGPL-3.0-or-later"
	case LicenseGPL2:
		return "GPL-2.0-or-later"
	case LicenseGPL3:
		return "GPL-3.0-or-later"
	case LicenseNonfree:
		return "nonfree (non-redistributable)"
	default:
		return "LGPL-2.1-or-later"
	}
}

// FFmpegConfiguration returns the ./configure flags used to build the linked
// libavcodec, as reported by avcodec_configuration().
func FFmpegConfiguration() string {
	return C.GoString(C.avcodec_configuration())
}

// DetectLicense inspects the linked FFmpeg's build configuration and returns
// the most restrictive applicable license level.
func DetectLicense() LicenseLevel {
	cfg := FFmpegConfiguration()
	flags := strings.Fields(cfg)

	hasGPL := false
	hasVersion3 := false
	hasNonfree := false

	for _, f := range flags {
		switch f {
		case "--enable-gpl":
			hasGPL = true
		case "--enable-version3":
			hasVersion3 = true
		case "--enable-nonfree":
			hasNonfree = true
		}
	}

	if hasNonfree {
		return LicenseNonfree
	}
	if hasGPL && hasVersion3 {
		return LicenseGPL3
	}
	if hasGPL {
		return LicenseGPL2
	}
	if hasVersion3 {
		return LicenseLGPL3
	}
	return LicenseLGPL21
}
