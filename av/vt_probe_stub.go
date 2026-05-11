// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !darwin

package av

// VTPlatformCapabilities holds codecs discovered by querying the VideoToolbox
// platform directly. On non-Darwin platforms this is always empty.
type VTPlatformCapabilities struct {
	ExtraEncoders []HWCodecInfo
	ExtraDecoders []HWCodecInfo
}

// QueryVTCapabilities is a no-op on non-Darwin platforms.
func QueryVTCapabilities() VTPlatformCapabilities { return VTPlatformCapabilities{} }
