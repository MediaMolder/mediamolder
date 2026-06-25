// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build !with_libraw

package raw

import "image"

// This is the default build: no LibRaw. Capable reports false and Decode errors cleanly, so
// callers compile and run with no native dependency. Build with `-tags with_libraw` (and bundle
// the static library via scripts/bundle-libraw.sh) to enable real RAW develop.

// Capable reports whether this build can develop RAW. The default build cannot.
func Capable() bool { return false }

// Decode is unavailable without the `with_libraw` build tag.
func Decode(path string) (image.Image, error) { return nil, ErrUnsupported }
