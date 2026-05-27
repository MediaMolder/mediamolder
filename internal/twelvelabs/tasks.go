// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// CreateIndexTask uploads a video to the given index and returns the resulting
// task. The video is streamed from disk (TaskSource.File) or submitted by URL
// (TaskSource.URL) without buffering the entire file in RAM.
//
// When both File and URL are set, File takes precedence.
func (c *Client) CreateIndexTask(ctx context.Context, indexID string, src TaskSource) (*Task, error) {
	if indexID == "" {
		return nil, fmt.Errorf("twelvelabs: CreateIndexTask: indexID is required")
	}
	if src.File == "" && src.URL == "" {
		return nil, fmt.Errorf("twelvelabs: CreateIndexTask: File or URL is required")
	}

	fields := []formField{{"index_id", indexID}}
	if src.Language != "" {
		fields = append(fields, formField{"language", src.Language})
	}

	if src.File != "" {
		f, err := os.Open(src.File)
		if err != nil {
			return nil, fmt.Errorf("twelvelabs: CreateIndexTask: open %s: %w", src.File, err)
		}
		defer f.Close()

		filename := src.FileName
		if filename == "" {
			filename = filepath.Base(src.File)
		}

		resp, err := c.uploadMultipart(ctx, "/tasks", fields, "video_file", filename, f)
		if err != nil {
			return nil, err
		}
		var task Task
		return &task, decodeJSON(resp.Body, &task)
	}

	// URL-based submission: send as a JSON field alongside the other form fields
	// by appending to the multipart with no file part.
	fields = append(fields, formField{"video_url", src.URL})
	resp, err := c.uploadMultipart(ctx, "/tasks", fields, "", "", nil)
	if err != nil {
		return nil, err
	}
	var task Task
	return &task, decodeJSON(resp.Body, &task)
}

// GetTask returns the current state of an indexing task.
func (c *Client) GetTask(ctx context.Context, id string) (*Task, error) {
	if id == "" {
		return nil, fmt.Errorf("twelvelabs: GetTask: id is required")
	}
	resp, err := c.do(ctx, "GET", "/tasks/"+id, nil)
	if err != nil {
		return nil, err
	}
	var task Task
	return &task, decodeJSON(resp.Body, &task)
}

// WaitForTask polls GetTask until the task reaches a terminal state (ready or
// failed) or ctx is cancelled. It returns an error if the task fails or if
// polling itself fails.
func (c *Client) WaitForTask(ctx context.Context, id string, opts WaitOpts) (*Task, error) {
	if id == "" {
		return nil, fmt.Errorf("twelvelabs: WaitForTask: id is required")
	}
	interval := opts.InitialInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	maxInterval := opts.MaxInterval
	if maxInterval <= 0 {
		maxInterval = 30 * time.Second
	}

	for {
		task, err := c.GetTask(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("twelvelabs: WaitForTask: %w", err)
		}
		switch task.Status {
		case TaskStatusReady:
			return task, nil
		case TaskStatusFailed:
			return nil, fmt.Errorf("twelvelabs: task %s failed", id)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(interval):
		}
		interval *= 2
		if interval > maxInterval {
			interval = maxInterval
		}
	}
}
