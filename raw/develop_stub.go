// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_libraw

package raw

// Default build: no LibRaw. The high-precision develop is unavailable, mirroring [Decode].

// DecodeDevelop is unavailable without the `with_libraw` build tag; it errors cleanly so callers
// compile and run with no native dependency (and fall back to [Decode]/the preview).
func DecodeDevelop(path string) (Develop, error) { return Develop{}, ErrUnsupported }
