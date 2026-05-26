// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image/jpeg"
	"net/http"
	"net/url"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

// VidiAnalyzer is a go_processor that batches decoded video frames, sends them
// to a Vidi 2.5 inference service over HTTP, and publishes the structured
// results as Metadata on the pipeline event bus.
//
// Required params:
//
//	"service_url"   — base URL of the inference service, e.g. "http://localhost:8000"
//
// Optional params:
//
//	"query"          — natural-language prompt sent with every batch (default: "describe the scene")
//	"task"           — inference task: "captioning", "grounding", "qa", "editing" (default: "captioning")
//	"buffer_frames"  — frames to accumulate before each /infer call (default: 8)
//	"process_every"  — only buffer every Nth video frame, others pass through unchanged (default: 1)
//	"jpeg_quality"   — JPEG quality for frame encoding, 1–100 (default: 75)
//	"timeout_s"      — per-request HTTP timeout in seconds (default: 30)
//	"time_base_num"  — PTS timebase numerator for duration calculation (default: 1)
//	"time_base_den"  — PTS timebase denominator for duration calculation (default: 1000000)
type VidiAnalyzer struct {
	serviceURL   string
	query        string
	task         string
	bufferFrames int
	processEvery uint64
	jpegQuality  int
	timeout      time.Duration
	timeBaseNum  int64 // PTS timebase numerator
	timeBaseDen  int64 // PTS timebase denominator

	client     *http.Client
	buf        []string // base64-encoded JPEG frames accumulated for the next batch
	frameCount uint64
	windowPTS  [2]int64 // PTS of first and last buffered frame
}

// vidiRequest is the JSON body sent to POST /infer.
type vidiRequest struct {
	Frames    []string `json:"frames"` // base64-encoded JPEG, one per buffered frame
	Query     string   `json:"query"`
	Task      string   `json:"task"`
	DurationS float64  `json:"duration_s"` // window duration in seconds; used by grounding
}

// vidiResponse is the expected JSON body returned by the inference service.
// All fields are optional; the service may return any subset depending on task.
type vidiResponse struct {
	Timestamps []vidiTimestamp  `json:"timestamps,omitempty"`
	Boxes      []vidiBox        `json:"boxes,omitempty"`
	Caption    string           `json:"caption,omitempty"`
	Answer     string           `json:"answer,omitempty"`
	EditPlan   []vidiEditAction `json:"edit_plan,omitempty"`
	Extra      map[string]any   `json:"extra,omitempty"`
}

// vidiTimestamp represents a temporal segment returned by the inference service.
type vidiTimestamp struct {
	StartS     float64 `json:"start_s"`
	EndS       float64 `json:"end_s,omitempty"`
	Label      string  `json:"label,omitempty"`
	Confidence float64 `json:"confidence,omitempty"`
}

// vidiBox is a single spatial detection returned by the service.
type vidiBox struct {
	FrameIndex int        `json:"frame_index"`
	Label      string     `json:"label,omitempty"`
	Confidence float64    `json:"confidence,omitempty"`
	Box2D      [4]float64 `json:"box_2d"` // x1, y1, x2, y2 in pixels
}

// vidiEditAction is one step in an edit plan returned by the service.
type vidiEditAction struct {
	Action string  `json:"action"` // "cut", "trim", "overlay", ...
	StartS float64 `json:"start_s,omitempty"`
	EndS   float64 `json:"end_s,omitempty"`
	Label  string  `json:"label,omitempty"`
}

// Init implements Processor. Called once during graph construction.
func (p *VidiAnalyzer) Init(params map[string]any) error {
	su, _ := params["service_url"].(string)
	if su == "" {
		return fmt.Errorf("vidi_analyzer: \"service_url\" param is required")
	}
	if _, err := url.ParseRequestURI(su); err != nil {
		return fmt.Errorf("vidi_analyzer: invalid service_url %q: %w", su, err)
	}
	p.serviceURL = su

	p.query = "describe the scene"
	if q, ok := params["query"].(string); ok && q != "" {
		p.query = q
	}

	p.task = "captioning"
	if t, ok := params["task"].(string); ok && t != "" {
		p.task = t
	}

	p.bufferFrames = 8
	if n, ok := params["buffer_frames"].(float64); ok && n >= 1 {
		p.bufferFrames = int(n)
	}

	p.processEvery = 1
	if n, ok := params["process_every"].(float64); ok && n >= 1 {
		p.processEvery = uint64(n)
	}

	p.jpegQuality = 75
	if q, ok := params["jpeg_quality"].(float64); ok && q >= 1 && q <= 100 {
		p.jpegQuality = int(q)
	}

	p.timeout = 30 * time.Second
	if s, ok := params["timeout_s"].(float64); ok && s > 0 {
		p.timeout = time.Duration(s * float64(time.Second))
	}

	p.timeBaseNum = 1
	if n, ok := params["time_base_num"].(float64); ok && n > 0 {
		p.timeBaseNum = int64(n)
	}
	p.timeBaseDen = 1_000_000 // microsecond PTS by default
	if d, ok := params["time_base_den"].(float64); ok && d > 0 {
		p.timeBaseDen = int64(d)
	}

	p.client = &http.Client{Timeout: p.timeout}
	return nil
}

