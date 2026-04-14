// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"testing"
)

// FuzzParseConfig exercises the JSON config parser with arbitrary input.
// Invariant: ParseConfig must never panic, regardless of input.
func FuzzParseConfig(f *testing.F) {
	// Seed corpus: valid config.
	f.Add([]byte(validConfig))

	// Minimal valid.
	f.Add([]byte(`{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"y"}]}`))

	// Missing fields.
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"schema_version":"1.0"}`))
	f.Add([]byte(`{"schema_version":"2.0","inputs":[],"graph":{"nodes":[],"edges":[]},"outputs":[]}`))

	// Malformed JSON.
	f.Add([]byte(`{`))
	f.Add([]byte(`null`))
	f.Add([]byte(``))
	f.Add([]byte(`{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[{"from":"a:v:0","to":"b:v","type":"bogus"}]},"outputs":[{"id":"b","url":"y"}]}`))

	// Duplicate IDs.
	f.Add([]byte(`{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]},{"id":"a","url":"y","streams":[{"input_index":0,"type":"audio","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"z"}]}`))

	// Unknown fields.
	f.Add([]byte(`{"schema_version":"1.0","bogus":true,"inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[],"edges":[]},"outputs":[{"id":"b","url":"y"}]}`))

	// Large nesting.
	f.Add([]byte(`{"schema_version":"1.0","inputs":[{"id":"a","url":"x","streams":[{"input_index":0,"type":"video","track":0}]}],"graph":{"nodes":[{"id":"n","type":"filter","filter":"scale","params":{"w":1280},"error_policy":{"policy":"retry","max_retries":3,"fallback_node":"fb"}}],"edges":[{"from":"a:v:0","to":"n:default","type":"video"},{"from":"n:default","to":"b:v","type":"video"}]},"outputs":[{"id":"b","url":"y","codec_video":"libx264","bsf_video":"h264_mp4toannexb"}],"global_options":{"threads":4,"hw_accel":"cuda","hw_device":"0","realtime":true}}`))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Must not panic. Errors are expected and fine.
		cfg, err := ParseConfig(data)
		if err != nil {
			return
		}
		// If parsing succeeded, the config should be structurally valid.
		if cfg.SchemaVersion != "1.0" {
			t.Errorf("parsed config has schema_version %q, want 1.0", cfg.SchemaVersion)
		}
		if len(cfg.Inputs) == 0 {
			t.Error("parsed config has no inputs")
		}
		if len(cfg.Outputs) == 0 {
			t.Error("parsed config has no outputs")
		}
	})
}
