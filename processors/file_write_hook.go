// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"fmt"
	"os"
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

	f, err := os.Create(path)
	if err != nil {
		return nil, fmt.Errorf("%s: open output_file %q: %w", processorName, path, err)
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
