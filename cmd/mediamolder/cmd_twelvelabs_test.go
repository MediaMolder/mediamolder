// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// captureTLStdout runs fn while capturing os.Stdout and returns the captured bytes.
func captureTLStdout(t *testing.T, fn func() error) ([]byte, error) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	old := os.Stdout
	os.Stdout = w
	defer func() { os.Stdout = old }()

	done := make(chan struct{})
	var buf bytes.Buffer
	go func() {
		_, _ = io.Copy(&buf, r)
		close(done)
	}()

	runErr := fn()
	_ = w.Close()
	<-done
	return buf.Bytes(), runErr
}

func newTLMock(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/indexes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case http.MethodGet:
			_ = json.NewEncoder(w).Encode(map[string]any{
				"data": []map[string]any{
					{"_id": "idx-1", "name": "demo", "models": []any{}},
				},
			})
		case http.MethodPost:
			var body map[string]any
			_ = json.NewDecoder(r.Body).Decode(&body)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"_id": "idx-new", "name": body["index_name"],
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
	mux.HandleFunc("/analyze", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/x-ndjson")
		for _, line := range []string{
			`{"event_type":"stream_start","metadata":{}}`,
			`{"event_type":"text_generation","text":"summary"}`,
			`{"event_type":"stream_end","metadata":{}}`,
		} {
			_, _ = w.Write([]byte(line + "\n"))
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestTwelveLabsCLI_Help(t *testing.T) {
	out, err := captureTLStdout(t, func() error { return cmdTwelveLabs([]string{"help"}) })
	if err != nil {
		t.Fatalf("help: %v", err)
	}
	if !strings.Contains(string(out), "Subcommands:") {
		t.Errorf("missing usage banner: %s", out)
	}
}

func TestTwelveLabsCLI_Unknown(t *testing.T) {
	_, err := captureTLStdout(t, func() error { return cmdTwelveLabs([]string{"nope"}) })
	if err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("expected unknown subcommand error, got %v", err)
	}
}

func TestTwelveLabsCLI_IndexesList(t *testing.T) {
	srv := newTLMock(t)
	t.Setenv("TWELVELABS_API_KEY", "k")
	out, err := captureTLStdout(t, func() error {
		return cmdTwelveLabs([]string{"indexes", "list", "--base-url", srv.URL})
	})
	if err != nil {
		t.Fatalf("indexes list: %v", err)
	}
	if !strings.Contains(string(out), "idx-1") {
		t.Errorf("expected idx-1 in output, got %s", out)
	}
}

func TestTwelveLabsCLI_IndexesCreate(t *testing.T) {
	srv := newTLMock(t)
	t.Setenv("TWELVELABS_API_KEY", "k")
	out, err := captureTLStdout(t, func() error {
		return cmdTwelveLabs([]string{"indexes", "create",
			"--base-url", srv.URL, "--name", "demo", "--models", "marengo3.0"})
	})
	if err != nil {
		t.Fatalf("indexes create: %v", err)
	}
	if !strings.Contains(string(out), "idx-new") {
		t.Errorf("expected idx-new in output, got %s", out)
	}
}

func TestTwelveLabsCLI_IndexesDelete(t *testing.T) {
	srv := newTLMock(t)
	t.Setenv("TWELVELABS_API_KEY", "k")
	out, err := captureTLStdout(t, func() error {
		return cmdTwelveLabs([]string{"indexes", "delete",
			"--base-url", srv.URL, "idx-1"})
	})
	if err != nil {
		t.Fatalf("indexes delete: %v", err)
	}
	if !strings.Contains(string(out), `"deleted"`) {
		t.Errorf("expected deleted result, got %s", out)
	}
}

func TestTwelveLabsCLI_Search(t *testing.T) {
	srv := newTLMock(t)
	t.Setenv("TWELVELABS_API_KEY", "k")
	out, err := captureTLStdout(t, func() error {
		return cmdTwelveLabs([]string{"search", "--base-url", srv.URL,
			"--index", "idx-1", "--query", "cat"})
	})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !strings.Contains(string(out), "matches") {
		t.Errorf("expected matches in output, got %s", out)
	}
}

func TestTwelveLabsCLI_Analyze(t *testing.T) {
	srv := newTLMock(t)
	t.Setenv("TWELVELABS_API_KEY", "k")
	out, err := captureTLStdout(t, func() error {
		return cmdTwelveLabs([]string{"analyze", "--base-url", srv.URL,
			"--video-id", "v1", "--prompt", "summarise"})
	})
	if err != nil {
		t.Fatalf("analyze: %v", err)
	}
	if !strings.Contains(string(out), `"summary"`) {
		t.Errorf("expected summary in output, got %s", out)
	}
}

func TestTwelveLabsCLI_RequiredFlags(t *testing.T) {
	t.Setenv("TWELVELABS_API_KEY", "k")
	cases := []struct {
		name string
		args []string
		want string
	}{
		{"index missing --index", []string{"index", "file.mp4"}, "--index"},
		{"index missing file", []string{"index", "--index", "i"}, "exactly one"},
		{"analyze missing video", []string{"analyze", "--prompt", "p"}, "--video-id or --video-url"},
		{"search missing index", []string{"search", "--query", "q"}, "--index"},
		{"search missing query", []string{"search", "--index", "i"}, "--query"},
		{"embed missing video", []string{"embed"}, "--video"},
		{"indexes create missing name", []string{"indexes", "create"}, "--name"},
		{"indexes delete missing id", []string{"indexes", "delete"}, "exactly one"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := cmdTwelveLabs(tc.args)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Errorf("got err=%v, want substring %q", err, tc.want)
			}
		})
	}
}

func TestResolveAPIKey_Precedence(t *testing.T) {
	// Flag wins.
	t.Setenv("TWELVELABS_API_KEY", "envk")
	got, err := resolveAPIKey("flagk")
	if err != nil || got != "flagk" {
		t.Errorf("flag precedence: got %q err=%v", got, err)
	}
	// Env wins when flag empty.
	got, err = resolveAPIKey("")
	if err != nil || got != "envk" {
		t.Errorf("env precedence: got %q err=%v", got, err)
	}
	// Config file fallback when env unset and file present.
	t.Setenv("TWELVELABS_API_KEY", "")
	dir := t.TempDir()
	cfg := filepath.Join(dir, "twelvelabs.json")
	if err := os.WriteFile(cfg, []byte(`{"api_key":"filek"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	oldPath := twelvelabsConfigFile
	twelvelabsConfigFile = cfg
	defer func() { twelvelabsConfigFile = oldPath }()
	got, err = resolveAPIKey("")
	if err != nil || got != "filek" {
		t.Errorf("file precedence: got %q err=%v", got, err)
	}
	// No source → error.
	twelvelabsConfigFile = filepath.Join(dir, "missing.json")
	if _, err := resolveAPIKey(""); err == nil {
		t.Error("expected error when no key source available")
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV("a, b ,,c")
	want := []string{"a", "b", "c"}
	if len(got) != len(want) {
		t.Fatalf("got %v want %v", got, want)
	}
	for i := range got {
		if got[i] != want[i] {
			t.Errorf("got[%d]=%q want %q", i, got[i], want[i])
		}
	}
}
