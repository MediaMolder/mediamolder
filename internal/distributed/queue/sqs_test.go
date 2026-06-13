// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/job"
)

// redirectTransport rewrites every outbound request to a fixed base URL so
// the test server receives traffic intended for SQS.
type redirectTransport struct {
	target *url.URL
}

func (rt *redirectTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	cloned := req.Clone(req.Context())
	cloned.URL.Scheme = rt.target.Scheme
	cloned.URL.Host = rt.target.Host
	cloned.URL.Path = rt.target.Path
	cloned.Host = rt.target.Host
	return http.DefaultTransport.RoundTrip(cloned)
}

// newTestSQSQueue constructs an SQSQueue with credentials stubbed out and its
// HTTP client pointed at the provided test server.
func newTestSQSQueue(t *testing.T, srv *httptest.Server) *SQSQueue {
	t.Helper()
	u, _ := url.Parse(srv.URL)
	return &SQSQueue{
		queueURL: "https://sqs.us-east-1.amazonaws.com/123/test-queue",
		region:   "us-east-1",
		httpClient: &http.Client{
			Timeout:   5 * time.Second,
			Transport: &redirectTransport{target: u},
		},
		accessKey: "AKIAIOSFODNN7EXAMPLE",
		secretKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		receipts:  make(map[string]string),
		tasks:     make(map[string]job.Task),
	}
}

// TestSQS_Publish_SigV4Header verifies that Publish sends a request with the
// AWS Authorization SigV4 header present and the correct X-Amz-Target.
func TestSQS_Publish_SigV4Header(t *testing.T) {
	var captured *http.Request
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		captured = r.Clone(r.Context())
		io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "application/json")
		// Minimal SendMessage response
		json.NewEncoder(w).Encode(map[string]any{"MessageId": "msg-1"})
	}))
	defer srv.Close()

	q := newTestSQSQueue(t, srv)
	task := job.Task{ID: "t-sigv4-test", JobID: "j-1"}

	if err := q.Publish(nil, task); err != nil { //nolint:staticcheck
		t.Fatalf("Publish: %v", err)
	}

	if captured == nil {
		t.Fatal("no request received by mock server")
	}
	auth := captured.Header.Get("Authorization")
	if auth == "" {
		t.Error("Authorization header is missing")
	}
	if len(auth) < 4 || auth[:4] != "AWS4" {
		t.Errorf("Authorization header does not look like SigV4: %q", auth)
	}
	target := captured.Header.Get("X-Amz-Target")
	if target != "AmazonSQS.SendMessage" {
		t.Errorf("X-Amz-Target = %q, want AmazonSQS.SendMessage", target)
	}
}

// TestSQS_Publish_Receive_Ack exercises a full round-trip against a mock
// server that simulates the minimal SQS JSON API.
func TestSQS_Publish_Receive_Ack(t *testing.T) {
	task := job.Task{ID: "rt-task-1", JobID: "j-2"}
	taskJSON, _ := json.Marshal(task)

	var receivedSendBody []byte
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		target := r.Header.Get("X-Amz-Target")
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		switch target {
		case "AmazonSQS.SendMessage":
			receivedSendBody = body
			json.NewEncoder(w).Encode(map[string]any{"MessageId": "msg-abc"})
		case "AmazonSQS.ReceiveMessage":
			json.NewEncoder(w).Encode(map[string]any{
				"Messages": []map[string]any{{
					"MessageId":     "msg-abc",
					"ReceiptHandle": "receipt-xyz",
					"Body":          string(taskJSON),
				}},
			})
		case "AmazonSQS.DeleteMessage":
			json.NewEncoder(w).Encode(map[string]any{})
		case "AmazonSQS.GetQueueAttributes":
			json.NewEncoder(w).Encode(map[string]any{
				"Attributes": map[string]string{
					"ApproximateNumberOfMessages": "0",
				},
			})
		default:
			http.Error(w, "unknown target: "+target, http.StatusBadRequest)
		}
	}))
	defer srv.Close()

	q := newTestSQSQueue(t, srv)

	// Publish
	if err := q.Publish(nil, task); err != nil { //nolint:staticcheck
		t.Fatalf("Publish: %v", err)
	}
	var sendReq map[string]any
	if err := json.Unmarshal(receivedSendBody, &sendReq); err != nil {
		t.Fatalf("parse SendMessage body: %v", err)
	}
	if _, ok := sendReq["MessageBody"]; !ok {
		t.Error("SendMessage body missing MessageBody field")
	}

	// Receive
	ctx := t.Context()
	lease, err := q.Receive(ctx, ReceiveFilter{})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if lease.Task.ID != task.ID {
		t.Errorf("Receive task ID = %q, want %q", lease.Task.ID, task.ID)
	}

	// Ack
	if err := q.Ack(ctx, task.ID); err != nil {
		t.Fatalf("Ack: %v", err)
	}
}
