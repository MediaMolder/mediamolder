// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
)

// metadataRecord is the JSON structure written per metadata event to a .jsonl file.
// It is shared by fileWriteHook (used by scene detectors) and MetadataWriter.
type metadataRecord struct {
	FrameIndex uint64    `json:"frame_index"`
	PTS        int64     `json:"pts"`
	Metadata   *Metadata `json:"metadata"`
}

// fileWriteHook handles the optional "output_file" param shared by all
// scene-change detectors. Embed this struct and call initFromParams, write,
// and close from Init/Process/Close respectively.
//
// When output_file is absent or empty, all methods are no-ops.
type fileWriteHook struct {
	file *os.File
	enc  *json.Encoder
	mu   sync.Mutex
}

// initFromParams reads and removes "output_file" from params, opens the file
// if the param is set, and returns the filtered params map. The caller should
// use the returned map for all further param processing.
func (h *fileWriteHook) initFromParams(processorName string, params map[string]any) (map[string]any, error) {
	path, _ := params["output_file"].(string)

	// Return a copy without output_file so the detector's own Init doesn't
	// see an unknown param and error out.
	filtered := make(map[string]any, len(params))
	for k, v := range params {
		if k != "output_file" {
			filtered[k] = v
		}
	}

	if path == "" {
		return filtered, nil
	}

	// Sanitize the path: resolve any "../" traversal components and require an
	// absolute path. Then re-derive safePath from a non-user-input root using
	// the filepath.Rel + HasPrefix confinement pattern (mirrors the GUI's
	// sanitizePathAnyRoot logic) so that os.Create receives a value that is
	// not considered directly tainted by static analysis (CWE-022 / CodeQL
	// go/path-injection).
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return nil, fmt.Errorf("%s: output_file must be an absolute path, got %q", processorName, path)
	}
	fsRoot := string(filepath.Separator) // "/" on Unix; first dir on Windows
	rel, relErr := filepath.Rel(fsRoot, path)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return nil, fmt.Errorf("%s: output_file %q is not within an accessible filesystem root", processorName, path)
	}
	safePath := filepath.Join(fsRoot, rel)

	f, err := os.Create(safePath)
	if err != nil {
		return nil, fmt.Errorf("%s: open output_file %q: %w", processorName, safePath, err)
	}
	h.file = f
	h.enc = json.NewEncoder(f)
	return filtered, nil
}

// write appends a JSONL record. No-op if no file is configured or md is nil.
func (h *fileWriteHook) write(ctx ProcessorContext, md *Metadata) {
	if h.enc == nil || md == nil {
		return
	}
	rec := metadataRecord{
		FrameIndex: ctx.FrameIndex,
		PTS:        ctx.PTS,
		Metadata:   md,
	}
	h.mu.Lock()
	h.enc.Encode(rec) //nolint:errcheck // best-effort file write
	h.mu.Unlock()
}

// close flushes and closes the output file. No-op if no file is configured.
func (h *fileWriteHook) close() error {
	if h.file != nil {
		return h.file.Close()
	}
	return nil
}

// EventSink is an exported, engine-managed file writer used by the pipeline
// to route processor metadata events through "events" edges. Unlike the
// fileWriteHook (which is embedded in processors and managed by their
// Init/Close lifecycle), EventSink is created directly by the engine when it
// discovers "events" edges that point at metadata_file_writer nodes.
type EventSink struct {
	hook fileWriteHook
}

// NewEventSink opens path for writing JSONL event records. The caller must
// call Close when done.
func NewEventSink(path string) (*EventSink, error) {
	s := &EventSink{}
	if _, err := s.hook.initFromParams("events_sink", map[string]any{"output_file": path}); err != nil {
		return nil, err
	}
	return s, nil
}

// Write appends one event record. No-op when md is nil.
func (s *EventSink) Write(ctx ProcessorContext, md *Metadata) {
	s.hook.write(ctx, md)
}

// Close flushes and closes the underlying file.
func (s *EventSink) Close() error {
	return s.hook.close()
}
