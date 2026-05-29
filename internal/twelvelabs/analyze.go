// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"net/http"
	"strings"
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

// Analyze issues a Pegasus analysis request and returns the accumulated result.
// The TwelveLabs v1.3 /analyze endpoint always responds with a streaming NDJSON
// body; this helper collects all "text_generation" chunks into AnalyzeResult.Text.
func (c *Client) Analyze(ctx context.Context, req AnalyzeRequest) (*AnalyzeResult, error) {
	var sb strings.Builder
	if err := c.AnalyzeStream(ctx, req, func(chunk AnalyzeChunk) error {
		sb.WriteString(chunk.Text)
		return nil
	}); err != nil {
		return nil, err
	}
	return &AnalyzeResult{Text: sb.String()}, nil
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
