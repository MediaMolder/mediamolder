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

// TwelveLabsSearcher is a processor that periodically runs a Marengo search
// against a TwelveLabs index, emitting Metadata.Custom["twelvelabs"]={event:"search",...}
// events containing the latest matches.
//
// Operating modes (mutually exclusive):
//   - Timer mode: when interval_s > 0, the searcher runs the query on a
//     fixed interval starting at Init time.
//   - Segment mode: when interval_s == 0, the searcher runs the query each
//     time a SegmentCompleted event arrives from an upstream segment_sink.
type TwelveLabsSearcher struct {
	indexID       string
	query         string
	queryMediaURL string
	searchOpts    []string
	threshold     string
	pageLimit     int
	minScore      float64
	intervalS     float64
	timeout       time.Duration

	client    *twelvelabs.Client
	emit      MetadataEmitter
	stopCh    chan struct{}
	wg        sync.WaitGroup
	closeOnce sync.Once
}

// Init configures the searcher from params.
//
// Params (in addition to the auth params from tlClientFromParams):
//   - index_id (string): index to search. Required.
//   - query (string): natural-language query text. Required if no query_media_url.
//   - query_media_url (string): image/audio URL alternative to query.
//   - search_options ([]string, default ["visual","audio"]): modalities.
//   - threshold (string, default "medium"): "low" | "medium" | "high".
//   - page_limit (int, default 0 = API default).
//   - min_score (float, default 0): drop matches below this score.
//   - interval_s (float, default 0): if > 0, run on a ticker; else run per segment.
//   - request_timeout_s (float, default 0): per-request timeout.
func (p *TwelveLabsSearcher) Init(params map[string]any) error {
	_, c, err := tlClientFromParams(params)
	if err != nil {
		return fmt.Errorf("twelvelabs_searcher: %w", err)
	}
	p.client = c

	p.indexID, _ = params["index_id"].(string)
	if p.indexID == "" {
		return fmt.Errorf("twelvelabs_searcher: index_id is required")
	}
	p.query, _ = params["query"].(string)
	p.queryMediaURL, _ = params["query_media_url"].(string)
	if p.query == "" && p.queryMediaURL == "" {
		return fmt.Errorf("twelvelabs_searcher: query or query_media_url is required")
	}
	p.searchOpts = tlStringList(params["search_options"])
	if len(p.searchOpts) == 0 {
		p.searchOpts = []string{"visual", "audio"}
	}
	p.threshold = "medium"
	if s, ok := params["threshold"].(string); ok && s != "" {
		p.threshold = s
	}
	if v, ok := params["page_limit"].(float64); ok && v > 0 {
		p.pageLimit = int(v)
	}
	if v, ok := params["min_score"].(float64); ok && v > 0 {
		p.minScore = v
	}
	if v, ok := params["interval_s"].(float64); ok && v > 0 {
		p.intervalS = v
	}
	if v, ok := params["request_timeout_s"].(float64); ok && v > 0 {
		p.timeout = time.Duration(v * float64(time.Second))
	}

	if p.intervalS > 0 {
		p.stopCh = make(chan struct{})
	}
	return nil
}

func (p *TwelveLabsSearcher) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

// Close stops the ticker (timer mode) and waits for any in-flight queries.
func (p *TwelveLabsSearcher) Close() error {
	p.closeOnce.Do(func() {
		if p.stopCh != nil {
			close(p.stopCh)
		}
		p.wg.Wait()
	})
	return nil
}

// SetMetadataEmitter installs the engine-provided emitter and, in timer
// mode, kicks off the periodic-query goroutine.
func (p *TwelveLabsSearcher) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
	if p.intervalS <= 0 {
		return
	}
	p.wg.Add(1)
	go p.tickerLoop()
}

func (p *TwelveLabsSearcher) tickerLoop() {
	defer p.wg.Done()
	t := time.NewTicker(time.Duration(p.intervalS * float64(time.Second)))
	defer t.Stop()
	// Run once immediately so the first result is not delayed by interval_s.
	p.runQuery(context.Background(), SegmentEvent{})
	for {
		select {
		case <-p.stopCh:
			return
		case <-t.C:
			p.runQuery(context.Background(), SegmentEvent{})
		}
	}
}

// OnSegmentCompleted runs one query per completed segment (segment mode only).
func (p *TwelveLabsSearcher) OnSegmentCompleted(ctx context.Context, ev SegmentEvent) {
	if p.intervalS > 0 {
		// Ignored in timer mode.
		return
	}
	p.wg.Add(1)
	go func() {
		defer p.wg.Done()
		p.runQuery(ctx, ev)
	}()
}

func (p *TwelveLabsSearcher) runQuery(parent context.Context, ev SegmentEvent) {
	ctx := parent
	if p.timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(parent, p.timeout)
		defer cancel()
	}

	results, err := p.client.Search(ctx, twelvelabs.SearchRequest{
		IndexID:       p.indexID,
		Query:         p.query,
		QueryMediaURL: p.queryMediaURL,
		SearchOptions: p.searchOpts,
		Threshold:     p.threshold,
		PageLimit:     p.pageLimit,
	})
	if err != nil {
		p.postError(ev, err)
		return
	}

	matches := make([]map[string]any, 0, len(results))
	for _, r := range results {
		if p.minScore > 0 && r.Score < p.minScore {
			continue
		}
		matches = append(matches, map[string]any{
			"video_id":   r.VideoID,
			"start_s":    r.StartS,
			"end_s":      r.EndS,
			"score":      r.Score,
			"confidence": r.Confidence,
		})
	}

	payload := map[string]any{
		"event":    "search",
		"index_id": p.indexID,
		"query":    p.query,
		"matches":  matches,
		"count":    len(matches),
	}
	if ev.FilePath != "" {
		payload["file_path"] = ev.FilePath
		payload["segment_index"] = ev.SegmentIndex
		payload["output_id"] = ev.OutputID
	}
	p.postEvent(payload)
}

func (p *TwelveLabsSearcher) postEvent(payload map[string]any) {
	if p.emit == nil {
		log.Printf("twelvelabs_searcher: no emitter installed; payload=%v", payload)
		return
	}
	p.emit(&Metadata{Custom: map[string]any{"twelvelabs": payload}})
}

func (p *TwelveLabsSearcher) postError(ev SegmentEvent, err error) {
	payload := map[string]any{
		"event":  "error",
		"source": "twelvelabs_searcher",
		"error":  err.Error(),
	}
	if ev.FilePath != "" {
		payload["file_path"] = ev.FilePath
		payload["segment_index"] = ev.SegmentIndex
		payload["output_id"] = ev.OutputID
	}
	p.postEvent(payload)
}

func init() {
	Register("twelvelabs_searcher", func() Processor { return &TwelveLabsSearcher{} })
}
