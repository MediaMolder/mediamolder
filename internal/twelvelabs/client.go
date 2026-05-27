// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package twelvelabs provides a pure-Go REST client for the TwelveLabs
// Video Understanding API (v1.3). It has no dependencies outside the Go
// standard library.
//
// Construct a Client with New, then call the indexing, analysis, search or
// embedding methods. The Client is safe for concurrent use once its fields
// are set.
package twelvelabs

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"time"
)

const (
	defaultBaseURL       = "https://api.twelvelabs.io/v1.3"
	maxResponseBodyBytes = 10 << 20 // 10 MiB — guards against runaway responses
)

// formField is a single multipart text field. Using a slice of formField
// (rather than map[string]string) allows repeated keys, e.g. for multi-valued
// embedding scopes.
type formField struct {
	Key, Value string
}

// Client sends requests to the TwelveLabs REST API.
// All exported fields may be set directly before first use.
type Client struct {
	BaseURL   string       // default: https://api.twelvelabs.io/v1.3
	APIKey    string       // value of the x-api-key request header
	HTTP      *http.Client // default: 60 s timeout
	UserAgent string       // sent in the User-Agent header
}

// New returns a Client configured with apiKey and production defaults.
func New(apiKey string) *Client {
	return &Client{
		BaseURL:   defaultBaseURL,
		APIKey:    apiKey,
		HTTP:      &http.Client{Timeout: 60 * time.Second},
		UserAgent: "mediamolder",
	}
}

// do executes a JSON request and returns the response.
//
// On 2xx the caller owns resp.Body and must close it.
// On API errors (4xx excluding 429, or exhausted retries) do returns *APIError.
// 429 and 5xx responses are retried automatically via withRetry.
func (c *Client) do(ctx context.Context, method, path string, body any) (*http.Response, error) {
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("twelvelabs: marshal request: %w", err)
		}
	}

	url := c.baseURL() + path
	resp, err := withRetry(ctx, func(ctx context.Context) (*http.Response, error) {
		var br io.Reader
		if bodyBytes != nil {
			br = bytes.NewReader(bodyBytes)
		}
		req, err := http.NewRequestWithContext(ctx, method, url, br)
		if err != nil {
			return nil, fmt.Errorf("twelvelabs: build request %s %s: %w", method, path, err)
		}
		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		c.setCommonHeaders(req)
		return c.httpClient().Do(req)
	})
	if err != nil {
		return nil, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseErrorResponse(resp)
	}
	return resp, nil
}

// uploadMultipart streams a multipart/form-data POST without buffering the
// entire body in memory. fields are plain text fields; if r is non-nil it is
// streamed as a file part named fileField with the given filename.
//
// Multipart uploads are not retried on network errors because the reader
// cannot generally be rewound.
//
// On 2xx the caller owns resp.Body and must close it.
func (c *Client) uploadMultipart(
	ctx context.Context,
	path string,
	fields []formField,
	fileField, filename string,
	r io.Reader,
) (*http.Response, error) {
	pr, pw := io.Pipe()
	mw := multipart.NewWriter(pw)

	go func() {
		var werr error
		defer func() {
			mw.Close()
			pw.CloseWithError(werr)
		}()
		for _, f := range fields {
			if werr = mw.WriteField(f.Key, f.Value); werr != nil {
				return
			}
		}
		if r != nil {
			fw, err := mw.CreateFormFile(fileField, filename)
			if err != nil {
				werr = err
				return
			}
			if _, err := io.Copy(fw, r); err != nil {
				werr = fmt.Errorf("twelvelabs: stream upload body: %w", err)
			}
		}
	}()

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL()+path, pr)
	if err != nil {
		pr.CloseWithError(err)
		return nil, fmt.Errorf("twelvelabs: build upload request: %w", err)
	}
	req.Header.Set("Content-Type", mw.FormDataContentType())
	c.setCommonHeaders(req)

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("twelvelabs: upload %s: %w", path, err)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, parseErrorResponse(resp)
	}
	return resp, nil
}

// decodeJSON limits the read to maxResponseBodyBytes, decodes JSON from r into
// v, and closes r.
func decodeJSON(r io.ReadCloser, v any) error {
	defer r.Close()
	return json.NewDecoder(io.LimitReader(r, maxResponseBodyBytes)).Decode(v)
}

func (c *Client) baseURL() string {
	if c.BaseURL != "" {
		return c.BaseURL
	}
	return defaultBaseURL
}

func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return http.DefaultClient
}

func (c *Client) setCommonHeaders(req *http.Request) {
	req.Header.Set("x-api-key", c.APIKey)
	if c.UserAgent != "" {
		req.Header.Set("User-Agent", c.UserAgent)
	}
}
