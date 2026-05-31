// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package state

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
	"time"

	"github.com/MediaMolder/MediaMolder/job"
)

// DynamoDBStore is a state.Store backed by Amazon DynamoDB using the JSON API
// and pure-Go SigV4 signing (no AWS SDK required).
//
// Single-table design — all records share one table with composite primary key
// PK (partition) + SK (sort). Key patterns:
//
//	Jobs:       PK="JOB#<id>",         SK="META"
//	Tasks:      PK="JOB#<jobID>",      SK="TASK#<stageID>#<taskID>"
//	Task index: PK="TASKIDX#<taskID>", SK="REV"  (reverse lookup)
//	Events:     PK="EVTS#<jobID>",     SK=<id as 20-digit zero-padded int>
//	DLQ:        PK="DLQ",              SK="DLQ#<taskID>"
//
// URI format: dynamodb://<host>/<tableName>
// e.g.        dynamodb://dynamodb.us-east-1.amazonaws.com/mediamolder
//
// Credentials: AWS_ACCESS_KEY_ID, AWS_SECRET_ACCESS_KEY, AWS_SESSION_TOKEN
// (optional), AWS_REGION (inferred from host when absent).
type DynamoDBStore struct {
	endpoint   string // https://dynamodb.<region>.amazonaws.com
	region     string
	table      string
	httpClient *http.Client

	accessKey    string
	secretKey    string
	sessionToken string
}

// NewDynamoDBStore creates a DynamoDBStore from a dynamodb:// URI.
// Credentials are loaded from standard AWS environment variables.
func NewDynamoDBStore(rawURI string) (*DynamoDBStore, error) {
	u, err := url.Parse(rawURI)
	if err != nil {
		return nil, fmt.Errorf("dynamodb: parse URI %q: %w", rawURI, err)
	}
	if u.Scheme != "dynamodb" {
		return nil, fmt.Errorf("dynamodb: URI must use dynamodb:// scheme, got %q", u.Scheme)
	}
	host := u.Host
	table := strings.TrimPrefix(u.Path, "/")
	if table == "" {
		return nil, fmt.Errorf("dynamodb: URI must include table name as path, e.g. dynamodb://dynamodb.us-east-1.amazonaws.com/mytable")
	}

	// Infer region from host (e.g. dynamodb.us-east-1.amazonaws.com).
	region := os.Getenv("AWS_REGION")
	if region == "" {
		parts := strings.Split(host, ".")
		if len(parts) >= 3 {
			region = parts[1]
		}
	}
	if region == "" {
		return nil, fmt.Errorf("dynamodb: cannot determine region from URI %q and AWS_REGION is not set", rawURI)
	}

	accessKey := os.Getenv("AWS_ACCESS_KEY_ID")
	secretKey := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if accessKey == "" || secretKey == "" {
		return nil, fmt.Errorf("dynamodb: AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY must be set")
	}

	return &DynamoDBStore{
		endpoint:     "https://" + host,
		region:       region,
		table:        table,
		httpClient:   &http.Client{Timeout: 10 * time.Second},
		accessKey:    accessKey,
		secretKey:    secretKey,
		sessionToken: os.Getenv("AWS_SESSION_TOKEN"),
	}, nil
}

// ---- Store interface --------------------------------------------------------

func (d *DynamoDBStore) CreateJob(ctx context.Context, j job.Job) error {
	jobJSON, err := json.Marshal(j)
	if err != nil {
		return err
	}
	statusJSON, _ := json.Marshal(JobStatusRecord{Status: JobStatusQueued, UpdatedAt: time.Now()})
	item := dynItem{
		"PK":          dynS("JOB#" + j.ID),
		"SK":          dynS("META"),
		"job_json":    dynS(string(jobJSON)),
		"status_json": dynS(string(statusJSON)),
	}
	return d.putItem(ctx, item, "attribute_not_exists(PK)")
}

