// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// TwelveLabsIndexer is an event-driven processor that uploads
// completed-segment files to a TwelveLabs index. It is wired via an
// "events" edge from an upstream segment_sink output (Flow B/C of the
// integration plan) and emits an "indexed" Metadata.Custom event per file.
//
// The processor does not touch frames: Process is a pass-through and the
// real work happens in OnSegmentCompleted, dispatched by the engine when
// the upstream sink finishes writing a segment.
type TwelveLabsIndexer struct {
	apiKey          string
	indexID         string
	autoCreateIndex bool
	models          []string
	indexName       string
	waitForReady    bool
	pollInterval    time.Duration
	pollMaxInterval time.Duration
	maxConcurrent   int
	// url, when non-empty, is uploaded directly to TwelveLabs instead of
	// using the FilePath from a SegmentEvent. Supports http(s) URLs as well
	// as local file paths. Set via the "url" param in Init.
	url string

	client     *twelvelabs.Client
	emit       MetadataEmitter
	sem        chan struct{}
	wg         sync.WaitGroup
	closeOnce  sync.Once
	createOnce sync.Once
	createErr  error
}

// Init configures the indexer from params.
//
// Params:
//   - api_key_env (string, default "TWELVELABS_API_KEY"): env var holding the API key.
//   - api_key (string, optional): API key literal (overrides api_key_env).
//   - index_id (string): existing index ID. Required unless auto_create_index=true.
//   - auto_create_index (bool, default false): create the index on first segment.
//   - index_name (string, optional): name used when auto-creating; required if auto_create_index=true.
//   - models ([]string, default ["marengo2.7"]): model names for auto-created index.
//   - wait_for_ready (bool, default true): block OnSegmentCompleted until task reaches "ready".
//   - poll_interval_s (float, default 2): initial poll interval for WaitForTask.
//   - poll_max_interval_s (float, default 30): max poll interval.
//   - max_concurrent (int, default 2): cap on in-flight uploads.
//   - url (string, optional): file path or HTTP URL to upload directly.
//     When set, SegmentEvent.FilePath is ignored in favour of this value.
//     Useful for input-only pipelines (no output sink) where the file URL
//     is known at config time (see docs/twelvelabs.md).
//   - base_url (string, optional): override TwelveLabs API base URL (for tests).
func (p *TwelveLabsIndexer) Init(params map[string]any) error {
	p.url, _ = params["url"].(string)
	envName := "TWELVELABS_API_KEY"
	if s, ok := params["api_key_env"].(string); ok && s != "" {
		envName = s
	}
	if s, ok := params["api_key"].(string); ok && s != "" {
		p.apiKey = s
	} else {
		p.apiKey = os.Getenv(envName)
	}
	if p.apiKey == "" {
		// Fall back to ~/.config/mediamolder/twelvelabs.json.
		p.apiKey, _ = twelvelabs.ResolveAPIKey("")
	}
	if p.apiKey == "" {
		return fmt.Errorf("twelvelabs_indexer: api key not set (env %q empty and no api_key param)", envName)
	}

	p.indexID, _ = params["index_id"].(string)
	if b, ok := params["auto_create_index"].(bool); ok {
		p.autoCreateIndex = b
	}
	if !p.autoCreateIndex && p.indexID == "" {
		return fmt.Errorf("twelvelabs_indexer: index_id is required (or set auto_create_index=true)")
	}
	if p.autoCreateIndex {
		p.indexName, _ = params["index_name"].(string)
		if p.indexName == "" {
			return fmt.Errorf("twelvelabs_indexer: index_name is required when auto_create_index=true")
		}
	}

	p.models = []string{"marengo2.7"}
	if v, ok := params["models"].([]any); ok && len(v) > 0 {
		p.models = p.models[:0]
		for _, m := range v {
			if s, ok := m.(string); ok && s != "" {
				p.models = append(p.models, s)
			}
		}
		if len(p.models) == 0 {
			return fmt.Errorf("twelvelabs_indexer: models list is empty after filtering")
		}
	} else if v, ok := params["models"].([]string); ok && len(v) > 0 {
		p.models = append([]string{}, v...)
	}

	p.waitForReady = true
	if b, ok := params["wait_for_ready"].(bool); ok {
		p.waitForReady = b
	}

	p.pollInterval = 2 * time.Second
	if s, ok := params["poll_interval_s"].(float64); ok && s > 0 {
		p.pollInterval = time.Duration(s * float64(time.Second))
	}
	p.pollMaxInterval = 30 * time.Second
	if s, ok := params["poll_max_interval_s"].(float64); ok && s > 0 {
		p.pollMaxInterval = time.Duration(s * float64(time.Second))
	}

	p.maxConcurrent = 2
	if n, ok := params["max_concurrent"].(float64); ok && n >= 1 {
		p.maxConcurrent = int(n)
	}
	p.sem = make(chan struct{}, p.maxConcurrent)

	p.client = twelvelabs.New(p.apiKey)
	if base, ok := params["base_url"].(string); ok && base != "" {
		p.client.BaseURL = base
	}
	return nil
}

