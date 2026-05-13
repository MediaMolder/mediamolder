// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// bbbSource is the filename of the Big Buck Bunny 1080p source file.
// It is downloaded by scripts/fetch-bbb.sh and is not tracked in git.
const bbbSource = "BBB_1080p.avi"

// bbbSourcePath returns the absolute path to the BBB source file and
// skips the test if the file has not been downloaded yet.
func bbbSourcePath(t *testing.T) string {
	t.Helper()
	abs, err := filepath.Abs(filepath.Join("..", "testdata", bbbSource))
	if err != nil {
		t.Fatalf("abs path for %s: %v", bbbSource, err)
	}
	if _, err := os.Stat(abs); err != nil {
		t.Skipf("testdata/%s missing; run: bash scripts/fetch-bbb.sh", bbbSource)
	}
	return abs
}

// injectBBBSeek adds ss=450 and t=10 to every file-kind input whose URL
// contains "BBB_1080p". This makes tests that previously consumed a
// pre-trimmed 10-second clip work directly against the full source file.
func injectBBBSeek(cfg *Config) {
	for i := range cfg.Inputs {
		inp := &cfg.Inputs[i]
		if inp.Kind != "" && inp.Kind != "file" {
			continue
		}
		if !strings.Contains(inp.URL, "BBB_1080p") {
			continue
		}
		if inp.Options == nil {
			inp.Options = map[string]any{}
		}
		inp.Options["ss"] = "450"
		inp.Options["t"] = "10"
	}
}
