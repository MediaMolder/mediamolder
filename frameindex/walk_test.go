// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package frameindex

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func tiny(t *testing.T) string {
	t.Helper()
	p := filepath.Join("..", "av", "testdata", "tiny.mp4")
	if _, err := os.Stat(p); err != nil {
		t.Skip("testdata/tiny.mp4 not available:", err)
	}
	return p
}

func TestPacketsAndDecodeAgree(t *testing.T) {
	path := tiny(t)
	pk, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := WalkDecode(context.Background(), path, pk.Stream, nil)
	if err != nil {
		t.Fatal(err)
	}
	if pk.Stream != dc.Stream {
		t.Fatalf("stream packet %d decode %d", pk.Stream, dc.Stream)
	}
	if pk.TimeBase != dc.TimeBase || pk.TimeBase[1] == 0 {
		t.Fatalf("timebase packet %v decode %v", pk.TimeBase, dc.TimeBase)
	}
	if len(pk.PTS) == 0 || len(pk.PTS) != len(dc.PTS) {
		t.Fatalf("len packet %d decode %d", len(pk.PTS), len(dc.PTS))
	}
	for i := range pk.PTS {
		if i > 0 && pk.PTS[i] < pk.PTS[i-1] {
			t.Fatalf("packet PTS not monotone at %d: %d then %d", i, pk.PTS[i-1], pk.PTS[i])
		}
		if pk.PTS[i] != dc.PTS[i] {
			t.Fatalf("PTS[%d] packet %d decode %d", i, pk.PTS[i], dc.PTS[i])
		}
	}
	frames, stream, tb, err := Estimate(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	if stream != pk.Stream || tb != pk.TimeBase {
		t.Fatalf("estimate stream %d tb %v, walk stream %d tb %v", stream, tb, pk.Stream, pk.TimeBase)
	}
	if frames < 0 {
		t.Fatalf("estimate %d", frames)
	}
}

func TestCancelBeforeOpen(t *testing.T) {
	path := tiny(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := WalkPackets(ctx, path, -1, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("packets: %v", err)
	}
	if _, err := WalkDecode(ctx, path, -1, nil); !errors.Is(err, context.Canceled) {
		t.Fatalf("decode: %v", err)
	}
}

func TestWalkAbortsPastCap(t *testing.T) {
	path := tiny(t)
	pk, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pk.PTS) < 2 {
		t.Skip("fixture has fewer than 2 frames")
	}
	old := maxFrames
	maxFrames = len(pk.PTS) - 1
	t.Cleanup(func() { maxFrames = old })

	if _, err := WalkPackets(context.Background(), path, -1, nil); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("packets: %v", err)
	}
	if _, err := WalkDecode(context.Background(), path, -1, nil); !errors.Is(err, ErrTooManyFrames) {
		t.Fatalf("decode: %v", err)
	}
}

