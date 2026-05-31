// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package server

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// minimalConfig is a valid job.Config JSON for test submissions.
// It uses a lavfi source so no real media file is needed.
const minimalConfig = `{
  "schema_version": "1.1",
  "inputs": [
    {
      "id": "src",
      "url": "anullsrc=r=48000:cl=stereo",
      "kind": "lavfi",
      "streams": [{"type": "audio", "track": 0}]
    }
  ],
  "graph": {
    "nodes": [],
    "edges": []
  },
  "outputs": [
    {
      "id": "out",
      "url": "/dev/null",
      "codec_audio": "aac"
    }
  ]
}`

func newTestServer(t *testing.T) *httptest.Server {
	t.Helper()
	srv, err := NewServer(Options{
		Addr:      "127.0.0.1:0",
		AuthToken: "test-token",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	return httptest.NewServer(srv.httpSrv.Handler)
}

func TestHealthz(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("healthz: got %d, want 200", resp.StatusCode)
	}
}

func TestSubmitJob_Unauthorized(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	resp, err := http.Post(ts.URL+"/v1/jobs", "application/json", strings.NewReader(minimalConfig))
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", resp.StatusCode)
	}
}

func TestSubmitJob_BadContentType(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/jobs", strings.NewReader(minimalConfig))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "text/plain")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusUnsupportedMediaType {
		t.Errorf("expected 415, got %d", resp.StatusCode)
	}
}

func TestSubmitJob_InvalidJSON(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/jobs", strings.NewReader("{not json}"))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", resp.StatusCode)
	}
}

func TestGetJob_NotFound(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/jobs/nonexistent", nil)
	req.Header.Set("Authorization", "Bearer test-token")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("expected 404, got %d", resp.StatusCode)
	}
}

func TestSSEAuth_QueryParam(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	// SSE endpoint with token in query param (simulates EventSource which can't
	// set Authorization headers).
	req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/jobs/nonexistent/events?token=test-token", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Job doesn't exist so we expect 404, not 401.
	if resp.StatusCode == http.StatusUnauthorized {
		t.Error("token via query param should be accepted")
	}
}

func TestAllocateUpload_Disabled(t *testing.T) {
	ts := newTestServer(t)
	defer ts.Close()

	req, _ := http.NewRequest(http.MethodPost, ts.URL+"/v1/uploads", bytes.NewReader(nil))
	req.Header.Set("Authorization", "Bearer test-token")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	// Upload is disabled by default — expect 405 Method Not Allowed or 404.
	if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusCreated {
		t.Error("upload endpoint should be disabled when Uploads is nil")
	}
}

func TestBearerAuth_TokenFile(t *testing.T) {
	// Verify that the auth middleware correctly accepts/rejects tokens.
	srv, err := NewServer(Options{
		Addr:      "127.0.0.1:0",
		AuthToken: "secret-bearer",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.httpSrv.Handler)
	defer ts.Close()

	makeReq := func(tok string) int {
		req, _ := http.NewRequest(http.MethodGet, ts.URL+"/v1/jobs/x", nil)
		if tok != "" {
			req.Header.Set("Authorization", "Bearer "+tok)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		return resp.StatusCode
	}

	if got := makeReq(""); got != http.StatusUnauthorized {
		t.Errorf("no token: expected 401, got %d", got)
	}
	if got := makeReq("wrong"); got != http.StatusUnauthorized {
		t.Errorf("wrong token: expected 401, got %d", got)
	}
	if got := makeReq("secret-bearer"); got == http.StatusUnauthorized {
		t.Errorf("correct token: should not be 401, got %d", got)
	}
}

func TestJobArtifacts_ReturnsArtifactList(t *testing.T) {
	// Inject a fake completed job directly into the job manager and verify
	// the artifacts endpoint returns its output list.
	srv, err := NewServer(Options{
		Addr:      "127.0.0.1:0",
		AuthToken: "",
	})
	if err != nil {
		t.Fatalf("NewServer: %v", err)
	}
	ts := httptest.NewServer(srv.httpSrv.Handler)
	defer ts.Close()

	fakeJob := &runningJob{
		id:        "testjob",
		artifacts: []string{"s3://bucket/output.mp4", "s3://bucket/thumbnail.jpg"},
		status:    statusSucceeded,
		done:      make(chan struct{}),
	}
	close(fakeJob.done)
	srv.jobs.mu.Lock()
	srv.jobs.jobs["testjob"] = fakeJob
	srv.jobs.mu.Unlock()

	resp, err := http.Get(ts.URL + "/v1/jobs/testjob/artifacts")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", resp.StatusCode)
	}
	var result struct {
		Artifacts []string `json:"artifacts"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(result.Artifacts) != 2 {
		t.Errorf("expected 2 artifacts, got %d", len(result.Artifacts))
	}
}
