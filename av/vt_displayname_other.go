// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !darwin

package av

// queryVTDisplayName is a no-op stub on non-Darwin platforms.
// VideoToolbox is only available on macOS; this function satisfies the call
// site in QueryCapabilities so the package compiles on Linux and Windows.
func queryVTDisplayName() string {
	return "Apple GPU"
}
