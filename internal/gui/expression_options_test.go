// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// TestIsExpressionOption covers the curated registry's positive,
// negative, and unknown-filter branches (Wave 5 #19).
func TestIsExpressionOption(t *testing.T) {
	cases := []struct {
		filter, opt string
		want        bool
	}{
		{"drawtext", "x", true},
		{"drawtext", "enable", true},
		{"drawtext", "fontfile", false}, // not an expression
		{"overlay", "x", true},
		{"setpts", "expr", true},
		{"asetpts", "expr", true},
		{"scale", "w", true},
		{"scale", "eval", false},      // explicitly false: it's an enum
		{"scale", "format", false},    // not in registry
		{"unknown_xyz", "any", false}, // unknown filter
	}
	for _, c := range cases {
		if got := IsExpressionOption(c.filter, c.opt); got != c.want {
			t.Errorf("IsExpressionOption(%q,%q): got %v want %v", c.filter, c.opt, got, c.want)
		}
	}
}

// TestFilterExprVariablesFallback ensures unknown filters fall back
// to the universal timeline-enable variable set.
func TestFilterExprVariables(t *testing.T) {
	got := FilterExprVariables("drawtext")
	if len(got) == 0 || got[0] == "t" && len(got) < 5 {
		t.Errorf("drawtext variables looked truncated: %v", got)
	}
	got = FilterExprVariables("unknown_xyz")
	want := []string{"t", "n", "pos", "w", "h"}
	if len(got) != len(want) {
		t.Fatalf("fallback variables: got %v want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("fallback[%d]: got %q want %q", i, got[i], want[i])
		}
	}
}

// TestHandleFilterOptionsAnnotatesExpression hits the live
// /api/filters/{name}/options endpoint against a real libavfilter
// build and asserts that an expression-typed option (overlay.x) is
// stamped with `expression: true` + the curated variable list, while
// a non-expression option on the same filter is not.
func TestHandleFilterOptionsAnnotatesExpression(t *testing.T) {
	// Reset the cache so the annotation logic actually runs.
	filterOptionsCacheMu.Lock()
	filterOptionsCache = map[string]av.FilterOptionsInfo{}
	filterOptionsCacheMu.Unlock()

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/filters/{name}/options", handleFilterOptions)
	srv := httptest.NewServer(mux)
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/api/filters/overlay/options")
	if err != nil {
		t.Fatalf("GET: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status: %d", resp.StatusCode)
	}
	var info av.FilterOptionsInfo
	if err := json.NewDecoder(resp.Body).Decode(&info); err != nil {
		t.Fatalf("decode: %v", err)
	}

	var x, format *av.EncoderOption
	for i := range info.Options {
		switch info.Options[i].Name {
		case "x":
			x = &info.Options[i]
		case "format":
			format = &info.Options[i]
		}
	}
	if x == nil {
		t.Fatal("overlay.x option not found")
	}
	if !x.Expression {
		t.Errorf("overlay.x: Expression=false, want true")
	}
	if len(x.Variables) == 0 {
		t.Errorf("overlay.x: Variables empty")
	}
	// Sanity: at least one well-known overlay var present.
	found := false
	for _, v := range x.Variables {
		if v == "main_w" || v == "W" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("overlay.x: Variables missing main_w/W: %v", x.Variables)
	}
	if format != nil && format.Expression {
		t.Errorf("overlay.format: Expression=true, want false (not in registry)")
	}
}
