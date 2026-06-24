// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MediaMolder/MediaMolder/raw"
)

func TestHandleRawCapabilities(t *testing.T) {
	rr := httptest.NewRecorder()
	handleRawCapabilities(rr, httptest.NewRequest(http.MethodGet, "/api/raw-capabilities", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}
	var got rawCapabilities
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if got.Capable != raw.Capable() {
		t.Errorf("capable = %v, want %v", got.Capable, raw.Capable())
	}
	if got.Version != raw.LibRawVersion {
		t.Errorf("version = %q, want %q", got.Version, raw.LibRawVersion)
	}
}
