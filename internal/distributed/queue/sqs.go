// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package queue

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// SQSQueue is a task queue backed by Amazon SQS, using the JSON API
// (Content-Type: application/x-amz-json-1.0) and pure-Go AWS SigV4 signing.
//
// URI format: sqs://sqs.{region}.amazonaws.com/{account-id}/{queue-name}
// Credentials are loaded from the standard AWS environment variables:
// AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN (optional),
// AWS_REGION (fallback if not encoded in the URL).
type SQSQueue struct {
	queueURL   string
	region     string
	httpClient *http.Client

	// SigV4 credentials — read once at construction.
	accessKey    string
	secretKey    string
	sessionToken string

	// In-flight leases: task ID → SQS receipt handle.
	mu       sync.Mutex
	receipts map[string]string

	// In-flight task payloads for Nack re-enqueue.
	tasks map[string]pipeline.Task
}

// NewSQSQueue creates an SQSQueue from a sqs:// URI.
// Credentials are loaded from AWS_ACCESS_KEY_ID / AWS_SECRET_ACCESS_KEY.
func NewSQSQueue(rawURI string) (*SQSQueue, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("sqs: parse URI %q: %w", rawURI, err)
	}
	if u.Scheme != "sqs" {
		return nil, fmt.Errorf("sqs: URI must use sqs:// scheme, got %q", u.Scheme)
	}

	queueURL := "https://" + u.Host + u.Path

	// Extract region from host: sqs.{region}.amazonaws.com
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	parts := strings.Split(u.Host, ".")
	if len(parts) >= 4 && parts[0] == "sqs" && parts[len(parts)-2] == "amazonaws" {
		region = strings.Join(parts[1:len(parts)-2], ".")
	}
	if region == "" {
		return nil, fmt.Errorf("sqs: cannot determine region from URI %q; set AWS_REGION", rawURI)
	}

	key := os.Getenv("AWS_ACCESS_KEY_ID")
	secret := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if key == "" || secret == "" {
		return nil, fmt.Errorf("sqs: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be set")
	}

	return &SQSQueue{
		queueURL:     queueURL,
		region:       region,
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		accessKey:    key,
		secretKey:    secret,
		sessionToken: os.Getenv("AWS_SESSION_TOKEN"),
		receipts:     make(map[string]string),
		tasks:        make(map[string]pipeline.Task),
	}, nil
}

// Publish serialises t to JSON and sends it to SQS.
func (q *SQSQueue) Publish(_ context.Context, t pipeline.Task) error {
	msgBody, err := json.Marshal(t)
	if err != nil {
		return fmt.Errorf("sqs: marshal task: %w", err)
	}
	body, _ := json.Marshal(map[string]any{
		"QueueUrl":    q.queueURL,
		"MessageBody": string(msgBody),
	})
	_, err = q.call("AmazonSQS.SendMessage", body)
	return err
}

// Receive long-polls SQS (up to 20 s) until a message arrives or ctx is cancelled.
func (q *SQSQueue) Receive(ctx context.Context, _ ReceiveFilter) (Lease, error) {
	for {
		select {
		case <-ctx.Done():
			return Lease{}, ctx.Err()
		default:
		}

		body, _ := json.Marshal(map[string]any{
			"QueueUrl":            q.queueURL,
			"MaxNumberOfMessages": 1,
			"WaitTimeSeconds":     5,
			"VisibilityTimeout":   30,
		})
		resp, err := q.call("AmazonSQS.ReceiveMessage", body)
		if err != nil {
			return Lease{}, err
		}

		var result struct {
			Messages []struct {
				MessageId     string `json:"MessageId"`
				ReceiptHandle string `json:"ReceiptHandle"`
				Body          string `json:"Body"`
			} `json:"Messages"`
		}
		if err := json.Unmarshal(resp, &result); err != nil {
			return Lease{}, fmt.Errorf("sqs: parse ReceiveMessage response: %w", err)
		}
		if len(result.Messages) == 0 {
			continue
		}

		msg := result.Messages[0]
		var t pipeline.Task
		if err := json.Unmarshal([]byte(msg.Body), &t); err != nil {
			// Discard unparseable messages.
			delBody, _ := json.Marshal(map[string]string{
				"QueueUrl":      q.queueURL,
				"ReceiptHandle": msg.ReceiptHandle,
			})
			_, _ = q.call("AmazonSQS.DeleteMessage", delBody)
			continue
		}

		q.mu.Lock()
		q.receipts[t.ID] = msg.ReceiptHandle
		q.tasks[t.ID] = t
		q.mu.Unlock()

		return Lease{
			Task:       t,
			LeaseUntil: time.Now().Add(30 * time.Second),
		}, nil
	}
}

// Heartbeat extends the visibility timeout for taskID by extend.
func (q *SQSQueue) Heartbeat(_ context.Context, taskID string, extend time.Duration) error {
	q.mu.Lock()
	receipt, ok := q.receipts[taskID]
	q.mu.Unlock()
	if !ok {
		return fmt.Errorf("sqs: no in-flight lease for task %q", taskID)
	}
	secs := int(extend.Seconds())
	if secs > 43200 {
		secs = 43200
	}
	body, _ := json.Marshal(map[string]any{
		"QueueUrl":          q.queueURL,
		"ReceiptHandle":     receipt,
		"VisibilityTimeout": secs,
	})
	_, err := q.call("AmazonSQS.ChangeMessageVisibility", body)
	return err
}

