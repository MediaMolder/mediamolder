// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
)

// S3Credentials holds the AWS credentials needed to generate presigned URLs.
type S3Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
	Region          string
}

// s3CredsFile is the on-disk JSON representation.
type s3CredsFile struct {
	AccessKeyID     string `json:"access_key_id"`
	SecretAccessKey string `json:"secret_access_key"`
	SessionToken    string `json:"session_token,omitempty"`
	Region          string `json:"region"`
}

// LoadS3CredentialsFromEnv reads credentials from the standard AWS
// environment variables. Returns (creds, true) when at least
// AWS_ACCESS_KEY_ID and AWS_SECRET_ACCESS_KEY are present.
func LoadS3CredentialsFromEnv() (S3Credentials, bool) {
	ak := os.Getenv("AWS_ACCESS_KEY_ID")
	sk := os.Getenv("AWS_SECRET_ACCESS_KEY")
	if ak == "" || sk == "" {
		return S3Credentials{}, false
	}
	region := os.Getenv("AWS_REGION")
	if region == "" {
		region = os.Getenv("AWS_DEFAULT_REGION")
	}
	return S3Credentials{
		AccessKeyID:     ak,
		SecretAccessKey: sk,
		SessionToken:    os.Getenv("AWS_SESSION_TOKEN"),
		Region:          region,
	}, true
}

// LoadS3CredentialsFromFile reads credentials from a JSON file.
// The file must have mode 0600; any other permission is rejected to
// prevent accidental credential exposure.
//
// File format:
//
//	{"access_key_id":"AKIA…","secret_access_key":"…","session_token":"…","region":"us-east-1"}
func LoadS3CredentialsFromFile(path string) (S3Credentials, error) {
	info, err := os.Stat(path)
	if err != nil {
		return S3Credentials{}, fmt.Errorf("s3 credentials: %w", err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		return S3Credentials{}, fmt.Errorf(
			"s3 credentials file %q must have mode 0600 (current: %04o); run: chmod 600 %s",
			path, perm, path,
		)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return S3Credentials{}, fmt.Errorf("s3 credentials: %w", err)
	}
	var cf s3CredsFile
	if err := json.Unmarshal(data, &cf); err != nil {
		return S3Credentials{}, fmt.Errorf("s3 credentials %q: %w", path, err)
	}
	if cf.AccessKeyID == "" || cf.SecretAccessKey == "" {
		return S3Credentials{}, errors.New("s3 credentials: access_key_id and secret_access_key are required")
	}
	return S3Credentials{
		AccessKeyID:     cf.AccessKeyID,
		SecretAccessKey: cf.SecretAccessKey,
		SessionToken:    cf.SessionToken,
		Region:          cf.Region,
	}, nil
}
