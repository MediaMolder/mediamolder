// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime/debug"
	"time"
)

// CrashReport captures diagnostic state when a pipeline panics or
// hits an unrecoverable error.
type CrashReport struct {
	Timestamp    time.Time              `json:"timestamp"`
	PipelineID   string                 `json:"pipeline_id,omitempty"`
	PanicValue   string                 `json:"panic_value,omitempty"`
	Error        string                 `json:"error,omitempty"`
	State        string                 `json:"state"`
	NodeStates   map[string]string      `json:"node_states,omitempty"`
	BufferLevels map[string]float64     `json:"buffer_levels,omitempty"`
	LastEvents   []CrashEvent           `json:"last_events,omitempty"`
	Metrics      *MetricsSnapshot       `json:"metrics,omitempty"`
	StackTrace   string                 `json:"stack_trace,omitempty"`
	BuildInfo    map[string]string      `json:"build_info,omitempty"`
	Config       map[string]interface{} `json:"config,omitempty"`
}

// CrashEvent is a simplified event record for the crash report.
type CrashEvent struct {
	Type string `json:"type"`
	Data string `json:"data"`
	Time string `json:"time,omitempty"`
}

// CrashReporter collects pipeline state and writes crash reports to disk.
type CrashReporter struct {
	dir        string // directory for crash report files
	eventRing  []CrashEvent
	ringSize   int
	ringPos    int
	pipelineID string
}

// NewCrashReporter creates a reporter that keeps the last N events and
// writes reports to dir.
func NewCrashReporter(dir string, ringSize int) *CrashReporter {
	if ringSize <= 0 {
		ringSize = 100
	}
	return &CrashReporter{
		dir:       dir,
		ringSize:  ringSize,
		eventRing: make([]CrashEvent, 0, ringSize),
	}
}

// SetPipelineID sets an identifier for crash reports.
func (c *CrashReporter) SetPipelineID(id string) {
	c.pipelineID = id
}

// RecordEvent stores an event in the ring buffer for inclusion in crash reports.
func (c *CrashReporter) RecordEvent(e Event) {
	ce := CrashEvent{
		Type: fmt.Sprintf("%T", e),
		Data: fmt.Sprintf("%+v", e),
	}
	if len(c.eventRing) < c.ringSize {
		c.eventRing = append(c.eventRing, ce)
	} else {
		c.eventRing[c.ringPos] = ce
	}
	c.ringPos = (c.ringPos + 1) % c.ringSize
}

// lastEvents returns events in chronological order.
func (c *CrashReporter) lastEvents() []CrashEvent {
	if len(c.eventRing) < c.ringSize {
		out := make([]CrashEvent, len(c.eventRing))
		copy(out, c.eventRing)
		return out
	}
	out := make([]CrashEvent, c.ringSize)
	copy(out, c.eventRing[c.ringPos:])
	copy(out[c.ringSize-c.ringPos:], c.eventRing[:c.ringPos])
	return out
}

// CaptureFromPanic creates a crash report from a recovered panic value.
func (c *CrashReporter) CaptureFromPanic(p *Pipeline, panicVal interface{}) *CrashReport {
	report := c.buildBase(p)
	report.PanicValue = fmt.Sprintf("%v", panicVal)
	report.StackTrace = string(debug.Stack())
	return report
}

// CaptureFromError creates a crash report from an unrecoverable error.
func (c *CrashReporter) CaptureFromError(p *Pipeline, err error) *CrashReport {
	report := c.buildBase(p)
	report.Error = err.Error()
	report.StackTrace = string(debug.Stack())
	return report
}

func (c *CrashReporter) buildBase(p *Pipeline) *CrashReport {
	report := &CrashReport{
		Timestamp:  time.Now().UTC(),
		PipelineID: c.pipelineID,
		LastEvents: c.lastEvents(),
	}

	if p != nil {
		report.State = p.State().String()

		snap := p.GetMetrics()
		report.Metrics = &snap

		// Node states from metrics.
		report.NodeStates = make(map[string]string)
		for _, n := range snap.Nodes {
			report.NodeStates[n.NodeID] = fmt.Sprintf("frames=%d errors=%d fps=%.1f",
				n.Frames, n.Errors, n.FPS)
		}
	}

	// Build info.
	report.BuildInfo = make(map[string]string)
	if info, ok := debug.ReadBuildInfo(); ok {
		report.BuildInfo["go_version"] = info.GoVersion
		report.BuildInfo["module"] = info.Main.Path
		report.BuildInfo["version"] = info.Main.Version
	}

	return report
}

// WriteReport writes a crash report to a JSON file.
// Returns the file path of the written report.
func (c *CrashReporter) WriteReport(report *CrashReport) (string, error) {
	dir := c.dir
	if dir == "" {
		dir = "."
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", fmt.Errorf("create crash report dir: %w", err)
	}

	name := fmt.Sprintf("crash-%s.json", report.Timestamp.Format("20060102-150405"))
	path := filepath.Join(dir, name)

	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal crash report: %w", err)
	}

	if err := os.WriteFile(path, data, 0o640); err != nil {
		return "", fmt.Errorf("write crash report: %w", err)
	}
	return path, nil
}
