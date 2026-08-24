// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package cbs is a read-only Go port of FFmpeg's Coded Bitstream framework
// (libavcodec/cbs*.c, LGPL-2.1-or-later) for H.264, H.265 and AV1.
//
// It splits elementary-stream packets and codec extradata (Annex B, length
// prefixed avcC/hvcC, AV1 Section 5 / av1C) into NAL units / OBUs and
// decomposes each unit's headers into typed structs, optionally reporting
// every syntax element through a Tracer. The Tracer event stream can be
// rendered byte-for-byte identical to `ffmpeg -bsf:v trace_headers`
// (see NewTextTracer), which is how the port is validated against upstream.
//
// The package is cgo-free and never imports the av package: inputs are plain
// byte slices. Struct, field and syntax-element names mirror the FFmpeg
// sources (cbs_h264.h, cbs_h265.h, cbs_av1.h, cbs_sei.h) so the port can be
// audited line-for-line against the C templates.
//
// Unlike FFmpeg, a malformed unit does not abort the fragment: the error is
// recorded on the Unit and parsing continues with the next unit.
package cbs
