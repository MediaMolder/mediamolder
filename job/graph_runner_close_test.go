// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// closeErrorProc is a sink-style go_processor (no outputs, like a
// speech-to-text or file-writing processor) whose real work — and failure —
// happens in Close().
type closeErrorProc struct{ frames int }

var errCloseProc = errors.New("close-time failure")

func (p *closeErrorProc) Init(map[string]any) error { return nil }
func (p *closeErrorProc) Process(f *av.Frame, _ processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
	p.frames++
	return nil, nil, nil
}
func (p *closeErrorProc) Close() error { return errCloseProc }

const closeErrorProcName = "test_close_error_proc"

func init() {
	processors.Register(closeErrorProcName, func() processors.Processor { return &closeErrorProc{} })
}

func closeProcJob(t *testing.T, src, global string) error {
	t.Helper()
	if global != "" {
		global = `"global_options": ` + global + `,`
	}
	_, err := runJob(t, fmt.Sprintf(`{
      "schema_version": "1.1",
      %s
      "inputs": [{"id": "in0", "url": %q, "streams": [{"input_index": 0, "type": "audio", "track": 0}]}],
      "graph": {
        "nodes": [{"id": "sink", "type": "go_processor", "processor": %q}],
        "edges": [{"from": "in0:a:0", "to": "sink:default", "type": "audio"}]
      },
      "outputs": []
    }`, global, filepath.ToSlash(src), closeErrorProcName))
	return err
}

// A go_processor's Close() error reaches Run's caller; it used to be dropped
// on the floor, so a processor that does its work at close time could fail
// silently.
func TestRunSurfacesProcessorCloseError(t *testing.T) {
	dir := t.TempDir()
	wav := filepath.Join(dir, "src.wav")
	writeSineWAV(t, wav, 48000, 48000)
	err := closeProcJob(t, wav, "")
	if !errors.Is(err, errCloseProc) {
		t.Fatalf("Run = %v, want the processor's close error", err)
	}
	if !strings.Contains(err.Error(), `go_processor "sink" close`) {
		t.Fatalf("message = %q", err)
	}
}

// When the run itself failed, that failure is the reported one — the close
// error is a consequence, not the cause.
func TestRunErrorWinsOverCloseError(t *testing.T) {
	dir := t.TempDir()
	clean := writeAC3TS(t, dir)
	bad := filepath.Join(dir, "damaged.ts")
	damageAudioPackets(t, clean, bad, 6)
	err := closeProcJob(t, bad, `{"exit_on_error": true}`)
	if err == nil {
		t.Fatal("exit_on_error on a damaged file must fail")
	}
	if errors.Is(err, errCloseProc) || strings.Contains(err.Error(), "close") {
		t.Fatalf("the run error must win over the close error: %v", err)
	}
	if !strings.Contains(err.Error(), "exit_on_error") {
		t.Fatalf("want the source's exit_on_error failure, got %v", err)
	}
}
