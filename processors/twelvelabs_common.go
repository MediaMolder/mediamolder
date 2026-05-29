// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
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
//   - log_file (string, optional): path to a JSONL file; every API round-trip
//     is appended as one JSON object per line.
//   - log_api_calls (bool, optional): if true, also log round-trips to stderr
//     via the standard logger in addition to (or instead of) log_file.
//
// Returns the resolved API key, a configured client, and an error if no key
// is available.
func tlClientFromParams(params map[string]any) (string, *twelvelabs.Client, error) {
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
		return "", nil, fmt.Errorf("twelvelabs: api key not set (env %q empty and no api_key param)", envName)
	}
	c := twelvelabs.New(key)
	if base, ok := params["base_url"].(string); ok && base != "" {
		c.BaseURL = base
	}

	// Install API call logger if requested.
	logFile, _ := params["log_file"].(string)
	logToStderr, _ := params["log_api_calls"].(bool)
	if logFile != "" || logToStderr {
		var loggers []func(twelvelabs.APILogEntry)
		if logFile != "" {
			fn, closer, err := newJSONLLogger(logFile)
			if err != nil {
				return "", nil, fmt.Errorf("twelvelabs: open log_file %q: %w", logFile, err)
			}
			_ = closer // file stays open for the process lifetime; OS closes on exit
			loggers = append(loggers, fn)
		}
		if logToStderr {
			loggers = append(loggers, func(e twelvelabs.APILogEntry) {
				log.Printf("twelvelabs api: %s %s status=%d dur=%dms err=%s",
					e.Method, e.URL, e.Status, e.DurationMS, e.Err)
			})
		}
		c = c.WithLogger(func(e twelvelabs.APILogEntry) {
			for _, fn := range loggers {
				fn(e)
			}
		})
	}
	return key, c, nil
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
	const maxConcurrentLimit = 64
	n := 2
	if v, ok := params["max_concurrent"].(float64); ok && v >= 1 {
		n = int(v)
		if n > maxConcurrentLimit {
			n = maxConcurrentLimit
		}
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

// newJSONLLogger opens (or creates) path for appending and returns a logger
// function that marshals each APILogEntry as a single JSON line. The returned
// io.Closer should be called when the file is no longer needed; the caller is
// responsible for its lifecycle.
func newJSONLLogger(path string) (func(twelvelabs.APILogEntry), io.Closer, error) {
	f, err := os.OpenFile(filepath.Clean(path), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, nil, err
	}
	enc := json.NewEncoder(f)
	var mu sync.Mutex
	fn := func(e twelvelabs.APILogEntry) {
		mu.Lock()
		defer mu.Unlock()
		_ = enc.Encode(e)
	}
	return fn, f, nil
}
