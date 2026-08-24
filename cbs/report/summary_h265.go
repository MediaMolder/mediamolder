// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import "github.com/MediaMolder/MediaMolder/cbs"

var h265SliceTypeNames = [3]string{"B", "P", "I"}

func h265VPSSummary(vps *cbs.H265RawVPS) map[string]any {
	return map[string]any{
		"vps_id":         vps.VpsVideoParameterSetID,
		"max_layers":     vps.VpsMaxLayersMinus1 + 1,
		"max_sub_layers": vps.VpsMaxSubLayersMinus1 + 1,
		"profile_idc":    vps.ProfileTierLevel.GeneralProfileIdc,
		"level_idc":      vps.ProfileTierLevel.GeneralLevelIdc,
		"tier":           cond(vps.ProfileTierLevel.GeneralTierFlag != 0, "high", "main"),
	}
}

func h265SPSSummary(sps *cbs.H265RawSPS) map[string]any {
	// Rec. ITU-T H.265 7.4.3.2.1: conformance window cropping.
	width := int(sps.PicWidthInLumaSamples)
	height := int(sps.PicHeightInLumaSamples)
	if sps.ConformanceWindowFlag != 0 {
		subWidthC, subHeightC := 1, 1
		switch sps.ChromaFormatIdc {
		case 1:
			subWidthC, subHeightC = 2, 2
		case 2:
			subWidthC, subHeightC = 2, 1
		}
		width -= subWidthC * (int(sps.ConfWinLeftOffset) + int(sps.ConfWinRightOffset))
		height -= subHeightC * (int(sps.ConfWinTopOffset) + int(sps.ConfWinBottomOffset))
	}
	s := map[string]any{
		"sps_id":            sps.SpsSeqParameterSetID,
		"vps_id":            sps.SpsVideoParameterSetID,
		"profile_idc":       sps.ProfileTierLevel.GeneralProfileIdc,
		"level_idc":         sps.ProfileTierLevel.GeneralLevelIdc,
		"tier":              cond(sps.ProfileTierLevel.GeneralTierFlag != 0, "high", "main"),
		"width":             width,
		"height":            height,
		"chroma_format_idc": sps.ChromaFormatIdc,
		"bit_depth_luma":    sps.BitDepthLumaMinus8 + 8,
		"bit_depth_chroma":  sps.BitDepthChromaMinus8 + 8,
		"max_sub_layers":    sps.SpsMaxSubLayersMinus1 + 1,
	}
	if sps.VuiParametersPresentFlag != 0 {
		vui := map[string]any{}
		v := &sps.Vui
		if v.VuiTimingInfoPresentFlag != 0 {
			vui["timing"] = [2]uint32{v.VuiNumUnitsInTick, v.VuiTimeScale}
		}
		if v.VideoSignalTypePresentFlag != 0 {
			vui["colour"] = map[string]any{
				"primaries":  v.ColourPrimaries,
				"transfer":   v.TransferCharacteristics,
				"matrix":     v.MatrixCoefficients,
				"full_range": v.VideoFullRangeFlag != 0,
			}
		}
		s["vui"] = vui
	}
	return s
}

func h265PPSSummary(pps *cbs.H265RawPPS) map[string]any {
	return map[string]any{
		"pps_id":              pps.PpsPicParameterSetID,
		"sps_id":              pps.PpsSeqParameterSetID,
		"init_qp":             26 + int(pps.InitQpMinus26),
		"weighted_pred":       pps.WeightedPredFlag != 0,
		"tiles_enabled":       pps.TilesEnabledFlag != 0,
		"entropy_coding_sync": pps.EntropyCodingSyncEnabledFlag != 0,
		"tile_columns":        pps.NumTileColumnsMinus1 + 1,
		"tile_rows":           pps.NumTileRowsMinus1 + 1,
		"cabac_init_present":  pps.CabacInitPresentFlag != 0,
	}
}

func h265SliceSummary(sh *cbs.H265RawSliceHeader) map[string]any {
	st := "?"
	if sh.SliceType < 3 {
		st = h265SliceTypeNames[sh.SliceType]
	}
	return map[string]any{
		"slice_type":            st,
		"slice_type_value":      sh.SliceType,
		"pps_id":                sh.SlicePicParameterSetID,
		"first_slice_in_pic":    sh.FirstSliceSegmentInPicFlag != 0,
		"dependent":             sh.DependentSliceSegmentFlag != 0,
		"slice_segment_address": sh.SliceSegmentAddress,
		"pic_order_cnt_lsb":     sh.SlicePicOrderCntLsb,
		"slice_qp_delta":        sh.SliceQpDelta,
	}
}
