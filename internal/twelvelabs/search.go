// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"fmt"
	"net/http"
)

// searchBody is the JSON payload for POST /search.
type searchBody struct {
	IndexID       string   `json:"index_id"`
	QueryText     string   `json:"query_text,omitempty"`
	QueryMediaURL string   `json:"query_media_url,omitempty"`
	SearchOptions []string `json:"search_options,omitempty"`
	Threshold     string   `json:"threshold,omitempty"`
	PageLimit     int      `json:"page_limit,omitempty"`
}

// Search executes a Marengo search query and returns all matching clips.
// Only the first page of results is returned; pagination is not yet supported.
func (c *Client) Search(ctx context.Context, req SearchRequest) ([]SearchResult, error) {
	if req.IndexID == "" {
		return nil, fmt.Errorf("twelvelabs: Search: IndexID is required")
	}
	if req.Query == "" && req.QueryMediaURL == "" {
		return nil, fmt.Errorf("twelvelabs: Search: Query or QueryMediaURL is required")
	}
	body := searchBody{
		IndexID:       req.IndexID,
		QueryText:     req.Query,
		QueryMediaURL: req.QueryMediaURL,
		SearchOptions: req.SearchOptions,
		Threshold:     req.Threshold,
		PageLimit:     req.PageLimit,
	}
	resp, err := c.do(ctx, http.MethodPost, "/search", body)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []SearchResult `json:"data"`
	}
	if err := decodeJSON(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("twelvelabs: Search: decode: %w", err)
	}
	return page.Data, nil
}