func (d *DynamoDBStore) GetJob(ctx context.Context, id string) (job.Job, JobStatusRecord, error) {
	item, err := d.getItem(ctx, dynKey{"PK": dynS("JOB#" + id), "SK": dynS("META")})
	if err != nil {
		return job.Job{}, JobStatusRecord{}, err
	}
	var j job.Job
	if err := json.Unmarshal([]byte(dynGetS(item, "job_json")), &j); err != nil {
		return job.Job{}, JobStatusRecord{}, err
	}
	var sr JobStatusRecord
	if err := json.Unmarshal([]byte(dynGetS(item, "status_json")), &sr); err != nil {
		return job.Job{}, JobStatusRecord{}, err
	}
	return j, sr, nil
}

func (d *DynamoDBStore) UpdateJobStatus(ctx context.Context, jobID string, s JobStatusRecord) error {
	statusJSON, err := json.Marshal(s)
	if err != nil {
		return err
	}
	return d.updateItem(ctx,
		dynKey{"PK": dynS("JOB#" + jobID), "SK": dynS("META")},
		"SET status_json = :v",
		map[string]dynAttr{":v": dynS(string(statusJSON))},
	)
}

func (d *DynamoDBStore) AppendEvent(ctx context.Context, e JobEvent) error {
	now := time.Now()
	e.CreatedAt = now
	id := now.UnixNano()
	e.ID = id
	b, err := json.Marshal(e)
	if err != nil {
		return err
	}
	item := dynItem{
		"PK":         dynS("EVTS#" + e.JobID),
		"SK":         dynS(fmt.Sprintf("%020d", id)),
		"event_json": dynS(string(b)),
	}
	return d.putItem(ctx, item, "")
}

func (d *DynamoDBStore) ListEvents(ctx context.Context, jobID string, afterID int64) ([]JobEvent, error) {
	condition := "PK = :pk AND SK > :sk"
	vals := map[string]dynAttr{
		":pk": dynS("EVTS#" + jobID),
		":sk": dynS(fmt.Sprintf("%020d", afterID)),
	}
	items, err := d.query(ctx, condition, vals, false)
	if err != nil {
		return nil, err
	}
	events := make([]JobEvent, 0, len(items))
	for _, item := range items {
		var ev JobEvent
		if err := json.Unmarshal([]byte(dynGetS(item, "event_json")), &ev); err != nil {
			continue
		}
		events = append(events, ev)
	}
	return events, nil
}

func (d *DynamoDBStore) UpsertTask(ctx context.Context, t job.Task, status TaskStatus) error {
	taskJSON, err := json.Marshal(t)
	if err != nil {
		return err
	}
	sk := taskSK(t.StageID, t.ID)
	// Write main task record.
	item := dynItem{
		"PK":          dynS("JOB#" + t.JobID),
		"SK":          dynS(sk),
		"task_id":     dynS(t.ID),
		"job_id":      dynS(t.JobID),
		"stage_id":    dynS(t.StageID),
		"task_json":   dynS(string(taskJSON)),
		"status":      dynS(string(status)),
		"lease_until": dynS(t.LeaseUntil.Format(time.RFC3339Nano)),
	}
	if err := d.putItem(ctx, item, ""); err != nil {
		return err
	}
	// Write reverse index for GetTask by task ID.
	rev := dynItem{
		"PK":   dynS("TASKIDX#" + t.ID),
		"SK":   dynS("REV"),
		"pk":   dynS("JOB#" + t.JobID),
		"sk":   dynS(sk),
	}
	return d.putItem(ctx, rev, "")
}

func (d *DynamoDBStore) SetTaskResult(ctx context.Context, taskID string, r job.TaskResult) error {
	pk, sk, err := d.resolveTaskKey(ctx, taskID)
	if err != nil {
		return err
	}
	resultJSON, err := json.Marshal(r)
	if err != nil {
		return err
	}
	statusVal := string(TaskStatusSucceeded)
	if r.Error != "" {
		statusVal = string(TaskStatusFailed)
	}
	return d.updateItem(ctx,
		dynKey{"PK": dynS(pk), "SK": dynS(sk)},
		"SET result_json = :r, #s = :s",
		map[string]dynAttr{":r": dynS(string(resultJSON)), ":s": dynS(statusVal)},
	)
}

