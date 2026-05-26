// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"net/http"
)

// analyzeBody is the JSON payload for POST /analyze.
type analyzeBody struct {
	VideoID     string  `json:"video_id,omitempty"`
	VideoURL    string  `json:"video_url,omitempty"`
	Prompt      string  `json:"prompt"`
	Stream      bool    `json:"stream,omitempty"`
	Temperature float32 `json:"temperature,omitempty"`
	Segments    bool    `json:"segments,omitempty"`
}

// Analyze sends a synchronous (non-streaming) Pegasus analysis request and
// returns the complete result.
func (c *Client) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error) {
	body := analyzeBody{
		VideoID:     req.VideoID,
		VideoURL:    req.VideoURL,
		Prompt:      req.Prompt,
		Temperature: req.Temperature,
		Segments:    req.Segments,
	}
	resp, err := c.do(ctx, http.MethodPost, "/analyze", body)
	if err != nil {
		return nil, err
	}
	var result AnalyzeResult
	return &result, decodeJSON(resp.Body, &result)
}

// AnalyzeStream sends a streaming Pegasus analysis request and invokes fn for
// each AnalyzeChunk received over the SSE connection. It returns when the
// stream ends, fn returns an error, or ctx is cancelled.
func (c *Client) AnalyzeStream(ctx context.Context, req AnalyzeRequest, fn func(AnalyzeChunk) error) error {
	body := analyzeBody{
		VideoID:     req.VideoID,
		VideoURL:    req.VideoURL,
		Prompt:      req.Prompt,
		Stream:      true,
		Temperature: req.Temperature,
		Segments:    req.Segments,
	}
	resp, err := c.do(ctx, http.MethodPost, "/analyze", body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return scanSSE(resp.Body, fn)
}
