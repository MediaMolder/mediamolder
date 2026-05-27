// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"fmt"
	"log"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// TwelveLabsAnalyzer is an event-driven processor that runs Pegasus analyze
// on each completed segment file. The processor uploads the file to a
// staging TwelveLabs index, waits for the indexing task to reach "ready",
// then issues an analyze request with the configured prompt. Results are
// emitted as Metadata.Custom["twelvelabs"] = {event: "analyzed", ...}.
//
// Frame Process is a pass-through. All work happens in OnSegmentCompleted
// dispatched by the engine from an upstream segment_sink "events" edge.
type TwelveLabsAnalyzer struct {
	indexID     string
	prompt      string
	temperature float32
	segments    bool
	pollOpts    twelvelabs.WaitOpts
	maxConc     int
	timeout     time.Duration

	client    *twelvelabs.Client
	emit      MetadataEmitter
	sem       chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Init configures the analyzer from params.
//
// Params (in addition to the auth params from tlClientFromParams):
//   - index_id (string): staging index for uploads. Required.
//   - prompt (string, default "Describe what happens in this video."): Pegasus prompt.
//   - temperature (float, default 0.2): Pegasus sampling temperature.
//   - segments (bool, default false): request structured timestamped chapters.
//   - poll_interval_s / poll_max_interval_s (float): WaitForTask backoff.
//   - max_concurrent (int, default 2): cap on in-flight analyses.
//   - request_timeout_s (float, default 0 = no per-request timeout).
func (p *TwelveLabsAnalyzer) Init(params map[string]any) error {
	c, err := tlClientFromParams(params)
	if err != nil {
		return fmt.Errorf("twelvelabs_analyzer: %w", err)
	}
	p.client = c

	p.indexID, _ = params["index_id"].(string)
	if p.indexID == "" {
		return fmt.Errorf("twelvelabs_analyzer: index_id is required")
	}

	p.prompt = "Describe what happens in this video."
	if s, ok := params["prompt"].(string); ok && s != "" {
		p.prompt = s
	}
	p.temperature = 0.2
	if v, ok := params["temperature"].(float64); ok {
		p.temperature = float32(v)
	}
	if v, ok := params["segments"].(bool); ok {
		p.segments = v
	}
	if v, ok := params["request_timeout_s"].(float64); ok && v > 0 {
		p.timeout = time.Duration(v * float64(time.Second))
	}

	p.pollOpts = tlPollOpts(params)
	p.maxConc = tlMaxConcurrent(params)
	p.sem = make(chan struct{}, p.maxConc)
	return nil
}

// Process is a pass-through.
func (p *TwelveLabsAnalyzer) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

// Close waits for in-flight analyses.
func (p *TwelveLabsAnalyzer) Close() error {
	p.closeOnce.Do(func() { p.wg.Wait() })
	return nil
}

// SetMetadataEmitter installs the engine-provided emitter.
func (p *TwelveLabsAnalyzer) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
}

// OnSegmentCompleted uploads the segment, waits for it to be indexed, then
// runs Pegasus analyze.
func (p *TwelveLabsAnalyzer) OnSegmentCompleted(ctx context.Context, ev SegmentEvent) {
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
		p.analyzeOne(ctx, ev)
	}()
}

func (p *TwelveLabsAnalyzer) analyzeOne(parent context.Context, ev SegmentEvent) {
	ctx := parent
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, p.timeout)
		defer cancel()
	}

	var taskID, videoID string

	// If the upstream indexer forwarded its result via SegmentEvent.Custom,
	// use those IDs directly instead of re-uploading the file.
	if tl, ok := ev.Custom["twelvelabs"].(map[string]any); ok {
		videoID, _ = tl["video_id"].(string)
		taskID, _ = tl["task_id"].(string)
	}
	if videoID == "" {
		var err error
		taskID, videoID, err = tlUploadAndWait(ctx, p.client, p.indexID, ev.FilePath, p.pollOpts)
		if err != nil {
			p.postError(ev, err)
			return
		}
	}

	result, err := p.client.Analyze(ctx, twelvelabs.AnalyzeRequest{
		VideoID:     videoID,
		Prompt:      p.prompt,
		Temperature: p.temperature,
		Segments:    p.segments,
	})
	if err != nil {
		p.postError(ev, fmt.Errorf("analyze video %s: %w", videoID, err))
		return
	}

	payload := map[string]any{
		"event":         "analyzed",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"index_id":      p.indexID,
		"task_id":       taskID,
		"video_id":      videoID,
		"prompt":        p.prompt,
		"text":          result.Text,
	}
	if len(result.Chapters) > 0 {
		chapters := make([]map[string]any, len(result.Chapters))
		for i, c := range result.Chapters {
			chapters[i] = map[string]any{
				"start_s": c.StartS,
				"end_s":   c.EndS,
				"title":   c.Title,
			}
		}
		payload["chapters"] = chapters
	}
	p.postEvent(payload)
}

func (p *TwelveLabsAnalyzer) postEvent(payload map[string]any) {
	if p.emit == nil {
		log.Printf("twelvelabs_analyzer: no emitter installed; payload=%v", payload)
		return
	}
	p.emit(&Metadata{Custom: map[string]any{"twelvelabs": payload}})
}

func (p *TwelveLabsAnalyzer) postError(ev SegmentEvent, err error) {
	p.postEvent(map[string]any{
		"event":         "error",
		"source":        "twelvelabs_analyzer",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"error":         err.Error(),
	})
}

func init() {
	Register("twelvelabs_analyzer", func() Processor { return &TwelveLabsAnalyzer{} })
}
