// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

// corpus_test.go — Expanded config parse test corpus (P4.3).

import "testing"

var configCorpusTests = []struct {
	name    string
	json    string
	wantErr bool
}{
	// ---- Minimal valid configs ----
	{
		name: "minimal-valid",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "minimal-audio-only",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.wav","streams":[{"input_index":0,"type":"audio","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp3"}]}`,
	},
	{
		name: "minimal-subtitle-stream",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[{"input_index":0,"type":"subtitle","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mkv"}]}`,
	},
	{
		name: "minimal-data-stream",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.ts","streams":[{"input_index":0,"type":"data","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.ts"}]}`,
	},

	// ---- Multi-input configs ----
	{
		name: "two-inputs",
		json: `{"schema_version":"1.0","inputs":[{"id":"a","url":"a.mp4","streams":[{"input_index":0,"type":"video","track":0}]},{"id":"b","url":"b.mp4","streams":[{"input_index":0,"type":"audio","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "three-inputs",
		json: `{"schema_version":"1.0","inputs":[{"id":"a","url":"a.mp4","streams":[]},{"id":"b","url":"b.mp4","streams":[]},{"id":"c","url":"c.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "five-inputs",
		json: `{"schema_version":"1.0","inputs":[{"id":"a","url":"a.mp4","streams":[]},{"id":"b","url":"b.mp4","streams":[]},{"id":"c","url":"c.mp4","streams":[]},{"id":"d","url":"d.mp4","streams":[]},{"id":"e","url":"e.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},

	// ---- Multi-output configs ----
	{
		name: "two-outputs",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"a","url":"a.mp4"},{"id":"b","url":"b.mp4"}]}`,
	},
	{
		name: "four-outputs",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"a","url":"a.mp4"},{"id":"b","url":"b.mp4"},{"id":"c","url":"c.mp4"},{"id":"d","url":"d.mp4"}]}`,
	},

	// ---- Multi-stream inputs ----
	{
		name: "multi-stream-input",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[{"input_index":0,"type":"video","track":0},{"input_index":0,"type":"audio","track":0},{"input_index":0,"type":"audio","track":1},{"input_index":0,"type":"subtitle","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mkv"}]}`,
	},

	// ---- Graph with nodes ----
	{
		name: "single-filter-node",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"s","type":"filter","filter":"scale","params":{"w":1280,"h":720}}],"edges":[{"from":"in:v:0","to":"s:default","type":"video"},{"from":"s:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "encoder-node",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"enc","type":"encoder","params":{"codec":"libx264","crf":23}}],"edges":[{"from":"in:v:0","to":"enc:default","type":"video"},{"from":"enc:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "three-node-chain",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"a","type":"filter","filter":"yadif"},{"id":"b","type":"filter","filter":"scale","params":{"w":1280}},{"id":"c","type":"filter","filter":"fps","params":{"fps":30}}],"edges":[{"from":"in:v:0","to":"a:default","type":"video"},{"from":"a:default","to":"b:default","type":"video"},{"from":"b:default","to":"c:default","type":"video"},{"from":"c:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "node-with-error-policy-abort",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"f","type":"filter","filter":"scale","error_policy":{"policy":"abort"}}],"edges":[{"from":"in:v:0","to":"f:default","type":"video"},{"from":"f:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "node-with-error-policy-skip",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"f","type":"filter","filter":"scale","error_policy":{"policy":"skip"}}],"edges":[{"from":"in:v:0","to":"f:default","type":"video"},{"from":"f:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "node-with-error-policy-retry",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"f","type":"filter","filter":"scale","error_policy":{"policy":"retry","max_retries":3}}],"edges":[{"from":"in:v:0","to":"f:default","type":"video"},{"from":"f:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "node-with-error-policy-fallback",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"f","type":"filter","filter":"scale","error_policy":{"policy":"fallback","fallback_node":"g"}},{"id":"g","type":"filter","filter":"null"}],"edges":[{"from":"in:v:0","to":"f:default","type":"video"},{"from":"f:default","to":"out:v","type":"video"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},

	// ---- Output field variants (Phase 3) ----
	{
		name: "output-all-codecs",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mkv","codec_video":"libx264","codec_audio":"aac","codec_subtitle":"srt"}]}`,
	},
	{
		name: "output-bsf-video",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.ts","bsf_video":"h264_mp4toannexb"}]}`,
	},
	{
		name: "output-bsf-audio",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.ts","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4","bsf_audio":"aac_adtstoasc"}]}`,
	},
	{
		name: "output-bsf-both",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.ts","bsf_video":"h264_mp4toannexb","bsf_audio":"aac_adtstoasc"}]}`,
	},
	{
		name: "output-format-mp4",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4","format":"mp4"}]}`,
	},
	{
		name: "output-format-matroska",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mkv","format":"matroska"}]}`,
	},
	{
		name: "output-with-options",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4","options":{"movflags":"faststart","crf":18}}]}`,
	},

	// ---- Global options ----
	{
		name: "global-threads",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"global_options":{"threads":4}}`,
	},
	{
		name: "global-hw-accel",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"global_options":{"hw_accel":"cuda"}}`,
	},
	{
		name: "global-hw-device",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"global_options":{"hw_device":"/dev/dri/renderD128"}}`,
	},
	{
		name: "global-realtime",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"global_options":{"realtime":true}}`,
	},
	{
		name: "global-all-options",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"global_options":{"threads":8,"hw_accel":"vaapi","hw_device":"/dev/dri/renderD128","realtime":false}}`,
	},

	// ---- Input options ----
	{
		name: "input-with-options",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[],"options":{"ss":"00:00:30","t":"60"}}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},

	// ---- Edge type variety ----
	{
		name: "edge-audio",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:a:0","to":"out:a","type":"audio"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},
	{
		name: "edge-subtitle",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:s:0","to":"out:s","type":"subtitle"}]},"outputs":[{"id":"out","url":"o.mkv"}]}`,
	},
	{
		name: "edge-data",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.ts","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:d:0","to":"out:d","type":"data"}]},"outputs":[{"id":"out","url":"o.ts"}]}`,
	},
	{
		name: "edges-all-types",
		json: `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mkv","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:v:0","to":"out:v","type":"video"},{"from":"in:a:0","to":"out:a","type":"audio"},{"from":"in:s:0","to":"out:s","type":"subtitle"},{"from":"in:d:0","to":"out:d","type":"data"}]},"outputs":[{"id":"out","url":"o.mkv"}]}`,
	},

	// ---- Complex multi-I/O configs ----
	{
		name: "multi-io-2in-2out",
		json: `{"schema_version":"1.0","inputs":[{"id":"v","url":"v.mp4","streams":[{"input_index":0,"type":"video","track":0}]},{"id":"a","url":"a.wav","streams":[{"input_index":0,"type":"audio","track":0}]}],"graph":{"nodes":[],"edges":[{"from":"v:v:0","to":"hd:v","type":"video"},{"from":"v:v:0","to":"sd:v","type":"video"},{"from":"a:a:0","to":"hd:a","type":"audio"},{"from":"a:a:0","to":"sd:a","type":"audio"}]},"outputs":[{"id":"hd","url":"hd.mp4"},{"id":"sd","url":"sd.mp4"}]}`,
	},

	// ---- Error cases: schema_version ----
	{
		name:    "err-missing-schema",
		json:    `{"inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-empty-schema",
		json:    `{"schema_version":"","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-schema-2.0",
		json:    `{"schema_version":"2.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-schema-0.9",
		json:    `{"schema_version":"0.9","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-schema-2.0",
		json:    `{"schema_version":"2.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name: "ok-schema-1.1",
		json: `{"schema_version":"1.1","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
	},

	// ---- Error cases: inputs ----
	{
		name:    "err-no-inputs",
		json:    `{"schema_version":"1.0","inputs":[],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-input-missing-id",
		json:    `{"schema_version":"1.0","inputs":[{"url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-input-missing-url",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-input-empty-id",
		json:    `{"schema_version":"1.0","inputs":[{"id":"","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-input-empty-url",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-duplicate-input-id",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"a.mp4","streams":[]},{"id":"in","url":"b.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-stream-missing-type",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[{"input_index":0,"track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},

	// ---- Error cases: outputs ----
	{
		name:    "err-no-outputs",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[]}`,
		wantErr: true,
	},
	{
		name:    "err-output-missing-id",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-output-missing-url",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out"}]}`,
		wantErr: true,
	},
	{
		name:    "err-output-empty-id",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-output-empty-url",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":""}]}`,
		wantErr: true,
	},
	{
		name:    "err-duplicate-output-id",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"a.mp4"},{"id":"out","url":"b.mp4"}]}`,
		wantErr: true,
	},

	// ---- Error cases: edges ----
	{
		name:    "err-edge-invalid-type",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:v:0","to":"out:v","type":"bogus"}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-edge-empty-type",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[{"from":"in:v:0","to":"out:v","type":""}]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},

	// ---- Error cases: JSON syntax ----
	{
		name:    "err-invalid-json",
		json:    `{not json}`,
		wantErr: true,
	},
	{
		name:    "err-empty-string",
		json:    ``,
		wantErr: true,
	},
	{
		name:    "err-null",
		json:    `null`,
		wantErr: true,
	},
	{
		name:    "err-array",
		json:    `[]`,
		wantErr: true,
	},
	{
		name:    "err-string",
		json:    `"hello"`,
		wantErr: true,
	},
	{
		name:    "err-number",
		json:    `42`,
		wantErr: true,
	},
	{
		name:    "err-unknown-field",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}],"unknown":true}`,
		wantErr: true,
	},
	{
		name:    "err-nested-unknown-field-input",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[],"extra":1}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
	{
		name:    "err-nested-unknown-field-output",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4","extra":1}]}`,
		wantErr: true,
	},
	{
		name:    "err-truncated-json",
		json:    `{"schema_version":"1.0","inputs":[{"id":"in","url":"f.mp4`,
		wantErr: true,
	},
	{
		name:    "err-wrong-type-schema",
		json:    `{"schema_version":1,"inputs":[{"id":"in","url":"f.mp4","streams":[]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"out","url":"o.mp4"}]}`,
		wantErr: true,
	},
}

func TestConfigCorpus(t *testing.T) {
	for _, tt := range configCorpusTests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParseConfig([]byte(tt.json))
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}
