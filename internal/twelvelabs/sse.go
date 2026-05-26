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

const sseMaxTokenBytes = 1 << 20 // 1 MiB per SSE line

// scanSSE reads a Server-Sent Events stream from r and calls fn for each
// parsed AnalyzeChunk. It stops at EOF, on the first error returned by fn,
// or if the scanner encounters a line exceeding sseMaxTokenBytes.
//
// Lines not starting with "data:" and the sentinel "data: [DONE]" are
// silently skipped. Malformed JSON in a data line is also silently skipped
// so a single bad event does not abort the stream.
func scanSSE(r io.Reader, fn func(AnalyzeChunk) error) error {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4*1024), sseMaxTokenBytes)

	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "" || data == "[DONE]" {
			continue
		}
		var chunk AnalyzeChunk
		if err := json.Unmarshal([]byte(data), &chunk); err != nil {
			// Malformed event: skip rather than abort.
			continue
		}
		if err := fn(chunk); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("twelvelabs: SSE scan: %w", err)
	}
	return nil
}
