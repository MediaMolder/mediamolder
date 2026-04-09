package pipeline

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

// BenchmarkEngineLinearTranscode measures end-to-end pipeline throughput for
// a minimal video-only transcode (rawvideo AVI → H.264 MP4).
func BenchmarkEngineLinearTranscode(b *testing.B) {
	input := filepath.Join("..", "testdata", "test_av.avi")
	if _, err := os.Stat(input); err != nil {
		b.Skip("testdata/test_av.avi not found")
	}

	outDir := b.TempDir()

	codec := pickBenchEncoder(b)

	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		output := filepath.Join(outDir, fmt.Sprintf("out_%d.mp4", i))

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
				"codec_video": %q
			}]
		}`, input, output, codec)

		cfg, err := ParseConfig([]byte(rawCfg))
		if err != nil {
			b.Fatalf("ParseConfig: %v", err)
		}
		eng, err := NewPipeline(cfg)
		if err != nil {
			b.Fatalf("NewPipeline: %v", err)
		}
		if err := eng.Run(context.Background()); err != nil {
			b.Fatalf("Run: %v", err)
		}
	}
}

func pickBenchEncoder(b *testing.B) string {
	b.Helper()
	for _, name := range []string{"h264_videotoolbox", "libx264", "mpeg4"} {
		if av.FindEncoder(name) {
			b.Logf("using encoder: %s", name)
			return name
		}
	}
	b.Skip("no suitable video encoder found")
	return ""
}
