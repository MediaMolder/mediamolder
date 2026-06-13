// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"mime"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

func TestUploadMultipart_SendsFields(t *testing.T) {
	var gotFields map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm: %v", err)
			w.WriteHeader(500)
			return
		}
		gotFields = map[string]string{
			"index_id": r.FormValue("index_id"),
			"language": r.FormValue("language"),
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "task1"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	resp, err := c.uploadMultipart(
		context.Background(),
		"/tasks",
		[]formField{
			{"index_id", "idx1"},
			{"language", "en"},
		},
		"", "", nil,
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if gotFields["index_id"] != "idx1" {
		t.Errorf("index_id: got %q, want idx1", gotFields["index_id"])
	}
	if gotFields["language"] != "en" {
		t.Errorf("language: got %q, want en", gotFields["language"])
	}
}

func TestUploadMultipart_StreamsFileBytes(t *testing.T) {
	const fileContent = "fake video bytes 1234567890"
	var receivedContent string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		_, params, err := mime.ParseMediaType(ct)
		if err != nil {
			t.Errorf("parse content-type: %v", err)
			w.WriteHeader(400)
			return
		}
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if err != nil {
				t.Errorf("multipart read: %v", err)
				break
			}
			if part.FormName() == "file" {
				b, _ := io.ReadAll(part)
				receivedContent = string(b)
			}
			part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	resp, err := c.uploadMultipart(
		context.Background(),
		"/tasks",
		[]formField{{"index_id", "idx1"}},
		"file", "clip.mp4",
		strings.NewReader(fileContent),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if receivedContent != fileContent {
		t.Errorf("file content: got %q, want %q", receivedContent, fileContent)
	}
}

func TestUploadMultipart_LargeStream(t *testing.T) {
	// Verify 1 MiB of data arrives intact without being buffered in a []byte.
	const size = 1 << 20
	payload := bytes.Repeat([]byte("x"), size)
	var received int

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ct := r.Header.Get("Content-Type")
		_, params, _ := mime.ParseMediaType(ct)
		mr := multipart.NewReader(r.Body, params["boundary"])
		for {
			part, err := mr.NextPart()
			if err == io.EOF {
				break
			}
			if part.FormName() == "file" {
				n, _ := io.Copy(io.Discard, part)
				received = int(n)
			}
			part.Close()
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1"})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	resp, err := c.uploadMultipart(
		context.Background(),
		"/tasks",
		[]formField{{"index_id", "idx1"}},
		"file", "big.mp4",
		bytes.NewReader(payload),
	)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()

	if received != size {
		t.Errorf("received %d bytes, want %d", received, size)
	}
}

func TestUploadMultipart_APIError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(400)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"code":    "invalid_index",
			"message": "index not found",
		})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	_, err := c.uploadMultipart(context.Background(), "/tasks", nil, "", "", nil)
	if err == nil {
		t.Fatal("expected error for 400 response")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.HTTPStatus != 400 {
		t.Errorf("HTTPStatus: got %d, want 400", apiErr.HTTPStatus)
	}
}

func TestCreateIndexTask_File(t *testing.T) {
	var gotField string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		if _, _, err := r.FormFile("video_file"); err == nil {
			gotField = "video_file"
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t2", Status: TaskStatusPending})
	}))
	defer srv.Close()

	tmp, err := os.CreateTemp(t.TempDir(), "*.mp4")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = tmp.WriteString("fake")
	tmp.Close()

	c := newTestClient(srv)
	task, err := c.CreateIndexTask(context.Background(), "idx1", TaskSource{File: tmp.Name()})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "t2" {
		t.Errorf("ID: got %q, want t2", task.ID)
	}
	if gotField != "video_file" {
		t.Errorf("multipart field: got %q, want video_file", gotField)
	}
}

func TestCreateIndexTask_URL(t *testing.T) {
	var gotURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseMultipartForm(1 << 20)
		gotURL = r.FormValue("video_url")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(Task{ID: "t1", Status: TaskStatusPending})
	}))
	defer srv.Close()

	c := newTestClient(srv)
	task, err := c.CreateIndexTask(context.Background(), "idx1", TaskSource{
		URL: "https://example.com/video.mp4",
	})
	if err != nil {
		t.Fatal(err)
	}
	if task.ID != "t1" {
		t.Errorf("ID: got %q, want t1", task.ID)
	}
	if gotURL != "https://example.com/video.mp4" {
		t.Errorf("video_url field: got %q", gotURL)
	}
}

func TestCreateIndexTask_MissingSource(t *testing.T) {
	c := New("key")
	_, err := c.CreateIndexTask(context.Background(), "idx1", TaskSource{})
	if err == nil {
		t.Fatal("expected error when neither File nor URL is set")
	}
}

func TestCreateIndexTask_EmptyIndexID(t *testing.T) {
	c := New("key")
	_, err := c.CreateIndexTask(context.Background(), "", TaskSource{URL: "http://x.com/v.mp4"})
	if err == nil {
		t.Fatal("expected error for empty indexID")
	}
}
