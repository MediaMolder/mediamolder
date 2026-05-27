// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs
// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"io"
	"net/http"
	"strings"
	"testing"
)

func fakeResponse(code int, body string) *http.Response {
	return &http.Response{
		StatusCode: code,
		Body:       io.NopCloser(strings.NewReader(body)),
		Header:     make(http.Header),
	}
}

func TestAPIError_Error_WithCode(t *testing.T) {
	err := &APIError{HTTPStatus: 404, Code: "index_not_found", Message: "Index not found"}
	want := "twelvelabs 404 index_not_found: Index not found"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestAPIError_Error_NoCode(t *testing.T) {
	err := &APIError{HTTPStatus: 500, Message: "Internal Server Error"}
	want := "twelvelabs 500: Internal Server Error"
	if err.Error() != want {
		t.Errorf("got %q, want %q", err.Error(), want)
	}
}

func TestParseErrorResponse_ValidJSON(t *testing.T) {
	body := `{"code":"rate_limit_exceeded","message":"Too Many Requests","docs_url":"https://docs.twelvelabs.io"}`
	resp := fakeResponse(429, body)
	err := parseErrorResponse(resp)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus != 429 {
		t.Errorf("HTTPStatus: got %d, want 429", apiErr.HTTPStatus)
	}
	if apiErr.Code != "rate_limit_exceeded" {
		t.Errorf("Code: got %q, want %q", apiErr.Code, "rate_limit_exceeded")
	}
	if apiErr.DocsURL != "https://docs.twelvelabs.io" {
		t.Errorf("DocsURL: got %q", apiErr.DocsURL)
	}
}

func TestParseErrorResponse_InvalidJSON(t *testing.T) {
	resp := fakeResponse(503, "not json")
	err := parseErrorResponse(resp)
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.HTTPStatus != 503 {
		t.Errorf("HTTPStatus: got %d, want 503", apiErr.HTTPStatus)
	}
	if apiErr.Message == "" {
		t.Error("Message should be non-empty for unrecognised body")
	}
}

func TestParseErrorResponse_EmptyMessage(t *testing.T) {
	// JSON present but message field is empty → fall back to status text.
	resp := fakeResponse(404, `{"code":"not_found","message":""}`)
	err := parseErrorResponse(resp)
	apiErr := err.(*APIError)
	if apiErr.Message == "" {
		t.Error("Message should be non-empty")
	}
}
