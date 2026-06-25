// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"

	"github.com/MediaMolder/MediaMolder/raw"
)

// rawCapabilities is the JSON shape returned by GET /api/raw-capabilities. It lets the GUI tell
// whether this binary can develop camera RAW (the bundled LibRaw is built in) so it can surface
// the raw_decode node's readiness without the user having to run a job to find out.
type rawCapabilities struct {
	Capable bool   `json:"capable"`
	Version string `json:"version"` // pinned LibRaw version (informational)
}

// handleRawCapabilities implements GET /api/raw-capabilities. Capability is a compile-time fact
// (raw.Capable() reflects the with_libraw build tag), so there is nothing to probe.
func handleRawCapabilities(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rawCapabilities{
		Capable: raw.Capable(),
		Version: raw.LibRawVersion,
	})
}
