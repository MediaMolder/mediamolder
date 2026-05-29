// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

const defaultEmbedModel = "marengo3.0"

// embedTaskRaw is used to unmarshal the nested API response for embed tasks.
// It is flattened into EmbedTask by decodeEmbedTask.
type embedTaskRaw struct {
	ID     string `json:"_id"`
	Status string `json:"status"`
	Video  struct {
		Segments []struct {
			Scope  string    `json:"embedding_scope"`
			StartS float64   `json:"start_offset_sec"`
			EndS   float64   `json:"end_offset_sec"`
			Vector []float32 `json:"float_array"`
		} `json:"segments"`
	} `json:"video_embedding"`
}

// EmbedText submits a text string to the embed endpoint and returns the
// resulting embedding vector as a single Embedding with Scope "text".
func (c *Client) EmbedText(ctx context.Context, text string, opts EmbedOpts) ([]Embedding, error) {
	if text == "" {
		return nil, fmt.Errorf("twelvelabs: EmbedText: text is required")
	}
	model := opts.Model
	if model == "" {
		model = defaultEmbedModel
	}
	body := map[string]any{
		"model_name": model,
		"text":       text,
	}
	resp, err := c.do(ctx, http.MethodPost, "/embed", body)
	if err != nil {
		return nil, err
	}

	var result struct {
		TextEmbedding struct {
			FloatArray []float32 `json:"float_array"`
		} `json:"text_embedding"`
	}
	if err := decodeJSON(resp.Body, &result); err != nil {
		return nil, fmt.Errorf("twelvelabs: EmbedText: decode: %w", err)
	}
	return []Embedding{{
		Scope:  "text",
		Vector: result.TextEmbedding.FloatArray,
	}}, nil
}

// EmbedVideo creates an asynchronous video embedding task. Use GetEmbedTask
// or WaitForEmbedTask to retrieve the result once the task is ready.
func (c *Client) EmbedVideo(ctx context.Context, src EmbedSource, opts EmbedOpts) (*EmbedTask, error) {
	if src.File == "" && src.URL == "" {
		return nil, fmt.Errorf("twelvelabs: EmbedVideo: File or URL is required")
	}
	model := opts.Model
	if model == "" {
		model = defaultEmbedModel
	}
	scopes := opts.Scopes
	if len(scopes) == 0 {
		scopes = []string{"clip"}
	}

	if src.URL != "" {
		body := map[string]any{
			"model_name":       model,
			"video_url":        src.URL,
			"embedding_scopes": scopes,
		}
		if opts.WindowS > 0 {
			body["time_segment_duration"] = opts.WindowS
		}
		resp, err := c.do(ctx, http.MethodPost, "/embed/tasks", body)
		if err != nil {
			return nil, err
		}
		return decodeRawEmbedTask(resp.Body)
	}

	filePath := filepath.Clean(src.File)
	if !filepath.IsAbs(filePath) {
		if cwd, err := os.Getwd(); err == nil {
			filePath = filepath.Join(cwd, filePath)
		}
	}
	if !strings.HasPrefix(filePath, string(filepath.Separator)) {
		return nil, fmt.Errorf("twelvelabs: EmbedVideo: invalid path %q", src.File)
	}
	f, err := os.Open(filePath)
	if err != nil {
		return nil, fmt.Errorf("twelvelabs: EmbedVideo: open %s: %w", src.File, err)
	}
	defer f.Close()

	fields := []formField{{"model_name", model}}
	for _, s := range scopes {
		fields = append(fields, formField{"embedding_scopes", s})
	}
	if opts.WindowS > 0 {
		fields = append(fields, formField{"time_segment_duration",
			fmt.Sprintf("%g", opts.WindowS)})
	}

	resp, err := c.uploadMultipart(ctx, "/embed/tasks", fields, "video_file", filepath.Base(src.File), f)
	if err != nil {
		return nil, err
	}
	return decodeRawEmbedTask(resp.Body)
}

// GetEmbedTask returns the current state of an embedding task.
// When Status is TaskStatusReady, EmbedTask.Embeddings is populated.
func (c *Client) GetEmbedTask(ctx context.Context, id string) (*EmbedTask, error) {
	if id == "" {
		return nil, fmt.Errorf("twelvelabs: GetEmbedTask: id is required")
	}
	resp, err := c.do(ctx, http.MethodGet, "/embed/tasks/"+id, nil)
	if err != nil {
		return nil, err
	}
	return decodeRawEmbedTask(resp.Body)
}

// WaitForEmbedTask polls GetEmbedTask until the task reaches a terminal state
// or ctx is cancelled.
func (c *Client) WaitForEmbedTask(ctx context.Context, id string, opts WaitOpts) (*EmbedTask, error) {
	if id == "" {
		return nil, fmt.Errorf("twelvelabs: WaitForEmbedTask: id is required")
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
		task, err := c.GetEmbedTask(ctx, id)
		if err != nil {
			return nil, fmt.Errorf("twelvelabs: WaitForEmbedTask: %w", err)
		}
		switch task.Status {
		case TaskStatusReady:
			return task, nil
		case TaskStatusFailed:
			return nil, fmt.Errorf("twelvelabs: embed task %s failed", id)
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

// decodeRawEmbedTask reads the nested API response and returns a flat EmbedTask.
func decodeRawEmbedTask(body io.ReadCloser) (*EmbedTask, error) {
	var raw embedTaskRaw
	if err := decodeJSON(body, &raw); err != nil {
		return nil, fmt.Errorf("twelvelabs: decode embed task: %w", err)
	}
	task := &EmbedTask{
		ID:     raw.ID,
		Status: raw.Status,
	}
	for _, s := range raw.Video.Segments {
		task.Embeddings = append(task.Embeddings, Embedding{
			Scope:  s.Scope,
			StartS: s.StartS,
			EndS:   s.EndS,
			Vector: s.Vector,
		})
	}
	return task, nil
}