func (d *DynamoDBStore) GetTask(ctx context.Context, taskID string) (TaskRecord, error) {
	pk, sk, err := d.resolveTaskKey(ctx, taskID)
	if err != nil {
		return TaskRecord{}, err
	}
	item, err := d.getItem(ctx, dynKey{"PK": dynS(pk), "SK": dynS(sk)})
	if err != nil {
		return TaskRecord{}, err
	}
	return d.itemToTaskRecord(item)
}

func (d *DynamoDBStore) TasksByStage(ctx context.Context, jobID, stageID string) ([]TaskRecord, error) {
	prefix := "TASK#" + stageID + "#"
	condition := "PK = :pk AND begins_with(SK, :prefix)"
	vals := map[string]dynAttr{
		":pk":     dynS("JOB#" + jobID),
		":prefix": dynS(prefix),
	}
	items, err := d.query(ctx, condition, vals, false)
	if err != nil {
		return nil, err
	}
	return d.itemsToTaskRecords(items)
}

func (d *DynamoDBStore) ListTasks(ctx context.Context, jobID string) ([]TaskRecord, error) {
	condition := "PK = :pk AND begins_with(SK, :prefix)"
	vals := map[string]dynAttr{
		":pk":     dynS("JOB#" + jobID),
		":prefix": dynS("TASK#"),
	}
	items, err := d.query(ctx, condition, vals, false)
	if err != nil {
		return nil, err
	}
	return d.itemsToTaskRecords(items)
}

func (d *DynamoDBStore) RenewTaskLease(ctx context.Context, taskID string, until time.Time) error {
	pk, sk, err := d.resolveTaskKey(ctx, taskID)
	if err != nil {
		return err
	}
	return d.updateItem(ctx,
		dynKey{"PK": dynS(pk), "SK": dynS(sk)},
		"SET lease_until = :v",
		map[string]dynAttr{":v": dynS(until.Format(time.RFC3339Nano))},
	)
}

func (d *DynamoDBStore) ListExpiredLeases(ctx context.Context) ([]TaskRecord, error) {
	// Scan for all tasks with status=running and lease_until < now.
	// For production, a GSI on lease_until would be preferable to a full table scan.
	now := time.Now().Format(time.RFC3339Nano)
	result, err := d.scan(ctx,
		"begins_with(SK, :prefix) AND #s = :running AND lease_until < :now",
		map[string]dynAttr{
			":prefix":  dynS("TASK#"),
			":running": dynS(string(TaskStatusRunning)),
			":now":     dynS(now),
		},
		map[string]string{"#s": "status"},
	)
	if err != nil {
		return nil, err
	}
	return d.itemsToTaskRecords(result)
}

func (d *DynamoDBStore) DeadLetterTask(ctx context.Context, taskID, reason string) error {
	// Fetch the task to capture its JSON.
	rec, err := d.GetTask(ctx, taskID)
	if err != nil {
		return err
	}
	taskJSON, _ := json.Marshal(rec.Task)
	dlq := DeadLetterRecord{
		TaskID:    taskID,
		JobID:     rec.Task.JobID,
		StageID:   rec.Task.StageID,
		TaskJSON:  string(taskJSON),
		Reason:    reason,
		Attempt:   rec.Task.Attempt,
		CreatedAt: time.Now(),
	}
	b, err := json.Marshal(dlq)
	if err != nil {
		return err
	}
	item := dynItem{
		"PK":       dynS("DLQ"),
		"SK":       dynS("DLQ#" + taskID),
		"dlq_json": dynS(string(b)),
	}
	return d.putItem(ctx, item, "")
}

func (d *DynamoDBStore) ListDeadLetterTasks(ctx context.Context, jobID string) ([]DeadLetterRecord, error) {
	condition := "PK = :pk AND begins_with(SK, :prefix)"
	vals := map[string]dynAttr{":pk": dynS("DLQ"), ":prefix": dynS("DLQ#")}
	items, err := d.query(ctx, condition, vals, false)
	if err != nil {
		return nil, err
	}
	var out []DeadLetterRecord
	for _, item := range items {
		var r DeadLetterRecord
		if err := json.Unmarshal([]byte(dynGetS(item, "dlq_json")), &r); err != nil {
			continue
		}
		if jobID == "" || r.JobID == jobID {
			out = append(out, r)
		}
	}
	return out, nil
}

