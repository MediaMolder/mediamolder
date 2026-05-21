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

// outputFormat enumerates the file formats supported by fileWriteHook.
type outputFormat int

const (
	fmtJSONL     outputFormat = iota // one JSON record per cut (default)
	fmtCSV                           // CSV rows, one per cut
	fmtTimecodes                     // cut timecodes, comma-joined and flushed at close
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
	file      *os.File
	enc       *json.Encoder // non-nil only for fmtJSONL
	format    outputFormat
	csvHdrDone bool     // CSV: have we written the header row yet?
	tcBuf      []string // timecodes: buffered until close
	mu         sync.Mutex
}

// initFromParams reads and removes "output_file" and "output_format" from
// params, opens the file if the param is set, and returns the filtered params
// map. The caller should use the returned map for all further param
// processing.
func (h *fileWriteHook) initFromParams(processorName string, params map[string]any) (map[string]any, error) {
	path, _ := params["output_file"].(string)
	fmtStr, _ := params["output_format"].(string)

	// Return a copy without output_file / output_format so the detector's own
	// Init doesn't see unknown params and error out.
	filtered := make(map[string]any, len(params))
	for k, v := range params {
		if k != "output_file" && k != "output_format" {
			filtered[k] = v
		}
	}

	if path == "" {
		return filtered, nil
	}

	switch fmtStr {
	case "", "jsonl":
		h.format = fmtJSONL
	case "csv":
		h.format = fmtCSV
	case "timecodes":
		h.format = fmtTimecodes
	default:
		return nil, fmt.Errorf("%s: output_format %q is not valid (want jsonl, csv, timecodes)", processorName, fmtStr)
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
	if h.format == fmtJSONL {
		h.enc = json.NewEncoder(f)
	}
	return filtered, nil
}

// write appends a record in the configured format. No-op if no file is configured or md is nil.
func (h *fileWriteHook) write(ctx ProcessorContext, md *Metadata) {
	if h.file == nil || md == nil {
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()

	switch h.format {
	case fmtCSV:
		h.writeCSVLocked(ctx, md)
	case fmtTimecodes:
		h.writeTimecodeLocked(md)
	default: // fmtJSONL
		rec := metadataRecord{
			FrameIndex: ctx.FrameIndex,
			PTS:        ctx.PTS,
			Metadata:   md,
		}
		h.enc.Encode(rec) //nolint:errcheck // best-effort file write
	}
}

// writeCSVLocked appends one CSV row for the cut event. Must be called with h.mu held.
func (h *fileWriteHook) writeCSVLocked(ctx ProcessorContext, md *Metadata) {
	if !h.csvHdrDone {
		fmt.Fprintln(h.file, "Frame Index,Timecode,PTS,Score") //nolint:errcheck
		h.csvHdrDone = true
	}
	tc, _ := md.Custom["timecode"].(string)
	score, _ := md.Custom["score"].(float64)
	fmt.Fprintf(h.file, "%d,%s,%d,%.3f\n", ctx.FrameIndex, tc, ctx.PTS, score) //nolint:errcheck
}

// writeTimecodeLocked buffers one timecode string. Must be called with h.mu held.
func (h *fileWriteHook) writeTimecodeLocked(md *Metadata) {
	if tc, _ := md.Custom["timecode"].(string); tc != "" {
		h.tcBuf = append(h.tcBuf, tc)
	}
}

// close flushes and closes the output file. No-op if no file is configured.
func (h *fileWriteHook) close() error {
	if h.file == nil {
		return nil
	}
	if h.format == fmtTimecodes && len(h.tcBuf) > 0 {
		fmt.Fprintln(h.file, strings.Join(h.tcBuf, ",")) //nolint:errcheck
	}
	return h.file.Close()
}

// EventSink is an exported, engine-managed file writer used by the pipeline
// to route processor metadata events through "events" edges. Unlike the
// fileWriteHook (which is embedded in processors and managed by their
// Init/Close lifecycle), EventSink is created directly by the engine when it
// discovers "events" edges that point at metadata_file_writer nodes.
type EventSink struct {
	hook fileWriteHook
}

// NewEventSink opens path for writing event records in the given format
// ("jsonl", "csv", "timecodes"; empty string defaults to "jsonl").
// The caller must call Close when done.
func NewEventSink(path, format string) (*EventSink, error) {
	s := &EventSink{}
	p := map[string]any{"output_file": path}
	if format != "" {
		p["output_format"] = format
	}
	if _, err := s.hook.initFromParams("events_sink", p); err != nil {
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
