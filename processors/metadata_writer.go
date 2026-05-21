// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"

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
	hook  fileWriteHook
	inner Processor
}

func (w *MetadataWriter) Init(params map[string]any) error {
	_, hasOutputFile := params["output_file"]
	if !hasOutputFile {
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

	// Strip wrapper-only keys and open the file via the hook.
	innerParams := make(map[string]any, len(params))
	for k, v := range params {
		if k == "inner_processor" {
			continue
		}
		innerParams[k] = v
	}
	detectorParams, err := w.hook.initFromParams("metadata_file_writer", innerParams)
	if err != nil {
		w.inner.Close()
		return err
	}
	if err := w.inner.Init(detectorParams); err != nil {
		w.hook.close() //nolint:errcheck
		return fmt.Errorf("metadata_file_writer: inner %q Init: %w", innerName, err)
	}
	return nil
}

func (w *MetadataWriter) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	out, md, err := w.inner.Process(frame, ctx)
	if err != nil {
		return out, md, err
	}
	w.hook.write(ctx, md)
	return out, md, nil
}

func (w *MetadataWriter) Close() error {
	var errs []error
	if w.inner != nil {
		if err := w.inner.Close(); err != nil {
			errs = append(errs, err)
		}
	}
	if err := w.hook.close(); err != nil {
		errs = append(errs, err)
	}
	if len(errs) > 0 {
		return errs[0]
	}
	return nil
}

func init() {
	Register("metadata_file_writer", func() Processor { return &MetadataWriter{} })
}
