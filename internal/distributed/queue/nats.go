// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"github.com/nats-io/nats.go"

	"github.com/MediaMolder/MediaMolder/job"
)

const (
	natsDefaultStream   = "MEDIAMOLDER_TASKS"
	natsDefaultSubject  = "mediamolder.tasks"
	natsDefaultConsumer = "mediamolder-worker"
	natsDefaultAckWait  = 35 * time.Second // slightly longer than leaseExtension
)

// NATSQueue is a durable task queue backed by NATS JetStream.
//
// Messages carry the full job.Task payload as JSON. The stream uses
// WorkQueuePolicy so each message is delivered to exactly one consumer.
// Heartbeat calls msg.InProgress() to reset the AckWait timer; Ack/Nack
// call msg.Ack() / msg.NakWithDelay() respectively.
//
// URI format: nats://[user:pass@]host:port[/stream-name]
type NATSQueue struct {
	nc      *nats.Conn
	js      nats.JetStreamContext
	stream  string
	subject string
	sub     *nats.Subscription

	mu       sync.Mutex
	inFlight map[string]*nats.Msg // task ID → message
}

// NewNATSQueue connects to a NATS server and creates (or binds to) a
// JetStream WorkQueue stream. streamName defaults to MEDIAMOLDER_TASKS.
func NewNATSQueue(url, streamName string) (*NATSQueue, error) {
	if streamName == "" {
		streamName = natsDefaultStream
	}
	subject := fmt.Sprintf("mediamolder.tasks.%s", streamName)

	nc, err := nats.Connect(url,
		nats.RetryOnFailedConnect(true),
		nats.MaxReconnects(10),
		nats.ReconnectWait(2*time.Second),
	)
	if err != nil {
		return nil, fmt.Errorf("nats: connect %q: %w", url, err)
	}

	js, err := nc.JetStream()
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: jetstream context: %w", err)
	}

	// Create stream if it doesn't exist yet.
	_, err = js.StreamInfo(streamName)
	if err != nil {
		_, err = js.AddStream(&nats.StreamConfig{
			Name:      streamName,
			Subjects:  []string{subject},
			Retention: nats.WorkQueuePolicy,
			Storage:   nats.FileStorage,
			MaxAge:    7 * 24 * time.Hour,
		})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("nats: create stream %q: %w", streamName, err)
		}
	}

	// Create durable pull consumer if it doesn't exist yet.
	consumerName := fmt.Sprintf("%s-worker", streamName)
	_, err = js.ConsumerInfo(streamName, consumerName)
	if err != nil {
		_, err = js.AddConsumer(streamName, &nats.ConsumerConfig{
			Durable:       consumerName,
			AckPolicy:     nats.AckExplicitPolicy,
			AckWait:       natsDefaultAckWait,
			MaxDeliver:    -1,
			FilterSubject: subject,
		})
		if err != nil {
			nc.Close()
			return nil, fmt.Errorf("nats: create consumer %q: %w", consumerName, err)
		}
	}

	sub, err := js.PullSubscribe(subject, consumerName, nats.BindStream(streamName))
	if err != nil {
		nc.Close()
		return nil, fmt.Errorf("nats: pull subscribe %q: %w", subject, err)
	}

	return &NATSQueue{
		nc:       nc,
		js:       js,
		stream:   streamName,
		subject:  subject,
		sub:      sub,
		inFlight: make(map[string]*nats.Msg),
	}, nil
}

// Publish serialises t to JSON and publishes it to the JetStream subject.
func (q *NATSQueue) Publish(_ context.Context, t job.Task) error {
	b, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("nats: marshal task: %w", err)
	}
	_, err = q.js.Publish(q.subject, b)
	return err
}

// Receive blocks until a task is available, or ctx is cancelled.
// It fetches one message per call using a short poll loop so ctx is honoured.
func (q *NATSQueue) Receive(ctx context.Context, _ ReceiveFilter) (Lease, error) {
	for {
		msgs, err := q.sub.Fetch(1, nats.MaxWait(250*time.Millisecond))
		if err == nil && len(msgs) == 1 {
			msg := msgs[0]
			var t job.Task
			if err := json.Unmarshal(msg.Data, &t); err != nil {
				// Unparseable message — ack it to discard, don't block the queue.
				_ = msg.Ack()
				continue
			}
			q.mu.Lock()
			q.inFlight[t.ID] = msg
			q.mu.Unlock()
			return Lease{
				Task:       t,
				LeaseUntil: time.Now().Add(natsDefaultAckWait),
			}, nil
		}
		// Nothing available yet — check ctx before retrying.
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		default:
		}
	}
}

// Heartbeat sends an InProgress signal to reset the AckWait for taskID.
func (q *NATSQueue) Heartbeat(_ context.Context, taskID string, _ time.Duration) error {
	q.mu.Lock()
	msg, ok := q.inFlight[taskID]
	q.mu.Unlock()
	if !ok {
		return fmt.Errorf("nats: no in-flight lease for task %q", taskID)
	}
	return msg.InProgress()
}

// Ack acknowledges taskID (successful completion).
func (q *NATSQueue) Ack(_ context.Context, taskID string) error {
	q.mu.Lock()
	msg, ok := q.inFlight[taskID]
	delete(q.inFlight, taskID)
	q.mu.Unlock()
	if !ok {
		return nil
	}
	return msg.Ack()
}

// Nack returns taskID to the queue after retryAfter.
func (q *NATSQueue) Nack(_ context.Context, taskID string, retryAfter time.Duration) error {
	q.mu.Lock()
	msg, ok := q.inFlight[taskID]
	delete(q.inFlight, taskID)
	q.mu.Unlock()
	if !ok {
		return nil
	}
	return msg.NakWithDelay(retryAfter)
}

// Len returns the approximate number of pending (undelivered) messages.
func (q *NATSQueue) Len(_ context.Context) (int, error) {
	info, err := q.js.StreamInfo(q.stream)
	if err != nil {
		return 0, fmt.Errorf("nats: stream info: %w", err)
	}
	return int(info.State.Msgs), nil
}

// Close drains the NATS connection.
func (q *NATSQueue) Close() {
	q.nc.Drain() //nolint:errcheck
}
