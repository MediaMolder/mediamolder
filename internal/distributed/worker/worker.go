// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package worker implements the task execution loop. Each Worker receives tasks
// from a Queue, runs them through the pipeline engine, and reports results back
// to an Orchestrator. In Phase B (single-binary) the worker calls the
// orchestrator directly; Phase C replaces that call with an HTTP POST to
// /v1/tasks/{id}/result on the API server.
package worker

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/orchestrator"
	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

const (
	heartbeatInterval = 10 * time.Second
	leaseExtension    = 30 * time.Second
)

// Worker runs concurrency pipeline tasks in parallel.
type Worker struct {
	queue       queue.Queue
	store       state.Store
	orch        *orchestrator.Orchestrator
	concurrency int
}

// New creates a Worker.
// concurrency controls the number of simultaneous pipeline executions; 0 is
// treated as 1.
func New(q queue.Queue, st state.Store, orch *orchestrator.Orchestrator, concurrency int) *Worker {
	if concurrency < 1 {
		concurrency = 1
	}
	return &Worker{
		queue:       q,
		store:       st,
		orch:        orch,
		concurrency: concurrency,
	}
}

// Run starts concurrency goroutines and blocks until ctx is cancelled.
// All goroutines are joined before Run returns.
func (w *Worker) Run(ctx context.Context) error {
	var wg sync.WaitGroup
	for i := 0; i < w.concurrency; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			w.loop(ctx)
		}()
	}
	wg.Wait()
	return ctx.Err()
}

// loop is the per-goroutine task execution loop.
func (w *Worker) loop(ctx context.Context) {
	filter := queue.ReceiveFilter{}
	for {
		lease, err := w.queue.Receive(ctx, filter)
		if err != nil {
			// ctx was cancelled; exit cleanly.
			return
		}
		w.executeTask(ctx, lease)
	}
}

// executeTask runs a single pipeline task and reports the result.
func (w *Worker) executeTask(ctx context.Context, lease queue.Lease) {
	t := lease.Task

	// Mark task running in state store.
	if err := w.store.UpsertTask(ctx, t, state.TaskStatusRunning); err != nil {
		// Non-fatal: log and proceed; the task will still be executed.
		fmt.Printf("worker: upsert task %s running: %v\n", t.ID, err)
	}

	// Start a heartbeat goroutine that keeps the lease alive while the
	// pipeline is running. It exits when the heartbeat context is cancelled.
	hbCtx, hbCancel := context.WithCancel(ctx)
	hbDone := make(chan struct{})
	go func() {
		defer close(hbDone)
		ticker := time.NewTicker(heartbeatInterval)
		defer ticker.Stop()
		for {
			select {
			case <-hbCtx.Done():
				return
			case <-ticker.C:
				if err := w.queue.Heartbeat(ctx, t.ID, leaseExtension); err != nil {
					// Non-fatal: the lease will expire and the task will be
					// re-delivered if the pipeline takes too long.
					fmt.Printf("worker: heartbeat %s: %v\n", t.ID, err)
				}
			}
		}
	}()

	result := w.run(ctx, &t)

	hbCancel()
	<-hbDone

	if result.Error != "" {
		if err := w.queue.Nack(ctx, t.ID, 5*time.Second); err != nil {
			fmt.Printf("worker: nack %s: %v\n", t.ID, err)
		}
	} else {
		if err := w.queue.Ack(ctx, t.ID); err != nil {
			fmt.Printf("worker: ack %s: %v\n", t.ID, err)
		}
	}

	if err := w.orch.OnTaskCompleted(ctx, t.ID, result); err != nil {
		fmt.Printf("worker: OnTaskCompleted %s: %v\n", t.ID, err)
	}
}

// run executes the pipeline and returns a TaskResult. All errors are captured
// in TaskResult.Error so the worker always reports a structured result.
func (w *Worker) run(ctx context.Context, t *pipeline.Task) (result pipeline.TaskResult) {
	result.StartedAt = time.Now()
	defer func() { result.FinishedAt = time.Now() }()

	// Enforce the task deadline by deriving a scoped context.
	taskCtx := ctx
	if !t.Deadline.IsZero() && time.Until(t.Deadline) > 0 {
		var cancel context.CancelFunc
		taskCtx, cancel = context.WithDeadline(ctx, t.Deadline)
		defer cancel()
	}

	pipe, err := pipeline.NewPipeline(&t.Config)
	if err != nil {
		result.Error = fmt.Sprintf("build pipeline: %v", err)
		return
	}

	// Drain events to prevent the channel from blocking.
	go func() {
		for range pipe.Events() {
		}
	}()

	if err := pipe.Run(taskCtx); err != nil {
		result.Error = fmt.Sprintf("run pipeline: %v", err)
		return
	}
	return
}
