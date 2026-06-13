// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// AWS Signature Version 4 presigned URL generation for Amazon S3.
// Pure Go — no external dependencies.
//
// Reference:
//
//	https://docs.aws.amazon.com/AmazonS3/latest/API/sigv4-query-string-auth.html
package storage

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"
)

// s3PresignURL generates an AWS SigV4 presigned URL for an S3 object.
// uri must be in the form s3://bucket/key.
func s3PresignURL(uri string, creds S3Credentials, op Op, now time.Time, ttl time.Duration) (string, error) {
	bucket, key, err := parseS3URI(uri)
	if err != nil {
		return "", err
	}
	if bucket == "" {
		return "", fmt.Errorf("s3 presign: missing bucket in %q", uri)
	}
	if creds.Region == "" {
		creds.Region = "us-east-1"
	}

	method := httpMethod(op)
	host := fmt.Sprintf("%s.s3.%s.amazonaws.com", bucket, creds.Region)

	// Encode the object key preserving '/' path separators.
	keyPath := "/" + s3EncodeKey(key)

	// ISO8601 timestamps used by SigV4.
	dateTime := now.UTC().Format("20060102T150405Z")
	date := now.UTC().Format("20060102")

	credential := fmt.Sprintf("%s/%s/%s/s3/aws4_request",
		creds.AccessKeyID, date, creds.Region)
	expiresStr := fmt.Sprintf("%d", int64(ttl.Seconds()))

	// Build the query parameters (all except X-Amz-Signature).
	params := url.Values{}
	params.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	params.Set("X-Amz-Credential", credential)
	params.Set("X-Amz-Date", dateTime)
	params.Set("X-Amz-Expires", expiresStr)
	if creds.SessionToken != "" {
		params.Set("X-Amz-Security-Token", creds.SessionToken)
	}
	params.Set("X-Amz-SignedHeaders", "host")

	canonicalQS := buildCanonicalQueryString(params)

	// Canonical request:
	//   Method\nURI\nQueryString\nCanonicalHeaders\n\nSignedHeaders\nPayloadHash
	canonicalRequest := strings.Join([]string{
		method,
		keyPath,
		canonicalQS,
		"host:" + host, // canonical headers (each header ends with \n via Join separator)
		"",             // blank line separator between headers and signedHeaders
		"host",
		"UNSIGNED-PAYLOAD",
	}, "\n")

	// String to sign.
	scope := strings.Join([]string{date, creds.Region, "s3", "aws4_request"}, "/")
	h := sha256.New()
	h.Write([]byte(canonicalRequest))
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		dateTime,
		scope,
		hex.EncodeToString(h.Sum(nil)),
	}, "\n")

	// Derived signing key: HMAC(HMAC(HMAC(HMAC("AWS4"+secret, date), region), service), "aws4_request")
	signingKey := sigv4DerivedKey(creds.SecretAccessKey, date, creds.Region, "s3")
	sig := hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	return fmt.Sprintf("https://%s%s?%s&X-Amz-Signature=%s", host, keyPath, canonicalQS, sig), nil
}

// parseS3URI splits "s3://bucket/key/to/object" into (bucket, key, nil).
func parseS3URI(uri string) (bucket, key string, err error) {
	if !strings.HasPrefix(uri, "s3://") {
		return "", "", fmt.Errorf("not an S3 URI: %q", uri)
	}
	rest := strings.TrimPrefix(uri, "s3://")
	idx := strings.IndexByte(rest, '/')
	if idx < 0 {
		return rest, "", nil
	}
	return rest[:idx], rest[idx+1:], nil
}

// s3EncodeKey percent-encodes an S3 object key, preserving '/' separators.
func s3EncodeKey(key string) string {
	parts := strings.Split(key, "/")
	encoded := make([]string, len(parts))
	for i, p := range parts {
		encoded[i] = strings.ReplaceAll(url.PathEscape(p), "+", "%20")
	}
	return strings.Join(encoded, "/")
}

// buildCanonicalQueryString sorts params and encodes them in the form
// required by AWS SigV4 (spaces as %20, not +).
func buildCanonicalQueryString(params url.Values) string {
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	parts := make([]string, 0, len(keys))
	for _, k := range keys {
		ek := awsQueryEscape(k)
		ev := awsQueryEscape(params.Get(k))
		parts = append(parts, ek+"="+ev)
	}
	return strings.Join(parts, "&")
}

// awsQueryEscape percent-encodes a string for use in an AWS canonical query
// string (spaces become %20, not +).
func awsQueryEscape(s string) string {
	return strings.ReplaceAll(url.QueryEscape(s), "+", "%20")
}

// sigv4DerivedKey computes the AWS SigV4 derived signing key via four rounds
// of HMAC-SHA256.
func sigv4DerivedKey(secretKey, date, region, service string) []byte {
	kSecret := []byte("AWS4" + secretKey)
	kDate := hmacSHA256(kSecret, []byte(date))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	h := hmac.New(sha256.New, key)
	h.Write(data)
	return h.Sum(nil)
}

func httpMethod(op Op) string {
	switch op {
	case OpPut:
		return "PUT"
	case OpHead:
		return "HEAD"
	default:
		return "GET"
	}
}