func (d *DynamoDBStore) Close() error { return nil }

// ---- internal helpers -------------------------------------------------------

// resolveTaskKey looks up the primary key of a task using the reverse index.
func (d *DynamoDBStore) resolveTaskKey(ctx context.Context, taskID string) (pk, sk string, err error) {
	item, err := d.getItem(ctx, dynKey{"PK": dynS("TASKIDX#" + taskID), "SK": dynS("REV")})
	if err != nil {
		return "", "", fmt.Errorf("dynamodb: task index not found for %q: %w", taskID, err)
	}
	return dynGetS(item, "pk"), dynGetS(item, "sk"), nil
}

func taskSK(stageID, taskID string) string {
	return "TASK#" + stageID + "#" + taskID
}

func (d *DynamoDBStore) itemToTaskRecord(item dynItem) (TaskRecord, error) {
	var t job.Task
	if err := json.Unmarshal([]byte(dynGetS(item, "task_json")), &t); err != nil {
		return TaskRecord{}, err
	}
	rec := TaskRecord{
		Task:      t,
		Status:    TaskStatus(dynGetS(item, "status")),
		UpdatedAt: time.Now(),
	}
	if resultJSON := dynGetS(item, "result_json"); resultJSON != "" {
		var r job.TaskResult
		if err := json.Unmarshal([]byte(resultJSON), &r); err == nil {
			rec.Result = &r
		}
	}
	if leaseStr := dynGetS(item, "lease_until"); leaseStr != "" {
		if t, err := time.Parse(time.RFC3339Nano, leaseStr); err == nil {
			rec.LeaseUntil = t
		}
	}
	return rec, nil
}

func (d *DynamoDBStore) itemsToTaskRecords(items []dynItem) ([]TaskRecord, error) {
	out := make([]TaskRecord, 0, len(items))
	for _, item := range items {
		rec, err := d.itemToTaskRecord(item)
		if err != nil {
			return nil, err
		}
		out = append(out, rec)
	}
	return out, nil
}

// ---- DynamoDB API types ----------------------------------------------------

type dynAttr map[string]any // {"S": "value"} or {"N": "123"}
type dynItem map[string]dynAttr
type dynKey = map[string]dynAttr

func dynS(v string) dynAttr { return dynAttr{"S": v} }

func dynGetS(item dynItem, key string) string {
	attr, ok := item[key]
	if !ok {
		return ""
	}
	s, _ := attr["S"].(string)
	return s
}

// ---- DynamoDB operations ----------------------------------------------------

// putItem calls DynamoDB PutItem.
func (d *DynamoDBStore) putItem(ctx context.Context, item dynItem, condition string) error {
	req := map[string]any{"TableName": d.table, "Item": item}
	if condition != "" {
		req["ConditionExpression"] = condition
	}
	_, err := d.call(ctx, "DynamoDB_20120810.PutItem", req)
	return err
}

