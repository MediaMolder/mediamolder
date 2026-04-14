// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
import "C"

import "fmt"

// Minimum required major versions for FFmpeg 8.1.
const (
	minAvCodecMajor  = 62
	minAvFormatMajor = 62
	minAvFilterMajor = 11
	minAvUtilMajor   = 60
)

// CheckVersion verifies that the linked libav* libraries meet the minimum
// version requirements for MediaMolder (FFmpeg 8.1+). It returns an error
// describing any library that is too old.
func CheckVersion() error {
	type lib struct {
		name  string
		major uint
		min   uint
	}

	libs := []lib{
		{"libavcodec", uint(C.LIBAVCODEC_VERSION_MAJOR), minAvCodecMajor},
		{"libavformat", uint(C.LIBAVFORMAT_VERSION_MAJOR), minAvFormatMajor},
		{"libavfilter", uint(C.LIBAVFILTER_VERSION_MAJOR), minAvFilterMajor},
		{"libavutil", uint(C.LIBAVUTIL_VERSION_MAJOR), minAvUtilMajor},
	}

	for _, l := range libs {
		if l.major < l.min {
			return fmt.Errorf(
				"mediamolder requires %s major version >= %d (FFmpeg 8.1+); got %d.\n"+
					"Install FFmpeg 8.1+ and rebuild.",
				l.name, l.min, l.major,
			)
		}
	}
	return nil
}

// LibVersions returns a human-readable summary of the linked library versions.
func LibVersions() string {
	return fmt.Sprintf(
		"libavcodec=%d libavformat=%d libavfilter=%d libavutil=%d",
		uint(C.LIBAVCODEC_VERSION_MAJOR),
		uint(C.LIBAVFORMAT_VERSION_MAJOR),
		uint(C.LIBAVFILTER_VERSION_MAJOR),
		uint(C.LIBAVUTIL_VERSION_MAJOR),
	)
}
