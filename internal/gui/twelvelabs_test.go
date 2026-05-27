// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

func newTLAPIMock(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/indexes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"_id": "idx-1", "index_name": "demo"},
				},
			})
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_id": "idx-new", "index_name": body["index_name"],
			})
		}
	})
	mux.HandleFunc("/indexes/", func(w http.ResponseWriter, r *http.Request) {
		// DELETE /indexes/{id}
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/search", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []map[string]any{
				{"video_id": "v1", "start": 0.0, "end": 1.0, "score": 0.9, "confidence": "high"},
			},
		})
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

// withTLClient swaps the package-level factory for the duration of a test.
func withTLClient(t *testing.T, baseURL string) {
	t.Helper()
	orig := twelvelabsClientFactory
	twelvelabsClientFactory = func(_ *http.Request) (*twelvelabs.Client, error) {
		c := twelvelabs.New("test-key")
		c.BaseURL = baseURL
		return c, nil
	}
	t.Cleanup(func() { twelvelabsClientFactory = orig })
}

func tlMux() *http.ServeMux {
	mux := http.NewServeMux()
	registerTwelveLabsRoutes(mux)
	return mux
}

func doJSON(t *testing.T, h http.Handler, method, path string, body any) (*http.Response, []byte) {
	t.Helper()
	var r *http.Request
	if body != nil {
		buf, _ := json.Marshal(body)
		r = httptest.NewRequest(method, path, bytes.NewReader(buf))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	rw := httptest.NewRecorder()
	h.ServeHTTP(rw, r)
	resp := rw.Result()
	data, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, data
}

func TestTwelveLabsAPI_Ping(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodGet, "/api/twelvelabs/ping", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"ok":true`) {
		t.Fatalf("ping: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_ListIndexes(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodGet, "/api/twelvelabs/indexes", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), "idx-1") {
		t.Fatalf("list: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_CreateIndex(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodPost, "/api/twelvelabs/indexes", map[string]any{
		"index_name": "demo",
		"models": []map[string]any{{"model_name": "marengo3.0"}},
	})
	if resp.StatusCode != 200 || !strings.Contains(string(body), "idx-new") {
		t.Fatalf("create: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_CreateIndex_MissingName(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodPost, "/api/twelvelabs/indexes", map[string]any{})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "name") {
		t.Fatalf("expected 400 missing name, got status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_DeleteIndex(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodDelete, "/api/twelvelabs/indexes/idx-1", nil)
	if resp.StatusCode != 200 || !strings.Contains(string(body), `"deleted"`) {
		t.Fatalf("delete: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_Search(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodPost, "/api/twelvelabs/search", map[string]any{
		"index_id": "idx-1",
		"query":    "cat",
	})
	if resp.StatusCode != 200 || !strings.Contains(string(body), "matches") {
		t.Fatalf("search: status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_Search_MissingIndex(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodPost, "/api/twelvelabs/search", map[string]any{
		"query": "cat",
	})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "index_id") {
		t.Fatalf("expected 400, got status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_Search_MissingQuery(t *testing.T) {
	srv := newTLAPIMock(t)
	withTLClient(t, srv.URL)
	resp, body := doJSON(t, tlMux(), http.MethodPost, "/api/twelvelabs/search", map[string]any{
		"index_id": "idx-1",
	})
	if resp.StatusCode != http.StatusBadRequest || !strings.Contains(string(body), "query") {
		t.Fatalf("expected 400, got status=%d body=%s", resp.StatusCode, body)
	}
}

func TestTwelveLabsAPI_AuthError(t *testing.T) {
	orig := twelvelabsClientFactory
	twelvelabsClientFactory = func(*http.Request) (*twelvelabs.Client, error) {
		return nil, errAuthForTest
	}
	t.Cleanup(func() { twelvelabsClientFactory = orig })
	resp, _ := doJSON(t, tlMux(), http.MethodGet, "/api/twelvelabs/ping", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("expected 401, got %d", resp.StatusCode)
	}
}

var errAuthForTest = errAuth("no key")

type errAuth string

func (e errAuth) Error() string { return string(e) }