// getItem calls DynamoDB GetItem.
func (d *DynamoDBStore) getItem(ctx context.Context, key dynKey) (dynItem, error) {
	req := map[string]any{
		"TableName":      d.table,
		"Key":            key,
		"ConsistentRead": true,
	}
	resp, err := d.call(ctx, "DynamoDB_20120810.GetItem", req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Item dynItem `json:"Item"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	if out.Item == nil {
		return nil, fmt.Errorf("dynamodb: item not found")
	}
	return out.Item, nil
}

// updateItem calls DynamoDB UpdateItem.
func (d *DynamoDBStore) updateItem(ctx context.Context, key dynKey, updateExpr string, vals map[string]dynAttr) error {
	req := map[string]any{
		"TableName":                d.table,
		"Key":                      key,
		"UpdateExpression":         updateExpr,
		"ExpressionAttributeValues": vals,
	}
	_, err := d.call(ctx, "DynamoDB_20120810.UpdateItem", req)
	return err
}

// query calls DynamoDB Query.
func (d *DynamoDBStore) query(ctx context.Context, keyCondition string, vals map[string]dynAttr, scanIndexForward bool) ([]dynItem, error) {
	req := map[string]any{
		"TableName":                 d.table,
		"KeyConditionExpression":    keyCondition,
		"ExpressionAttributeValues": vals,
		"ScanIndexForward":          scanIndexForward,
	}
	resp, err := d.call(ctx, "DynamoDB_20120810.Query", req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []dynItem `json:"Items"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// scan calls DynamoDB Scan with a filter expression.
func (d *DynamoDBStore) scan(ctx context.Context, filter string, vals map[string]dynAttr, names map[string]string) ([]dynItem, error) {
	req := map[string]any{
		"TableName":                 d.table,
		"FilterExpression":          filter,
		"ExpressionAttributeValues": vals,
	}
	if len(names) > 0 {
		req["ExpressionAttributeNames"] = names
	}
	resp, err := d.call(ctx, "DynamoDB_20120810.Scan", req)
	if err != nil {
		return nil, err
	}
	var out struct {
		Items []dynItem `json:"Items"`
	}
	if err := json.Unmarshal(resp, &out); err != nil {
		return nil, err
	}
	return out.Items, nil
}

// call sends a signed DynamoDB JSON API request.
func (d *DynamoDBStore) call(ctx context.Context, target string, body any) ([]byte, error) {
	payload, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, d.endpoint+"/", bytes.NewReader(payload))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-amz-json-1.0")
	req.Header.Set("X-Amz-Target", target)
	req.ContentLength = int64(len(payload))

	if err := dynSignRequest(req, payload, d.accessKey, d.secretKey, d.sessionToken, d.region); err != nil {
		return nil, fmt.Errorf("dynamodb: sign request: %w", err)
	}

	resp, err := d.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 400 {
		// Ignore ConditionalCheckFailed for idempotent PutItem with condition.
		if strings.Contains(string(respBody), "ConditionalCheckFailedException") {
			return nil, nil
		}
		return nil, fmt.Errorf("dynamodb: %s returned %d: %s", target, resp.StatusCode, string(respBody))
	}
	return respBody, nil
}

// ---- SigV4 (same pattern as SQS adapter) -----------------------------------

func dynSignRequest(req *http.Request, body []byte, key, secret, sessionToken, region string) error {
	now := time.Now().UTC()
	dateStr := now.Format("20060102")
	timeStr := now.Format("20060102T150405Z")

	bodyHash := dynHexSHA256(body)
	req.Header.Set("X-Amz-Date", timeStr)
	req.Header.Set("X-Amz-Content-Sha256", bodyHash)
	if sessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", sessionToken)
	}

	host := req.URL.Host
	if host == "" {
		host = "dynamodb." + region + ".amazonaws.com"
	}
	req.Header.Set("Host", host)

	orderedHeaders := []string{"content-type", "host", "x-amz-content-sha256", "x-amz-date"}
	if sessionToken != "" {
		orderedHeaders = append(orderedHeaders, "x-amz-security-token")
	}
	var signedHeaders []string
	var canonicalHeaderLines []string
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
		req.Method, canonicalURI, "",
		canonicalHeaders, signedHeadersStr, bodyHash,
	}, "\n")

	credentialScope := dateStr + "/" + region + "/dynamodb/aws4_request"
	stringToSign := strings.Join([]string{
		"AWS4-HMAC-SHA256", timeStr, credentialScope,
		dynHexSHA256([]byte(canonicalRequest)),
	}, "\n")

	signingKey := dynHmacSHA256(
		dynHmacSHA256(
			dynHmacSHA256(
				dynHmacSHA256([]byte("AWS4"+secret), []byte(dateStr)),
				[]byte(region)),
			[]byte("dynamodb")),
		[]byte("aws4_request"))
	signature := hex.EncodeToString(dynHmacSHA256(signingKey, []byte(stringToSign)))

	req.Header.Set("Authorization", fmt.Sprintf(
		"AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		key, credentialScope, signedHeadersStr, signature,
	))
	return nil
}

func dynHexSHA256(data []byte) string {
	h := sha256.Sum256(data)
	return hex.EncodeToString(h[:])
}

func dynHmacSHA256(key, data []byte) []byte {
	mac := hmac.New(sha256.New, key)
	mac.Write(data)
	return mac.Sum(nil)
}
