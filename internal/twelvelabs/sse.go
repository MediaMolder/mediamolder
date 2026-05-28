// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

const sseMaxTokenBytes = 1 << 20 // 1 MiB per NDJSON line

// scanSSE reads a TwelveLabs NDJSON analyze stream from r and calls fn for
// each AnalyzeChunk with EventType "text_generation". It stops at EOF, on a
// "stream_end" event, on the first error returned by fn, or if the scanner
// encounters a line exceeding sseMaxTokenBytes.
//
// Non-JSON lines and events with unrecognised EventType are silently skipped
// so a single unexpected event does not abort the stream.
func scanSSE(r io.Reader, fn func(AnalyzeChunk) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4*1024), sseMaxTokenBytes)

	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var chunk AnalyzeChunk
		if err := json.Unmarshal([]byte(line), &chunk); err != nil {
			// Malformed line: skip rather than abort.
			continue
		}
		if chunk.EventType == "stream_end" {
			break
		}
		if chunk.EventType != "text_generation" {
			continue
		}
		if err := fn(chunk); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("twelvelabs: NDJSON scan: %w", err)
	}
	return nil
}
