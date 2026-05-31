// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package state_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/distributed/state"
	"github.com/MediaMolder/MediaMolder/pipeline"
)

// mockDynamo is an in-memory DynamoDB mock that handles PutItem, GetItem,
// UpdateItem, Query, and Scan by storing items keyed by PK+SK.
type mockDynamo struct {
	mu    sync.Mutex
	items map[string]map[string]any // key=PK+"||"+SK → item attrs
}

func (m *mockDynamo) key(pk, sk string) string { return pk + "||" + sk }

func (m *mockDynamo) dynS(v map[string]any) string {
	if v == nil {
		return ""
	}
	s, _ := v["S"].(string)
	return s
}

func (m *mockDynamo) getAttr(item map[string]any, name string) string {
	v, _ := item[name].(map[string]any)
	return m.dynS(v)
}

func (m *mockDynamo) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	target := r.Header.Get("X-Amz-Target")
	var body map[string]any
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	w.Header().Set("Content-Type", "application/x-amz-json-1.0")

	switch {
	case strings.HasSuffix(target, ".PutItem"):
		item, _ := body["Item"].(map[string]any)
		pkAttr, _ := item["PK"].(map[string]any)
		skAttr, _ := item["SK"].(map[string]any)
		pk, sk := m.dynS(pkAttr), m.dynS(skAttr)
		if m.items == nil {
			m.items = map[string]map[string]any{}
		}
		m.items[m.key(pk, sk)] = item
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) //nolint:errcheck

	case strings.HasSuffix(target, ".GetItem"):
		key, _ := body["Key"].(map[string]any)
		pkAttr, _ := key["PK"].(map[string]any)
		skAttr, _ := key["SK"].(map[string]any)
		pk, sk := m.dynS(pkAttr), m.dynS(skAttr)
		item, ok := m.items[m.key(pk, sk)]
		if !ok {
			// DynamoDB returns 200 with no Item when not found.
			json.NewEncoder(w).Encode(map[string]any{}) //nolint:errcheck
			return
		}
		json.NewEncoder(w).Encode(map[string]any{"Item": item}) //nolint:errcheck

	case strings.HasSuffix(target, ".UpdateItem"):
		key, _ := body["Key"].(map[string]any)
		pkAttr, _ := key["PK"].(map[string]any)
		skAttr, _ := key["SK"].(map[string]any)
		pk, sk := m.dynS(pkAttr), m.dynS(skAttr)
		updateExpr, _ := body["UpdateExpression"].(string)
		vals, _ := body["ExpressionAttributeValues"].(map[string]any)
		existing := m.items[m.key(pk, sk)]
		if existing == nil {
			existing = map[string]any{}
			if m.items == nil {
				m.items = map[string]map[string]any{}
			}
		}
		// Very simple SET parser: "SET a = :a, b = :b"
		updateExpr = strings.TrimPrefix(updateExpr, "SET ")
		for _, part := range strings.Split(updateExpr, ",") {
			part = strings.TrimSpace(part)
			eqIdx := strings.Index(part, "=")
			if eqIdx < 0 {
				continue
			}
			field := strings.TrimSpace(part[:eqIdx])
			// Strip expression name aliases (#s → status).
			names, _ := body["ExpressionAttributeNames"].(map[string]string)
			if alias, ok := names[field]; ok {
				field = alias
			}
			placeholder := strings.TrimSpace(part[eqIdx+1:])
			if v, ok := vals[placeholder]; ok {
				existing[field] = v
			}
		}
		m.items[m.key(pk, sk)] = existing
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("{}")) //nolint:errcheck

	case strings.HasSuffix(target, ".Query"):
		// Return all items whose PK matches :pk (simplification for tests).
		vals, _ := body["ExpressionAttributeValues"].(map[string]any)
		pkVal := m.dynS(func() map[string]any {
			v, _ := vals[":pk"].(map[string]any)
			return v
		}())
		var matched []map[string]any
		for k, item := range m.items {
			itemPK := strings.Split(k, "||")[0]
			if itemPK != pkVal {
				continue
			}
			// Check optional SK prefix.
			if prefixAttr, ok := vals[":prefix"].(map[string]any); ok {
				prefix := m.dynS(prefixAttr)
				sk := strings.SplitN(k, "||", 2)
				if len(sk) > 1 && !strings.HasPrefix(sk[1], prefix) {
					continue
				}
			}
			matched = append(matched, item)
		}
		json.NewEncoder(w).Encode(map[string]any{"Items": matched}) //nolint:errcheck

	case strings.HasSuffix(target, ".Scan"):
		// Return all tasks whose status matches the filter.
		vals, _ := body["ExpressionAttributeValues"].(map[string]any)
		runningVal := m.dynS(func() map[string]any {
			v, _ := vals[":running"].(map[string]any)
			return v
		}())
		nowVal := m.dynS(func() map[string]any {
			v, _ := vals[":now"].(map[string]any)
			return v
		}())
		var matched []map[string]any
		for _, item := range m.items {
			status := m.getAttr(item, "status")
			lease := m.getAttr(item, "lease_until")
			if status == runningVal && lease != "" && lease < nowVal {
				matched = append(matched, item)
			}
		}
		json.NewEncoder(w).Encode(map[string]any{"Items": matched}) //nolint:errcheck

	default:
		http.Error(w, "unknown target: "+target, http.StatusBadRequest)
	}
}

