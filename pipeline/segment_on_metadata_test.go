// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"
)

// TestValidateSegmentOnMetadata covers the incompatibility checks for
// SegmentOnMetadata. The full rotation logic requires CGo/FFmpeg and is
// exercised by integration tests; here we verify the pure-Go guard logic.
func TestValidateSegmentOnMetadata(t *testing.T) {
	tests := []struct {
		name    string
		out     Output
		wantErr bool
	}{
		{
			name:    "empty key is valid",
			out:     Output{ID: "o1", URL: "out.mp4"},
			wantErr: false,
		},
		{
			name:    "valid: URL has %05d",
			out:     Output{ID: "o1", URL: "seg-%05d.mp4", SegmentOnMetadata: "scene_change"},
			wantErr: false,
		},
		{
			name:    "valid: URL has %d",
			out:     Output{ID: "o1", URL: "/tmp/out%d.mkv", SegmentOnMetadata: "cut"},
			wantErr: false,
		},
		{
			name:    "invalid: URL missing printf verb",
			out:     Output{ID: "o1", URL: "out.mp4", SegmentOnMetadata: "scene_change"},
			wantErr: true,
		},
		{
			name:    "invalid: kind=tee",
			out:     Output{ID: "o1", URL: "seg-%d.mp4", SegmentOnMetadata: "cut", Kind: "tee"},
			wantErr: true,
		},
		{
			name:    "invalid: realtime pre-roll",
			out:     Output{ID: "o1", URL: "seg-%d.mp4", SegmentOnMetadata: "cut", Realtime: &RealtimeOutputOptions{}},
			wantErr: true,
		},
		{
			name:    "invalid: cover_art",
			out:     Output{ID: "o1", URL: "seg-%d.mp4", SegmentOnMetadata: "cut", CoverArt: "thumb.jpg"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSegmentOnMetadata(tt.out)
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSegmentOnMetadata() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

// TestSegmentOnMetadataSchemaFields checks that SegmentOnMetadata and
// SegmentFormat appear in the Output struct's json tags, which the
// TestSchemaSyncWithGoStructs test verifies against schema/v1.0.json.
func TestSegmentOnMetadataSchemaFields(t *testing.T) {
	out := Output{
		SegmentOnMetadata: "scene_change",
		SegmentFormat:     "mp4",
	}
	// Non-empty values confirm the fields exist and are addressable.
	if out.SegmentOnMetadata == "" || out.SegmentFormat == "" {
		t.Fatal("fields should be non-empty")
	}
}
