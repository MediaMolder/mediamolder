// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHandleListTransitions(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/transitions", nil)
	rec := httptest.NewRecorder()

	handleListTransitions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var names []string
	if err := json.Unmarshal(rec.Body.Bytes(), &names); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	if len(names) == 0 {
		t.Fatal("transitions list is empty")
	}
	have := map[string]bool{}
	for _, n := range names {
		have[n] = true
	}
	for _, n := range []string{"dissolve", "wipeleft", "zoomin"} {
		if !have[n] {
			t.Errorf("transitions list missing %q", n)
		}
	}
}
