// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !linux

package av

// AMFCodecCaps holds AMD AMF encoder capability data for one codec.
// On non-Linux platforms (where libamfrt64.so.1 is not available) this type
// exists for API compatibility but QueryAMFCaps always returns nil.
type AMFCodecCaps struct {
	CodecName      string `json:"codec_name"`
	MaxNumOfStreams int    `json:"max_num_of_streams"`
	MinWidth       int    `json:"min_width"`
	MaxWidth       int    `json:"max_width"`
	MinHeight      int    `json:"min_height"`
	MaxHeight      int    `json:"max_height"`
}

// QueryAMFCaps is a no-op stub on non-Linux platforms.
func QueryAMFCaps() []AMFCodecCaps { return nil }
