// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdlib.h>
// #include <string.h>
// #include "libavformat/avformat.h"
// #include "libavcodec/codec_par.h"
// #include "libavcodec/packet.h"
// #include "libavutil/mastering_display_metadata.h"
// #include "libavutil/mem.h"
// #include "libavutil/pixdesc.h"
// #include "libavutil/rational.h"
//
// // Lookup helpers returning -1 for unknown names so the Go side can
// // produce a clean validation error instead of silently writing
// // AVCOL_*_UNSPECIFIED.
// static int color_range_from_name(const char *n)     { return av_color_range_from_name(n); }
// static int color_primaries_from_name(const char *n) { return av_color_primaries_from_name(n); }
// static int color_transfer_from_name(const char *n)  { return av_color_transfer_from_name(n); }
// static int color_space_from_name(const char *n)     { return av_color_space_from_name(n); }
// static int chroma_location_from_name(const char *n) { return av_chroma_location_from_name(n); }
//
// // Apply a parsed Color block to an output stream's codecpar. Each
// // value is checked against -1 (unset / not requested by caller) so
// // partially-specified blocks leave the other fields alone.
// static int apply_color(AVFormatContext *fc, int idx,
//                        int range, int primaries, int trc, int space, int chroma) {
//     if (!fc || idx < 0 || idx >= (int)fc->nb_streams) return -1;
//     AVStream *st = fc->streams[idx];
//     if (range     >= 0) st->codecpar->color_range     = (enum AVColorRange)range;
//     if (primaries >= 0) st->codecpar->color_primaries = (enum AVColorPrimaries)primaries;
//     if (trc       >= 0) st->codecpar->color_trc       = (enum AVColorTransferCharacteristic)trc;
//     if (space     >= 0) st->codecpar->color_space     = (enum AVColorSpace)space;
//     if (chroma    >= 0) st->codecpar->chroma_location = (enum AVChromaLocation)chroma;
//     return 0;
// }
//
// // Attach AVMasteringDisplayMetadata to stream codecpar.coded_side_data.
// // primaries/whitepoint/luminance use 50000-denominator rationals (the
// // canonical encoding x264/x265/HEVC SEI use). White point and
// // primaries arrays are flattened: prim={Rx,Ry,Gx,Gy,Bx,By}, wp={Wx,Wy}.
// static int attach_mastering(AVFormatContext *fc, int idx,
//                             int has_prim, int prim[6],
//                             int has_wp,   int wp[2],
//                             int has_lum,  int min_lum, int max_lum) {
//     if (!fc || idx < 0 || idx >= (int)fc->nb_streams) return -1;
//     AVStream *st = fc->streams[idx];
//     size_t size = 0;
//     AVMasteringDisplayMetadata *md = av_mastering_display_metadata_alloc_size(&size);
//     if (!md) return AVERROR(ENOMEM);
//     if (has_prim) {
//         for (int i = 0; i < 3; i++) {
//             md->display_primaries[i][0] = (AVRational){prim[i*2],     50000};
//             md->display_primaries[i][1] = (AVRational){prim[i*2 + 1], 50000};
//         }
//     }
//     if (has_wp) {
//         md->white_point[0] = (AVRational){wp[0], 50000};
//         md->white_point[1] = (AVRational){wp[1], 50000};
//     }
//     if (has_lum) {
//         md->min_luminance = (AVRational){min_lum, 10000};
//         md->max_luminance = (AVRational){max_lum, 10000};
//     }
//     md->has_primaries = has_prim ? 1 : 0;
//     md->has_luminance = has_lum  ? 1 : 0;
//     AVPacketSideData *sd = av_packet_side_data_add(
//         &st->codecpar->coded_side_data, &st->codecpar->nb_coded_side_data,
//         AV_PKT_DATA_MASTERING_DISPLAY_METADATA, md, size, 0);
//     if (!sd) { av_freep(&md); return AVERROR(ENOMEM); }
//     return 0;
// }
//
// static int attach_cll(AVFormatContext *fc, int idx,
//                       unsigned max_cll, unsigned max_fall) {
//     if (!fc || idx < 0 || idx >= (int)fc->nb_streams) return -1;
//     AVStream *st = fc->streams[idx];
//     size_t size = 0;
//     AVContentLightMetadata *cll = av_content_light_metadata_alloc(&size);
//     if (!cll) return AVERROR(ENOMEM);
//     cll->MaxCLL  = max_cll;
//     cll->MaxFALL = max_fall;
//     AVPacketSideData *sd = av_packet_side_data_add(
//         &st->codecpar->coded_side_data, &st->codecpar->nb_coded_side_data,
//         AV_PKT_DATA_CONTENT_LIGHT_LEVEL, cll, size, 0);
//     if (!sd) { av_freep(&cll); return AVERROR(ENOMEM); }
//     return 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// ColorParams holds the canonical FFmpeg color metadata names for an
// output stream. Empty strings mean "do not change". Names are the
// libavutil canonical names (av_color_*_name): e.g. "tv"/"pc",
// "bt709"/"bt2020", "smpte2084"/"arib-std-b67", etc.
type ColorParams struct {
	Range          string // tv, pc
	Primaries      string // bt709, bt2020, smpte170m, ...
	Transfer       string // bt709, smpte2084 (PQ), arib-std-b67 (HLG), ...
	Space          string // bt709, bt2020nc, ...
	ChromaLocation string // left, center, topleft, ...
}

