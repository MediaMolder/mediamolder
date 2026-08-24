// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Per-unit summaries: the derived fields a reader wants without walking
// the element trace. Purely a function of the decomposed content structs.

package report

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// unitHeaderJSON exposes the raw NAL/OBU header fields, available even
// for undecomposed units.
func unitHeaderJSON(u *cbs.Unit) map[string]any {
	switch u.Content.(type) {
	default:
	}
	h := map[string]any{}
	switch {
	case u.NuhLayerID != 0 || u.TemporalID != 0 || u.SpatialID != 0:
		h["nuh_layer_id"] = u.NuhLayerID
		h["temporal_id"] = u.TemporalID
		if u.SpatialID != 0 {
			h["spatial_id"] = u.SpatialID
		}
	default:
		h["nal_ref_idc"] = u.NalRefIDC
	}
	h["type"] = u.Type
	return h
}

var h264SliceTypeNames = [10]string{"P", "B", "I", "SP", "SI", "P", "B", "I", "SP", "SI"}

// summarize builds the per-unit summary for known content types.
func summarize(u *cbs.Unit) map[string]any {
	switch c := u.Content.(type) {
	case *cbs.H264RawSPS:
		return h264SPSSummary(c)
	case *cbs.H264RawPPS:
		return h264PPSSummary(c)
	case *cbs.H264RawSlice:
		return h264SliceSummary(&c.Header)
	case *cbs.H264RawSEI:
		return map[string]any{"messages": seiListSummary(&c.MessageList)}
	case *cbs.H264RawAUD:
		return map[string]any{"primary_pic_type": c.PrimaryPicType}
	case *cbs.H264RawFiller:
		return map[string]any{"filler_size": c.FillerSize}
	}
	return nil
}

func h264SPSSummary(sps *cbs.H264RawSPS) map[string]any {
	// Rec. ITU-T H.264 7.4.2.1.1: derived frame dimensions with cropping.
	width := (int(sps.PicWidthInMbsMinus1) + 1) * 16
	height := (int(sps.PicHeightInMapUnitsMinus1) + 1) * 16 * (2 - int(sps.FrameMbsOnlyFlag))
	if sps.FrameCroppingFlag != 0 {
		cropX, cropY := 1, 1
		switch sps.ChromaFormatIdc {
		case 1:
			cropX, cropY = 2, 2
		case 2:
			cropX, cropY = 2, 1
		}
		if sps.SeparateColourPlaneFlag == 0 && sps.ChromaFormatIdc == 0 {
			cropX, cropY = 1, 1
		}
		cropY *= 2 - int(sps.FrameMbsOnlyFlag)
		width -= cropX * (int(sps.FrameCropLeftOffset) + int(sps.FrameCropRightOffset))
		height -= cropY * (int(sps.FrameCropTopOffset) + int(sps.FrameCropBottomOffset))
	}

	s := map[string]any{
		"sps_id":             sps.SeqParameterSetID,
		"profile_idc":        sps.ProfileIdc,
		"level_idc":          sps.LevelIdc,
		"width":              width,
		"height":             height,
		"chroma_format_idc":  sps.ChromaFormatIdc,
		"bit_depth_luma":     sps.BitDepthLumaMinus8 + 8,
		"bit_depth_chroma":   sps.BitDepthChromaMinus8 + 8,
		"frame_mbs_only":     sps.FrameMbsOnlyFlag != 0,
		"max_num_ref_frames": sps.MaxNumRefFrames,
		"pic_order_cnt_type": sps.PicOrderCntType,
		"log2_max_frame_num": sps.Log2MaxFrameNumMinus4 + 4,
	}
	if sps.VuiParametersPresentFlag != 0 {
		vui := map[string]any{}
		v := &sps.Vui
		if v.TimingInfoPresentFlag != 0 {
			vui["timing"] = [2]uint32{v.NumUnitsInTick, v.TimeScale}
			vui["fixed_frame_rate"] = v.FixedFrameRateFlag != 0
		}
		vui["hrd"] = v.NalHrdParametersPresentFlag != 0 || v.VclHrdParametersPresentFlag != 0
		vui["pic_struct_present"] = v.PicStructPresentFlag != 0
		if v.VideoSignalTypePresentFlag != 0 {
			vui["colour"] = map[string]any{
				"primaries":  v.ColourPrimaries,
				"transfer":   v.TransferCharacteristics,
				"matrix":     v.MatrixCoefficients,
				"full_range": v.VideoFullRangeFlag != 0,
			}
		}
		if v.AspectRatioInfoPresentFlag != 0 {
			vui["aspect_ratio_idc"] = v.AspectRatioIdc
			if v.AspectRatioIdc == 255 {
				vui["sar"] = [2]uint16{v.SarWidth, v.SarHeight}
			}
		}
		s["vui"] = vui
	}
	return s
}

func h264PPSSummary(pps *cbs.H264RawPPS) map[string]any {
	entropy := "cavlc"
	if pps.EntropyCodingModeFlag != 0 {
		entropy = "cabac"
	}
	return map[string]any{
		"pps_id":                pps.PicParameterSetID,
		"sps_id":                pps.SeqParameterSetID,
		"entropy_coding":        entropy,
		"weighted_pred":         pps.WeightedPredFlag != 0,
		"weighted_bipred_idc":   pps.WeightedBipredIdc,
		"init_qp":               26 + int(pps.PicInitQpMinus26),
		"deblocking_control":    pps.DeblockingFilterControlPresentFlag != 0,
		"transform_8x8":         pps.Transform8x8ModeFlag != 0,
		"redundant_pic_present": pps.RedundantPicCntPresentFlag != 0,
		"num_slice_groups":      pps.NumSliceGroupsMinus1 + 1,
	}
}

