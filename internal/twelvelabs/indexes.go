// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"fmt"
	"net/http"
)

// CreateIndex creates a new TwelveLabs index with the given name and models.
func (c *Client) CreateIndex(ctx context.Context, name string, models []ModelSpec) (*Index, error) {
	if name == "" {
		return nil, fmt.Errorf("twelvelabs: CreateIndex: name is required")
	}
	body := map[string]any{
		"name":   name,
		"models": models,
	}
	resp, err := c.do(ctx, http.MethodPost, "/indexes", body)
	if err != nil {
		return nil, err
	}
	var idx Index
	return &idx, decodeJSON(resp.Body, &idx)
}

// ListIndexes returns all indexes in the account.
// Only the first page of results is returned; pagination is not yet supported.
func (c *Client) ListIndexes(ctx context.Context) ([]Index, error) {
	resp, err := c.do(ctx, http.MethodGet, "/indexes", nil)
	if err != nil {
		return nil, err
	}
	var page struct {
		Data []Index `json:"data"`
	}
	if err := decodeJSON(resp.Body, &page); err != nil {
		return nil, fmt.Errorf("twelvelabs: ListIndexes: decode: %w", err)
	}
	return page.Data, nil
}

// DeleteIndex deletes the index with the given id.
func (c *Client) DeleteIndex(ctx context.Context, id string) error {
	if id == "" {
		return fmt.Errorf("twelvelabs: DeleteIndex: id is required")
	}
	resp, err := c.do(ctx, http.MethodDelete, "/indexes/"+id, nil)
	if err != nil {
		return err
	}
	drainClose(resp.Body)
	return nil
}
