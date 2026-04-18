// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"

	"github.com/MediaMolder/MediaMolder/av"
)

// MetadataWriter is a built-in processor that wraps another processor and
// writes all emitted metadata to a JSON Lines (.jsonl) file. The inner
// processor's metadata is still returned normally (so it also reaches the
// event bus).
//
// Required params:
//
//	"output_file"     — path to the output .jsonl file (required)
//	"inner_processor" — name of a registered processor to wrap (required)
//
// All other params are forwarded to the inner processor's Init().
//
// Example JSON config:
//
//	{
//	  "id": "detect_and_log",
//	  "type": "go_processor",
//	  "processor": "metadata_file_writer",
//	  "params": {
//	    "output_file": "detections.jsonl",
//	    "inner_processor": "yolo_v8",
//	    "model": "/models/yolov8n.onnx",
//	    "conf": 0.5
//	  }
//	}
type MetadataWriter struct {
	inner Processor
	file  *os.File
	enc   *json.Encoder
	mu    sync.Mutex // protects file writes
}

// metadataRecord is the JSON structure written per metadata event.
type metadataRecord struct {
	FrameIndex uint64    `json:"frame_index"`
	PTS        int64     `json:"pts"`
	Metadata   *Metadata `json:"metadata"`
}

func (w *MetadataWriter) Init(params map[string]any) error {
	outPath, _ := params["output_file"].(string)
	if outPath == "" {
		return fmt.Errorf("metadata_file_writer: \"output_file\" param is required")
	}
	innerName, _ := params["inner_processor"].(string)
	if innerName == "" {
		return fmt.Errorf("metadata_file_writer: \"inner_processor\" param is required (name of processor to wrap)")
	}

	inner, err := Get(innerName)
	if err != nil {
		return fmt.Errorf("metadata_file_writer: %w", err)
	}
	w.inner = inner

	// Forward remaining params to the inner processor.
	innerParams := make(map[string]any, len(params))
	for k, v := range params {
		if k == "output_file" || k == "inner_processor" {
			continue
		}
		innerParams[k] = v
	}
	if err := w.inner.Init(innerParams); err != nil {
		return fmt.Errorf("metadata_file_writer: inner %q Init: %w", innerName, err)
	}

	f, err := os.Create(outPath)
	if err != nil {
		w.inner.Close()
		return fmt.Errorf("metadata_file_writer: open %q: %w", outPath, err)
	}
	w.file = f
	w.enc = json.NewEncoder(f)
	return nil
}

func (w *MetadataWriter) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	out, md, err := w.inner.Process(frame, ctx)
	if err != nil {
		return out, md, err
	}

	if md != nil {
		rec := metadataRecord{
			FrameIndex: ctx.FrameIndex,
			PTS:        ctx.PTS,
			Metadata:   md,
		}
		w.mu.Lock()
		w.enc.Encode(rec) //nolint:errcheck // best-effort file write
		w.mu.Unlock()
	}

	return out, md, nil
}

func (w *MetadataWriter) Close() error {
	var errs []error
	if w.inner != nil {
		if err := w.inner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if w.file != nil {
		if err := w.file.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func init() {
	Register("metadata_file_writer", func() Processor { return &MetadataWriter{} })
}