// Process is a no-op pass-through. This processor is purely event-driven.
func (p *TwelveLabsIndexer) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

// Close waits for any in-flight upload goroutines to finish.
func (p *TwelveLabsIndexer) Close() error {
	p.closeOnce.Do(func() {
		p.wg.Wait()
	})
	return nil
}

// SetMetadataEmitter installs the engine-provided emitter used to post
// "indexed" events asynchronously.
func (p *TwelveLabsIndexer) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
}

// OnSegmentCompleted uploads the completed segment file to the configured
// TwelveLabs index and (optionally) waits for the indexing task to reach a
// terminal state. Result or error is reported via the metadata emitter.
//
// Concurrency is bounded by max_concurrent; if the semaphore is full the
// call blocks until a slot is available or ctx is cancelled.
func (p *TwelveLabsIndexer) OnSegmentCompleted(ctx context.Context, ev SegmentEvent) {
	select {
	case p.sem <- struct{}{}:
	case <-ctx.Done():
		p.postError(ev, ctx.Err())
		return
	}

	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		defer func() { <-p.sem }()
		p.indexOne(ctx, ev)
	}()
}

func (p *TwelveLabsIndexer) indexOne(ctx context.Context, ev SegmentEvent) {
	// url param overrides the event's FilePath.
	if p.url != "" {
		ev.FilePath = p.url
	}
	if p.autoCreateIndex {
		p.createOnce.Do(func() {
			specs := make([]twelvelabs.ModelSpec, 0, len(p.models))
			for _, m := range p.models {
				specs = append(specs, twelvelabs.ModelSpec{Name: m})
			}
			idx, err := p.client.CreateIndex(ctx, p.indexName, specs)
			if err != nil {
				p.createErr = err
				return
			}
			p.indexID = idx.ID
		})
		if p.createErr != nil {
			p.postError(ev, fmt.Errorf("auto-create index: %w", p.createErr))
			return
		}
	}

	src := twelvelabs.TaskSource{File: ev.FilePath}
	if strings.HasPrefix(ev.FilePath, "http://") || strings.HasPrefix(ev.FilePath, "https://") {
		src = twelvelabs.TaskSource{URL: ev.FilePath}
	}
	p.postProgress(ev, map[string]any{
		"event":     "uploading",
		"file_path": ev.FilePath,
		"index_id":  p.indexID,
	})
	task, err := p.client.CreateIndexTask(ctx, p.indexID, src)
	if err != nil {
		p.postError(ev, fmt.Errorf("create task: %w", err))
		return
	}
	p.postProgress(ev, map[string]any{
		"event":    "task_created",
		"task_id":  task.ID,
		"index_id": p.indexID,
	})

	status := task.Status
	videoID := task.VideoID
	if p.waitForReady {
		p.postProgress(ev, map[string]any{
			"event":   "waiting",
			"task_id": task.ID,
		})
		done, werr := p.client.WaitForTask(ctx, task.ID, twelvelabs.WaitOpts{
			InitialInterval: p.pollInterval,
			MaxInterval:     p.pollMaxInterval,
		})
		if werr != nil {
			p.postError(ev, fmt.Errorf("wait task %s: %w", task.ID, werr))
			return
		}
		status = done.Status
		videoID = done.VideoID
	}

	p.postEvent(ev, map[string]any{
		"event":         "indexed",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"index_id":      p.indexID,
		"task_id":       task.ID,
		"video_id":      videoID,
		"status":        status,
	})
}

func (p *TwelveLabsIndexer) postEvent(ev SegmentEvent, payload map[string]any) {
	if p.emit == nil {
		log.Printf("twelvelabs_indexer: no emitter installed; payload=%v", payload)
		return
	}
	p.emit(&Metadata{FilePath: ev.FilePath, Custom: map[string]any{"twelvelabs": payload}})
}

// postProgress emits an intermediate progress metadata event without
// logging to stderr when no emitter is installed (progress is best-effort).
func (p *TwelveLabsIndexer) postProgress(ev SegmentEvent, payload map[string]any) {
	if p.emit == nil {
		return
	}
	p.emit(&Metadata{FilePath: ev.FilePath, Custom: map[string]any{"twelvelabs": payload}})
}

func (p *TwelveLabsIndexer) postError(ev SegmentEvent, err error) {
	p.postEvent(ev, map[string]any{
		"event":         "error",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"error":         err.Error(),
	})
}

func init() {
	Register("twelvelabs_indexer", func() Processor { return &TwelveLabsIndexer{} })
}
