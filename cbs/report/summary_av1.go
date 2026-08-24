// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import "github.com/MediaMolder/MediaMolder/cbs"

var av1FrameTypeNames = [4]string{"KEY", "INTER", "INTRA_ONLY", "SWITCH"}

// summarizeAV1 handles *cbs.AV1RawOBU contents.
func summarizeAV1(obu *cbs.AV1RawOBU) map[string]any {
	switch obu.Header.OBUType {
	case 1: // OBU_SEQUENCE_HEADER
		return av1SequenceHeaderSummary(&obu.SequenceHeader)
	case 2: // OBU_TEMPORAL_DELIMITER
		return map[string]any{}
	case 3, 7: // OBU_FRAME_HEADER / OBU_REDUNDANT_FRAME_HEADER
		return av1FrameHeaderSummary(&obu.FrameHeader, nil)
	case 6: // OBU_FRAME
		return av1FrameHeaderSummary(&obu.Frame.Header, &obu.Frame.TileGroup)
	case 4: // OBU_TILE_GROUP
		return av1TileGroupSummary(&obu.TileGroup)
	case 5: // OBU_METADATA
		return av1MetadataSummary(&obu.Metadata)
	case 15: // OBU_PADDING
		return map[string]any{"padding_size": len(obu.Padding.Payload)}
	}
	return nil
}

func av1SequenceHeaderSummary(sh *cbs.AV1RawSequenceHeader) map[string]any {
	cc := &sh.ColorConfig
	bitDepth := 8
	if cc.HighBitdepth != 0 {
		bitDepth = 10
		if sh.SeqProfile == 2 && cc.TwelveBit != 0 {
			bitDepth = 12
		}
	}
	s := map[string]any{
		"seq_profile":               sh.SeqProfile,
		"still_picture":             sh.StillPicture != 0,
		"max_width":                 int(sh.MaxFrameWidthMinus1) + 1,
		"max_height":                int(sh.MaxFrameHeightMinus1) + 1,
		"bit_depth":                 bitDepth,
		"mono_chrome":               cc.MonoChrome != 0,
		"subsampling":               [2]uint8{cc.SubsamplingX, cc.SubsamplingY},
		"enable_order_hint":         sh.EnableOrderHint != 0,
		"film_grain_params_present": sh.FilmGrainParamsPresent != 0,
		"operating_points":          int(sh.OperatingPointsCntMinus1) + 1,
		"seq_level_idx":             sh.SeqLevelIdx[0],
		"seq_tier":                  sh.SeqTier[0],
	}
	if cc.ColorPrimaries != 0 || cc.TransferCharacteristics != 0 || cc.MatrixCoefficients != 0 {
		s["colour"] = map[string]any{
			"primaries":  cc.ColorPrimaries,
			"transfer":   cc.TransferCharacteristics,
			"matrix":     cc.MatrixCoefficients,
			"full_range": cc.ColorRange != 0,
		}
	}
	if sh.TimingInfoPresentFlag != 0 {
		s["timing"] = [2]uint32{sh.TimingInfo.NumUnitsInDisplayTick, sh.TimingInfo.TimeScale}
	}
	return s
}

func av1FrameHeaderSummary(fh *cbs.AV1RawFrameHeader, tg *cbs.AV1RawTileGroup) map[string]any {
	if fh.ShowExistingFrame != 0 {
		return map[string]any{
			"show_existing_frame":   true,
			"frame_to_show_map_idx": fh.FrameToShowMapIdx,
		}
	}
	ft := "?"
	if fh.FrameType < 4 {
		ft = av1FrameTypeNames[fh.FrameType]
	}
	s := map[string]any{
		"frame_type":           ft,
		"show_frame":           fh.ShowFrame != 0,
		"showable_frame":       fh.ShowableFrame != 0,
		"error_resilient_mode": fh.ErrorResilientMode != 0,
		"order_hint":           fh.OrderHint,
		"refresh_frame_flags":  fh.RefreshFrameFlags,
		"width":                int(fh.FrameWidthMinus1) + 1,
		"height":               int(fh.FrameHeightMinus1) + 1,
		"primary_ref_frame":    fh.PrimaryRefFrame,
		"base_q_idx":           fh.BaseQIdx,
		"tile_cols_log2":       fh.TileColsLog2,
		"tile_rows_log2":       fh.TileRowsLog2,
	}
	if fh.FrameType == 1 || fh.FrameType == 3 { // INTER / SWITCH
		s["ref_frame_idx"] = fh.RefFrameIdx
	}
	if fh.UseSuperres != 0 {
		s["superres_denom"] = fh.CodedDenom
	}
	if tg != nil {
		s["tile_data_size"] = len(tg.TileData.Data)
	}
	return s
}

func av1TileGroupSummary(tg *cbs.AV1RawTileGroup) map[string]any {
	return map[string]any{
		"tg_start":       tg.TgStart,
		"tg_end":         tg.TgEnd,
		"tile_data_size": len(tg.TileData.Data),
	}
}

func av1MetadataSummary(md *cbs.AV1RawMetadata) map[string]any {
	s := map[string]any{"metadata_type": md.MetadataType}
	switch md.MetadataType {
	case 1: // METADATA_TYPE_HDR_CLL
		s["name"] = "hdr_cll"
		s["max_cll"] = md.HDRCLL.MaxCLL
		s["max_fall"] = md.HDRCLL.MaxFALL
	case 2: // METADATA_TYPE_HDR_MDCV
		s["name"] = "hdr_mdcv"
		s["primary_chromaticity_x"] = md.HDRMDCV.PrimaryChromaticityX
		s["primary_chromaticity_y"] = md.HDRMDCV.PrimaryChromaticityY
		s["white_point"] = [2]uint16{md.HDRMDCV.WhitePointChromaticityX, md.HDRMDCV.WhitePointChromaticityY}
		s["luminance_max"] = md.HDRMDCV.LuminanceMax
		s["luminance_min"] = md.HDRMDCV.LuminanceMin
	case 3:
		s["name"] = "scalability"
	case 4: // METADATA_TYPE_ITUT_T35
		s["name"] = "itut_t35"
		s["country_code"] = md.ITUTT35.ItuTT35CountryCode
		s["data_length"] = len(md.ITUTT35.Payload)
	case 5:
		s["name"] = "timecode"
	default:
		s["name"] = "unknown"
	}
	return s
}
