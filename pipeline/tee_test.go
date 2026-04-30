// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"strings"
	"testing"
)

func TestBuildTeeSlavesURL(t *testing.T) {
	tests := []struct {
		name    string
		targets []TeeTarget
		want    string
		wantErr bool
	}{
		{
			name: "single bare URL",
			targets: []TeeTarget{
				{URL: "out.mp4"},
			},
			want: "out.mp4",
		},
		{
			name: "promoted typed fields ordered deterministically",
			targets: []TeeTarget{
				{URL: "a.mp4", Format: "mp4", Select: "v,a:0", OnFail: "ignore"},
			},
			want: "[f=mp4:select=v,a\\:0:onfail=ignore]a.mp4",
		},
		{
			name: "two slaves joined by pipe",
			targets: []TeeTarget{
				{URL: "out.mp4", Format: "mp4"},
				{URL: "out.m3u8", Format: "hls"},
			},
			want: "[f=mp4]out.mp4|[f=hls]out.m3u8",
		},
		{
			name: "use_fifo + fifo_options",
			targets: []TeeTarget{
				{URL: "rtmp://x/live", Format: "flv", UseFifo: true, FifoOptions: "queue_size=120;drop_pkts_on_overflow=1"},
			},
			want: "[f=flv:use_fifo=1:fifo_options=queue_size=120;drop_pkts_on_overflow=1]rtmp://x/live",
		},
		{
			name: "free-form options sorted alphabetically",
			targets: []TeeTarget{
				{URL: "out.mp4", Options: map[string]any{"zeta": "z", "alpha": "a"}},
			},
			want: "[alpha=a:zeta=z]out.mp4",
		},
		{
			name: "URL with reserved chars is escaped",
			targets: []TeeTarget{
				{URL: "out|name[1].mp4"},
			},
			want: "out\\|name\\[1].mp4",
		},
		{
			name:    "empty targets is an error",
			targets: nil,
			wantErr: true,
		},
		{
			name: "missing URL is an error",
			targets: []TeeTarget{
				{Format: "mp4"},
			},
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := buildTeeSlavesURL(tc.targets)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("got  %q\nwant %q", got, tc.want)
			}
		})
	}
}

func TestValidateTeeOutput(t *testing.T) {
	base := func(out Output) *Config {
		return &Config{
			SchemaVersion: "1.0",
			Inputs:        []Input{{ID: "in", URL: "x.mp4", Streams: []StreamSelect{{Type: "video"}}}},
			Outputs:       []Output{out},
		}
	}
	tests := []struct {
		name    string
		out     Output
		wantErr string
	}{
		{
			name:    "kind=tee requires targets",
			out:     Output{ID: "o", URL: "tee", Kind: "tee"},
			wantErr: "requires at least one entry in targets",
		},
		{
			name:    "kind=tee with target missing url",
			out:     Output{ID: "o", URL: "tee", Kind: "tee", Targets: []TeeTarget{{Format: "mp4"}}},
			wantErr: "missing url",
		},
		{
			name:    "kind=tee invalid onfail",
			out:     Output{ID: "o", URL: "tee", Kind: "tee", Targets: []TeeTarget{{URL: "a.mp4", OnFail: "panic"}}},
			wantErr: "invalid onfail",
		},
		{
			name:    "kind=file with targets is rejected",
			out:     Output{ID: "o", URL: "x.mp4", Kind: "file", Targets: []TeeTarget{{URL: "a.mp4"}}},
			wantErr: "targets is only valid",
		},
		{
			name:    "invalid kind",
			out:     Output{ID: "o", URL: "x.mp4", Kind: "split"},
			wantErr: "invalid kind",
		},
		{
			name: "kind=tee with valid targets is accepted",
			out: Output{ID: "o", URL: "tee", Kind: "tee", Targets: []TeeTarget{
				{URL: "a.mp4", Format: "mp4"},
				{URL: "b.m3u8", Format: "hls"},
			}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validate(base(tc.out))
			if tc.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tc.wantErr)
			}
		})
	}
}
