// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// tlClientFromParams builds a *twelvelabs.Client from the standard auth
// params shared by every TwelveLabs processor.
//
// Params:
//   - api_key (string, optional): API key literal.
//   - api_key_env (string, default "TWELVELABS_API_KEY"): env var holding key.
//   - base_url (string, optional): override the API base URL (used by tests).
//
// Returns an error if no key is available.
func tlClientFromParams(params map[string]any) (*twelvelabs.Client, error) {
	envName := "TWELVELABS_API_KEY"
	if s, ok := params["api_key_env"].(string); ok && s != "" {
		envName = s
	}
	key, _ := params["api_key"].(string)
	if key == "" {
		key = os.Getenv(envName)
	}
	if key == "" {
		// Fall back to the shared config-file resolver
		// (~/.config/mediamolder/twelvelabs.json).
		key, _ = twelvelabs.ResolveAPIKey("")
	}
	if key == "" {
		return nil, fmt.Errorf("twelvelabs: api key not set (env %q empty and no api_key param)", envName)
	}
	c := twelvelabs.New(key)
	if base, ok := params["base_url"].(string); ok && base != "" {
		c.BaseURL = base
	}
	return c, nil
}

// tlPollOpts pulls the optional poll_interval_s / poll_max_interval_s pair
// out of params, applying the same defaults as the TwelveLabs client itself
// (2s initial, 30s cap).
func tlPollOpts(params map[string]any) twelvelabs.WaitOpts {
	initial := 2 * time.Second
	if s, ok := params["poll_interval_s"].(float64); ok && s > 0 {
		initial = time.Duration(s * float64(time.Second))
	}
	maxI := 30 * time.Second
	if s, ok := params["poll_max_interval_s"].(float64); ok && s > 0 {
		maxI = time.Duration(s * float64(time.Second))
	}
	return twelvelabs.WaitOpts{InitialInterval: initial, MaxInterval: maxI}
}

// tlMaxConcurrent extracts the max_concurrent param with a default of 2.
func tlMaxConcurrent(params map[string]any) int {
	n := 2
	if v, ok := params["max_concurrent"].(float64); ok && v >= 1 {
		n = int(v)
	}
	return n
}

// tlStringList parses a []any (the JSON-decoded form of a string list) or a
// []string into a flat []string, dropping empty entries.
func tlStringList(v any) []string {
	switch xs := v.(type) {
	case []any:
		out := make([]string, 0, len(xs))
		for _, x := range xs {
			if s, ok := x.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	case []string:
		out := make([]string, 0, len(xs))
		for _, s := range xs {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// tlUploadAndWait uploads a local file to the given index, waits for the
// resulting task to reach a terminal state, and returns the video_id.
// Used by twelvelabs_analyzer to obtain a video_id for Pegasus analyze.
func tlUploadAndWait(ctx context.Context, c *twelvelabs.Client, indexID, file string, opts twelvelabs.WaitOpts) (taskID, videoID string, err error) {
	task, err := c.CreateIndexTask(ctx, indexID, twelvelabs.TaskSource{File: file})
	if err != nil {
		return "", "", fmt.Errorf("create task: %w", err)
	}
	done, err := c.WaitForTask(ctx, task.ID, opts)
	if err != nil {
		return task.ID, "", fmt.Errorf("wait task %s: %w", task.ID, err)
	}
	return task.ID, done.VideoID, nil
}
