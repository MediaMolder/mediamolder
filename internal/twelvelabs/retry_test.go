// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"net/http"
	"sync/atomic"
	"testing"
	"time"
)

func init() {
	// Speed up backoff during tests.
	initBackoff = time.Millisecond
	maxBackoff = 10 * time.Millisecond
}

func TestWithRetry_SuccessFirstAttempt(t *testing.T) {
	calls := 0
	resp, err := withRetry(context.Background(), func(_ context.Context) (*http.Response, error) {
		calls++
		return fakeResponse(200, `{}`), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls != 1 {
		t.Errorf("calls: got %d, want 1", calls)
	}
}

func TestWithRetry_RetriesOn5xx(t *testing.T) {
	var calls int32
	resp, err := withRetry(context.Background(), func(_ context.Context) (*http.Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n < 3 {
			return fakeResponse(503, `{"message":"unavailable"}`), nil
		}
		return fakeResponse(200, `{}`), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls != 3 {
		t.Errorf("calls: got %d, want 3", calls)
	}
}

func TestWithRetry_RetriesOn429(t *testing.T) {
	var calls int32
	resp, err := withRetry(context.Background(), func(_ context.Context) (*http.Response, error) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			r := fakeResponse(429, `{"code":"rate_limit","message":"Too many requests"}`)
			r.Header.Set("Retry-After", "0")
			return r, nil
		}
		return fakeResponse(200, `{}`), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls != 2 {
		t.Errorf("calls: got %d, want 2", calls)
	}
}

func TestWithRetry_ExhaustsAttempts(t *testing.T) {
	var calls int32
	_, err := withRetry(context.Background(), func(_ context.Context) (*http.Response, error) {
		atomic.AddInt32(&calls, 1)
		return fakeResponse(500, `{"message":"error"}`), nil
	})
	if err == nil {
		t.Fatal("expected error after retries exhausted")
	}
	if calls != maxAttempts {
		t.Errorf("calls: got %d, want %d", calls, maxAttempts)
	}
}

func TestWithRetry_PassesThrough4xx(t *testing.T) {
	// 4xx errors (not 429) should be returned without retry.
	calls := 0
	resp, err := withRetry(context.Background(), func(_ context.Context) (*http.Response, error) {
		calls++
		return fakeResponse(400, `{"code":"bad_request","message":"bad"}`), nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resp.Body.Close()
	if calls != 1 {
		t.Errorf("calls: got %d, want 1 (4xx should not retry)", calls)
	}
}

func TestWithRetry_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	_, err := withRetry(ctx, func(_ context.Context) (*http.Response, error) {
		calls++
		cancel() // cancel before retry sleep; next iteration should abort
		return fakeResponse(503, ""), nil
	})
	if err == nil {
		t.Fatal("expected error when context is cancelled")
	}
	// Should stop quickly — at most 2 calls before the cancelled ctx is checked.
	if calls > 2 {
		t.Errorf("calls: got %d, want ≤2 with immediate cancel", calls)
	}
}

func TestParseRetryAfter_ValidInt(t *testing.T) {
	if got := parseRetryAfter("30"); got != 30*time.Second {
		t.Errorf("got %v, want 30s", got)
	}
}

func TestParseRetryAfter_Zero(t *testing.T) {
	if got := parseRetryAfter("0"); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestParseRetryAfter_Empty(t *testing.T) {
	if got := parseRetryAfter(""); got != 0 {
		t.Errorf("got %v, want 0", got)
	}
}

func TestParseRetryAfter_NonNumeric(t *testing.T) {
	if got := parseRetryAfter("Wed, 21 Oct 2015 07:28:00 GMT"); got != 0 {
		t.Errorf("got %v, want 0 for HTTP-date format", got)
	}
}
