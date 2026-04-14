// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !ffstatic

package av

// #cgo pkg-config: libavcodec libavformat libavfilter libavutil libswscale libswresample
//
// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
// #include "libswscale/swscale.h"
// #include "libswresample/swresample.h"
import "C"
