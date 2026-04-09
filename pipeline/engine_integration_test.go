package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// TestEngineLinearTranscode runs the pipeline end-to-end against a real media
// file. It is skipped when MEDIAMOLDER_INTEGRATION is not set to avoid
// requiring test media in CI before P0.11 is fully wired.
func TestEngineLinearTranscode(t *testing.T) {
	if os.Getenv("MEDIAMOLDER_INTEGRATION") == "" {
		// Run unconditionally if the test media file exists next to this file.
		input := filepath.Join("..", "testdata", "test_av.avi")
		if _, err := os.Stat(input); err != nil {
			t.Skip("set MEDIAMOLDER_INTEGRATION=1 or provide testdata/test_av.avi")
		}
	}

	input := filepath.Join("..", "testdata", "test_av.avi")
	outDir := t.TempDir()
	output := filepath.Join(outDir, "out.mp4")

	rawCfg := fmt.Sprintf(`{
		"schema_version": "1.0",
		"inputs": [{
			"id": "src",
			"url": %q,
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {"nodes": [], "edges": []},
		"outputs": [{
			"id": "out",
			"url": %q,
			"codec_video": "h264_videotoolbox"
		}]
	}`, input, output)

	cfg, err := ParseConfig([]byte(rawCfg))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}

	eng, err := NewEngine(cfg)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}

	info, err := os.Stat(output)
	if err != nil {
		t.Fatalf("output file missing: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("output file is empty")
	}
	t.Logf("output: %s (%d bytes)", output, info.Size())
}
