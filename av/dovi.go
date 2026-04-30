// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdlib.h>
// #include "libavformat/avformat.h"
// #include "libavcodec/codec_par.h"
// #include "libavcodec/packet.h"
// #include "libavutil/dovi_meta.h"
// #include "libavutil/mem.h"
//
// // Attach AV_PKT_DATA_DOVI_CONF (AVDOVIDecoderConfigurationRecord) to
// // the output stream's codecpar.coded_side_data so the muxer writes
// // the `dvcC`/`dvvC` box (mp4/mov), the BlockAddIDExtraData entry
// // (matroska), or the SEI / track header (mpegts) — exactly the path
// // FFmpeg takes when copying a Dolby Vision stream. Mirrors the
// // ff_isom_write_dvcc_dvvc pre-write side-data lookup in
// // libavformat/movenc.c.
// //
// // Profile values are the canonical DV spec: 4 (HEVC dual-layer),
// // 5 (HEVC base+RPU, single-layer, BL-incompatible), 7 (HEVC dual,
// // BL-compatible), 8 (HEVC single-layer, BL-compatible), 9 (AVC),
// // 10 (AV1). Levels 0-13 from the DV bitstream spec.
// //
// // bl_signal_compat_id selects the cross-compatibility hint inside
// // profile-8/profile-10 streams: 0 = none, 1 = HDR10, 2 = SDR/BT.709,
// // 4 = HLG. Defaulted to 0 here; callers that need an explicit value
// // should add a Compat field on DoVi later — out of scope for the
// // initial passthrough landing.
// static int attach_dovi(AVFormatContext *fc, int idx,
//                        unsigned profile, unsigned level,
//                        int rpu_present, int el_present, int bl_present,
//                        unsigned bl_compat_id) {
//     if (!fc || idx < 0 || idx >= (int)fc->nb_streams) return -1;
//     AVStream *st = fc->streams[idx];
//     size_t size = 0;
//     AVDOVIDecoderConfigurationRecord *cfg = av_dovi_alloc(&size);
//     if (!cfg) return AVERROR(ENOMEM);
//     cfg->dv_version_major = 1;
//     cfg->dv_version_minor = 0;
//     cfg->dv_profile = (uint8_t)profile;
//     cfg->dv_level   = (uint8_t)level;
//     cfg->rpu_present_flag = rpu_present ? 1 : 0;
//     cfg->el_present_flag  = el_present  ? 1 : 0;
//     cfg->bl_present_flag  = bl_present  ? 1 : 0;
//     cfg->dv_bl_signal_compatibility_id = (uint8_t)bl_compat_id;
//     AVPacketSideData *sd = av_packet_side_data_add(
//         &st->codecpar->coded_side_data, &st->codecpar->nb_coded_side_data,
//         AV_PKT_DATA_DOVI_CONF, cfg, size, 0);
//     if (!sd) { av_freep(&cfg); return AVERROR(ENOMEM); }
//     return 0;
// }
import "C"

// DoViConfig is the stream-level Dolby Vision configuration record
// passed through to the muxer via AV_PKT_DATA_DOVI_CONF (Wave 6 #35).
// Mirrors the relevant fields of libavutil/dovi_meta.h's
// AVDOVIDecoderConfigurationRecord.
type DoViConfig struct {
	Profile           uint8 // 4, 5, 7, 8, 9, 10
	Level             uint8 // 0..13
	RPUPresent        bool  // RPU NAL units present in bitstream (default: true)
	ELPresent         bool  // enhancement-layer present (profiles 4 & 7 only)
	BLPresent         bool  // base-layer present (default: true)
	BLCompatibilityID uint8 // 0=none, 1=HDR10, 2=SDR/BT.709, 4=HLG
}

// SetStreamDoViConfig attaches AV_PKT_DATA_DOVI_CONF
// (AVDOVIDecoderConfigurationRecord) to the output stream's codecpar
// so mp4/mov writes the dvcC/dvvC box, matroska writes the
// BlockAddIDExtraData entry, and mpegts writes the DV registration
// descriptor. Must be called between AddStream and WriteHeader.
// Mirrors the side-data the FFmpeg `-c:v copy` path carries through
// for Dolby Vision streams (libavformat/movenc.c::mov_write_dvcc_tag
// + libavcodec/dovi_rpu.c).
func (f *OutputFormatContext) SetStreamDoViConfig(idx int, c DoViConfig) error {
	if f == nil || f.p == nil {
		return nil
	}
	if c.Profile == 0 {
		return nil
	}
	rpu := C.int(0)
	if c.RPUPresent {
		rpu = 1
	}
	el := C.int(0)
	if c.ELPresent {
		el = 1
	}
	bl := C.int(0)
	if c.BLPresent {
		bl = 1
	}
	rc := C.attach_dovi(f.p, C.int(idx),
		C.unsigned(c.Profile), C.unsigned(c.Level),
		rpu, el, bl,
		C.unsigned(c.BLCompatibilityID))
	if rc < 0 {
		return newErr(rc)
	}
	return nil
}
