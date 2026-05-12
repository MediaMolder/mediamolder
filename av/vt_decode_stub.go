// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !darwin

package av

// IsVTCodec reports whether codecTag is a VT-native codec.
// On non-Darwin platforms this always returns false.
func IsVTCodec(_ uint32) bool { return false }

// VTDecoderContext is a stub on non-Darwin platforms.
// OpenVTDecoder always returns an error on these platforms.
type VTDecoderContext struct{}

// OpenVTDecoder returns an error on non-Darwin platforms.
func OpenVTDecoder(_ *InputFormatContext, streamIndex int) (*VTDecoderContext, error) {
	return nil, errNoVTSupport
}

var errNoVTSupport = &Err{Code: -38, Message: "VideoToolbox not available on this platform"}

func (d *VTDecoderContext) SendPacket(_ *Packet) error  { return errNoVTSupport }
func (d *VTDecoderContext) ReceiveFrame(_ *Frame) error { return errNoVTSupport }
func (d *VTDecoderContext) Flush() error                { return nil }
func (d *VTDecoderContext) Close() error                { return nil }
