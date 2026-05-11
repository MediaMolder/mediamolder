// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !linux && !darwin

package av

// queryQSVDisplayName returns a generic name for the Intel QSV device on
// platforms other than Linux (Windows, etc.) where the underlying device
// path is not accessible without the MFX SDK.
func queryQSVDisplayName() string {
	return "Intel GPU (QSV)"
}