// Ack deletes the message from SQS (successful completion).
func (q *SQSQueue) Ack(_ context.Context, taskID string) error {
	q.mu.Lock()
	receipt, ok := q.receipts[taskID]
	delete(q.receipts, taskID)
	delete(q.tasks, taskID)
	q.mu.Unlock()
	if !ok {
		return nil
	}
	body, _ := json.Marshal(map[string]string{
		"QueueUrl":      q.queueURL,
		"ReceiptHandle": receipt,
	})
	_, err := q.call("AmazonSQS.DeleteMessage", body)
	return err
}

// Nack returns the message to the queue after retryAfter by setting its
// VisibilityTimeout to the requested delay, then removing the in-flight record.
func (q *SQSQueue) Nack(_ context.Context, taskID string, retryAfter time.Duration) error {
	q.mu.Lock()
	receipt, ok := q.receipts[taskID]
	delete(q.receipts, taskID)
	delete(q.tasks, taskID)
	q.mu.Unlock()
	if !ok {
		return nil
	}
	secs := int(retryAfter.Seconds())
	if secs > 43200 {
		secs = 43200
	}
	body, _ := json.Marshal(map[string]any{
		"QueueUrl":          q.queueURL,
		"ReceiptHandle":     receipt,
		"VisibilityTimeout": secs,
	})
	_, err := q.call("AmazonSQS.ChangeMessageVisibility", body)
	return err
}

// Len returns the approximate number of messages available in the queue.
func (q *SQSQueue) Len(_ context.Context) (int, error) {
	body, _ := json.Marshal(map[string]any{
		"QueueUrl":       q.queueURL,
		"AttributeNames": []string{"ApproximateNumberOfMessages"},
	})
	resp, err := q.call("AmazonSQS.GetQueueAttributes", body)
	if err != nil {
		return 0, err
	}
	var result struct {
		Attributes map[string]string `json:"Attributes"`
	}
	if err := json.Unmarshal(resp, &result); err != nil {
		return 0, fmt.Errorf("sqs: parse GetQueueAttributes: %w", err)
	}
	var n int
	fmt.Sscanf(result.Attributes["ApproximateNumberOfMessages"], "%d", &n)
	return n, nil
}

// ---- HTTP + SigV4 ---------------------------------------------------------

// call sends an authenticated SQS JSON API request and returns the body.
func (q *SQSQueue) call(target string, body []byte) ([]byte, error) {
	req, err := http.NewRequest(http.MethodPost, "https://sqs."+q.region+".amazonaws.com/", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("sqs: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)
	req.ContentLength = int64(len(body))

	if err := sqsSignRequest(req, body, q.accessKey, q.secretKey, q.sessionToken, q.region); err != nil {
		return nil, fmt.Errorf("sqs: sign request: %w", err)
	}

	resp, err := q.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("sqs: http %s: %w", target, err)
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		return nil, fmt.Errorf("sqs: %s returned %d: %s", target, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// sqsSignRequest signs req with AWS SigV4 (Authorization header).
// It adds X-Amz-Date (and X-Amz-Security-Token if sessionToken != "").
func sqsSignRequest(req *http.Request, body []byte, key, secret, sessionToken, region string) error {
	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	timeStr := now.Format("20060102T150405Z")

	bodyHash := sqsHexSHA256(body)
	req.Header.Set("X-Amz-Date", timeStr)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	// Build canonical headers (lowercase, sorted, trimmed).
	// We only sign: content-type, host, x-amz-content-sha256, x-amz-date,
	// and x-amz-security-token (if present).
	host := req.URL.Host
	if host == "" {
		host = "sqs." + region + ".amazonaws.com"
	}
	req.Header.Set("Host", host)

	var signedHeaders []string
	var canonicalHeaderLines []string
	orderedHeaders := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if sessionToken != "" {
		orderedHeaders = append(orderedHeaders, "x-amz-security-token")
	}
	for _, h := range orderedHeaders {
		v := req.Header.Get(h)
		canonicalHeaderLines = append(canonicalHeaderLines, h+":"+strings.TrimSpace(v))
		signedHeaders = append(signedHeaders, h)
	}
	canonicalHeaders := strings.Join(canonicalHeaderLines, "\n") + "\n"
	signedHeadersStr := strings.Join(signedHeaders, ";")

	canonicalURI := "/"
	if req.URL.Path != "" {
		canonicalURI = req.URL.Path
	}
	canonicalRequest := strings.Join([]string{
		req.Method,
		canonicalURI,
		"", // no query string
		canonicalHeaders,
		signedHeadersStr,
		bodyHash,
	}, "\n")

	credentialScope := dateStr + "/" + region + "/sqs/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256",
		timeStr,
		credentialScope,
		sqsHexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := sqsHmacSHA256(
		sqsHmacSHA256(
			sqsHmacSHA256(
				sqsHmacSHA256([]byte("AWS4"+secret), []byte(dateStr)),
				[]byte(region)),
			[]byte("sqs")),
		[]byte("aws4_request"))
	signature := hex.EncodeToString(sqsHmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		key, credentialScope, signedHeadersStr, signature,
	))
	return nil
}

func sqsHexSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func sqsHmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
