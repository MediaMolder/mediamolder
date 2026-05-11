// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build linux

package av

// queryQSVDisplayName returns a human-readable name for the Intel QSV device.
//
// QSV on Linux is built on top of VA-API. The underlying DRI render node is
// typically renderD128 (configurable via the "child_device" option passed to
// av_hwdevice_ctx_create, but we don't have that stored). We attempt to read
// the PCI IDs for renderD128 and fall back to "Intel GPU (QSV)".
func queryQSVDisplayName() string {
	name := queryVAAPIDisplayName("/dev/dri/renderD128")
	if name == "/dev/dri/renderD128" {
		// PCI lookup found nothing useful — use a generic Intel fallback.
		return "Intel GPU (QSV)"
	}
	return name
}
