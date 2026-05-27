// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"bytes"
	"io"
	"net/http"
	"strings"
	"time"
)

// APILogEntry records one HTTP round-trip to the TwelveLabs API.
// Entries are produced by loggingTransport and delivered to the caller-supplied
// callback; they are suitable for marshalling to JSONL.
type APILogEntry struct {
	Time       string            `json:"time"`
	Method     string            `json:"method"`
	URL        string            `json:"url"`
	ReqHeaders map[string]string `json:"req_headers,omitempty"`
	// ReqBody is the raw request body for JSON/text requests. For
	// multipart/form-data requests the file part is omitted and this field
	// contains a human-readable placeholder; the text form fields are
	// included verbatim.
	ReqBody    string `json:"req_body,omitempty"`
	Status     int    `json:"status,omitempty"`
	RespBody   string `json:"resp_body,omitempty"`
	DurationMS int64  `json:"duration_ms"`
	Err        string `json:"error,omitempty"`
}

// loggingTransport wraps an http.RoundTripper and calls fn synchronously
// after each completed round-trip. It is safe for concurrent use.
type loggingTransport struct {
	wrap http.RoundTripper
	fn   func(APILogEntry)
}

const maxLogBodyBytes = 64 << 10 // 64 KiB per request/response body

func (lt *loggingTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	start := time.Now()
	entry := APILogEntry{
		Time:   start.UTC().Format(time.RFC3339Nano),
		Method: req.Method,
		URL:    req.URL.String(),
	}

	ct := req.Header.Get("Content-Type")
	if ct != "" {
		entry.ReqHeaders = map[string]string{"content-type": ct}
	}

	if strings.HasPrefix(ct, "multipart/form-data") {
		// Multipart bodies carry the video file; never buffer them.
		entry.ReqBody = "(multipart upload — file payload omitted)"
	} else if req.Body != nil && req.Body != http.NoBody {
		body, _ := io.ReadAll(io.LimitReader(req.Body, maxLogBodyBytes))
		req.Body.Close()
		req.Body = io.NopCloser(bytes.NewReader(body))
		if len(body) > 0 {
			entry.ReqBody = string(body)
		}
	}

	resp, err := lt.wrap.RoundTrip(req)
	entry.DurationMS = time.Since(start).Milliseconds()
	if err != nil {
		entry.Err = err.Error()
		lt.fn(entry)
		return resp, err
	}

	entry.Status = resp.StatusCode

	// Buffer and restore the response body so callers can still read it.
	if resp.Body != nil {
		body, rerr := io.ReadAll(io.LimitReader(resp.Body, maxLogBodyBytes))
		resp.Body.Close()
		resp.Body = io.NopCloser(bytes.NewReader(body))
		if rerr == nil && len(body) > 0 {
			entry.RespBody = string(body)
		}
	}

	lt.fn(entry)
	return resp, nil
}
