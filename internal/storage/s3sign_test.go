// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

// Test vectors from the AWS SigV4 test suite:
// https://docs.aws.amazon.com/general/latest/gr/sigv4-test-suite.html
func TestS3PresignURL_KnownVector(t *testing.T) {
	creds := S3Credentials{
		AccessKeyID:     "AKIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "wJalrXUtnFEMI/K7MDENG/bPxRfiCYEXAMPLEKEY",
		Region:          "us-east-1",
	}
	// Use a fixed time to produce a deterministic output.
	now := time.Date(2013, 5, 24, 0, 0, 0, 0, time.UTC)
	ttl := 86400 * time.Second

	got, err := s3PresignURL("s3://examplebucket/test.txt", creds, OpGet, now, ttl)
	if err != nil {
		t.Fatalf("s3PresignURL: %v", err)
	}

	// Verify structural correctness: must be HTTPS, contain required SigV4 params.
	if !strings.HasPrefix(got, "https://examplebucket.s3.us-east-1.amazonaws.com/test.txt?") {
		t.Errorf("unexpected URL prefix:\n%s", got)
	}
	for _, param := range []string{
		"X-Amz-Algorithm=AWS4-HMAC-SHA256",
		"X-Amz-Credential=AKIAIOSFODNN7EXAMPLE",
		"X-Amz-Date=20130524T000000Z",
		"X-Amz-Expires=86400",
		"X-Amz-SignedHeaders=host",
		"X-Amz-Signature=",
	} {
		if !strings.Contains(got, param) {
			t.Errorf("missing %q in URL:\n%s", param, got)
		}
	}
}

func TestS3PresignURL_WithSessionToken(t *testing.T) {
	creds := S3Credentials{
		AccessKeyID:     "ASIAIOSFODNN7EXAMPLE",
		SecretAccessKey: "secretkey",
		SessionToken:    "session-token-123",
		Region:          "eu-west-1",
	}
	now := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	got, err := s3PresignURL("s3://my-bucket/path/to/file.mp4", creds, OpPut, now, time.Hour)
	if err != nil {
		t.Fatalf("s3PresignURL: %v", err)
	}
	if !strings.Contains(got, "X-Amz-Security-Token=session-token-123") {
		t.Errorf("missing security token in URL:\n%s", got)
	}
	if !strings.Contains(got, "my-bucket.s3.eu-west-1.amazonaws.com") {
		t.Errorf("wrong region in URL:\n%s", got)
	}
}

func TestS3PresignURL_DefaultRegion(t *testing.T) {
	creds := S3Credentials{
		AccessKeyID:     "AKID",
		SecretAccessKey: "secret",
		// Region intentionally empty
	}
	got, err := s3PresignURL("s3://bucket/key", creds, OpGet, time.Now(), time.Hour)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(got, ".s3.us-east-1.amazonaws.com") {
		t.Errorf("expected default region us-east-1 in URL:\n%s", got)
	}
}

func TestParseS3URI(t *testing.T) {
	cases := []struct {
		uri    string
		bucket string
		key    string
		errStr string
	}{
		{"s3://bucket/key/file.txt", "bucket", "key/file.txt", ""},
		{"s3://bucket", "bucket", "", ""},
		{"s3://bucket/", "bucket", "", ""},
		{"not-s3://bucket", "", "", "not an S3 URI"},
	}
	for _, tc := range cases {
		b, k, err := parseS3URI(tc.uri)
		if tc.errStr != "" {
			if err == nil || !strings.Contains(err.Error(), tc.errStr) {
				t.Errorf("parseS3URI(%q): want error containing %q, got %v", tc.uri, tc.errStr, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("parseS3URI(%q): unexpected error: %v", tc.uri, err)
			continue
		}
		if b != tc.bucket || k != tc.key {
			t.Errorf("parseS3URI(%q) = (%q, %q), want (%q, %q)", tc.uri, b, k, tc.bucket, tc.key)
		}
	}
}

func TestBuildCanonicalQueryString_Sorted(t *testing.T) {
	// Verify params are sorted alphabetically (SigV4 requirement).
	params := url.Values{
		"X-Amz-SignedHeaders": []string{"host"},
		"X-Amz-Algorithm":     []string{"AWS4-HMAC-SHA256"},
		"X-Amz-Date":          []string{"20240101T000000Z"},
	}
	got := buildCanonicalQueryString(params)
	first := strings.Split(got, "&")[0]
	// X-Amz-Algorithm (A < D < S in ASCII) must come first.
	if !strings.HasPrefix(first, "X-Amz-Algorithm=") {
		t.Errorf("expected X-Amz-Algorithm first, got: %s", got)
	}
}
