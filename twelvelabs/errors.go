// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"encoding/json"
	"fmt"
	"net/http"
)

// APIError represents a non-2xx response from the TwelveLabs API.
type APIError struct {
	HTTPStatus int    // HTTP status code
	Code       string // API error code, e.g. "index_not_found"
	Message    string // human-readable message from the API
	DocsURL    string // link to relevant documentation
}

func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("twelvelabs %d %s: %s", e.HTTPStatus, e.Code, e.Message)
	}
	return fmt.Sprintf("twelvelabs %d: %s", e.HTTPStatus, e.Message)
}

// parseErrorResponse reads the API error body from resp and returns an *APIError.
// It always closes resp.Body.
func parseErrorResponse(resp *http.Response) error {
	defer resp.Body.Close()
	var body struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		DocsURL string `json:"docs_url"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Message == "" {
		return &APIError{
			HTTPStatus: resp.StatusCode,
			Message:    http.StatusText(resp.StatusCode),
		}
	}
	return &APIError{
		HTTPStatus: resp.StatusCode,
		Code:       body.Code,
		Message:    body.Message,
		DocsURL:    body.DocsURL,
	}
}
