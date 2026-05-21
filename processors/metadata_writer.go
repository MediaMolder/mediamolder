// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
)

// MetadataWriter is a built-in processor that captures metadata events from
// a processor and writes them to a JSON Lines (.jsonl) file.
//
// Two usage modes:
//
//  1. Wrapper mode (legacy / JSON config): set "inner_processor" to the name
//     of a registered processor. MetadataWriter wraps it, intercepts the
//     metadata return values, writes them to "output_file", and forwards the
//     original metadata to the caller (and thus to the event bus).
//
//  2. Events-wiring mode (GUI / "events" edges): omit "inner_processor". The
//     engine detects an "events" edge pointing at this node and routes
//     metadata events from the upstream go_processor directly to the sink via
//     EventSink — MetadataWriter.Process is never called.  Init must still
//     succeed so that Close can release resources opened by the engine; in
//     pure-sink mode Init is effectively a no-op (no file is opened here —
//     the engine opens an EventSink directly from the output_file param).
type MetadataWriter struct {
	hook  fileWriteHook
	inner Processor
}

func (w *MetadataWriter) Init(params map[string]any) error {
	innerName, _ := params["inner_processor"].(string)
	if innerName == "" {
		// Pure-sink mode: the engine handles file I/O via EventSink.
		// No resources to initialise here; output_file is read by the
		// engine directly.
		return nil
	}

	// Wrapper mode: inner_processor is set.
	_, hasOutputFile := params["output_file"]
	if !hasOutputFile {
		return fmt.Errorf("metadata_file_writer: \"output_file\" param is required")
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
	if w.inner == nil {
		// Pure-sink mode: pass the frame through unchanged.
		// (This path is not normally reached because the engine does not
		// wire video edges to pure-sink metadata_file_writer nodes.)
		return frame, nil, nil
	}
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
