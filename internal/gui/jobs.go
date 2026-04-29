// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// jobStatus is the lifecycle status of a managed pipeline job.
type jobStatus string

const (
	statusRunning   jobStatus = "running"
	statusSucceeded jobStatus = "succeeded"
	statusFailed    jobStatus = "failed"
	statusCanceled  jobStatus = "canceled"
)

// jobEvent is a single event sent over the SSE channel.
// "Type" identifies the kind: "state" | "metrics" | "error" | "log" | "done".
type jobEvent struct {
	Type string `json:"type"`
	Time int64  `json:"time_ms"` // ms since pipeline start
	Data any    `json:"data,omitempty"`
}

// runningJob holds per-job state and the broadcast fan-out for SSE clients.
type runningJob struct {
	id     string
	cfg    *pipeline.Config
	pipe   *pipeline.Pipeline
	cancel context.CancelFunc
	start  time.Time

	mu          sync.Mutex
	status      jobStatus
	finalErr    string
	historyBuf  []jobEvent // bounded ring of recent events for late subscribers
	subscribers map[chan jobEvent]struct{}
	done        chan struct{}
}

const historyCap = 64

// jobManager owns all in-flight and recent jobs.
type jobManager struct {
	mu   sync.Mutex
	jobs map[string]*runningJob
}

func newJobManager() *jobManager {
	return &jobManager{jobs: make(map[string]*runningJob)}
}

// start launches a new pipeline run and returns its job ID.
func (m *jobManager) start(cfg *pipeline.Config) (string, error) {
	pipe, err := pipeline.NewPipeline(cfg)
	if err != nil {
		return "", err
	}
	id := newJobID()
	ctx, cancel := context.WithCancel(context.Background())
	j := &runningJob{
		id:          id,
		cfg:         cfg,
		pipe:        pipe,
		cancel:      cancel,
		start:       time.Now(),
		status:      statusRunning,
		subscribers: make(map[chan jobEvent]struct{}),
		done:        make(chan struct{}),
	}

	m.mu.Lock()
	m.jobs[id] = j
	// Garbage-collect: keep only the 16 most recent finished jobs alongside running ones.
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

// gcLocked drops finished jobs beyond a small retention window.
// Caller holds m.mu.
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
	// Drop oldest by start time.
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

// subscribe returns a fresh channel that receives every future event plus the
// recent history buffer. Caller must drain the channel; if it blocks too long
// the publisher will drop events for that subscriber.
func (j *runningJob) subscribe() (<-chan jobEvent, func()) {
	ch := make(chan jobEvent, 32)
	j.mu.Lock()
	// Replay history first so a late subscriber can catch up.
	for _, e := range j.historyBuf {
		select {
		case ch <- e:
		default:
		}
	}
	if j.status != statusRunning {
		// Already finished — emit a synthetic done event and close.
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

// publish appends an event to history and fans out to subscribers.
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
			// Slow subscriber: drop this event for them rather than block the publisher.
		}
	}
	j.mu.Unlock()
}

// elapsedMs returns ms since job start. Caller holds j.mu OR doesn't care about races.
func (j *runningJob) elapsedMs() int64 {
	return time.Since(j.start).Milliseconds()
}

// run executes the pipeline and bridges its events to SSE subscribers.
func (j *runningJob) run(ctx context.Context) {
	defer close(j.done)

	// Periodic metrics sampling — Pipeline already emits MetricsSnapshotEvent
	// internally at its own cadence; we additionally poll GetMetrics() so the
	// frontend always has fresh data even between bus events.
	tickerCtx, tickerCancel := context.WithCancel(ctx)
	defer tickerCancel()
	go func() {
		t := time.NewTicker(500 * time.Millisecond)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				snap := j.pipe.GetMetrics()
				j.publish(jobEvent{
					Type: "metrics",
					Time: j.elapsedMs(),
					Data: snap,
				})
			case <-tickerCtx.Done():
				return
			}
		}
	}()

	// Forward pipeline bus events to SSE subscribers.
	go func() {
		for ev := range j.pipe.Events() {
			j.publish(translateEvent(ev, j.elapsedMs()))
		}
	}()

	err := j.pipe.Run(ctx)
	tickerCancel()

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

// translateEvent converts a pipeline.Event into the wire jobEvent shape.
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

func newJobID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}
