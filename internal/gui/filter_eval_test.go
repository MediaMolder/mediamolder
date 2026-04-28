// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleFilterEvalExpression covers the happy path (drawtext
// timeline gate), variable overrides via query string, evaluation
// failures (unknown identifier), and the malformed-request guards.
func TestHandleFilterEvalExpression(t *testing.T) {
	cases := []struct {
		name       string
		path       string
		wantStatus int
		wantOk     bool
		wantValue  float64
		wantErrSub string // substring match on response.Error
	}{
		{
			name:       "drawtext_enable_between_true",
			path:       "/api/filters/drawtext/eval-expression?expr=between(t%2C1%2C8)&t=4",
			wantStatus: http.StatusOK,
			wantOk:     true,
			wantValue:  1,
		},
		{
			name:       "drawtext_enable_between_false",
			path:       "/api/filters/drawtext/eval-expression?expr=between(t%2C1%2C8)&t=0.5",
			wantStatus: http.StatusOK,
			wantOk:     true,
			wantValue:  0,
		},
		{
			name:       "overlay_x_arithmetic",
			path:       "/api/filters/overlay/eval-expression?expr=W-w&W=1920&w=320",
			wantStatus: http.StatusOK,
			wantOk:     true,
			wantValue:  1600,
		},
		{
			name:       "drawtext_scrolling_x_default_zero",
			path:       "/api/filters/drawtext/eval-expression?expr=w-mod(40*t%2Cw%2Btw)&w=1280&tw=120&t=0",
			wantStatus: http.StatusOK,
			wantOk:     true,
			wantValue:  1280, // w - mod(0, ...) = w
		},
		{
			name:       "syntax_error",
			path:       "/api/filters/drawtext/eval-expression?expr=between(t%2C1%2C",
			wantStatus: http.StatusOK,
			wantOk:     false,
			wantErrSub: "averror",
		},
		{
			name:       "unknown_filter_falls_back",
			path:       "/api/filters/unknown_xyz/eval-expression?expr=t%2B1&t=5",
			wantStatus: http.StatusOK,
			wantOk:     true,
			wantValue:  6,
		},
		{
			name:       "missing_expr",
			path:       "/api/filters/drawtext/eval-expression",
			wantStatus: http.StatusBadRequest,
		},
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/filters/{name}/eval-expression", handleFilterEvalExpression)

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, tc.path, nil)
			rec := httptest.NewRecorder()
			mux.ServeHTTP(rec, req)
			if rec.Code != tc.wantStatus {
				t.Fatalf("status: got %d want %d (body=%s)",
					rec.Code, tc.wantStatus, rec.Body.String())
			}
			if tc.wantStatus != http.StatusOK {
				return
			}
			var got evalExpressionResponse
			if err := json.NewDecoder(rec.Body).Decode(&got); err != nil {
				t.Fatalf("decode response: %v (body=%s)", err, rec.Body.String())
			}
			if got.Ok != tc.wantOk {
				t.Fatalf("ok: got %v want %v (error=%q)",
					got.Ok, tc.wantOk, got.Error)
			}
			if tc.wantOk && got.Value != tc.wantValue {
				t.Fatalf("value: got %v want %v", got.Value, tc.wantValue)
			}
			if tc.wantErrSub != "" && !strings.Contains(got.Error, tc.wantErrSub) {
				t.Fatalf("error substring: got %q want substring %q",
					got.Error, tc.wantErrSub)
			}
		})
	}
}
