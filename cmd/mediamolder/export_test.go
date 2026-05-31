// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestCmdExport_FromGraph is the F1.5 acceptance gate: writing a
// minimal job.Config to disk and invoking `mediamolder export
// --from-graph <file>` must print the same FFmpeg command as the
// shorthand-sourced `mediamolder export <file>` for any
// configuration that round-trips through job.NormalizeConfig.
// This proves the CLI dispatches both sub-paths correctly and that
// they share the same renderer.
func TestCmdExport_FromGraph(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "job.json")
	const cfgJSON = `{
		"schema_version": "1.2",
		"inputs": [{"id": "in0", "url": "in.mp4"}],
		"outputs": [{
			"id": "out0", "url": "out.mp4",
			"codec_video": "libx264",
			"encoder_params_video": {"crf": 23, "preset": "fast"}
		}]
	}`
	if err := os.WriteFile(path, []byte(cfgJSON), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	short := captureStdout(t, func() {
		if err := cmdExport([]string{path}); err != nil {
			t.Fatalf("cmdExport (shorthand): %v", err)
		}
	})
	graphSrc := captureStdout(t, func() {
		if err := cmdExport([]string{"--from-graph", path}); err != nil {
			t.Fatalf("cmdExport (--from-graph): %v", err)
		}
	})
	if short != graphSrc {
		t.Errorf("export round-trip mismatch:\n  shorthand:   %s  --from-graph: %s", short, graphSrc)
	}
	if !strings.Contains(short, "ffmpeg") {
		t.Errorf("expected output to start with the ffmpeg command, got: %q", short)
	}
}

// captureStdout runs fn while capturing everything written to
// os.Stdout and returns it as a string. Stderr is left untouched so
// "note:" lines surface in the test log.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = orig }()

	done := make(chan string, 1)
	go func() {
		var buf bytes.Buffer
		_, _ = io.Copy(&buf, r)
		done <- buf.String()
	}()
	fn()
	w.Close()
	return <-done
}
