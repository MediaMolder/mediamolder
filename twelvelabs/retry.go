// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"context"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"time"
)

// initBackoff and maxBackoff are vars (not consts) so tests can override them.
var (
	initBackoff = time.Second
	maxBackoff  = 30 * time.Second
)

const maxAttempts = 5

// withRetry executes fn up to maxAttempts times, retrying on 429 and 5xx
// responses with jittered exponential backoff.
//
// On each retryable response the body is drained and closed before sleeping.
// Non-retryable responses (4xx other than 429, 2xx) are returned as-is for
// the caller to inspect and close.
// Returns a non-nil error after all attempts are exhausted.
func withRetry(ctx context.Context, fn func(context.Context) (*http.Response, error)) (*http.Response, error) {
	delay := initBackoff
	for attempt := 0; attempt < maxAttempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(delay):
			}
		}

		resp, err := fn(ctx)
		if err != nil {
			return nil, err
		}

		switch {
		case resp.StatusCode == 429:
			retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
			drainClose(resp.Body)
			if retryAfter > 0 {
				delay = retryAfter
			} else {
				delay = jitterDelay(delay, maxBackoff)
			}
		case resp.StatusCode >= 500 && resp.StatusCode < 600:
			drainClose(resp.Body)
			delay = jitterDelay(delay*2, maxBackoff)
		default:
			return resp, nil
		}
	}
	return nil, fmt.Errorf("twelvelabs: all %d retry attempts failed", maxAttempts)
}

// parseRetryAfter parses the Retry-After header as an integer number of seconds.
// Returns 0 if absent, non-numeric, or zero/negative.
func parseRetryAfter(h string) time.Duration {
	if h == "" {
		return 0
	}
	secs, err := strconv.Atoi(h)
	if err != nil || secs <= 0 {
		return 0
	}
	return time.Duration(secs) * time.Second
}

// jitterDelay caps d at max and adds up to 20% positive random jitter.
func jitterDelay(d, max time.Duration) time.Duration {
	if d <= 0 {
		d = initBackoff
	}
	if d > max {
		d = max
	}
	delta := time.Duration(rand.Int64N(int64(d/5) + 1))
	return d + delta
}

// drainClose discards remaining bytes so the TCP connection can be reused,
// then closes rc.
func drainClose(rc io.ReadCloser) {
	io.Copy(io.Discard, rc) //nolint:errcheck
	rc.Close()
}
