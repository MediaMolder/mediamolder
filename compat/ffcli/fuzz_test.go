package ffcli

import (
	"testing"
)

// FuzzParse exercises the FFmpeg CLI parser with arbitrary input.
// Invariant: Parse must never panic, regardless of input.
func FuzzParse(f *testing.F) {
	// Seed corpus: valid commands.
	f.Add("ffmpeg -i in.mp4 -c:v libx264 out.mp4")
	f.Add("ffmpeg -i in.mp4 -c:v libx264 -c:a aac out.mp4")
	f.Add("ffmpeg -i in.mp4 -vf scale=1280:720 -c:v libx264 out.mp4")
	f.Add("ffmpeg -i in.mp4 -vf \"scale=640:480,fps=30\" -c:v libx264 out.mp4")
	f.Add("ffmpeg -i in.mp4 -af \"volume=2.0\" -c:a aac out.mp3")
	f.Add("ffmpeg -i in.mp4 -an -c:v libx264 out.mp4")
	f.Add("ffmpeg -i in.mp4 -vn -c:a aac out.mp3")
	f.Add("ffmpeg -i in.mp4 -c copy out.mkv")
	f.Add("ffmpeg -i in.mp4 -b:v 2M -b:a 128k -c:v libx264 out.mp4")
	f.Add("ffmpeg -hwaccel cuda -i in.mp4 -c:v h264_nvenc out.mp4")
	f.Add("ffmpeg -i in.mp4 -bsf:v h264_mp4toannexb -c copy out.ts")
	f.Add("ffmpeg -i in.mp4 -c:s srt out.mkv")
	f.Add("ffmpeg -i in.mp4 -sn -c:v libx264 out.mp4")
	f.Add("ffmpeg -y -i in.mp4 -c:v libx264 out.mp4")
	f.Add("ffmpeg -i \"my file.mp4\" -c:v libx264 \"output.mp4\"")
	f.Add("ffmpeg -hwaccel vaapi -hwaccel_device /dev/dri/renderD128 -i in.mp4 -c:v h264_vaapi out.mp4")

	// Edge cases.
	f.Add("")
	f.Add("ffmpeg")
	f.Add("-i")
	f.Add("-c:v")
	f.Add("ffmpeg -i in.mp4")
	f.Add("out.mp4")

	f.Fuzz(func(t *testing.T, cmdline string) {
		// Must not panic. Errors are expected and fine.
		cfg, err := Parse(cmdline)
		if err != nil {
			return
		}
		// If parsing succeeded, basic structural invariants should hold.
		if cfg.SchemaVersion != "1.0" {
			t.Errorf("schema_version = %q, want 1.0", cfg.SchemaVersion)
		}
		if len(cfg.Inputs) == 0 {
			t.Error("parsed config has no inputs")
		}
		if len(cfg.Outputs) == 0 {
			t.Error("parsed config has no outputs")
		}
	})
}

// FuzzParseArgs exercises the argument-based parser with arbitrary tokens.
func FuzzParseArgs(f *testing.F) {
	f.Add([]byte("-i\x00in.mp4\x00-c:v\x00libx264\x00out.mp4"))
	f.Add([]byte("ffmpeg\x00-i\x00in.mp4\x00out.mp4"))
	f.Add([]byte(""))
	f.Add([]byte("-i\x00\x00out.mp4"))

	f.Fuzz(func(t *testing.T, data []byte) {
		// Split on null bytes to create arg slices.
		var args []string
		start := 0
		for i, b := range data {
			if b == 0 {
				if i > start {
					args = append(args, string(data[start:i]))
				}
				start = i + 1
			}
		}
		if start < len(data) {
			args = append(args, string(data[start:]))
		}

		// Must not panic.
		ParseArgs(args)
	})
}
