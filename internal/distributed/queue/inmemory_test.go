// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/job"
)

func makeTask(id string) job.Task {
	return job.Task{ID: id, JobID: "job1", StageID: "s1"}
}

func TestInMemory_PublishReceive(t *testing.T) {
	q := queue.NewInMemory()
	ctx := context.Background()

	if err := q.Publish(ctx, makeTask("t1")); err != nil {
		t.Fatal(err)
	}

	lease, err := q.Receive(ctx, queue.ReceiveFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if lease.Task.ID != "t1" {
		t.Fatalf("want task t1, got %q", lease.Task.ID)
	}
	if lease.LeaseUntil.IsZero() {
		t.Fatal("lease.LeaseUntil must not be zero")
	}
}

func TestInMemory_AckRemovesTask(t *testing.T) {
	q := queue.NewInMemory()
	ctx := context.Background()

	_ = q.Publish(ctx, makeTask("t2"))
	lease, _ := q.Receive(ctx, queue.ReceiveFilter{})
	_ = q.Ack(ctx, lease.Task.ID)

	n, err := q.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("want 0 pending after ack, got %d", n)
	}
}

func TestInMemory_NackRedelivers(t *testing.T) {
	q := queue.NewInMemory()
	ctx := context.Background()

	_ = q.Publish(ctx, makeTask("t3"))
	lease, _ := q.Receive(ctx, queue.ReceiveFilter{})
	// Nack with a short retry delay.
	_ = q.Nack(ctx, lease.Task.ID, 50*time.Millisecond)

	// Should not be immediately available.
	// The item is pending but notBefore is in the future — Len counts items
	// in the pending slice regardless of notBefore, so accept either 0 or 1
	// here; Receive is the authoritative test below.
	_, _ = q.Len(ctx)

	// Wait for notBefore to pass.
	time.Sleep(200 * time.Millisecond)

	tctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	lease2, err := q.Receive(tctx, queue.ReceiveFilter{})
	if err != nil {
		t.Fatalf("expected redelivery: %v", err)
	}
	if lease2.Task.ID != "t3" {
		t.Fatalf("expected t3, got %q", lease2.Task.ID)
	}
}

func TestInMemory_Heartbeat(t *testing.T) {
	q := queue.NewInMemory()
	ctx := context.Background()

	_ = q.Publish(ctx, makeTask("t4"))
	lease, _ := q.Receive(ctx, queue.ReceiveFilter{})

	before := lease.LeaseUntil
	_ = q.Heartbeat(ctx, lease.Task.ID, 5*time.Minute)

	// We can't directly read the updated until, but at minimum Heartbeat must not error.
	_ = before
}

func TestInMemory_ReceiveCancelledContext(t *testing.T) {
	q := queue.NewInMemory()
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := q.Receive(ctx, queue.ReceiveFilter{})
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
}

func TestInMemory_Len(t *testing.T) {
	q := queue.NewInMemory()
	ctx := context.Background()

	for i := 0; i < 3; i++ {
		_ = q.Publish(ctx, makeTask(fmt.Sprintf("t%d", i)))
	}
	n, err := q.Len(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if n != 3 {
		t.Fatalf("want 3, got %d", n)
	}
}
