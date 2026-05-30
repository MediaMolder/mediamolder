// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/storage"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// jobStatus is the lifecycle state of a managed pipeline job.
type jobStatus string

const (
	statusRunning   jobStatus = "running"
	statusSucceeded jobStatus = "succeeded"
	statusFailed    jobStatus = "failed"
	statusCanceled  jobStatus = "canceled"
)

// jobEvent is a single event sent over the SSE channel.
type jobEvent struct {
	Type string `json:"type"`
	Time int64  `json:"time_ms"`
	Data any    `json:"data,omitempty"`
}

// runningJob holds per-job state and SSE fan-out bookkeeping.
type runningJob struct {
	id     string
	pipe   *pipeline.Pipeline
	cancel context.CancelFunc
	start  time.Time

	// artifacts holds the output URLs as recorded in the resolved config.
	artifacts []string

	mu          sync.Mutex
	status      jobStatus
	finalErr    string
	historyBuf  []jobEvent
	subscribers map[chan jobEvent]struct{}
	done        chan struct{}
}

const historyCap = 64

// jobManager owns all in-flight and recently finished jobs.
type jobManager struct {
	mu   sync.Mutex
	jobs map[string]*runningJob
}

func newJobManager() *jobManager {
	return &jobManager{jobs: make(map[string]*runningJob)}
}

// Options for job submission.
type startOptions struct {
	presign *storage.PresignResolver
	uploads *storage.UploadStore
}

// start resolves upload:// and s3:// URIs, creates the pipeline, and begins
// execution. Returns the assigned job ID.
func (m *jobManager) start(cfg *pipeline.Config, opts startOptions) (string, error) {
	resolved, err := resolveURIs(cfg, opts)
	if err != nil {
		return "", err
	}

	artifacts := collectArtifacts(resolved)

	pipe, err := pipeline.NewPipeline(resolved)
	if err != nil {
		return "", err
	}

	id := newJobID()
	ctx, cancel := context.WithCancel(context.Background())
	j := &runningJob{
		id:          id,
		pipe:        pipe,
		cancel:      cancel,
		start:       time.Now(),
		artifacts:   artifacts,
		status:      statusRunning,
		subscribers: make(map[chan jobEvent]struct{}),
		done:        make(chan struct{}),
	}

	m.mu.Lock()
	m.jobs[id] = j
	m.gcLocked()
	m.mu.Unlock()

	go j.run(ctx)
	return id, nil
}

// get returns a job by id (nil if unknown).
func (m *jobManager) get(id string) *runningJob {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.jobs[id]
}

// cancel stops a running job.
func (m *jobManager) cancel(id string) error {
	j := m.get(id)
	if j == nil {
		return errors.New("job not found")
	}
	j.cancel()
	return nil
}

// gcLocked drops finished jobs beyond the retention window. Caller holds m.mu.
func (m *jobManager) gcLocked() {
	const keepFinished = 16
	finished := make([]*runningJob, 0, len(m.jobs))
	for _, j := range m.jobs {
		j.mu.Lock()
		if j.status != statusRunning {
			finished = append(finished, j)
		}
		j.mu.Unlock()
	}
	if len(finished) <= keepFinished {
		return
	}
	// Sort ascending by start time then evict the oldest surplus.
	for i := 0; i < len(finished)-1; i++ {
		for k := i + 1; k < len(finished); k++ {
			if finished[k].start.Before(finished[i].start) {
				finished[i], finished[k] = finished[k], finished[i]
			}
		}
	}
	for _, j := range finished[:len(finished)-keepFinished] {
		delete(m.jobs, j.id)
	}
}

// subscribe returns a channel that receives all future events plus replayed
// history. Caller must drain; slow consumers have events dropped, not blocked.
func (j *runningJob) subscribe() (<-chan jobEvent, func()) {
	ch := make(chan jobEvent, historyCap+1)
	j.mu.Lock()
	for _, e := range j.historyBuf {
		select {
		case ch <- e:
		default:
		}
	}
	if j.status != statusRunning {
		select {
		case ch <- jobEvent{Type: "done", Time: j.elapsedMs(), Data: map[string]any{
			"status": j.status,
			"error":  j.finalErr,
		}}:
		default:
		}
		close(ch)
		j.mu.Unlock()
		return ch, func() {}
	}
	j.subscribers[ch] = struct{}{}
	j.mu.Unlock()

	cancel := func() {
		j.mu.Lock()
		if _, ok := j.subscribers[ch]; ok {
			delete(j.subscribers, ch)
			close(ch)
		}
		j.mu.Unlock()
	}
	return ch, cancel
}

