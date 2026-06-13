// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue_test

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/internal/distributed/queue"
	"github.com/MediaMolder/MediaMolder/job"
)

func TestTaskSatisfiedBy(t *testing.T) {
	t.Run("empty filter matches task with no requirements", func(t *testing.T) {
		// A task with no requirements is satisfied by any filter including an empty one.
		task := job.Task{}
		if !queue.TaskSatisfiedBy(task, queue.ReceiveFilter{}) {
			t.Fatal("empty filter should match task with no requirements")
		}
	})

	t.Run("capability match succeeds", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{
			HardwareAccel: []string{"cuda"},
			Codecs:        []string{"h264_nvenc"},
		}}
		f := queue.ReceiveFilter{Capabilities: []string{"CUDA", "H264_NVENC", "hevc_nvenc"}}
		if !queue.TaskSatisfiedBy(task, f) {
			t.Fatal("should match — worker has all required caps (case-insensitive)")
		}
	})

	t.Run("missing hardware accel rejects", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{HardwareAccel: []string{"cuda"}}}
		f := queue.ReceiveFilter{Capabilities: []string{"opencl"}}
		if queue.TaskSatisfiedBy(task, f) {
			t.Fatal("should not match — worker lacks cuda")
		}
	})

	t.Run("missing codec rejects", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{Codecs: []string{"h264_nvenc"}}}
		f := queue.ReceiveFilter{Capabilities: []string{"cuda"}}
		if queue.TaskSatisfiedBy(task, f) {
			t.Fatal("should not match — worker lacks h264_nvenc codec")
		}
	})

	t.Run("region match succeeds", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{Region: "us-east-1"}}
		f := queue.ReceiveFilter{Region: "us-east-1"}
		if !queue.TaskSatisfiedBy(task, f) {
			t.Fatal("region matches — should accept")
		}
	})

	t.Run("region mismatch rejects", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{Region: "eu-west-1"}}
		f := queue.ReceiveFilter{Region: "us-east-1"}
		if queue.TaskSatisfiedBy(task, f) {
			t.Fatal("region mismatch — should reject")
		}
	})

	t.Run("no region requirement accepts any worker region", func(t *testing.T) {
		task := job.Task{Requires: job.WorkerRequirements{}}
		f := queue.ReceiveFilter{Region: "ap-southeast-1"}
		if !queue.TaskSatisfiedBy(task, f) {
			t.Fatal("task has no region requirement — any worker region should match")
		}
	})

	t.Run("worker has no region, task requires one — accepts", func(t *testing.T) {
		// When filter.Region is empty the worker hasn't advertised a region,
		// so the region constraint is not enforced.
		task := job.Task{Requires: job.WorkerRequirements{Region: "us-east-1"}}
		f := queue.ReceiveFilter{}
		if !queue.TaskSatisfiedBy(task, f) {
			t.Fatal("worker without region should not be blocked by region requirement")
		}
	})
}