// Process implements Processor. Called for every frame on the node's input pad.
func (p *VidiAnalyzer) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	// Pass non-video streams through unchanged.
	if ctx.MediaType != av.MediaTypeVideo {
		return frame, nil, nil
	}

	p.frameCount++
	if p.processEvery > 1 && p.frameCount%p.processEvery != 0 {
		return frame, nil, nil
	}

	// Convert to RGBA immediately so the cost is spread across frames.
	rgba, err := FrameToRGBA(frame)
	if err != nil {
		return frame, nil, fmt.Errorf("vidi_analyzer: frame to RGBA: %w", err)
	}

	// Encode to JPEG.
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rgba, &jpeg.Options{Quality: p.jpegQuality}); err != nil {
		return frame, nil, fmt.Errorf("vidi_analyzer: jpeg encode: %w", err)
	}
	encoded := base64.StdEncoding.EncodeToString(buf.Bytes())

	p.buf = append(p.buf, encoded)
	if len(p.buf) == 1 {
		p.windowPTS[0] = ctx.PTS // record PTS of first frame in window
	}
	p.windowPTS[1] = ctx.PTS // update to PTS of latest buffered frame

	if len(p.buf) < p.bufferFrames {
		// Accumulate; emit frame downstream unchanged.
		return frame, nil, nil
	}

	// Snapshot the buffer and window PTS, then reset before the blocking HTTP call.
	b64frames := p.buf
	windowPTS := p.windowPTS
	p.buf = make([]string, 0, p.bufferFrames)

	// POST to the inference service.
	durationS := float64(windowPTS[1]-windowPTS[0]) * float64(p.timeBaseNum) / float64(p.timeBaseDen)
	vresp, err := p.callService(ctx.Context, b64frames, durationS)
	if err != nil {
		// Non-fatal: surface the error as metadata, keep the pipeline alive.
		return frame, &Metadata{Custom: map[string]any{"vidi_error": err.Error()}}, nil
	}

	return frame, p.toMetadata(vresp), nil
}

// callService POSTs a batch of base64 JPEG frames to /infer and returns the
// parsed response. Uses the ProcessorContext's context for cancellation.
func (p *VidiAnalyzer) callService(ctx context.Context, b64frames []string, durationS float64) (*vidiResponse, error) {
	body, err := json.Marshal(vidiRequest{
		Frames:    b64frames,
		Query:     p.query,
		Task:      p.task,
		DurationS: durationS,
	})
	if err != nil {
		return nil, fmt.Errorf("vidi_analyzer: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		p.serviceURL+"/infer", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("vidi_analyzer: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := p.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vidi_analyzer: POST /infer: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("vidi_analyzer: inference service returned %s", resp.Status)
	}

	var vresp vidiResponse
	if err := json.NewDecoder(resp.Body).Decode(&vresp); err != nil {
		return nil, fmt.Errorf("vidi_analyzer: decode response: %w", err)
	}
	return &vresp, nil
}

// toMetadata converts a vidiResponse into the canonical Metadata type.
// Spatial box detections map to Metadata.Detections; everything else goes into
// Metadata.Custom so downstream processors and the metadata_writer can access
// it without knowing the Vidi schema.
func (p *VidiAnalyzer) toMetadata(vresp *vidiResponse) *Metadata {
	var dets []Detection
	for _, box := range vresp.Boxes {
		dets = append(dets, Detection{
			Label:      box.Label,
			Confidence: box.Confidence,
			BBox:       box.Box2D,
		})
	}

	custom := make(map[string]any)
	if vresp.Caption != "" {
		custom["caption"] = vresp.Caption
	}
	if vresp.Answer != "" {
		custom["answer"] = vresp.Answer
	}
	if len(vresp.Timestamps) > 0 {
		custom["timestamps"] = vresp.Timestamps
	}
	if len(vresp.EditPlan) > 0 {
		custom["edit_plan"] = vresp.EditPlan
	}
	for k, v := range vresp.Extra {
		custom[k] = v
	}

	if len(dets) == 0 && len(custom) == 0 {
		return nil
	}
	return &Metadata{
		Detections: dets,
		Custom:     custom,
	}
}

// Close implements Processor. Called once on pipeline shutdown.
func (p *VidiAnalyzer) Close() error {
	p.buf = nil
	return nil
}

func init() {
	Register("vidi_analyzer", func() Processor { return &VidiAnalyzer{} })
}
