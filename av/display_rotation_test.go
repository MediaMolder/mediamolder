// Copyright (C) 2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// TestStreamDisplayRotation checks that StreamInfo.Rotation reports the clockwise angle needed to
// display a rotated stream upright — FFmpeg's own get_rotation() convention (theta =
// -av_display_rotation_get, normalized to [0,360)). It synthesizes display-matrix-tagged clips with
// `ffmpeg -display_rotation` as a STREAM COPY (so the matrix is written to the container verbatim, not
// baked into the pixels) and reads each back. Skips when ffmpeg or a video fixture is unavailable.
func TestStreamDisplayRotation(t *testing.T) {
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	base := "../testdata/two_audio_tracks.mp4"
	in, err := OpenInput(base, nil)
	if err != nil {
		t.Skip("video fixture unavailable:", err)
	}
	in.Close()

	dir := t.TempDir()
	// `ffmpeg -display_rotation D` records a matrix whose upright-clockwise angle is (360-D)%360.
	for _, tc := range []struct{ in, want int }{{0, 0}, {90, 270}, {180, 180}, {270, 90}} {
		out := filepath.Join(dir, "r"+strconv.Itoa(tc.in)+".mp4")
		args := []string{"-y", "-v", "error"}
		if tc.in != 0 {
			args = append(args, "-display_rotation", strconv.Itoa(tc.in))
		}
		args = append(args, "-i", base, "-c", "copy", "-t", "1", out)
		if o, e := exec.Command(ffmpeg, args...).CombinedOutput(); e != nil {
			t.Fatalf("ffmpeg -display_rotation %d: %v\n%s", tc.in, e, o)
		}
		f, e := OpenInput(out, nil)
		if e != nil {
			t.Fatalf("open r%d: %v", tc.in, e)
		}
		got, found := 0, false
		for i := 0; i < f.NumStreams(); i++ {
			if si, se := f.StreamInfo(i); se == nil && si.Type == MediaTypeVideo {
				got, found = si.Rotation, true
				break
			}
		}
		f.Close()
		if !found {
			t.Fatalf("r%d: no video stream in output", tc.in)
		}
		if got != tc.want {
			t.Errorf("-display_rotation %d: StreamInfo.Rotation = %d, want %d", tc.in, got, tc.want)
		}
	}
}
