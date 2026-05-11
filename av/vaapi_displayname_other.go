// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !linux

package av

// queryVAAPIDisplayName is a no-op stub on non-Linux platforms.
// VA-API is only available on Linux; this function satisfies the call site in
// QueryCapabilities so the package compiles on macOS and Windows without change.
func queryVAAPIDisplayName(_ string) string {
	return "VAAPI GPU"
}
