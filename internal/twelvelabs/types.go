// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import "time"

// ModelSpec identifies a TwelveLabs model to attach to an index.
type ModelSpec struct {
	Name    string   `json:"model_name"`
	Options []string `json:"model_options,omitempty"`
}

// Index is a TwelveLabs index resource.
type Index struct {
	ID        string      `json:"_id"`
	Name      string      `json:"index_name"`
	Models    []ModelSpec `json:"models"`
	CreatedAt string      `json:"created_at"`
}

// Task status values returned by the TwelveLabs API.
const (
	TaskStatusPending  = "pending"
	TaskStatusIndexing = "indexing"
	TaskStatusReady    = "ready"
	TaskStatusFailed   = "failed"
)

// Task represents an indexing task.
type Task struct {
	ID          string `json:"_id"`
	IndexID     string `json:"index_id"`
	VideoID     string `json:"video_id"`
	Status      string `json:"status"`
	ErrorReason string `json:"error_reason,omitempty"`
}

// TaskSource describes the source for a CreateIndexTask call.
// Exactly one of File or URL should be set.
type TaskSource struct {
	File         string                  // local file path
	FileName     string                  // filename sent in the multipart form; defaults to base of File
	URL          string                  // remote URL (mutually exclusive with File)
	Language     string                  // optional ISO-639-1 language hint
	ProgressFunc func(sent, total int64) // called periodically during file upload; may be nil
}

// WaitOpts controls WaitForTask and WaitForEmbedTask polling behaviour.
type WaitOpts struct {
	InitialInterval time.Duration    // default 2s
	MaxInterval     time.Duration    // default 30s
	StatusFunc      func(task *Task) // called after each poll with the latest task state; may be nil
}

// AnalyzeRequest drives a Pegasus analyze call.
type AnalyzeRequest struct {
	VideoID     string // for an already-indexed video
	VideoURL    string // for a one-shot URL
	Prompt      string
	Stream      bool
	Temperature float32
	Segments    bool // request structured timestamped chapters
}

// AnalyzeResult is the synchronous (non-streaming) analyze response.
type AnalyzeResult struct {
	ID       string    `json:"id"`
	Text     string    `json:"data"`
	Chapters []Chapter `json:"chapters,omitempty"`
}

// Chapter is a structured chapter from a Pegasus segments response.
type Chapter struct {
	StartS float64 `json:"start"`
	EndS   float64 `json:"end"`
	Title  string  `json:"chapter_title"`
}

// AnalyzeChunk is one SSE event during a streaming analyze call.
type AnalyzeChunk struct {
	Type string `json:"type"` // "text_delta" | "completed" | "error"
	Data string `json:"data"`
}

// SearchRequest drives a Marengo search call.
type SearchRequest struct {
	IndexID       string
	Query         string   // natural-language text query
	QueryMediaURL string   // image/audio query URL (alternative to Query)
	SearchOptions []string // e.g. ["visual", "audio", "text_in_video"]
	Threshold     string   // "low" | "medium" | "high"
	PageLimit     int
}

// SearchResult is one hit from a Marengo search.
type SearchResult struct {
	VideoID    string  `json:"video_id"`
	StartS     float64 `json:"start"`
	EndS       float64 `json:"end"`
	Score      float64 `json:"score"`
	Confidence string  `json:"confidence"`
}

// EmbedSource describes what to embed. Exactly one of File or URL should be set.
type EmbedSource struct {
	File string // local file path
	URL  string // remote URL
}

// EmbedOpts controls an embed request.
type EmbedOpts struct {
	Model   string   // default "marengo3.0"
	Scopes  []string // "clip" and/or "video"
	WindowS float64  // window length in seconds (used with "video" scope)
}

// EmbedTask is an asynchronous embedding task.
// Embeddings is populated once Status is TaskStatusReady.
type EmbedTask struct {
	ID         string      `json:"_id"`
	Status     string      `json:"status"`
	Embeddings []Embedding // flattened from video_embedding.segments
}

// Embedding is a single embedding vector for a time window.
type Embedding struct {
	Scope  string    // "clip" or "video"
	StartS float64   // start offset in seconds
	EndS   float64   // end offset in seconds
	Vector []float32 // the embedding vector (e.g. 1024 dimensions for marengo3.0)
}
