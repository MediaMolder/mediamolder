// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build darwin

package av

// queryQSVDisplayName is a no-op stub on macOS. Intel QSV is not available on
// macOS; this function is only referenced by the platform-agnostic
// QueryCapabilities() path and should never be called on Apple hardware.
func queryQSVDisplayName() string {
	return "Intel GPU (QSV)"
}
