// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// TwelveLabsEmbedder is an event-driven processor that requests Marengo
// video embeddings for each completed segment file. Embeddings are emitted
// as Metadata.Custom["twelvelabs"]={event:"embedded", ...} and optionally
// written to disk as JSON or JSONL alongside the segment.
type TwelveLabsEmbedder struct {
	model     string
	scopes    []string
	windowS   float64
	outDir    string
	outFormat string // "json" | "jsonl"
	pollOpts  twelvelabs.WaitOpts
	maxConc   int
	timeout   time.Duration

	client    *twelvelabs.Client
	emit      MetadataEmitter
	sem       chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Init configures the embedder from params.
//
// Params (in addition to the auth params from tlClientFromParams):
//   - model (string, default "marengo3.0").
//   - scopes ([]string, default ["clip"]): "clip" and/or "video".
//   - window_s (float, default 6): time_segment_duration for "video" scope.
//   - out_dir (string, optional): if set, embeddings are written to
//     "<out_dir>/<basename>.embeddings.<ext>" with vectors inline.
//   - out_format (string, default "json"): "json" or "jsonl".
//   - poll_interval_s / poll_max_interval_s (float): WaitForEmbedTask backoff.
//   - max_concurrent (int, default 2): cap on in-flight embeddings.
//   - request_timeout_s (float, default 0 = no per-request timeout).
func (p *TwelveLabsEmbedder) Init(params map[string]any) error {
	_, c, err := tlClientFromParams(params)
	if err != nil {
		return fmt.Errorf("twelvelabs_embedder: %w", err)
	}
	p.client = c

	p.model = "marengo3.0"
	if s, ok := params["model"].(string); ok && s != "" {
		p.model = s
	}
	p.scopes = tlStringList(params["scopes"])
	if len(p.scopes) == 0 {
		p.scopes = []string{"clip"}
	}
	p.windowS = 6
	if v, ok := params["window_s"].(float64); ok && v > 0 {
		p.windowS = v
	}
	if s, ok := params["out_dir"].(string); ok {
		p.outDir = s
	}
	p.outFormat = "json"
	if s, ok := params["out_format"].(string); ok && s != "" {
		s = strings.ToLower(s)
		if s != "json" && s != "jsonl" {
			return fmt.Errorf("twelvelabs_embedder: out_format must be json or jsonl, got %q", s)
		}
		p.outFormat = s
	}
	if v, ok := params["request_timeout_s"].(float64); ok && v > 0 {
		p.timeout = time.Duration(v * float64(time.Second))
	}

	p.pollOpts = tlPollOpts(params)
	p.maxConc = tlMaxConcurrent(params)
	p.sem = make(chan struct{}, p.maxConc)

	if p.outDir != "" {
		if err := os.MkdirAll(p.outDir, 0o755); err != nil {
			return fmt.Errorf("twelvelabs_embedder: create out_dir: %w", err)
		}
	}
	return nil
}

func (p *TwelveLabsEmbedder) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

func (p *TwelveLabsEmbedder) Close() error {
	p.closeOnce.Do(func() { p.wg.Wait() })
	return nil
}

func (p *TwelveLabsEmbedder) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
}

func (p *TwelveLabsEmbedder) OnSegmentCompleted(ctx context.Context, ev SegmentEvent) {
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
		p.embedOne(ctx, ev)
	}()
}

func (p *TwelveLabsEmbedder) embedOne(parent context.Context, ev SegmentEvent) {
	ctx := parent
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, p.timeout)
		defer cancel()
	}

	task, err := p.client.EmbedVideo(ctx, twelvelabs.EmbedSource{File: ev.FilePath}, twelvelabs.EmbedOpts{
		Model:   p.model,
		Scopes:  p.scopes,
		WindowS: p.windowS,
	})
	if err != nil {
		p.postError(ev, fmt.Errorf("create embed task: %w", err))
		return
	}
	done, err := p.client.WaitForEmbedTask(ctx, task.ID, p.pollOpts)
	if err != nil {
		p.postError(ev, fmt.Errorf("wait embed task %s: %w", task.ID, err))
		return
	}

	dim := 0
	if len(done.Embeddings) > 0 {
		dim = len(done.Embeddings[0].Vector)
	}

	var outFile string
	if p.outDir != "" {
		outFile, err = p.writeEmbeddings(ev.FilePath, done.Embeddings)
		if err != nil {
			p.postError(ev, fmt.Errorf("write embeddings: %w", err))
			return
		}
	}

	payload := map[string]any{
		"event":         "embedded",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"task_id":       task.ID,
		"model":         p.model,
		"dim":           dim,
		"count":         len(done.Embeddings),
	}
	if outFile != "" {
		payload["out_file"] = outFile
	} else {
		// Inline embeddings only when not writing to disk.
		embs := make([]map[string]any, len(done.Embeddings))
		for i, e := range done.Embeddings {
			embs[i] = map[string]any{
				"scope":   e.Scope,
				"start_s": e.StartS,
				"end_s":   e.EndS,
				"vector":  e.Vector,
			}
		}
		payload["embeddings"] = embs
	}
	p.postEvent(payload)
}

// writeEmbeddings serialises embeddings to outDir/<basename>.embeddings.<ext>.
// Returns the written path.
func (p *TwelveLabsEmbedder) writeEmbeddings(segmentFile string, embs []twelvelabs.Embedding) (string, error) {
	base := strings.TrimSuffix(filepath.Base(segmentFile), filepath.Ext(segmentFile))
	ext := p.outFormat
	out := filepath.Join(p.outDir, base+".embeddings."+ext)
	f, err := os.Create(out)
	if err != nil {
		return "", err
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	switch p.outFormat {
	case "jsonl":
		for _, e := range embs {
			if err := enc.Encode(map[string]any{
				"scope":   e.Scope,
				"start_s": e.StartS,
				"end_s":   e.EndS,
				"vector":  e.Vector,
			}); err != nil {
				return "", err
			}
		}
	default: // json
		records := make([]map[string]any, len(embs))
		for i, e := range embs {
			records[i] = map[string]any{
				"scope":   e.Scope,
				"start_s": e.StartS,
				"end_s":   e.EndS,
				"vector":  e.Vector,
			}
		}
		if err := enc.Encode(records); err != nil {
			return "", err
		}
	}
	return out, nil
}

func (p *TwelveLabsEmbedder) postEvent(payload map[string]any) {
	if p.emit == nil {
		log.Printf("twelvelabs_embedder: no emitter installed; payload=%v", payload)
		return
	}
	p.emit(&Metadata{Custom: map[string]any{"twelvelabs": payload}})
}

func (p *TwelveLabsEmbedder) postError(ev SegmentEvent, err error) {
	p.postEvent(map[string]any{
		"event":         "error",
		"source":        "twelvelabs_embedder",
		"output_id":     ev.OutputID,
		"file_path":     ev.FilePath,
		"segment_index": ev.SegmentIndex,
		"error":         err.Error(),
	})
}

func init() {
	Register("twelvelabs_embedder", func() Processor { return &TwelveLabsEmbedder{} })
}
