// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package storage

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/job"
)

// PresignResolver converts s3:// URIs inside a job.Config into presigned
// HTTPS URLs before the pipeline engine sees the configuration.
//
// Presigning activates only when a non-nil S3FS is provided. When fs is nil,
// Resolve is a no-op that returns the original config pointer unchanged.
type PresignResolver struct {
	fs  *S3FS
	ttl time.Duration
}

// NewPresignResolver creates a resolver backed by fs.
// ttl is the presigned URL validity window; defaults to 24 h when zero.
func NewPresignResolver(fs *S3FS, ttl time.Duration) *PresignResolver {
	if ttl == 0 {
		ttl = 24 * time.Hour
	}
	return &PresignResolver{fs: fs, ttl: ttl}
}

// Resolve walks cfg and replaces every s3:// URL with a presigned HTTPS URL.
// It returns a shallow copy of cfg with the substituted URLs; the original
// cfg is not modified. When no S3FS is configured, cfg is returned as-is.
func (r *PresignResolver) Resolve(ctx context.Context, cfg *job.Config) (*job.Config, error) {
	if r == nil || r.fs == nil {
		return cfg, nil
	}

	// Shallow-copy the top-level struct; deep-copy the slice headers so we
	// can replace individual elements without modifying the caller's slice.
	out := *cfg
	out.Inputs = make([]job.Input, len(cfg.Inputs))
	copy(out.Inputs, cfg.Inputs)
	out.Outputs = make([]job.Output, len(cfg.Outputs))
	copy(out.Outputs, cfg.Outputs)

	for i, inp := range out.Inputs {
		if strings.HasPrefix(inp.URL, "s3://") {
			signed, err := r.fs.Sign(ctx, inp.URL, OpGet, r.ttl)
			if err != nil {
				return nil, fmt.Errorf("presign input %q: %w", inp.ID, err)
			}
			out.Inputs[i].URL = signed
		}
		// Walk the inline concat playlist.
		if len(inp.ConcatList) > 0 {
			entries := make([]job.ConcatEntry, len(inp.ConcatList))
			copy(entries, inp.ConcatList)
			for j, e := range entries {
				if strings.HasPrefix(e.File, "s3://") {
					signed, err := r.fs.Sign(ctx, e.File, OpGet, r.ttl)
					if err != nil {
						return nil, fmt.Errorf("presign concat entry %d in input %q: %w", j, inp.ID, err)
					}
					entries[j].File = signed
				}
			}
			out.Inputs[i].ConcatList = entries
		}
	}

	for i, otp := range out.Outputs {
		if strings.HasPrefix(otp.URL, "s3://") {
			signed, err := r.fs.Sign(ctx, otp.URL, OpPut, r.ttl)
			if err != nil {
				return nil, fmt.Errorf("presign output %q: %w", otp.ID, err)
			}
			out.Outputs[i].URL = signed
		}
	}

	return &out, nil
}
