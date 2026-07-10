// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
//
// // Helpers: stream-group access. Unions and pointer arrays are awkward from Go, so tiny
// // accessors keep the Go side plain. All valid for libavformat >= 61 (FFmpeg 7.1+; the
// // package floor is 8.1 — see version.go), where stream groups are always present.
// static const AVStreamGroup *mm_stream_group(const AVFormatContext *ctx, unsigned i) {
//     return ctx->stream_groups[i];
// }
// static const AVStreamGroupTileGrid *mm_group_tile_grid(const AVStreamGroup *g) {
//     return g->type == AV_STREAM_GROUP_PARAMS_TILE_GRID ? g->params.tile_grid : NULL;
// }
// // Format-level stream index of group member i (offsets[].idx indexes the GROUP's stream
// // list, not the container's).
// static int mm_group_stream_index(const AVStreamGroup *g, unsigned i) {
//     return g->streams[i]->index;
// }
// static unsigned mm_tile_idx(const AVStreamGroupTileGrid *tg, unsigned i) { return tg->offsets[i].idx; }
// static int mm_tile_x(const AVStreamGroupTileGrid *tg, unsigned i) { return tg->offsets[i].horizontal; }
// static int mm_tile_y(const AVStreamGroupTileGrid *tg, unsigned i) { return tg->offsets[i].vertical; }
// static uint8_t mm_grid_bg(const AVStreamGroupTileGrid *tg, int i) { return tg->background[i]; }
// static int mm_group_is_default(const AVStreamGroup *g) { return (g->disposition & AV_DISPOSITION_DEFAULT) != 0; }
import "C"

// Tile places one coded stream's frame onto a tile-grid canvas. StreamIndex is the
// container-level stream index (usable directly with OpenDecoder); X/Y are the offsets in
// pixels of the tile's top-left corner from the canvas's top-left corner.
type Tile struct {
	StreamIndex int
	X, Y        int
}

// TileGridInfo describes one AV_STREAM_GROUP_PARAMS_TILE_GRID stream group: several
// independently-coded image streams composed onto a single canvas for presentation — the
// HEIF/AVIF grid layout smartphone HEIC photos use (e.g. a 4032x3024 photo stored as 48
// HEVC tiles of 512x512). Rendering contract (mirrors the libavformat documentation):
// fill a CodedWidth x CodedHeight canvas with Background, draw each tile's frame at its
// offset IN ORDER (later tiles overdraw earlier ones on overlap), then crop to the
// Width x Height window at (HorizontalOffset, VerticalOffset).
type TileGridInfo struct {
	GroupIndex  int // stream-group index in the container
	CodedWidth  int // full canvas, before cropping
	CodedHeight int

	// The presentation crop window.
	HorizontalOffset int
	VerticalOffset   int
	Width            int
	Height           int

	Background [4]uint8 // RGBA fill for canvas pixels no tile covers

	// Default reports the AV_DISPOSITION_DEFAULT flag: the container marks this group as
	// the primary presentation (HEIF primary item). Prefer it when a file carries several
	// grids (auxiliary gain-map/depth images are grids too).
	Default bool

	// Tiles in composition (z-)order. A stream may appear more than once.
	Tiles []Tile
}

// TileGrids returns the input's tile-grid stream groups, in container order. Most files
// have none (nil); a grid-coded HEIF typically has exactly one, whose canvas — not the
// individual 512x512 tile streams — is the image's true geometry. Callers decoding "the
// image" of such a file must decode every member stream and compose per TileGridInfo,
// or they will produce a single tile.
func (f *InputFormatContext) TileGrids() []TileGridInfo {
	n := int(f.p.nb_stream_groups)
	if n == 0 {
		return nil
	}
	var grids []TileGridInfo
	for gi := 0; gi < n; gi++ {
		g := C.mm_stream_group(f.p, C.uint(gi))
		tg := C.mm_group_tile_grid(g)
		if tg == nil {
			continue
		}
		info := TileGridInfo{
			GroupIndex:       gi,
			CodedWidth:       int(tg.coded_width),
			CodedHeight:      int(tg.coded_height),
			HorizontalOffset: int(tg.horizontal_offset),
			VerticalOffset:   int(tg.vertical_offset),
			Width:            int(tg.width),
			Height:           int(tg.height),
			Default:          C.mm_group_is_default(g) != 0,
		}
		for i := 0; i < 4; i++ {
			info.Background[i] = uint8(C.mm_grid_bg(tg, C.int(i)))
		}
		nTiles := int(tg.nb_tiles)
		info.Tiles = make([]Tile, 0, nTiles)
		malformed := false
		for ti := 0; ti < nTiles; ti++ {
			idx := C.mm_tile_idx(tg, C.uint(ti))
			if uint(idx) >= uint(g.nb_streams) {
				// The header contract says idx < nb_streams; a violation means corrupt
				// metadata (or a libavformat bug). A grid missing one tile would compose a
				// valid-LOOKING image with a background hole — silent corruption callers
				// cannot detect — so drop the whole group and let them fall back.
				malformed = true
				break
			}
			info.Tiles = append(info.Tiles, Tile{
				StreamIndex: int(C.mm_group_stream_index(g, idx)),
				X:           int(C.mm_tile_x(tg, C.uint(ti))),
				Y:           int(C.mm_tile_y(tg, C.uint(ti))),
			})
		}
		if malformed {
			continue
		}
		grids = append(grids, info)
	}
	return grids
}
