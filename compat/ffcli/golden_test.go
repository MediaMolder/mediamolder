package ffcli

import (
	"encoding/json"
	"testing"
)

// goldenTests covers ~20 common FFmpeg commands for parity testing.
var goldenTests = []struct {
	name    string
	cmd     string
	wantIn  int
	wantOut int
	wantN   int // graph nodes (filters)
	wantE   int // graph edges
	wantErr bool
}{
	{
		name: "simple copy",
		cmd:  "ffmpeg -i in.mp4 -c copy out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "h264 transcode",
		cmd:  "ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "video only no audio",
		cmd:  "ffmpeg -i in.mp4 -an -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 1,
	},
	{
		name: "audio only no video",
		cmd:  "ffmpeg -i in.mp4 -vn -c:a aac out.mp3",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 1,
	},
	{
		name: "scale filter",
		cmd:  "ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 1, wantE: 3,
	},
	{
		name: "filter chain two filters",
		cmd:  "ffmpeg -i in.mp4 -vf scale=640:480,fps=30 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 2, wantE: 4,
	},
	{
		name: "audio filter",
		cmd:  "ffmpeg -i in.mp4 -af volume=2.0 -c:a aac out.mp4",
		wantIn: 1, wantOut: 1, wantN: 1, wantE: 3,
	},
	{
		name: "both video and audio filters",
		cmd:  "ffmpeg -i in.mp4 -vf scale=1920:1080 -af loudnorm -c:v libx264 -c:a aac out.mp4",
		wantIn: 1, wantOut: 1, wantN: 2, wantE: 4,
	},
	{
		name: "format flag",
		cmd:  "ffmpeg -i in.mp4 -f mp4 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "bitrate flags",
		cmd:  "ffmpeg -i in.mp4 -b:v 2M -b:a 128k -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "framerate flag",
		cmd:  "ffmpeg -i in.mp4 -r 30 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "overwrite flag ignored",
		cmd:  "ffmpeg -y -i in.mp4 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "quoted path",
		cmd:  `ffmpeg -i "my file.mp4" -c:v libx264 "my output.mp4"`,
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "vcodec alias",
		cmd:  "ffmpeg -i in.mp4 -vcodec libx265 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "acodec alias",
		cmd:  "ffmpeg -i in.mp4 -acodec opus out.webm",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "generic codec flag",
		cmd:  "ffmpeg -i in.mp4 -codec copy out.mkv",
		wantIn: 1, wantOut: 1, wantN: 0, wantE: 2,
	},
	{
		name: "no input error",
		cmd:  "ffmpeg -c:v libx264 out.mp4",
		wantErr: true,
	},
	{
		name: "no output error",
		cmd:  "ffmpeg -i in.mp4 -c:v libx264",
		wantErr: true,
	},
	{
		name: "empty -i error",
		cmd:  "ffmpeg -i",
		wantErr: true,
	},
	{
		name: "three filter chain",
		cmd:  "ffmpeg -i in.mp4 -vf scale=640:480,pad=640:480:0:0,fps=24 -c:v libx264 out.mp4",
		wantIn: 1, wantOut: 1, wantN: 3, wantE: 5,
	},
}

func TestGoldenFFmpegCommands(t *testing.T) {
	for _, tt := range goldenTests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := Parse(tt.cmd)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if len(cfg.Inputs) != tt.wantIn {
				t.Errorf("inputs: got %d, want %d", len(cfg.Inputs), tt.wantIn)
			}
			if len(cfg.Outputs) != tt.wantOut {
				t.Errorf("outputs: got %d, want %d", len(cfg.Outputs), tt.wantOut)
			}
			if len(cfg.Graph.Nodes) != tt.wantN {
				t.Errorf("nodes: got %d, want %d", len(cfg.Graph.Nodes), tt.wantN)
			}
			if len(cfg.Graph.Edges) != tt.wantE {
				t.Errorf("edges: got %d, want %d", len(cfg.Graph.Edges), tt.wantE)
			}
		})
	}
}

func TestGoldenJSON(t *testing.T) {
	// Verify round-trip: parse → JSON → parse back produces identical config
	cfg, err := Parse("ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 -c:a aac out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		t.Fatal(err)
	}
	var cfg2 map[string]any
	if err := json.Unmarshal(b, &cfg2); err != nil {
		t.Fatal(err)
	}
	if cfg2["schema_version"] != "1.0" {
		t.Errorf("schema_version: got %v, want 1.0", cfg2["schema_version"])
	}
}

func TestFilterExprNamedParams(t *testing.T) {
	name, params := parseFilterExpr("scale=w=1280:h=720")
	if name != "scale" {
		t.Errorf("name: got %q, want scale", name)
	}
	if params["w"] != "1280" {
		t.Errorf("param w: got %v, want 1280", params["w"])
	}
	if params["h"] != "720" {
		t.Errorf("param h: got %v, want 720", params["h"])
	}
}

func TestFilterExprPositionalParams(t *testing.T) {
	name, params := parseFilterExpr("scale=1280:720")
	if name != "scale" {
		t.Errorf("name: got %q, want scale", name)
	}
	if params["_pos0"] != "1280" {
		t.Errorf("param _pos0: got %v, want 1280", params["_pos0"])
	}
	if params["_pos1"] != "720" {
		t.Errorf("param _pos1: got %v, want 720", params["_pos1"])
	}
}

func TestFilterExprNoParams(t *testing.T) {
	name, params := parseFilterExpr("null")
	if name != "null" {
		t.Errorf("name: got %q, want null", name)
	}
	if params != nil {
		t.Errorf("params: got %v, want nil", params)
	}
}

func TestParseOutputFormat(t *testing.T) {
	cfg, err := Parse("ffmpeg -i in.mp4 -f matroska -c:v libx264 out.mkv")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Outputs[0].Format != "matroska" {
		t.Errorf("format: got %q, want matroska", cfg.Outputs[0].Format)
	}
}

func TestParseQuotedPaths(t *testing.T) {
	cfg, err := Parse(`ffmpeg -i "my video.mp4" -c:v libx264 "my output.mp4"`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inputs[0].URL != "my video.mp4" {
		t.Errorf("input URL: got %q, want %q", cfg.Inputs[0].URL, "my video.mp4")
	}
	if cfg.Outputs[0].URL != "my output.mp4" {
		t.Errorf("output URL: got %q, want %q", cfg.Outputs[0].URL, "my output.mp4")
	}
}

func TestParseSingleQuotedPaths(t *testing.T) {
	cfg, err := Parse("ffmpeg -i 'my video.mp4' -c:v libx264 'my output.mp4'")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Inputs[0].URL != "my video.mp4" {
		t.Errorf("input URL: got %q", cfg.Inputs[0].URL)
	}
}

func TestParseStripsFfmpegPath(t *testing.T) {
	cfg, err := Parse("/usr/bin/ffmpeg -i in.mp4 -c:v libx264 out.mp4")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Inputs) != 1 {
		t.Fatalf("expected 1 input, got %d", len(cfg.Inputs))
	}
}