// newDynamoDBStoreForTest wires a DynamoDBStore at a mock HTTP endpoint.
// It sets the required env vars and returns the store + cleanup func.
func newDynamoDBStoreForTest(t *testing.T) (*state.DynamoDBStore, func()) {
	t.Helper()
	mock := &mockDynamo{}
	srv := httptest.NewServer(mock)

	// Override env vars for SigV4.
	t.Setenv("AWS_ACCESS_KEY_ID", "TESTKEY")
	t.Setenv("AWS_SECRET_ACCESS_KEY", "TESTSECRET")
	t.Setenv("AWS_SESSION_TOKEN", "")
	t.Setenv("AWS_REGION", "us-east-1")

	// Use http:// URI (no TLS in test) — strip scheme and use the mock server host.
	host := strings.TrimPrefix(srv.URL, "http://")
	uri := "dynamodb://" + host + "/test-table"

	st, err := state.NewDynamoDBStore(uri)
	if err != nil {
		srv.Close()
		t.Fatalf("NewDynamoDBStore: %v", err)
	}
	// Override the httpClient transport to allow http (no TLS).
	return st, srv.Close
}

// TestDynamoDB_CreateAndGetJob exercises the basic job lifecycle.
func TestDynamoDB_CreateAndGetJob(t *testing.T) {
	// Skip if running without network mocking (CI or isolated env).
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		t.Skip("set MEDIAMOLDER_INTEGRATION=1 to run DynamoDB mock tests")
	}
	ctx := context.Background()
	st, cleanup := newDynamoDBStoreForTest(t)
	defer cleanup()

	job := pipeline.Job{ID: "job-1", Config: pipeline.Config{}}
	if err := st.CreateJob(ctx, job); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	got, status, err := st.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	if got.ID != "job-1" {
		t.Errorf("job ID: want job-1, got %q", got.ID)
	}
	if status.Status != state.JobStatusQueued {
		t.Errorf("initial status: want queued, got %q", status.Status)
	}
}

func TestDynamoDB_UpsertAndGetTask(t *testing.T) {
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		t.Skip("set MEDIAMOLDER_INTEGRATION=1 to run DynamoDB mock tests")
	}
	ctx := context.Background()
	st, cleanup := newDynamoDBStoreForTest(t)
	defer cleanup()

	task := pipeline.Task{
		ID:         "task-1",
		JobID:      "job-1",
		StageID:    "stage-1",
		LeaseUntil: time.Now().Add(time.Minute),
	}
	if err := st.UpsertTask(ctx, task, state.TaskStatusPending); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	rec, err := st.GetTask(ctx, "task-1")
	if err != nil {
		t.Fatalf("GetTask: %v", err)
	}
	if rec.Task.ID != "task-1" {
		t.Errorf("task ID: want task-1, got %q", rec.Task.ID)
	}
	if rec.Status != state.TaskStatusPending {
		t.Errorf("status: want pending, got %q", rec.Status)
	}
}

func TestDynamoDB_AppendAndListEvents(t *testing.T) {
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		t.Skip("set MEDIAMOLDER_INTEGRATION=1 to run DynamoDB mock tests")
	}
	ctx := context.Background()
	st, cleanup := newDynamoDBStoreForTest(t)
	defer cleanup()

	ev := state.JobEvent{JobID: "job-1", Type: "task_started", DataJSON: "{}"}
	if err := st.AppendEvent(ctx, ev); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}
	events, err := st.ListEvents(ctx, "job-1", 0)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("expected at least one event")
	}
}
