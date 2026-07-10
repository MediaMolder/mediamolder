// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"os"
	"testing"
)

// TestTileGridsHEIF exercises the tile-grid stream-group surface against a real grid-coded
// HEIF (a smartphone HEIC stored as 512x512 HEVC tiles). Env-gated like the whisper tests:
// grid fixtures are personal photos, so none is checked in.
//
//	HEIF_GRID_TEST_FILE=/path/to/grid.heic go test -run TileGrids ./av
func TestTileGridsHEIF(t *testing.T) {
	path := os.Getenv("HEIF_GRID_TEST_FILE")
	if path == "" {
		t.Skip("set HEIF_GRID_TEST_FILE to a grid-coded HEIC/HEIF to run tile-grid tests")
	}
	if _, err := os.Stat(path); err != nil {
		t.Skipf("HEIF_GRID_TEST_FILE not readable: %v", err)
	}

	in, err := OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput: %v", err)
	}
	defer in.Close()

	grids := in.TileGrids()
	if len(grids) == 0 {
		t.Fatalf("no tile-grid stream groups found in %s (not a grid-coded HEIF?)", path)
	}
	g := grids[0]
	t.Logf("grid: canvas %dx%d, crop %dx%d@(%d,%d), %d tiles",
		g.CodedWidth, g.CodedHeight, g.Width, g.Height,
		g.HorizontalOffset, g.VerticalOffset, len(g.Tiles))

	// The libavformat invariants the composition contract depends on.
	if g.CodedWidth <= 0 || g.CodedHeight <= 0 {
		t.Fatalf("canvas %dx%d not positive", g.CodedWidth, g.CodedHeight)
	}
	if g.Width <= 0 || g.Width > g.CodedWidth-g.HorizontalOffset ||
		g.Height <= 0 || g.Height > g.CodedHeight-g.VerticalOffset {
		t.Fatalf("crop %dx%d@(%d,%d) out of canvas %dx%d",
			g.Width, g.Height, g.HorizontalOffset, g.VerticalOffset, g.CodedWidth, g.CodedHeight)
	}
	if len(g.Tiles) == 0 {
		t.Fatal("grid has no tiles")
	}

	// Every tile must reference a real, decodable video stream that is smaller than the
	// presentation image (the whole point of the grid) and sit inside the canvas.
	n := in.NumStreams()
	seen := map[int]bool{}
	for _, tile := range g.Tiles {
		if tile.StreamIndex < 0 || tile.StreamIndex >= n {
			t.Fatalf("tile stream index %d out of range [0,%d)", tile.StreamIndex, n)
		}
		si, err := in.StreamInfo(tile.StreamIndex)
		if err != nil {
			t.Fatalf("StreamInfo(%d): %v", tile.StreamIndex, err)
		}
		if si.Type != MediaTypeVideo {
			t.Fatalf("tile stream %d is not video", tile.StreamIndex)
		}
		// Strict: HEIF grids tile the coded canvas exactly (coded_width = cols × tileW), so
		// every tile must sit fully inside it — a loose bound here would greenlight a
		// binding that mis-derives offsets and composes scrambled images.
		if tile.X < 0 || tile.Y < 0 ||
			tile.X+si.Width > g.CodedWidth || tile.Y+si.Height > g.CodedHeight {
			t.Fatalf("tile %d at (%d,%d) size %dx%d outside canvas %dx%d",
				tile.StreamIndex, tile.X, tile.Y, si.Width, si.Height, g.CodedWidth, g.CodedHeight)
		}
		seen[tile.StreamIndex] = true
	}
	// A grid photo's presentation image must be larger than any single tile — the bug this
	// surface exists to fix is exactly "decoded one tile, called it the image".
	first, _ := in.StreamInfo(g.Tiles[0].StreamIndex)
	if g.Width*g.Height <= first.Width*first.Height {
		t.Fatalf("presentation %dx%d not larger than a single %dx%d tile",
			g.Width, g.Height, first.Width, first.Height)
	}
	// Grid members should not cover EVERY stream when the container carries a separate
	// thumbnail image (smartphone HEICs do); tolerate containers without one.
	if len(seen) == n {
		t.Logf("note: every container stream is a grid member (no separate thumbnail stream)")
	}
}

// TestTileGridsNonGrid pins the common case with no fixture needed: an ordinary (non-grid)
// input has no tile-grid stream groups and TileGrids returns nil — always-on, so the cgo
// path (nb_stream_groups read) runs in every CI leg, not only where a HEIC fixture is set.
func TestTileGridsNonGrid(t *testing.T) {
	in, err := OpenInputWithFormat("color=black:s=64x64:d=0.1", "lavfi", nil)
	if err != nil {
		t.Fatalf("OpenInputWithFormat(lavfi): %v", err)
	}
	defer in.Close()
	if grids := in.TileGrids(); grids != nil {
		t.Fatalf("TileGrids on a non-grid input = %+v, want nil", grids)
	}
}