// publish appends ev to history and fans out to all SSE subscribers.
func (j *runningJob) publish(ev jobEvent) {
	j.mu.Lock()
	if len(j.historyBuf) >= historyCap {
		j.historyBuf = j.historyBuf[1:]
	}
	j.historyBuf = append(j.historyBuf, ev)
	for ch := range j.subscribers {
		select {
		case ch <- ev:
		default:
			// Slow subscriber: drop rather than block.
		}
	}
	j.mu.Unlock()
}

func (j *runningJob) elapsedMs() int64 {
	return time.Since(j.start).Milliseconds()
}

// run executes the pipeline and bridges its events to SSE subscribers.
func (j *runningJob) run(ctx context.Context) {
	defer close(j.done)

	tickCtx, tickCancel := context.WithCancel(ctx)
	defer tickCancel()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				j.publish(jobEvent{Type: "metrics", Time: j.elapsedMs(), Data: j.pipe.GetMetrics()})
			case <-tickCtx.Done():
				return
			}
		}
	}()

	go func() {
		for ev := range j.pipe.Events() {
			j.publish(translateEvent(ev, j.elapsedMs()))
		}
	}()

	err := j.pipe.Run(ctx)
	tickCancel()

	j.mu.Lock()
	switch {
	case err == nil:
		j.status = statusSucceeded
	case errors.Is(err, context.Canceled):
		j.status = statusCanceled
	default:
		j.status = statusFailed
		j.finalErr = err.Error()
	}
	finalEv := jobEvent{
		Type: "done",
		Time: j.elapsedMs(),
		Data: map[string]any{"status": j.status, "error": j.finalErr},
	}
	if len(j.historyBuf) >= historyCap {
		j.historyBuf = j.historyBuf[1:]
	}
	j.historyBuf = append(j.historyBuf, finalEv)
	for ch := range j.subscribers {
		select {
		case ch <- finalEv:
		default:
		}
		close(ch)
	}
	j.subscribers = nil
	j.mu.Unlock()
}

// translateEvent converts a pipeline.Event to the wire jobEvent shape.
func translateEvent(ev pipeline.Event, tMs int64) jobEvent {
	switch e := ev.(type) {
	case pipeline.StateChanged:
		return jobEvent{Type: "state", Time: tMs, Data: map[string]any{
			"from": e.From.String(),
			"to":   e.To.String(),
		}}
	case pipeline.ErrorEvent:
		msg := ""
		if e.Err != nil {
			msg = e.Err.Error()
		}
		return jobEvent{Type: "error", Time: tMs, Data: map[string]any{
			"node_id": e.NodeID,
			"stage":   e.Stage,
			"error":   msg,
		}}
	case pipeline.EOS:
		return jobEvent{Type: "log", Time: tMs, Data: map[string]any{"message": "end of stream"}}
	case pipeline.MetricsSnapshotEvent:
		return jobEvent{Type: "metrics", Time: tMs, Data: e.Snapshot}
	case pipeline.ProcessorMetadata:
		return jobEvent{Type: "metadata", Time: tMs, Data: e}
	default:
		return jobEvent{Type: "log", Time: tMs, Data: map[string]any{"event": "unknown"}}
	}
}

// resolveURIs handles upload:// and s3:// URI substitution.
func resolveURIs(cfg *pipeline.Config, opts startOptions) (*pipeline.Config, error) {
	out := *cfg
	out.Inputs = make([]pipeline.Input, len(cfg.Inputs))
	copy(out.Inputs, cfg.Inputs)
	out.Outputs = make([]pipeline.Output, len(cfg.Outputs))
	copy(out.Outputs, cfg.Outputs)

	// Resolve upload:// URIs first.
	if opts.uploads != nil {
		for i, inp := range out.Inputs {
			if strings.HasPrefix(inp.URL, "upload://") {
				path, err := opts.uploads.Resolve(inp.URL)
				if err != nil {
					return nil, err
				}
				out.Inputs[i].URL = path
			}
			if len(inp.ConcatList) > 0 {
				entries := make([]pipeline.ConcatEntry, len(inp.ConcatList))
				copy(entries, inp.ConcatList)
				for j, e := range entries {
					if strings.HasPrefix(e.File, "upload://") {
						path, err := opts.uploads.Resolve(e.File)
						if err != nil {
							return nil, err
						}
						entries[j].File = path
					}
				}
				out.Inputs[i].ConcatList = entries
			}
		}
	}

	// Presign any remaining s3:// URIs.
	if opts.presign != nil {
		var err error
		out2, err := opts.presign.Resolve(context.Background(), &out)
		if err != nil {
			return nil, err
		}
		return out2, nil
	}
	return &out, nil
}

// collectArtifacts records the output URLs from a resolved config.
func collectArtifacts(cfg *pipeline.Config) []string {
	out := make([]string, 0, len(cfg.Outputs))
	for _, o := range cfg.Outputs {
		if o.URL != "" {
			out = append(out, o.URL)
		}
	}
	return out
}

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