func TestCapAllowsTheLastRow(t *testing.T) {
	path := tiny(t)
	pk, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	old := maxFrames
	maxFrames = len(pk.PTS)
	t.Cleanup(func() { maxFrames = old })
	got, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(got.PTS) != maxFrames {
		t.Fatalf("packets kept %d, cap %d", len(got.PTS), maxFrames)
	}
	dc, err := WalkDecode(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(dc.PTS) != maxFrames {
		t.Fatalf("decode kept %d, cap %d", len(dc.PTS), maxFrames)
	}
}

func TestCancelDuringWalkReturnsNoTable(t *testing.T) {
	path := tiny(t)
	for _, walk := range []struct {
		name string
		fn   func(context.Context, string, int, func(int)) (*Table, error)
	}{
		{"packets", WalkPackets},
		{"decode", WalkDecode},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		var saw int
		table, err := walk.fn(ctx, path, -1, func(n int) {
			saw = n
			if n == 1 {
				cancel()
			}
		})
		cancel()
		if !errors.Is(err, context.Canceled) || table != nil {
			t.Fatalf("%s table=%v err=%v", walk.name, table, err)
		}
		if saw < 1 {
			t.Fatalf("%s progress never reported a row", walk.name)
		}
	}
}

func TestProgressCountsAcceptedRows(t *testing.T) {
	path := tiny(t)
	var got []int
	table, err := WalkPackets(context.Background(), path, -1, func(n int) {
		got = append(got, n)
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != len(table.PTS) {
		t.Fatalf("progress %v, rows %d", got, len(table.PTS))
	}
	for i, n := range got {
		if n != i+1 {
			t.Fatalf("progress[%d] = %d, want %d", i, n, i+1)
		}
	}
}

func TestNotVideo(t *testing.T) {
	path := tiny(t)
	if _, _, _, err := Estimate(path, 99); err == nil {
		t.Fatal("expected an error for a missing stream")
	}
}

func TestVFRTableIsNotNominal(t *testing.T) {
	path := ffmpegClip(t, "vfr.mp4",
		"-f", "lavfi", "-i", "testsrc=size=160x120:rate=30:duration=1",
		"-vf", "select='not(eq(mod(n,3),1))',setpts=PTS",
		"-fps_mode", "vfr",
		"-c:v", "libx264", "-bf", "0", "-g", "5", "-pix_fmt", "yuv420p")
	pk, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := WalkDecode(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(pk.PTS) < 4 || len(pk.PTS) != len(dc.PTS) {
		t.Fatalf("len packet %d decode %d", len(pk.PTS), len(dc.PTS))
	}
	var gaps []int64
	seen := map[int64]bool{}
	for i := range pk.PTS {
		if pk.PTS[i] != dc.PTS[i] {
			t.Fatalf("PTS[%d] packet %d decode %d", i, pk.PTS[i], dc.PTS[i])
		}
		if i == 0 {
			continue
		}
		g := pk.PTS[i] - pk.PTS[i-1]
		if g <= 0 {
			t.Fatalf("gap %d at %d", g, i)
		}
		gaps = append(gaps, g)
		seen[g] = true
	}
	if len(seen) < 2 {
		t.Fatalf("PTS gaps are uniform (%v); this fixture must be variable", gaps)
	}
	est, _, _, err := Estimate(path, -1)
	if err != nil {
		t.Fatal(err)
	}
	if est != int64(len(pk.PTS)) {
		t.Fatalf("estimate %d, rows %d (the tick rate must not count this clip)", est, len(pk.PTS))
	}
}

func TestInterlacedRowsStayInStreamTimebase(t *testing.T) {
	path := ffmpegClip(t, "interlaced.mpg",
		"-f", "lavfi", "-i", "testsrc=duration=0.4:size=320x240:rate=25",
		"-flags", "+ildct+ilme", "-top", "1",
		"-c:v", "mpeg2video", "-b:v", "2000k", "-bf", "0", "-pix_fmt", "yuv420p")
	pk, err := WalkPackets(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	dc, err := WalkDecode(context.Background(), path, -1, nil)
	if err != nil {
		t.Fatal(err)
	}
	// A decoder can drop a timestamp the packet walk kept. What it must not do
	// is index fields, or stamp the deinterlacer's halved ticks.
	if len(dc.PTS) < 5 || len(dc.PTS) > len(pk.PTS) {
		t.Fatalf("len packet %d decode %d (a field must not be its own row)", len(pk.PTS), len(dc.PTS))
	}
	j := 0
	for _, pts := range dc.PTS {
		for j < len(pk.PTS) && pk.PTS[j] != pts {
			j++
		}
		if j == len(pk.PTS) {
			t.Fatalf("decode PTS %d is not a packet PTS in order (left the stream timebase)", pts)
		}
		j++
	}
	if pk.TimeBase != dc.TimeBase || dc.TimeBase[1] == 0 {
		t.Fatalf("timebase %v / %v", pk.TimeBase, dc.TimeBase)
	}
}

func ffmpegClip(t *testing.T, name string, args ...string) string {
	t.Helper()
	ff, err := exec.LookPath("ffmpeg")
	if err != nil {
		t.Skip("ffmpeg not on PATH")
	}
	out := filepath.Join(t.TempDir(), name)
	cmd := exec.Command(ff, append([]string{"-hide_banner", "-loglevel", "error", "-y"}, append(args, out)...)...)
	if msg, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("ffmpeg: %v\n%s", err, msg)
	}
	return out
}