func h264SliceSummary(sh *cbs.H264RawSliceHeader) map[string]any {
	st := "?"
	if sh.SliceType < 10 {
		st = h264SliceTypeNames[sh.SliceType]
	}
	s := map[string]any{
		"slice_type":       st,
		"slice_type_value": sh.SliceType,
		"first_mb":         sh.FirstMbInSlice,
		"pps_id":           sh.PicParameterSetID,
		"frame_num":        sh.FrameNum,
		"slice_qp_delta":   sh.SliceQpDelta,
	}
	if sh.NalUnitHeader.NalUnitType == 5 {
		s["idr_pic_id"] = sh.IdrPicID
	}
	if sh.FieldPicFlag != 0 {
		s["field"] = cond(sh.BottomFieldFlag != 0, "bottom", "top")
	}
	s["pic_order_cnt_lsb"] = sh.PicOrderCntLsb
	if sh.SliceType%5 == 0 || sh.SliceType%5 == 1 || sh.SliceType%5 == 3 {
		s["num_ref_idx_l0"] = sh.NumRefIdxL0ActiveMinus1 + 1
	}
	if sh.SliceType%5 == 1 {
		s["num_ref_idx_l1"] = sh.NumRefIdxL1ActiveMinus1 + 1
	}
	return s
}

// seiListSummary flattens an SEI message list into an inventory with
// decoded fields for the common payload types.
func seiListSummary(list *cbs.SEIRawMessageList) []map[string]any {
	out := make([]map[string]any, 0, len(list.Messages))
	for i := range list.Messages {
		m := &list.Messages[i]
		e := map[string]any{
			"payload_type": m.PayloadType,
			"payload_size": m.PayloadSize,
			"name":         seiTypeName(m.PayloadType),
		}
		switch p := m.Payload.(type) {
		case *cbs.SEIRawUserDataUnregistered:
			e["uuid"] = fmt.Sprintf("%x-%x-%x-%x-%x",
				p.UuidIsoIec11578[0:4], p.UuidIsoIec11578[4:6],
				p.UuidIsoIec11578[6:8], p.UuidIsoIec11578[8:10],
				p.UuidIsoIec11578[10:16])
			e["text"] = printablePrefix(p.Data, 200)
		case *cbs.SEIRawUserDataRegistered:
			e["country_code"] = p.ItuTT35CountryCode
			e["data_length"] = len(p.Data)
		case *cbs.SEIRawMasteringDisplayColourVolume:
			e["display_primaries_x"] = p.DisplayPrimariesX
			e["display_primaries_y"] = p.DisplayPrimariesY
			e["white_point"] = [2]uint16{p.WhitePointX, p.WhitePointY}
			e["max_luminance"] = p.MaxDisplayMasteringLuminance
			e["min_luminance"] = p.MinDisplayMasteringLuminance
		case *cbs.SEIRawContentLightLevelInfo:
			e["max_content_light_level"] = p.MaxContentLightLevel
			e["max_pic_average_light_level"] = p.MaxPicAverageLightLevel
		case *cbs.H264RawSEIRecoveryPoint:
			e["recovery_frame_cnt"] = p.RecoveryFrameCnt
			e["exact_match"] = p.ExactMatchFlag != 0
			e["broken_link"] = p.BrokenLinkFlag != 0
		case *cbs.H264RawSEIPicTiming:
			e["pic_struct"] = p.PicStruct
			e["cpb_removal_delay"] = p.CpbRemovalDelay
			e["dpb_output_delay"] = p.DpbOutputDelay
		case *cbs.H264RawSEIBufferingPeriod:
			e["sps_id"] = p.SeqParameterSetID
		}
		out = append(out, e)
	}
	return out
}

// seiTypeName gives the human name for the SEI payload types the report
// knows about; others get "type_N".
func seiTypeName(t uint32) string {
	names := map[uint32]string{
		0: "buffering_period", 1: "pic_timing", 2: "pan_scan_rect",
		3: "filler_payload", 4: "user_data_registered_itu_t_t35",
		5: "user_data_unregistered", 6: "recovery_point",
		19: "film_grain_characteristics", 45: "frame_packing_arrangement",
		47: "display_orientation", 129: "active_parameter_sets",
		132: "decoded_picture_hash", 136: "time_code",
		137: "mastering_display_colour_volume",
		144: "content_light_level_info",
		147: "alternative_transfer_characteristics",
		148: "ambient_viewing_environment", 165: "alpha_channel_info",
		176: "three_dimensional_reference_displays_info",
	}
	if n, ok := names[t]; ok {
		return n
	}
	return fmt.Sprintf("type_%d", t)
}

func printablePrefix(b []byte, n int) string {
	if len(b) > n {
		b = b[:n]
	}
	out := make([]byte, 0, len(b))
	for _, c := range b {
		if c >= 0x20 && c < 0x7f {
			out = append(out, c)
		} else if c == 0 {
			break
		} else {
			out = append(out, '.')
		}
	}
	return string(out)
}

func cond[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}