// MasteringDisplay encodes SMPTE ST 2086 mastering-display metadata.
// Primaries/WhitePoint use 0.00002 (1/50000) units (the standard
// HEVC/AV1 SEI encoding); MinLuminance/MaxLuminance use 0.0001 units
// (cd/m^2 ÷ 10000). HasPrimaries / HasLuminance gate which fields are
// emitted (mirrors AVMasteringDisplayMetadata's two flag fields).
type MasteringDisplay struct {
	HasPrimaries bool
	DisplayPrim  [6]int // Rx, Ry, Gx, Gy, Bx, By in 1/50000 units
	WhitePoint   [2]int // Wx, Wy in 1/50000 units
	HasLuminance bool
	MinLuminance int // cd/m^2 * 10000
	MaxLuminance int // cd/m^2 * 10000
}

// ContentLightLevel is CTA-861.3 MaxCLL / MaxFALL (cd/m^2).
type ContentLightLevel struct {
	MaxCLL  uint32
	MaxFALL uint32
}

// SetStreamColor writes the Color metadata onto output stream idx's
// codecpar. Empty fields are left unchanged. Unknown names produce an
// error (av_color_*_from_name returned a negative value or AVCOL_*
// _UNSPECIFIED). Must be called between AddStream and WriteHeader.
func (f *OutputFormatContext) SetStreamColor(idx int, c ColorParams) error {
	if f == nil || f.p == nil {
		return nil
	}
	lookup := func(label, name string, fn func(*C.char) C.int) (int, error) {
		if name == "" {
			return -1, nil
		}
		cs := C.CString(name)
		defer C.free(unsafe.Pointer(cs))
		v := int(fn(cs))
		if v < 0 {
			return -1, fmt.Errorf("unknown %s %q", label, name)
		}
		return v, nil
	}
	rng, err := lookup("color_range", c.Range, func(s *C.char) C.int { return C.color_range_from_name(s) })
	if err != nil {
		return err
	}
	pri, err := lookup("color_primaries", c.Primaries, func(s *C.char) C.int { return C.color_primaries_from_name(s) })
	if err != nil {
		return err
	}
	trc, err := lookup("color_trc", c.Transfer, func(s *C.char) C.int { return C.color_transfer_from_name(s) })
	if err != nil {
		return err
	}
	spc, err := lookup("colorspace", c.Space, func(s *C.char) C.int { return C.color_space_from_name(s) })
	if err != nil {
		return err
	}
	chr, err := lookup("chroma_location", c.ChromaLocation, func(s *C.char) C.int { return C.chroma_location_from_name(s) })
	if err != nil {
		return err
	}
	if rc := C.apply_color(f.p, C.int(idx), C.int(rng), C.int(pri), C.int(trc), C.int(spc), C.int(chr)); rc < 0 {
		return newErr(rc)
	}
	return nil
}

// SetStreamMasteringDisplay attaches AV_PKT_DATA_MASTERING_DISPLAY_METADATA
// side data to the output stream's codecpar so the muxer (mp4/mov,
// matroska/webm, mpegts via SEI passthrough) writes the SMPTE ST 2086
// box. Must be called between AddStream and WriteHeader.
func (f *OutputFormatContext) SetStreamMasteringDisplay(idx int, m MasteringDisplay) error {
	if f == nil || f.p == nil {
		return nil
	}
	if !m.HasPrimaries && !m.HasLuminance {
		return nil
	}
	hasPrim := C.int(0)
	if m.HasPrimaries {
		hasPrim = 1
	}
	hasLum := C.int(0)
	if m.HasLuminance {
		hasLum = 1
	}
	prim := [6]C.int{
		C.int(m.DisplayPrim[0]), C.int(m.DisplayPrim[1]),
		C.int(m.DisplayPrim[2]), C.int(m.DisplayPrim[3]),
		C.int(m.DisplayPrim[4]), C.int(m.DisplayPrim[5]),
	}
	wp := [2]C.int{C.int(m.WhitePoint[0]), C.int(m.WhitePoint[1])}
	rc := C.attach_mastering(f.p, C.int(idx),
		hasPrim, &prim[0],
		hasPrim, &wp[0],
		hasLum, C.int(m.MinLuminance), C.int(m.MaxLuminance))
	if rc < 0 {
		return newErr(rc)
	}
	return nil
}

// SetStreamContentLightLevel attaches AV_PKT_DATA_CONTENT_LIGHT_LEVEL
// side data (CTA-861.3 MaxCLL / MaxFALL) to the output stream's
// codecpar. Must be called between AddStream and WriteHeader.
func (f *OutputFormatContext) SetStreamContentLightLevel(idx int, cll ContentLightLevel) error {
	if f == nil || f.p == nil {
		return nil
	}
	if cll.MaxCLL == 0 && cll.MaxFALL == 0 {
		return nil
	}
	rc := C.attach_cll(f.p, C.int(idx), C.unsigned(cll.MaxCLL), C.unsigned(cll.MaxFALL))
	if rc < 0 {
		return newErr(rc)
	}
	return nil
}
