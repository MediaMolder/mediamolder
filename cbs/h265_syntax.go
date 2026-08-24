// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Line-for-line port of libavcodec/cbs_h265_syntax_template.c (READ side).
// Element names, ranges and control flow mirror the C template exactly; see
// that file for the specification references.

package cbs

import "math"

// avLog2 is av_log2 (note av_log2(0) == 0, unlike log2u32).
func avLog2(v uint32) int { return log2u32(v | 1) }

func (h *H265Context) rbspTrailingBits(r *Reader) {
	fixed(r, 1, "rbsp_stop_one_bit", 1)
	for r.byteAlignment() != 0 {
		fixed(r, 1, "rbsp_alignment_zero_bit", 0)
	}
}

func (h *H265Context) nalUnitHeader(r *Reader, current *H265RawNALUnitHeader, expectedNALUnitType int) {
	fixed(r, 1, "forbidden_zero_bit", 0)

	if expectedNALUnitType >= 0 {
		u(r, 6, "nal_unit_type", &current.NalUnitType,
			uint32(expectedNALUnitType), uint32(expectedNALUnitType))
	} else {
		ub(r, 6, "nal_unit_type", &current.NalUnitType)
	}

	u(r, 6, "nuh_layer_id", &current.NuhLayerID, 0, 62)
	u(r, 3, "nuh_temporal_id_plus1", &current.NuhTemporalIDPlus1, 1, 7)
}

func (h *H265Context) byteAlignment(r *Reader) {
	fixed(r, 1, "alignment_bit_equal_to_one", 1)
	for r.byteAlignment() != 0 {
		fixed(r, 1, "alignment_bit_equal_to_zero", 0)
	}
}

func (h *H265Context) extensionData(r *Reader, current *H265RawExtensionData) {
	start := *r
	k := 0
	for ; r.moreRBSPData(); k++ {
		r.br.skipBits(1)
	}
	current.BitLength = k
	if k > 0 {
		*r = start
		current.Data = make([]uint8, (current.BitLength+7)/8)
		for k = 0; k < current.BitLength; k++ {
			bit := readUnsignedRaw(r, 1, "extension_data", nil, 0, 1)
			current.Data[k/8] |= uint8(bit) << (7 - k%8)
		}
	}
}

func (h *H265Context) profileTierLevel(r *Reader, current *H265RawProfileTierLevel,
	profilePresentFlag, maxNumSubLayersMinus1 int) {
	if profilePresentFlag != 0 {
		u(r, 2, "general_profile_space", &current.GeneralProfileSpace, 0, 0)
		flag(r, "general_tier_flag", &current.GeneralTierFlag)
		ub(r, 5, "general_profile_idc", &current.GeneralProfileIdc)

		for j := 0; j < 32; j++ {
			flags(r, "general_profile_compatibility_flag[j]", &current.GeneralProfileCompatibilityFlag[j], j)
		}

		flag(r, "general_progressive_source_flag", &current.GeneralProgressiveSourceFlag)
		flag(r, "general_interlaced_source_flag", &current.GeneralInterlacedSourceFlag)
		flag(r, "general_non_packed_constraint_flag", &current.GeneralNonPackedConstraintFlag)
		flag(r, "general_frame_only_constraint_flag", &current.GeneralFrameOnlyConstraintFlag)

		profileCompatible := func(x int) bool {
			return int(current.GeneralProfileIdc) == x ||
				current.GeneralProfileCompatibilityFlag[x] != 0
		}
		if profileCompatible(4) || profileCompatible(5) ||
			profileCompatible(6) || profileCompatible(7) ||
			profileCompatible(8) || profileCompatible(9) ||
			profileCompatible(10) || profileCompatible(11) {
			flag(r, "general_max_12bit_constraint_flag", &current.GeneralMax12bitConstraintFlag)
			flag(r, "general_max_10bit_constraint_flag", &current.GeneralMax10bitConstraintFlag)
			flag(r, "general_max_8bit_constraint_flag", &current.GeneralMax8bitConstraintFlag)
			flag(r, "general_max_422chroma_constraint_flag", &current.GeneralMax422chromaConstraintFlag)
			flag(r, "general_max_420chroma_constraint_flag", &current.GeneralMax420chromaConstraintFlag)
			flag(r, "general_max_monochrome_constraint_flag", &current.GeneralMaxMonochromeConstraintFlag)
			flag(r, "general_intra_constraint_flag", &current.GeneralIntraConstraintFlag)
			flag(r, "general_one_picture_only_constraint_flag", &current.GeneralOnePictureOnlyConstraintFlag)
			flag(r, "general_lower_bit_rate_constraint_flag", &current.GeneralLowerBitRateConstraintFlag)

			if profileCompatible(5) || profileCompatible(9) ||
				profileCompatible(10) || profileCompatible(11) {
				flag(r, "general_max_14bit_constraint_flag", &current.GeneralMax14bitConstraintFlag)
				fixed(r, 24, "general_reserved_zero_33bits", 0)
				fixed(r, 9, "general_reserved_zero_33bits", 0)
			} else {
				fixed(r, 24, "general_reserved_zero_34bits", 0)
				fixed(r, 10, "general_reserved_zero_34bits", 0)
			}
		} else if profileCompatible(2) {
			fixed(r, 7, "general_reserved_zero_7bits", 0)
			flag(r, "general_one_picture_only_constraint_flag", &current.GeneralOnePictureOnlyConstraintFlag)
			fixed(r, 24, "general_reserved_zero_35bits", 0)
			fixed(r, 11, "general_reserved_zero_35bits", 0)
		} else {
			fixed(r, 24, "general_reserved_zero_43bits", 0)
			fixed(r, 19, "general_reserved_zero_43bits", 0)
		}

		if profileCompatible(1) || profileCompatible(2) ||
			profileCompatible(3) || profileCompatible(4) ||
			profileCompatible(5) || profileCompatible(9) ||
			profileCompatible(11) {
			flag(r, "general_inbld_flag", &current.GeneralInbldFlag)
		} else {
			fixed(r, 1, "general_reserved_zero_bit", 0)
		}
	}

	ub(r, 8, "general_level_idc", &current.GeneralLevelIdc)

	for i := 0; i < maxNumSubLayersMinus1; i++ {
		flags(r, "sub_layer_profile_present_flag[i]", &current.SubLayerProfilePresentFlag[i], i)
		flags(r, "sub_layer_level_present_flag[i]", &current.SubLayerLevelPresentFlag[i], i)
	}

	if maxNumSubLayersMinus1 > 0 {
		for i := maxNumSubLayersMinus1; i < 8; i++ {
			fixed(r, 2, "reserved_zero_2bits", 0)
		}
	}

	for i := 0; i < maxNumSubLayersMinus1; i++ {
		if current.SubLayerProfilePresentFlag[i] != 0 {
			us(r, 2, "sub_layer_profile_space[i]", &current.SubLayerProfileSpace[i], 0, 0, i)
			flags(r, "sub_layer_tier_flag[i]", &current.SubLayerTierFlag[i], i)
			ubs(r, 5, "sub_layer_profile_idc[i]", &current.SubLayerProfileIdc[i], i)

			for j := 0; j < 32; j++ {
				flags(r, "sub_layer_profile_compatibility_flag[i][j]",
					&current.SubLayerProfileCompatibilityFlag[i][j], i, j)
			}

			flags(r, "sub_layer_progressive_source_flag[i]", &current.SubLayerProgressiveSourceFlag[i], i)
			flags(r, "sub_layer_interlaced_source_flag[i]", &current.SubLayerInterlacedSourceFlag[i], i)
			flags(r, "sub_layer_non_packed_constraint_flag[i]", &current.SubLayerNonPackedConstraintFlag[i], i)
			flags(r, "sub_layer_frame_only_constraint_flag[i]", &current.SubLayerFrameOnlyConstraintFlag[i], i)

			profileCompatible := func(x int) bool {
				return int(current.SubLayerProfileIdc[i]) == x ||
					current.SubLayerProfileCompatibilityFlag[i][x] != 0
			}
			if profileCompatible(4) || profileCompatible(5) ||
				profileCompatible(6) || profileCompatible(7) ||
				profileCompatible(8) || profileCompatible(9) ||
				profileCompatible(10) || profileCompatible(11) {
				flags(r, "sub_layer_max_12bit_constraint_flag[i]", &current.SubLayerMax12bitConstraintFlag[i], i)
				flags(r, "sub_layer_max_10bit_constraint_flag[i]", &current.SubLayerMax10bitConstraintFlag[i], i)
				flags(r, "sub_layer_max_8bit_constraint_flag[i]", &current.SubLayerMax8bitConstraintFlag[i], i)
				flags(r, "sub_layer_max_422chroma_constraint_flag[i]", &current.SubLayerMax422chromaConstraintFlag[i], i)
				flags(r, "sub_layer_max_420chroma_constraint_flag[i]", &current.SubLayerMax420chromaConstraintFlag[i], i)
				flags(r, "sub_layer_max_monochrome_constraint_flag[i]", &current.SubLayerMaxMonochromeConstraintFlag[i], i)
				flags(r, "sub_layer_intra_constraint_flag[i]", &current.SubLayerIntraConstraintFlag[i], i)
				flags(r, "sub_layer_one_picture_only_constraint_flag[i]", &current.SubLayerOnePictureOnlyConstraintFlag[i], i)
				flags(r, "sub_layer_lower_bit_rate_constraint_flag[i]", &current.SubLayerLowerBitRateConstraintFlag[i], i)

				if profileCompatible(5) || profileCompatible(9) ||
					profileCompatible(10) || profileCompatible(11) {
					flags(r, "sub_layer_max_14bit_constraint_flag[i]", &current.SubLayerMax14bitConstraintFlag[i], i)
					fixed(r, 24, "sub_layer_reserved_zero_33bits", 0)
					fixed(r, 9, "sub_layer_reserved_zero_33bits", 0)
				} else {
					fixed(r, 24, "sub_layer_reserved_zero_34bits", 0)
					fixed(r, 10, "sub_layer_reserved_zero_34bits", 0)
				}
			} else if profileCompatible(2) {
				fixed(r, 7, "sub_layer_reserved_zero_7bits", 0)
				flags(r, "sub_layer_one_picture_only_constraint_flag[i]", &current.SubLayerOnePictureOnlyConstraintFlag[i], i)
				fixed(r, 24, "sub_layer_reserved_zero_43bits", 0)
				fixed(r, 11, "sub_layer_reserved_zero_43bits", 0)
			} else {
				fixed(r, 24, "sub_layer_reserved_zero_43bits", 0)
				fixed(r, 19, "sub_layer_reserved_zero_43bits", 0)
			}

			if profileCompatible(1) || profileCompatible(2) ||
				profileCompatible(3) || profileCompatible(4) ||
				profileCompatible(5) || profileCompatible(9) ||
				profileCompatible(11) {
				flags(r, "sub_layer_inbld_flag[i]", &current.SubLayerInbldFlag[i], i)
			} else {
				fixed(r, 1, "sub_layer_reserved_zero_bit", 0)
			}
		}
		if current.SubLayerLevelPresentFlag[i] != 0 {
			ubs(r, 8, "sub_layer_level_idc[i]", &current.SubLayerLevelIdc[i], i)
		}
	}
}

func (h *H265Context) subLayerHRDParameters(r *Reader, hrd *H265RawHRDParameters,
	nal, subLayerID int) {
	var current *H265RawSubLayerHRDParameters
	if nal != 0 {
		current = &hrd.NalSubLayerHrdParameters[subLayerID]
	} else {
		current = &hrd.VclSubLayerHrdParameters[subLayerID]
	}

	for i := 0; i <= int(hrd.CpbCntMinus1[subLayerID]); i++ {
		ues(r, "bit_rate_value_minus1[i]", &current.BitRateValueMinus1[i], 0, math.MaxUint32-1, i)
		ues(r, "cpb_size_value_minus1[i]", &current.CpbSizeValueMinus1[i], 0, math.MaxUint32-1, i)
		if hrd.SubPicHrdParamsPresentFlag != 0 {
			ues(r, "cpb_size_du_value_minus1[i]", &current.CpbSizeDuValueMinus1[i], 0, math.MaxUint32-1, i)
			ues(r, "bit_rate_du_value_minus1[i]", &current.BitRateDuValueMinus1[i], 0, math.MaxUint32-1, i)
		}
		flags(r, "cbr_flag[i]", &current.CbrFlag[i], i)
	}
}

func (h *H265Context) hrdParameters(r *Reader, current *H265RawHRDParameters,
	commonInfPresentFlag, maxNumSubLayersMinus1 int) {
	if commonInfPresentFlag != 0 {
		flag(r, "nal_hrd_parameters_present_flag", &current.NalHrdParametersPresentFlag)
		flag(r, "vcl_hrd_parameters_present_flag", &current.VclHrdParametersPresentFlag)

		if current.NalHrdParametersPresentFlag != 0 ||
			current.VclHrdParametersPresentFlag != 0 {
			flag(r, "sub_pic_hrd_params_present_flag", &current.SubPicHrdParamsPresentFlag)
			if current.SubPicHrdParamsPresentFlag != 0 {
				ub(r, 8, "tick_divisor_minus2", &current.TickDivisorMinus2)
				ub(r, 5, "du_cpb_removal_delay_increment_length_minus1", &current.DuCpbRemovalDelayIncrementLengthMinus1)
				flag(r, "sub_pic_cpb_params_in_pic_timing_sei_flag", &current.SubPicCpbParamsInPicTimingSeiFlag)
				ub(r, 5, "dpb_output_delay_du_length_minus1", &current.DpbOutputDelayDuLengthMinus1)
			}

			ub(r, 4, "bit_rate_scale", &current.BitRateScale)
			ub(r, 4, "cpb_size_scale", &current.CpbSizeScale)
			if current.SubPicHrdParamsPresentFlag != 0 {
				ub(r, 4, "cpb_size_du_scale", &current.CpbSizeDuScale)
			}

			ub(r, 5, "initial_cpb_removal_delay_length_minus1", &current.InitialCpbRemovalDelayLengthMinus1)
			ub(r, 5, "au_cpb_removal_delay_length_minus1", &current.AuCpbRemovalDelayLengthMinus1)
			ub(r, 5, "dpb_output_delay_length_minus1", &current.DpbOutputDelayLengthMinus1)
		} else {
			infer(&current.SubPicHrdParamsPresentFlag, 0)

			infer(&current.InitialCpbRemovalDelayLengthMinus1, 23)
			infer(&current.AuCpbRemovalDelayLengthMinus1, 23)
			infer(&current.DpbOutputDelayLengthMinus1, 23)
		}
	}

	for i := 0; i <= maxNumSubLayersMinus1; i++ {
		flags(r, "fixed_pic_rate_general_flag[i]", &current.FixedPicRateGeneralFlag[i], i)

		if current.FixedPicRateGeneralFlag[i] == 0 {
			flags(r, "fixed_pic_rate_within_cvs_flag[i]", &current.FixedPicRateWithinCvsFlag[i], i)
		} else {
			infer(&current.FixedPicRateWithinCvsFlag[i], 1)
		}

		if current.FixedPicRateWithinCvsFlag[i] != 0 {
			ues(r, "elemental_duration_in_tc_minus1[i]", &current.ElementalDurationInTcMinus1[i], 0, 2047, i)
			infer(&current.LowDelayHrdFlag[i], 0)
		} else {
			flags(r, "low_delay_hrd_flag[i]", &current.LowDelayHrdFlag[i], i)
		}

		if current.LowDelayHrdFlag[i] == 0 {
			ues(r, "cpb_cnt_minus1[i]", &current.CpbCntMinus1[i], 0, 31, i)
		} else {
			infer(&current.CpbCntMinus1[i], 0)
		}

		if current.NalHrdParametersPresentFlag != 0 {
			h.subLayerHRDParameters(r, current, 0, i)
		}
		if current.VclHrdParametersPresentFlag != 0 {
			h.subLayerHRDParameters(r, current, 1, i)
		}
	}
}

func (h *H265Context) vuiParameters(r *Reader, current *H265RawVUI, sps *H265RawSPS) {
	flag(r, "aspect_ratio_info_present_flag", &current.AspectRatioInfoPresentFlag)
	if current.AspectRatioInfoPresentFlag != 0 {
		ub(r, 8, "aspect_ratio_idc", &current.AspectRatioIdc)
		if current.AspectRatioIdc == 255 {
			ub(r, 16, "sar_width", &current.SarWidth)
			ub(r, 16, "sar_height", &current.SarHeight)
		}
	} else {
		infer(&current.AspectRatioIdc, 0)
	}

	flag(r, "overscan_info_present_flag", &current.OverscanInfoPresentFlag)
	if current.OverscanInfoPresentFlag != 0 {
		flag(r, "overscan_appropriate_flag", &current.OverscanAppropriateFlag)
	}

	flag(r, "video_signal_type_present_flag", &current.VideoSignalTypePresentFlag)
	if current.VideoSignalTypePresentFlag != 0 {
		ub(r, 3, "video_format", &current.VideoFormat)
		flag(r, "video_full_range_flag", &current.VideoFullRangeFlag)
		flag(r, "colour_description_present_flag", &current.ColourDescriptionPresentFlag)
		if current.ColourDescriptionPresentFlag != 0 {
			ub(r, 8, "colour_primaries", &current.ColourPrimaries)
			ub(r, 8, "transfer_characteristics", &current.TransferCharacteristics)
			ub(r, 8, "matrix_coefficients", &current.MatrixCoefficients)
		} else {
			infer(&current.ColourPrimaries, 2)
			infer(&current.TransferCharacteristics, 2)
			infer(&current.MatrixCoefficients, 2)
		}
	} else {
		infer(&current.VideoFormat, 5)
		infer(&current.VideoFullRangeFlag, 0)
		infer(&current.ColourPrimaries, 2)
		infer(&current.TransferCharacteristics, 2)
		infer(&current.MatrixCoefficients, 2)
	}

	flag(r, "chroma_loc_info_present_flag", &current.ChromaLocInfoPresentFlag)
	if current.ChromaLocInfoPresentFlag != 0 {
		ue(r, "chroma_sample_loc_type_top_field", &current.ChromaSampleLocTypeTopField, 0, 5)
		ue(r, "chroma_sample_loc_type_bottom_field", &current.ChromaSampleLocTypeBottomField, 0, 5)
	} else {
		infer(&current.ChromaSampleLocTypeTopField, 0)
		infer(&current.ChromaSampleLocTypeBottomField, 0)
	}

	flag(r, "neutral_chroma_indication_flag", &current.NeutralChromaIndicationFlag)
	flag(r, "field_seq_flag", &current.FieldSeqFlag)
	flag(r, "frame_field_info_present_flag", &current.FrameFieldInfoPresentFlag)

	flag(r, "default_display_window_flag", &current.DefaultDisplayWindowFlag)
	if current.DefaultDisplayWindowFlag != 0 {
		ue(r, "def_disp_win_left_offset", &current.DefDispWinLeftOffset, 0, 16384)
		ue(r, "def_disp_win_right_offset", &current.DefDispWinRightOffset, 0, 16384)
		ue(r, "def_disp_win_top_offset", &current.DefDispWinTopOffset, 0, 16384)
		ue(r, "def_disp_win_bottom_offset", &current.DefDispWinBottomOffset, 0, 16384)
	}

	flag(r, "vui_timing_info_present_flag", &current.VuiTimingInfoPresentFlag)
	if current.VuiTimingInfoPresentFlag != 0 {
		u(r, 32, "vui_num_units_in_tick", &current.VuiNumUnitsInTick, 1, math.MaxUint32)
		u(r, 32, "vui_time_scale", &current.VuiTimeScale, 1, math.MaxUint32)
		flag(r, "vui_poc_proportional_to_timing_flag", &current.VuiPocProportionalToTimingFlag)
		if current.VuiPocProportionalToTimingFlag != 0 {
			ue(r, "vui_num_ticks_poc_diff_one_minus1", &current.VuiNumTicksPocDiffOneMinus1, 0, math.MaxUint32-1)
		}

		flag(r, "vui_hrd_parameters_present_flag", &current.VuiHrdParametersPresentFlag)
		if current.VuiHrdParametersPresentFlag != 0 {
			h.hrdParameters(r, &current.HrdParameters,
				1, int(sps.SpsMaxSubLayersMinus1))
		}
	}

	flag(r, "bitstream_restriction_flag", &current.BitstreamRestrictionFlag)
	if current.BitstreamRestrictionFlag != 0 {
		flag(r, "tiles_fixed_structure_flag", &current.TilesFixedStructureFlag)
		flag(r, "motion_vectors_over_pic_boundaries_flag", &current.MotionVectorsOverPicBoundariesFlag)
		flag(r, "restricted_ref_pic_lists_flag", &current.RestrictedRefPicListsFlag)
		ue(r, "min_spatial_segmentation_idc", &current.MinSpatialSegmentationIdc, 0, 4095)
		ue(r, "max_bytes_per_pic_denom", &current.MaxBytesPerPicDenom, 0, 16)
		ue(r, "max_bits_per_min_cu_denom", &current.MaxBitsPerMinCuDenom, 0, 16)
		ue(r, "log2_max_mv_length_horizontal", &current.Log2MaxMvLengthHorizontal, 0, 16)
		ue(r, "log2_max_mv_length_vertical", &current.Log2MaxMvLengthVertical, 0, 16)
	} else {
		infer(&current.TilesFixedStructureFlag, 0)
		infer(&current.MotionVectorsOverPicBoundariesFlag, 1)
		infer(&current.MinSpatialSegmentationIdc, 0)
		infer(&current.MaxBytesPerPicDenom, 2)
		infer(&current.MaxBitsPerMinCuDenom, 1)
		infer(&current.Log2MaxMvLengthHorizontal, 15)
		infer(&current.Log2MaxMvLengthVertical, 15)
	}
}

func (h *H265Context) readVPS(r *Reader, current *H265RawVPS) {
	r.header("Video Parameter Set")

	h.nalUnitHeader(r, &current.NalUnitHeader, hevcNALVPS)

	ub(r, 4, "vps_video_parameter_set_id", &current.VpsVideoParameterSetID)

	flag(r, "vps_base_layer_internal_flag", &current.VpsBaseLayerInternalFlag)
	flag(r, "vps_base_layer_available_flag", &current.VpsBaseLayerAvailableFlag)
	u(r, 6, "vps_max_layers_minus1", &current.VpsMaxLayersMinus1, 0, hevcMaxLayers-1)
	u(r, 3, "vps_max_sub_layers_minus1", &current.VpsMaxSubLayersMinus1, 0, hevcMaxSubLayers-1)
	flag(r, "vps_temporal_id_nesting_flag", &current.VpsTemporalIDNestingFlag)

	if current.VpsMaxSubLayersMinus1 == 0 &&
		current.VpsTemporalIDNestingFlag != 1 {
		r.diag(LevelError, "Invalid stream: vps_temporal_id_nesting_flag must be 1 if vps_max_sub_layers_minus1 is 0.")
		r.fail(ErrInvalidData)
	}

	fixed(r, 16, "vps_reserved_0xffff_16bits", 0xffff)

	h.profileTierLevel(r, &current.ProfileTierLevel,
		1, int(current.VpsMaxSubLayersMinus1))

	flag(r, "vps_sub_layer_ordering_info_present_flag", &current.VpsSubLayerOrderingInfoPresentFlag)
	for i := cond(current.VpsSubLayerOrderingInfoPresentFlag != 0,
		0, int(current.VpsMaxSubLayersMinus1)); i <= int(current.VpsMaxSubLayersMinus1); i++ {
		ues(r, "vps_max_dec_pic_buffering_minus1[i]",
			&current.VpsMaxDecPicBufferingMinus1[i], 0, hevcMaxDPBSize-1, i)
		ues(r, "vps_max_num_reorder_pics[i]",
			&current.VpsMaxNumReorderPics[i], 0, uint32(current.VpsMaxDecPicBufferingMinus1[i]), i)
		ues(r, "vps_max_latency_increase_plus1[i]",
			&current.VpsMaxLatencyIncreasePlus1[i], 0, math.MaxUint32-1, i)
	}
	if current.VpsSubLayerOrderingInfoPresentFlag == 0 {
		for i := 0; i < int(current.VpsMaxSubLayersMinus1); i++ {
			infer(&current.VpsMaxDecPicBufferingMinus1[i],
				current.VpsMaxDecPicBufferingMinus1[current.VpsMaxSubLayersMinus1])
			infer(&current.VpsMaxNumReorderPics[i],
				current.VpsMaxNumReorderPics[current.VpsMaxSubLayersMinus1])
			infer(&current.VpsMaxLatencyIncreasePlus1[i],
				current.VpsMaxLatencyIncreasePlus1[current.VpsMaxSubLayersMinus1])
		}
	}

	u(r, 6, "vps_max_layer_id", &current.VpsMaxLayerID, 0, hevcMaxLayers-1)
	ue(r, "vps_num_layer_sets_minus1", &current.VpsNumLayerSetsMinus1, 0, hevcMaxLayerSets-1)
	for i := 1; i <= int(current.VpsNumLayerSetsMinus1); i++ {
		for j := 0; j <= int(current.VpsMaxLayerID); j++ {
			flags(r, "layer_id_included_flag[i][j]", &current.LayerIDIncludedFlag[i][j], i, j)
		}
	}
	for j := 0; j <= int(current.VpsMaxLayerID); j++ {
		infer(&current.LayerIDIncludedFlag[0][j], b2u8(j == 0))
	}

	flag(r, "vps_timing_info_present_flag", &current.VpsTimingInfoPresentFlag)
	if current.VpsTimingInfoPresentFlag != 0 {
		u(r, 32, "vps_num_units_in_tick", &current.VpsNumUnitsInTick, 1, math.MaxUint32)
		u(r, 32, "vps_time_scale", &current.VpsTimeScale, 1, math.MaxUint32)
		flag(r, "vps_poc_proportional_to_timing_flag", &current.VpsPocProportionalToTimingFlag)
		if current.VpsPocProportionalToTimingFlag != 0 {
			ue(r, "vps_num_ticks_poc_diff_one_minus1", &current.VpsNumTicksPocDiffOneMinus1, 0, math.MaxUint32-1)
		}
		ue(r, "vps_num_hrd_parameters", &current.VpsNumHrdParameters, 0, uint32(current.VpsNumLayerSetsMinus1)+1)
		for i := 0; i < int(current.VpsNumHrdParameters); i++ {
			ues(r, "hrd_layer_set_idx[i]", &current.HrdLayerSetIdx[i],
				cond[uint32](current.VpsBaseLayerInternalFlag != 0, 0, 1),
				uint32(current.VpsNumLayerSetsMinus1), i)
			if i > 0 {
				flags(r, "cprms_present_flag[i]", &current.CprmsPresentFlag[i], i)
			} else {
				infer(&current.CprmsPresentFlag[0], 1)
			}

			h.hrdParameters(r, &current.HrdParameters[i],
				int(current.CprmsPresentFlag[i]),
				int(current.VpsMaxSubLayersMinus1))
		}
	}

	flag(r, "vps_extension_flag", &current.VpsExtensionFlag)
	if current.VpsExtensionFlag != 0 {
		h.extensionData(r, &current.ExtensionData)
	}

	h.rbspTrailingBits(r)
}

func (h *H265Context) stRefPicSet(r *Reader, current *H265RawSTRefPicSet,
	stRpsIdx int, sps *H265RawSPS) {
	if stRpsIdx != 0 {
		flag(r, "inter_ref_pic_set_prediction_flag", &current.InterRefPicSetPredictionFlag)
	} else {
		infer(&current.InterRefPicSetPredictionFlag, 0)
	}

	if current.InterRefPicSetPredictionFlag != 0 {
		var refDeltaPocS0, refDeltaPocS1 [hevcMaxRefs]int
		var deltaPocS0, deltaPocS1 [hevcMaxRefs]int
		var usedByCurrPicS0, usedByCurrPicS1 [hevcMaxRefs]uint8

		if stRpsIdx == int(sps.NumShortTermRefPicSets) {
			ue(r, "delta_idx_minus1", &current.DeltaIdxMinus1, 0, uint32(stRpsIdx-1))
		} else {
			infer(&current.DeltaIdxMinus1, 0)
		}

		refRpsIdx := stRpsIdx - (int(current.DeltaIdxMinus1) + 1)
		ref := &sps.StRefPicSet[refRpsIdx]
		numDeltaPocs := int(ref.NumNegativePics) + int(ref.NumPositivePics)

		flag(r, "delta_rps_sign", &current.DeltaRpsSign)
		ue(r, "abs_delta_rps_minus1", &current.AbsDeltaRpsMinus1, 0, math.MaxInt16)
		deltaRps := (1 - 2*int(current.DeltaRpsSign)) *
			(int(current.AbsDeltaRpsMinus1) + 1)

		numRefPics := 0
		for j := 0; j <= numDeltaPocs; j++ {
			flags(r, "used_by_curr_pic_flag[j]", &current.UsedByCurrPicFlag[j], j)
			if current.UsedByCurrPicFlag[j] == 0 {
				flags(r, "use_delta_flag[j]", &current.UseDeltaFlag[j], j)
			} else {
				infer(&current.UseDeltaFlag[j], 1)
			}
			if current.UseDeltaFlag[j] != 0 {
				numRefPics++
			}
		}
		if numRefPics >= hevcMaxDPBSize {
			r.diag(LevelError, "Invalid stream: short-term ref pic set %d contains too many pictures.", stRpsIdx)
			r.fail(ErrInvalidData)
		}

		// Since the stored form of an RPS here is actually the delta-step
		// form used when inter_ref_pic_set_prediction_flag is not set, we
		// need to reconstruct that here in order to be able to refer to
		// the RPS later (which is required for parsing, because we don't
		// even know what syntax elements appear without it).  Therefore,
		// this code takes the delta-step form of the reference set, turns
		// it into the delta-array form, applies the prediction process of
		// 7.4.8, converts the result back to the delta-step form, and
		// stores that as the current set for future use.  Note that the
		// inferences here mean that writers using prediction will need
		// to fill in the delta-step values correctly as well - since the
		// whole RPS prediction process is somewhat overly sophisticated,
		// this hopefully forms a useful check for them to ensure their
		// predicted form actually matches what was intended rather than
		// an onerous additional requirement.

		dPoc := 0
		for i := 0; i < int(ref.NumNegativePics); i++ {
			dPoc -= int(ref.DeltaPocS0Minus1[i]) + 1
			refDeltaPocS0[i] = dPoc
		}
		dPoc = 0
		for i := 0; i < int(ref.NumPositivePics); i++ {
			dPoc += int(ref.DeltaPocS1Minus1[i]) + 1
			refDeltaPocS1[i] = dPoc
		}

		i := 0
		for j := int(ref.NumPositivePics) - 1; j >= 0; j-- {
			dPoc := refDeltaPocS1[j] + deltaRps
			if dPoc < 0 && current.UseDeltaFlag[int(ref.NumNegativePics)+j] != 0 {
				deltaPocS0[i] = dPoc
				usedByCurrPicS0[i] =
					current.UsedByCurrPicFlag[int(ref.NumNegativePics)+j]
				i++
			}
		}
		if deltaRps < 0 && current.UseDeltaFlag[numDeltaPocs] != 0 {
			deltaPocS0[i] = deltaRps
			usedByCurrPicS0[i] =
				current.UsedByCurrPicFlag[numDeltaPocs]
			i++
		}
		for j := 0; j < int(ref.NumNegativePics); j++ {
			dPoc := refDeltaPocS0[j] + deltaRps
			if dPoc < 0 && current.UseDeltaFlag[j] != 0 {
				deltaPocS0[i] = dPoc
				usedByCurrPicS0[i] = current.UsedByCurrPicFlag[j]
				i++
			}
		}

		infer(&current.NumNegativePics, uint8(i))
		for i := 0; i < int(current.NumNegativePics); i++ {
			prev := 0
			if i != 0 {
				prev = deltaPocS0[i-1]
			}
			infer(&current.DeltaPocS0Minus1[i],
				uint16(-(deltaPocS0[i]-prev)-1))
			infer(&current.UsedByCurrPicS0Flag[i], usedByCurrPicS0[i])
		}

		i = 0
		for j := int(ref.NumNegativePics) - 1; j >= 0; j-- {
			dPoc := refDeltaPocS0[j] + deltaRps
			if dPoc > 0 && current.UseDeltaFlag[j] != 0 {
				deltaPocS1[i] = dPoc
				usedByCurrPicS1[i] = current.UsedByCurrPicFlag[j]
				i++
			}
		}
		if deltaRps > 0 && current.UseDeltaFlag[numDeltaPocs] != 0 {
			deltaPocS1[i] = deltaRps
			usedByCurrPicS1[i] =
				current.UsedByCurrPicFlag[numDeltaPocs]
			i++
		}
		for j := 0; j < int(ref.NumPositivePics); j++ {
			dPoc := refDeltaPocS1[j] + deltaRps
			if dPoc > 0 && current.UseDeltaFlag[int(ref.NumNegativePics)+j] != 0 {
				deltaPocS1[i] = dPoc
				usedByCurrPicS1[i] =
					current.UsedByCurrPicFlag[int(ref.NumNegativePics)+j]
				i++
			}
		}

		infer(&current.NumPositivePics, uint8(i))
		for i := 0; i < int(current.NumPositivePics); i++ {
			prev := 0
			if i != 0 {
				prev = deltaPocS1[i-1]
			}
			infer(&current.DeltaPocS1Minus1[i],
				uint16(deltaPocS1[i]-prev-1))
			infer(&current.UsedByCurrPicS1Flag[i], usedByCurrPicS1[i])
		}
	} else {
		ue(r, "num_negative_pics", &current.NumNegativePics, 0, 15)
		ue(r, "num_positive_pics", &current.NumPositivePics, 0, uint32(15-current.NumNegativePics))

		for i := 0; i < int(current.NumNegativePics); i++ {
			ues(r, "delta_poc_s0_minus1[i]", &current.DeltaPocS0Minus1[i], 0, math.MaxInt16, i)
			flags(r, "used_by_curr_pic_s0_flag[i]", &current.UsedByCurrPicS0Flag[i], i)
		}

		for i := 0; i < int(current.NumPositivePics); i++ {
			ues(r, "delta_poc_s1_minus1[i]", &current.DeltaPocS1Minus1[i], 0, math.MaxInt16, i)
			flags(r, "used_by_curr_pic_s1_flag[i]", &current.UsedByCurrPicS1Flag[i], i)
		}
	}
}

func (h *H265Context) scalingListData(r *Reader, current *H265RawScalingList) {
	for sizeID := 0; sizeID < 4; sizeID++ {
		for matrixID := 0; matrixID < 6; matrixID += cond(sizeID == 3, 3, 1) {
			flags(r, "scaling_list_pred_mode_flag[sizeId][matrixId]",
				&current.ScalingListPredModeFlag[sizeID][matrixID], sizeID, matrixID)
			if current.ScalingListPredModeFlag[sizeID][matrixID] == 0 {
				ues(r, "scaling_list_pred_matrix_id_delta[sizeId][matrixId]",
					&current.ScalingListPredMatrixIDDelta[sizeID][matrixID],
					0, uint32(cond(sizeID == 3, matrixID/3, matrixID)),
					sizeID, matrixID)
			} else {
				n := min(64, 1<<(4+(sizeID<<1)))
				if sizeID > 1 {
					ses(r, "scaling_list_dc_coef_minus8[sizeId - 2][matrixId]",
						&current.ScalingListDcCoefMinus8[sizeID-2][matrixID], -7, +247,
						sizeID-2, matrixID)
				}
				for i := 0; i < n; i++ {
					ses(r, "scaling_list_delta_coeff[sizeId][matrixId][i]",
						&current.ScalingListDeltaCoeff[sizeID][matrixID][i],
						-128, +127, sizeID, matrixID, i)
				}
			}
		}
	}
}

func (h *H265Context) spsRangeExtension(r *Reader, current *H265RawSPS) {
	flag(r, "transform_skip_rotation_enabled_flag", &current.TransformSkipRotationEnabledFlag)
	flag(r, "transform_skip_context_enabled_flag", &current.TransformSkipContextEnabledFlag)
	flag(r, "implicit_rdpcm_enabled_flag", &current.ImplicitRdpcmEnabledFlag)
	flag(r, "explicit_rdpcm_enabled_flag", &current.ExplicitRdpcmEnabledFlag)
	flag(r, "extended_precision_processing_flag", &current.ExtendedPrecisionProcessingFlag)
	flag(r, "intra_smoothing_disabled_flag", &current.IntraSmoothingDisabledFlag)
	flag(r, "high_precision_offsets_enabled_flag", &current.HighPrecisionOffsetsEnabledFlag)
	flag(r, "persistent_rice_adaptation_enabled_flag", &current.PersistentRiceAdaptationEnabledFlag)
	flag(r, "cabac_bypass_alignment_enabled_flag", &current.CabacBypassAlignmentEnabledFlag)
}

func (h *H265Context) spsSccExtension(r *Reader, current *H265RawSPS) {
	flag(r, "sps_curr_pic_ref_enabled_flag", &current.SpsCurrPicRefEnabledFlag)

	flag(r, "palette_mode_enabled_flag", &current.PaletteModeEnabledFlag)
	if current.PaletteModeEnabledFlag != 0 {
		ue(r, "palette_max_size", &current.PaletteMaxSize, 0, 64)
		ue(r, "delta_palette_max_predictor_size", &current.DeltaPaletteMaxPredictorSize, 0, 128)

		flag(r, "sps_palette_predictor_initializer_present_flag", &current.SpsPalettePredictorInitializerPresentFlag)
		if current.SpsPalettePredictorInitializerPresentFlag != 0 {
			ue(r, "sps_num_palette_predictor_initializer_minus1", &current.SpsNumPalettePredictorInitializerMinus1, 0, 127)
			for comp := 0; comp < cond(current.ChromaFormatIdc != 0, 3, 1); comp++ {
				bitDepth := cond(comp == 0,
					int(current.BitDepthLumaMinus8)+8,
					int(current.BitDepthChromaMinus8)+8)
				for i := 0; i <= int(current.SpsNumPalettePredictorInitializerMinus1); i++ {
					ubs(r, bitDepth, "sps_palette_predictor_initializers[comp][i]",
						&current.SpsPalettePredictorInitializers[comp][i], comp, i)
				}
			}
		}
	}

	u(r, 2, "motion_vector_resolution_control_idc", &current.MotionVectorResolutionControlIdc, 0, 2)
	flag(r, "intra_boundary_filtering_disable_flag", &current.IntraBoundaryFilteringDisableFlag)
}

func (h *H265Context) spsMultilayerExtension(r *Reader, current *H265RawSPS) {
	flag(r, "inter_view_mv_vert_constraint_flag", &current.InterViewMvVertConstraintFlag)
}

func (h *H265Context) vuiParametersDefault(r *Reader, current *H265RawVUI, sps *H265RawSPS) {
	infer(&current.AspectRatioIdc, 0)

	infer(&current.VideoFormat, 5)
	infer(&current.VideoFullRangeFlag, 0)
	infer(&current.ColourPrimaries, 2)
	infer(&current.TransferCharacteristics, 2)
	infer(&current.MatrixCoefficients, 2)

	infer(&current.ChromaSampleLocTypeTopField, 0)
	infer(&current.ChromaSampleLocTypeBottomField, 0)

	infer(&current.TilesFixedStructureFlag, 0)
	infer(&current.MotionVectorsOverPicBoundariesFlag, 1)
	infer(&current.MinSpatialSegmentationIdc, 0)
	infer(&current.MaxBytesPerPicDenom, 2)
	infer(&current.MaxBitsPerMinCuDenom, 1)
	infer(&current.Log2MaxMvLengthHorizontal, 15)
	infer(&current.Log2MaxMvLengthVertical, 15)
}

func (h *H265Context) readSPS(r *Reader, current *H265RawSPS) {
	r.header("Sequence Parameter Set")

	h.nalUnitHeader(r, &current.NalUnitHeader, hevcNALSPS)

	ub(r, 4, "sps_video_parameter_set_id", &current.SpsVideoParameterSetID)
	h.activeVPS = h.vps[current.SpsVideoParameterSetID]
	vps := h.activeVPS
	if vps == nil {
		r.diag(LevelError, "VPS id %d not available.",
			current.SpsVideoParameterSetID)
		r.fail(ErrInvalidData)
	}

	if current.NalUnitHeader.NuhLayerID == 0 {
		u(r, 3, "sps_max_sub_layers_minus1", &current.SpsMaxSubLayersMinus1,
			0, uint32(vps.VpsMaxSubLayersMinus1))
	} else {
		u(r, 3, "sps_ext_or_max_sub_layers_minus1", &current.SpsExtOrMaxSubLayersMinus1,
			0, hevcMaxSubLayers)
		infer(&current.SpsMaxSubLayersMinus1,
			cond(current.SpsExtOrMaxSubLayersMinus1 == hevcMaxSubLayers,
				vps.VpsMaxSubLayersMinus1,
				current.SpsExtOrMaxSubLayersMinus1))
	}
	multiLayerExtSpsFlag := current.NalUnitHeader.NuhLayerID != 0 &&
		current.SpsExtOrMaxSubLayersMinus1 == hevcMaxSubLayers
	if !multiLayerExtSpsFlag {
		flag(r, "sps_temporal_id_nesting_flag", &current.SpsTemporalIDNestingFlag)

		if vps.VpsTemporalIDNestingFlag != 0 &&
			current.SpsTemporalIDNestingFlag == 0 {
			r.diag(LevelError, "Invalid stream: sps_temporal_id_nesting_flag must be 1 if vps_temporal_id_nesting_flag is 1.")
			r.fail(ErrInvalidData)
		}
		if current.SpsMaxSubLayersMinus1 == 0 &&
			current.SpsTemporalIDNestingFlag != 1 {
			r.diag(LevelError, "Invalid stream: sps_temporal_id_nesting_flag must be 1 if sps_max_sub_layers_minus1 is 0.")
			r.fail(ErrInvalidData)
		}

		h.profileTierLevel(r, &current.ProfileTierLevel,
			1, int(current.SpsMaxSubLayersMinus1))
	} else {
		if current.SpsMaxSubLayersMinus1 > 0 {
			infer(&current.SpsTemporalIDNestingFlag, vps.VpsTemporalIDNestingFlag)
		} else {
			infer(&current.SpsTemporalIDNestingFlag, 1)
		}
	}

	ue(r, "sps_seq_parameter_set_id", &current.SpsSeqParameterSetID, 0, 15)

	if multiLayerExtSpsFlag {
		flag(r, "update_rep_format_flag", &current.UpdateRepFormatFlag)
		if current.UpdateRepFormatFlag != 0 {
			ub(r, 8, "sps_rep_format_idx", &current.SpsRepFormatIdx)
		}
	} else {
		ue(r, "chroma_format_idc", &current.ChromaFormatIdc, 0, 3)
		if current.ChromaFormatIdc == 3 {
			flag(r, "separate_colour_plane_flag", &current.SeparateColourPlaneFlag)
		} else {
			infer(&current.SeparateColourPlaneFlag, 0)
		}

		ue(r, "pic_width_in_luma_samples", &current.PicWidthInLumaSamples, 1, hevcMaxWidth)
		ue(r, "pic_height_in_luma_samples", &current.PicHeightInLumaSamples, 1, hevcMaxHeight)

		flag(r, "conformance_window_flag", &current.ConformanceWindowFlag)
		if current.ConformanceWindowFlag != 0 {
			ue(r, "conf_win_left_offset", &current.ConfWinLeftOffset, 0, uint32(current.PicWidthInLumaSamples))
			ue(r, "conf_win_right_offset", &current.ConfWinRightOffset, 0, uint32(current.PicWidthInLumaSamples))
			ue(r, "conf_win_top_offset", &current.ConfWinTopOffset, 0, uint32(current.PicHeightInLumaSamples))
			ue(r, "conf_win_bottom_offset", &current.ConfWinBottomOffset, 0, uint32(current.PicHeightInLumaSamples))
		} else {
			infer(&current.ConfWinLeftOffset, 0)
			infer(&current.ConfWinRightOffset, 0)
			infer(&current.ConfWinTopOffset, 0)
			infer(&current.ConfWinBottomOffset, 0)
		}

		ue(r, "bit_depth_luma_minus8", &current.BitDepthLumaMinus8, 0, 8)
		ue(r, "bit_depth_chroma_minus8", &current.BitDepthChromaMinus8, 0, 8)
	}

	ue(r, "log2_max_pic_order_cnt_lsb_minus4", &current.Log2MaxPicOrderCntLsbMinus4, 0, 12)

	if !multiLayerExtSpsFlag {
		flag(r, "sps_sub_layer_ordering_info_present_flag", &current.SpsSubLayerOrderingInfoPresentFlag)
		for i := cond(current.SpsSubLayerOrderingInfoPresentFlag != 0,
			0, int(current.SpsMaxSubLayersMinus1)); i <= int(current.SpsMaxSubLayersMinus1); i++ {
			ues(r, "sps_max_dec_pic_buffering_minus1[i]",
				&current.SpsMaxDecPicBufferingMinus1[i], 0, hevcMaxDPBSize-1, i)
			ues(r, "sps_max_num_reorder_pics[i]",
				&current.SpsMaxNumReorderPics[i], 0, uint32(current.SpsMaxDecPicBufferingMinus1[i]), i)
			ues(r, "sps_max_latency_increase_plus1[i]",
				&current.SpsMaxLatencyIncreasePlus1[i], 0, math.MaxUint32-1, i)
		}
		if current.SpsSubLayerOrderingInfoPresentFlag == 0 {
			for i := 0; i < int(current.SpsMaxSubLayersMinus1); i++ {
				infer(&current.SpsMaxDecPicBufferingMinus1[i],
					current.SpsMaxDecPicBufferingMinus1[current.SpsMaxSubLayersMinus1])
				infer(&current.SpsMaxNumReorderPics[i],
					current.SpsMaxNumReorderPics[current.SpsMaxSubLayersMinus1])
				infer(&current.SpsMaxLatencyIncreasePlus1[i],
					current.SpsMaxLatencyIncreasePlus1[current.SpsMaxSubLayersMinus1])
			}
		}
	}

	ue(r, "log2_min_luma_coding_block_size_minus3", &current.Log2MinLumaCodingBlockSizeMinus3, 0, 3)
	minCbLog2SizeY := int(current.Log2MinLumaCodingBlockSizeMinus3) + 3

	ue(r, "log2_diff_max_min_luma_coding_block_size", &current.Log2DiffMaxMinLumaCodingBlockSize, 0, 3)
	ctbLog2SizeY := minCbLog2SizeY +
		int(current.Log2DiffMaxMinLumaCodingBlockSize)

	minCbSizeY := 1 << minCbLog2SizeY
	if int(current.PicWidthInLumaSamples)%minCbSizeY != 0 ||
		int(current.PicHeightInLumaSamples)%minCbSizeY != 0 {
		r.diag(LevelError, "Invalid dimensions: %dx%d not divisible by MinCbSizeY = %d.",
			current.PicWidthInLumaSamples,
			current.PicHeightInLumaSamples, minCbSizeY)
		r.fail(ErrInvalidData)
	}

	ue(r, "log2_min_luma_transform_block_size_minus2", &current.Log2MinLumaTransformBlockSizeMinus2, 0, uint32(minCbLog2SizeY-3))
	minTbLog2SizeY := int(current.Log2MinLumaTransformBlockSizeMinus2) + 2

	ue(r, "log2_diff_max_min_luma_transform_block_size", &current.Log2DiffMaxMinLumaTransformBlockSize,
		0, uint32(min(ctbLog2SizeY, 5)-minTbLog2SizeY))

	ue(r, "max_transform_hierarchy_depth_inter", &current.MaxTransformHierarchyDepthInter,
		0, uint32(ctbLog2SizeY-minTbLog2SizeY))
	ue(r, "max_transform_hierarchy_depth_intra", &current.MaxTransformHierarchyDepthIntra,
		0, uint32(ctbLog2SizeY-minTbLog2SizeY))

	flag(r, "scaling_list_enabled_flag", &current.ScalingListEnabledFlag)
	if current.ScalingListEnabledFlag != 0 {
		if multiLayerExtSpsFlag {
			flag(r, "sps_infer_scaling_list_flag", &current.SpsInferScalingListFlag)
		} else {
			infer(&current.SpsInferScalingListFlag, 0)
		}
		if current.SpsInferScalingListFlag != 0 {
			ub(r, 6, "sps_scaling_list_ref_layer_id", &current.SpsScalingListRefLayerID)
		} else {
			flag(r, "sps_scaling_list_data_present_flag", &current.SpsScalingListDataPresentFlag)
			if current.SpsScalingListDataPresentFlag != 0 {
				h.scalingListData(r, &current.ScalingList)
			}
		}
	} else {
		infer(&current.SpsScalingListDataPresentFlag, 0)
	}

	flag(r, "amp_enabled_flag", &current.AmpEnabledFlag)
	flag(r, "sample_adaptive_offset_enabled_flag", &current.SampleAdaptiveOffsetEnabledFlag)

	flag(r, "pcm_enabled_flag", &current.PcmEnabledFlag)
	if current.PcmEnabledFlag != 0 {
		u(r, 4, "pcm_sample_bit_depth_luma_minus1", &current.PcmSampleBitDepthLumaMinus1,
			0, uint32(current.BitDepthLumaMinus8)+8-1)
		u(r, 4, "pcm_sample_bit_depth_chroma_minus1", &current.PcmSampleBitDepthChromaMinus1,
			0, uint32(current.BitDepthChromaMinus8)+8-1)

		ue(r, "log2_min_pcm_luma_coding_block_size_minus3", &current.Log2MinPcmLumaCodingBlockSizeMinus3,
			uint32(min(minCbLog2SizeY, 5)-3), uint32(min(ctbLog2SizeY, 5)-3))
		ue(r, "log2_diff_max_min_pcm_luma_coding_block_size", &current.Log2DiffMaxMinPcmLumaCodingBlockSize,
			0, uint32(min(ctbLog2SizeY, 5)-(int(current.Log2MinPcmLumaCodingBlockSizeMinus3)+3)))

		flag(r, "pcm_loop_filter_disabled_flag", &current.PcmLoopFilterDisabledFlag)
	}

	ue(r, "num_short_term_ref_pic_sets", &current.NumShortTermRefPicSets, 0, hevcMaxShortTermRefPicSets)
	for i := 0; i < int(current.NumShortTermRefPicSets); i++ {
		h.stRefPicSet(r, &current.StRefPicSet[i], i, current)
	}

	flag(r, "long_term_ref_pics_present_flag", &current.LongTermRefPicsPresentFlag)
	if current.LongTermRefPicsPresentFlag != 0 {
		ue(r, "num_long_term_ref_pics_sps", &current.NumLongTermRefPicsSps, 0, hevcMaxLongTermRefPics)
		for i := 0; i < int(current.NumLongTermRefPicsSps); i++ {
			ubs(r, int(current.Log2MaxPicOrderCntLsbMinus4)+4,
				"lt_ref_pic_poc_lsb_sps[i]", &current.LtRefPicPocLsbSps[i], i)
			flags(r, "used_by_curr_pic_lt_sps_flag[i]", &current.UsedByCurrPicLtSpsFlag[i], i)
		}
	}

	flag(r, "sps_temporal_mvp_enabled_flag", &current.SpsTemporalMvpEnabledFlag)
	flag(r, "strong_intra_smoothing_enabled_flag", &current.StrongIntraSmoothingEnabledFlag)

	flag(r, "vui_parameters_present_flag", &current.VuiParametersPresentFlag)
	if current.VuiParametersPresentFlag != 0 {
		h.vuiParameters(r, &current.Vui, current)
	} else {
		h.vuiParametersDefault(r, &current.Vui, current)
	}

	flag(r, "sps_extension_present_flag", &current.SpsExtensionPresentFlag)
	if current.SpsExtensionPresentFlag != 0 {
		flag(r, "sps_range_extension_flag", &current.SpsRangeExtensionFlag)
		flag(r, "sps_multilayer_extension_flag", &current.SpsMultilayerExtensionFlag)
		flag(r, "sps_3d_extension_flag", &current.Sps3DExtensionFlag)
		flag(r, "sps_scc_extension_flag", &current.SpsSccExtensionFlag)
		ub(r, 4, "sps_extension_4bits", &current.SpsExtension4Bits)
	}

	if current.SpsRangeExtensionFlag != 0 {
		h.spsRangeExtension(r, current)
	}
	if current.SpsMultilayerExtensionFlag != 0 {
		h.spsMultilayerExtension(r, current)
	}
	if current.Sps3DExtensionFlag != 0 {
		r.fail(ErrPatchWelcome)
	}
	if current.SpsSccExtensionFlag != 0 {
		h.spsSccExtension(r, current)
	}
	if current.SpsExtension4Bits != 0 {
		h.extensionData(r, &current.ExtensionData)
	}

	h.rbspTrailingBits(r)
}

func (h *H265Context) ppsRangeExtension(r *Reader, current *H265RawPPS) {
	sps := h.activeSPS

	if current.TransformSkipEnabledFlag != 0 {
		ue(r, "log2_max_transform_skip_block_size_minus2", &current.Log2MaxTransformSkipBlockSizeMinus2, 0, 3)
	}
	flag(r, "cross_component_prediction_enabled_flag", &current.CrossComponentPredictionEnabledFlag)

	flag(r, "chroma_qp_offset_list_enabled_flag", &current.ChromaQpOffsetListEnabledFlag)
	if current.ChromaQpOffsetListEnabledFlag != 0 {
		ue(r, "diff_cu_chroma_qp_offset_depth", &current.DiffCuChromaQpOffsetDepth,
			0, uint32(sps.Log2DiffMaxMinLumaCodingBlockSize))
		ue(r, "chroma_qp_offset_list_len_minus1", &current.ChromaQpOffsetListLenMinus1, 0, 5)
		for i := 0; i <= int(current.ChromaQpOffsetListLenMinus1); i++ {
			ses(r, "cb_qp_offset_list[i]", &current.CbQpOffsetList[i], -12, +12, i)
			ses(r, "cr_qp_offset_list[i]", &current.CrQpOffsetList[i], -12, +12, i)
		}
	}

	ue(r, "log2_sao_offset_scale_luma", &current.Log2SaoOffsetScaleLuma,
		0, uint32(max(0, int(sps.BitDepthLumaMinus8)-2)))
	ue(r, "log2_sao_offset_scale_chroma", &current.Log2SaoOffsetScaleChroma,
		0, uint32(max(0, int(sps.BitDepthChromaMinus8)-2)))
}

func (h *H265Context) colourMappingOctants(r *Reader, current *H265RawPPS,
	inpDepth, idxY, idxCb, idxCr, inpLength int) {
	partNumY := 1 << current.CmYPartNumLog2

	if inpDepth < int(current.CmOctantDepth) {
		flags(r, "split_octant_flag[inp_depth]", &current.SplitOctantFlag[inpDepth], inpDepth)
	} else {
		infer(&current.SplitOctantFlag[inpDepth], 0)
	}

	if current.SplitOctantFlag[inpDepth] != 0 {
		for k := 0; k < 2; k++ {
			for m := 0; m < 2; m++ {
				for n := 0; n < 2; n++ {
					h.colourMappingOctants(r, current, inpDepth+1,
						idxY+partNumY*k*inpLength/2,
						idxCb+m*inpLength/2,
						idxCr+n*inpLength/2,
						inpLength/2)
				}
			}
		}
	} else {
		for i := 0; i < partNumY; i++ {
			idxShiftY := idxY + (i << (int(current.CmOctantDepth) - inpDepth))
			for j := 0; j < 4; j++ {
				flags(r, "coded_res_flag[idx_shift_y][idx_cb][idx_cr][j]",
					&current.CodedResFlag[idxShiftY][idxCb][idxCr][j],
					idxShiftY, idxCb, idxCr, j)
				if current.CodedResFlag[idxShiftY][idxCb][idxCr][j] != 0 {
					for c := 0; c < 3; c++ {
						ues(r, "res_coeff_q[idx_shift_y][idx_cb][idx_cr][j][c]",
							&current.ResCoeffQ[idxShiftY][idxCb][idxCr][j][c], 0, 3,
							idxShiftY, idxCb, idxCr, j, c)
						cmResBits := max(0, 10+(int(current.LumaBitDepthCmInputMinus8)+8)-
							(int(current.LumaBitDepthCmOutputMinus8)+8)-
							int(current.CmResQuantBits)-(int(current.CmDeltaFlcBitsMinus1)+1))
						if cmResBits != 0 {
							ubs(r, cmResBits, "res_coeff_r[idx_shift_y][idx_cb][idx_cr][j][c]",
								&current.ResCoeffR[idxShiftY][idxCb][idxCr][j][c],
								idxShiftY, idxCb, idxCr, j, c)
						} else {
							infer(&current.ResCoeffR[idxShiftY][idxCb][idxCr][j][c], 0)
						}
						if current.ResCoeffQ[idxShiftY][idxCb][idxCr][j][c] != 0 ||
							current.ResCoeffR[idxShiftY][idxCb][idxCr][j][c] != 0 {
							ub(r, 1, "res_coeff_s[idx_shift_y][idx_cb][idx_cr][j][c]",
								&current.ResCoeffS[idxShiftY][idxCb][idxCr][j][c])
						} else {
							infer(&current.ResCoeffS[idxShiftY][idxCb][idxCr][j][c], 0)
						}
					}
				} else {
					for c := 0; c < 3; c++ {
						infer(&current.ResCoeffQ[idxShiftY][idxCb][idxCr][j][c], 0)
						infer(&current.ResCoeffR[idxShiftY][idxCb][idxCr][j][c], 0)
						infer(&current.ResCoeffS[idxShiftY][idxCb][idxCr][j][c], 0)
					}
				}
			}
		}
	}
}

func (h *H265Context) colourMappingTable(r *Reader, current *H265RawPPS) {
	ue(r, "num_cm_ref_layers_minus1", &current.NumCmRefLayersMinus1, 0, 61)
	for i := 0; i <= int(current.NumCmRefLayersMinus1); i++ {
		ubs(r, 6, "cm_ref_layer_id[i]", &current.CmRefLayerID[i], i)
	}

	u(r, 2, "cm_octant_depth", &current.CmOctantDepth, 0, 1)
	u(r, 2, "cm_y_part_num_log2", &current.CmYPartNumLog2, 0, uint32(3-current.CmOctantDepth))

	ue(r, "luma_bit_depth_cm_input_minus8", &current.LumaBitDepthCmInputMinus8, 0, 8)
	ue(r, "chroma_bit_depth_cm_input_minus8", &current.ChromaBitDepthCmInputMinus8, 0, 8)
	ue(r, "luma_bit_depth_cm_output_minus8", &current.LumaBitDepthCmOutputMinus8, 0, 8)
	ue(r, "chroma_bit_depth_cm_output_minus8", &current.ChromaBitDepthCmOutputMinus8, 0, 8)

	ub(r, 2, "cm_res_quant_bits", &current.CmResQuantBits)
	ub(r, 2, "cm_delta_flc_bits_minus1", &current.CmDeltaFlcBitsMinus1)

	if current.CmOctantDepth == 1 {
		se(r, "cm_adapt_threshold_u_delta", &current.CmAdaptThresholdUDelta, -32768, 32767)
		se(r, "cm_adapt_threshold_v_delta", &current.CmAdaptThresholdVDelta, -32768, 32767)
	} else {
		infer(&current.CmAdaptThresholdUDelta, 0)
		infer(&current.CmAdaptThresholdVDelta, 0)
	}

	h.colourMappingOctants(r, current, 0, 0, 0, 0, 1<<current.CmOctantDepth)
}

func (h *H265Context) ppsMultilayerExtension(r *Reader, current *H265RawPPS) {
	vps := h.activeVPS

	flag(r, "poc_reset_info_present_flag", &current.PocResetInfoPresentFlag)
	flag(r, "pps_infer_scaling_list_flag", &current.PpsInferScalingListFlag)
	if current.PpsInferScalingListFlag != 0 {
		ub(r, 6, "pps_scaling_list_ref_layer_id", &current.PpsScalingListRefLayerID)
	}

	if vps == nil {
		r.diag(LevelError, "VPS missing for PPS Multilayer Extension.")
		r.fail(ErrInvalidData)
	}

	ue(r, "num_ref_loc_offsets", &current.NumRefLocOffsets, 0, uint32(vps.VpsMaxLayersMinus1))
	for i := 0; i < int(current.NumRefLocOffsets); i++ {
		ubs(r, 6, "ref_loc_offset_layer_id[i]", &current.RefLocOffsetLayerID[i], i)
		offset := int(current.RefLocOffsetLayerID[i])
		flags(r, "scaled_ref_layer_offset_present_flag[i]", &current.ScaledRefLayerOffsetPresentFlag[i], i)
		if current.ScaledRefLayerOffsetPresentFlag[i] != 0 {
			ses(r, "scaled_ref_layer_left_offset[offset]", &current.ScaledRefLayerLeftOffset[offset], -16384, 16383, offset)
			ses(r, "scaled_ref_layer_top_offset[offset]", &current.ScaledRefLayerTopOffset[offset], -16384, 16383, offset)
			ses(r, "scaled_ref_layer_right_offset[offset]", &current.ScaledRefLayerRightOffset[offset], -16384, 16383, offset)
			ses(r, "scaled_ref_layer_bottom_offset[offset]", &current.ScaledRefLayerBottomOffset[offset], -16384, 16383, offset)
		} else {
			infer(&current.ScaledRefLayerLeftOffset[offset], 0)
			infer(&current.ScaledRefLayerTopOffset[offset], 0)
			infer(&current.ScaledRefLayerRightOffset[offset], 0)
			infer(&current.ScaledRefLayerBottomOffset[offset], 0)
		}
		flags(r, "ref_region_offset_present_flag[i]", &current.RefRegionOffsetPresentFlag[i], i)
		if current.RefRegionOffsetPresentFlag[i] != 0 {
			ses(r, "ref_region_left_offset[offset]", &current.RefRegionLeftOffset[offset], -16384, 16383, offset)
			ses(r, "ref_region_top_offset[offset]", &current.RefRegionTopOffset[offset], -16384, 16383, offset)
			ses(r, "ref_region_right_offset[offset]", &current.RefRegionRightOffset[offset], -16384, 16383, offset)
			ses(r, "ref_region_bottom_offset[offset]", &current.RefRegionBottomOffset[offset], -16384, 16383, offset)
		} else {
			infer(&current.RefRegionLeftOffset[offset], 0)
			infer(&current.RefRegionTopOffset[offset], 0)
			infer(&current.RefRegionRightOffset[offset], 0)
			infer(&current.RefRegionBottomOffset[offset], 0)
		}
		flags(r, "resample_phase_set_present_flag[i]", &current.ResamplePhaseSetPresentFlag[i], i)
		if current.ResamplePhaseSetPresentFlag[i] != 0 {
			ues(r, "phase_hor_luma[offset]", &current.PhaseHorLuma[offset], 0, 31, offset)
			ues(r, "phase_ver_luma[offset]", &current.PhaseVerLuma[offset], 0, 31, offset)
			ues(r, "phase_hor_chroma_plus8[offset]", &current.PhaseHorChromaPlus8[offset], 0, 63, offset)
			ues(r, "phase_ver_chroma_plus8[offset]", &current.PhaseVerChromaPlus8[offset], 0, 63, offset)
		} else {
			infer(&current.PhaseHorLuma[offset], 0)
			infer(&current.PhaseVerLuma[offset], 0)
			infer(&current.PhaseHorChromaPlus8[offset], 8)
		}
	}

	flag(r, "colour_mapping_enabled_flag", &current.ColourMappingEnabledFlag)
	if current.ColourMappingEnabledFlag != 0 {
		h.colourMappingTable(r, current)
	}
}

func (h *H265Context) ppsSccExtension(r *Reader, current *H265RawPPS) {
	flag(r, "pps_curr_pic_ref_enabled_flag", &current.PpsCurrPicRefEnabledFlag)

	flag(r, "residual_adaptive_colour_transform_enabled_flag", &current.ResidualAdaptiveColourTransformEnabledFlag)
	if current.ResidualAdaptiveColourTransformEnabledFlag != 0 {
		flag(r, "pps_slice_act_qp_offsets_present_flag", &current.PpsSliceActQpOffsetsPresentFlag)
		se(r, "pps_act_y_qp_offset_plus5", &current.PpsActYQpOffsetPlus5, -7, +17)
		se(r, "pps_act_cb_qp_offset_plus5", &current.PpsActCbQpOffsetPlus5, -7, +17)
		se(r, "pps_act_cr_qp_offset_plus3", &current.PpsActCrQpOffsetPlus3, -9, +15)
	} else {
		infer(&current.PpsSliceActQpOffsetsPresentFlag, 0)
		infer(&current.PpsActYQpOffsetPlus5, 0)
		infer(&current.PpsActCbQpOffsetPlus5, 0)
		infer(&current.PpsActCrQpOffsetPlus3, 0)
	}

	flag(r, "pps_palette_predictor_initializer_present_flag", &current.PpsPalettePredictorInitializerPresentFlag)
	if current.PpsPalettePredictorInitializerPresentFlag != 0 {
		ue(r, "pps_num_palette_predictor_initializer", &current.PpsNumPalettePredictorInitializer, 0, 128)
		if current.PpsNumPalettePredictorInitializer > 0 {
			flag(r, "monochrome_palette_flag", &current.MonochromePaletteFlag)
			ue(r, "luma_bit_depth_entry_minus8", &current.LumaBitDepthEntryMinus8, 0, 8)
			if current.MonochromePaletteFlag == 0 {
				ue(r, "chroma_bit_depth_entry_minus8", &current.ChromaBitDepthEntryMinus8, 0, 8)
			}
			for comp := 0; comp < cond(current.MonochromePaletteFlag != 0, 1, 3); comp++ {
				bitDepth := cond(comp == 0,
					int(current.LumaBitDepthEntryMinus8)+8,
					int(current.ChromaBitDepthEntryMinus8)+8)
				for i := 0; i < int(current.PpsNumPalettePredictorInitializer); i++ {
					ubs(r, bitDepth, "pps_palette_predictor_initializers[comp][i]",
						&current.PpsPalettePredictorInitializers[comp][i], comp, i)
				}
			}
		}
	}
}

func (h *H265Context) readPPS(r *Reader, current *H265RawPPS) {
	r.header("Picture Parameter Set")

	h.nalUnitHeader(r, &current.NalUnitHeader, hevcNALPPS)

	ue(r, "pps_pic_parameter_set_id", &current.PpsPicParameterSetID, 0, 63)
	ue(r, "pps_seq_parameter_set_id", &current.PpsSeqParameterSetID, 0, 15)
	sps := h.sps[current.PpsSeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.",
			current.PpsSeqParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeSPS = sps

	flag(r, "dependent_slice_segments_enabled_flag", &current.DependentSliceSegmentsEnabledFlag)
	flag(r, "output_flag_present_flag", &current.OutputFlagPresentFlag)
	ub(r, 3, "num_extra_slice_header_bits", &current.NumExtraSliceHeaderBits)
	flag(r, "sign_data_hiding_enabled_flag", &current.SignDataHidingEnabledFlag)
	flag(r, "cabac_init_present_flag", &current.CabacInitPresentFlag)

	ue(r, "num_ref_idx_l0_default_active_minus1", &current.NumRefIdxL0DefaultActiveMinus1, 0, 14)
	ue(r, "num_ref_idx_l1_default_active_minus1", &current.NumRefIdxL1DefaultActiveMinus1, 0, 14)

	se(r, "init_qp_minus26", &current.InitQpMinus26, -(26 + 6*int32(sps.BitDepthLumaMinus8)), +25)

	flag(r, "constrained_intra_pred_flag", &current.ConstrainedIntraPredFlag)
	flag(r, "transform_skip_enabled_flag", &current.TransformSkipEnabledFlag)
	flag(r, "cu_qp_delta_enabled_flag", &current.CuQpDeltaEnabledFlag)
	if current.CuQpDeltaEnabledFlag != 0 {
		ue(r, "diff_cu_qp_delta_depth", &current.DiffCuQpDeltaDepth,
			0, uint32(sps.Log2DiffMaxMinLumaCodingBlockSize))
	} else {
		infer(&current.DiffCuQpDeltaDepth, 0)
	}

	se(r, "pps_cb_qp_offset", &current.PpsCbQpOffset, -12, +12)
	se(r, "pps_cr_qp_offset", &current.PpsCrQpOffset, -12, +12)
	flag(r, "pps_slice_chroma_qp_offsets_present_flag", &current.PpsSliceChromaQpOffsetsPresentFlag)

	flag(r, "weighted_pred_flag", &current.WeightedPredFlag)
	flag(r, "weighted_bipred_flag", &current.WeightedBipredFlag)

	flag(r, "transquant_bypass_enabled_flag", &current.TransquantBypassEnabledFlag)
	flag(r, "tiles_enabled_flag", &current.TilesEnabledFlag)
	flag(r, "entropy_coding_sync_enabled_flag", &current.EntropyCodingSyncEnabledFlag)

	if current.TilesEnabledFlag != 0 {
		ue(r, "num_tile_columns_minus1", &current.NumTileColumnsMinus1, 0, hevcMaxTileColumns)
		ue(r, "num_tile_rows_minus1", &current.NumTileRowsMinus1, 0, hevcMaxTileRows)
		flag(r, "uniform_spacing_flag", &current.UniformSpacingFlag)
		if current.UniformSpacingFlag == 0 {
			for i := 0; i < int(current.NumTileColumnsMinus1); i++ {
				ues(r, "column_width_minus1[i]", &current.ColumnWidthMinus1[i], 0, uint32(sps.PicWidthInLumaSamples), i)
			}
			for i := 0; i < int(current.NumTileRowsMinus1); i++ {
				ues(r, "row_height_minus1[i]", &current.RowHeightMinus1[i], 0, uint32(sps.PicHeightInLumaSamples), i)
			}
		}
		flag(r, "loop_filter_across_tiles_enabled_flag", &current.LoopFilterAcrossTilesEnabledFlag)
	} else {
		infer(&current.NumTileColumnsMinus1, 0)
		infer(&current.NumTileRowsMinus1, 0)
	}

	flag(r, "pps_loop_filter_across_slices_enabled_flag", &current.PpsLoopFilterAcrossSlicesEnabledFlag)
	flag(r, "deblocking_filter_control_present_flag", &current.DeblockingFilterControlPresentFlag)
	if current.DeblockingFilterControlPresentFlag != 0 {
		flag(r, "deblocking_filter_override_enabled_flag", &current.DeblockingFilterOverrideEnabledFlag)
		flag(r, "pps_deblocking_filter_disabled_flag", &current.PpsDeblockingFilterDisabledFlag)
		if current.PpsDeblockingFilterDisabledFlag == 0 {
			se(r, "pps_beta_offset_div2", &current.PpsBetaOffsetDiv2, -6, +6)
			se(r, "pps_tc_offset_div2", &current.PpsTcOffsetDiv2, -6, +6)
		} else {
			infer(&current.PpsBetaOffsetDiv2, 0)
			infer(&current.PpsTcOffsetDiv2, 0)
		}
	} else {
		infer(&current.DeblockingFilterOverrideEnabledFlag, 0)
		infer(&current.PpsDeblockingFilterDisabledFlag, 0)
		infer(&current.PpsBetaOffsetDiv2, 0)
		infer(&current.PpsTcOffsetDiv2, 0)
	}

	flag(r, "pps_scaling_list_data_present_flag", &current.PpsScalingListDataPresentFlag)
	if current.PpsScalingListDataPresentFlag != 0 {
		h.scalingListData(r, &current.ScalingList)
	}

	flag(r, "lists_modification_present_flag", &current.ListsModificationPresentFlag)

	ue(r, "log2_parallel_merge_level_minus2", &current.Log2ParallelMergeLevelMinus2,
		0, uint32(int(sps.Log2MinLumaCodingBlockSizeMinus3)+3+
			int(sps.Log2DiffMaxMinLumaCodingBlockSize)-2))

	flag(r, "slice_segment_header_extension_present_flag", &current.SliceSegmentHeaderExtensionPresentFlag)

	flag(r, "pps_extension_present_flag", &current.PpsExtensionPresentFlag)
	if current.PpsExtensionPresentFlag != 0 {
		flag(r, "pps_range_extension_flag", &current.PpsRangeExtensionFlag)
		flag(r, "pps_multilayer_extension_flag", &current.PpsMultilayerExtensionFlag)
		flag(r, "pps_3d_extension_flag", &current.Pps3DExtensionFlag)
		flag(r, "pps_scc_extension_flag", &current.PpsSccExtensionFlag)
		ub(r, 4, "pps_extension_4bits", &current.PpsExtension4Bits)
	}
	if current.PpsRangeExtensionFlag != 0 {
		h.ppsRangeExtension(r, current)
	}
	if current.PpsMultilayerExtensionFlag != 0 {
		h.ppsMultilayerExtension(r, current)
	}
	if current.Pps3DExtensionFlag != 0 {
		r.fail(ErrPatchWelcome)
	}
	if current.PpsSccExtensionFlag != 0 {
		h.ppsSccExtension(r, current)
	}
	if current.PpsExtension4Bits != 0 {
		h.extensionData(r, &current.ExtensionData)
	}

	h.rbspTrailingBits(r)
}

func (h *H265Context) aud(r *Reader, current *H265RawAUD) {
	r.header("Access Unit Delimiter")

	h.nalUnitHeader(r, &current.NalUnitHeader, hevcNALAUD)

	u(r, 3, "pic_type", &current.PicType, 0, 2)

	h.rbspTrailingBits(r)
}

func (h *H265Context) refPicListsModification(r *Reader, current *H265RawSliceHeader,
	numPicTotalCurr int) {
	entrySize := avLog2(uint32(numPicTotalCurr-1)) + 1

	flag(r, "ref_pic_list_modification_flag_l0", &current.RefPicListModificationFlagL0)
	if current.RefPicListModificationFlagL0 != 0 {
		for i := 0; i <= int(current.NumRefIdxL0ActiveMinus1); i++ {
			us(r, entrySize, "list_entry_l0[i]", &current.ListEntryL0[i],
				0, uint32(numPicTotalCurr-1), i)
		}
	}

	if current.SliceType == hevcSliceB {
		flag(r, "ref_pic_list_modification_flag_l1", &current.RefPicListModificationFlagL1)
		if current.RefPicListModificationFlagL1 != 0 {
			for i := 0; i <= int(current.NumRefIdxL1ActiveMinus1); i++ {
				us(r, entrySize, "list_entry_l1[i]", &current.ListEntryL1[i],
					0, uint32(numPicTotalCurr-1), i)
			}
		}
	}
}

func (h *H265Context) predWeightTable(r *Reader, current *H265RawSliceHeader) {
	sps := h.activeSPS
	chroma := sps.SeparateColourPlaneFlag == 0 &&
		sps.ChromaFormatIdc != 0

	ue(r, "luma_log2_weight_denom", &current.LumaLog2WeightDenom, 0, 7)
	if chroma {
		se(r, "delta_chroma_log2_weight_denom", &current.DeltaChromaLog2WeightDenom, -7, 7)
	} else {
		infer(&current.DeltaChromaLog2WeightDenom, 0)
	}

	for i := 0; i <= int(current.NumRefIdxL0ActiveMinus1); i++ {
		// is not same POC and same layer_id
		flags(r, "luma_weight_l0_flag[i]", &current.LumaWeightL0Flag[i], i)
	}
	if chroma {
		for i := 0; i <= int(current.NumRefIdxL0ActiveMinus1); i++ {
			// is not same POC and same layer_id
			flags(r, "chroma_weight_l0_flag[i]", &current.ChromaWeightL0Flag[i], i)
		}
	}

	for i := 0; i <= int(current.NumRefIdxL0ActiveMinus1); i++ {
		if current.LumaWeightL0Flag[i] != 0 {
			ses(r, "delta_luma_weight_l0[i]", &current.DeltaLumaWeightL0[i], -128, +127, i)
			ses(r, "luma_offset_l0[i]", &current.LumaOffsetL0[i],
				-(1 << (int32(sps.BitDepthLumaMinus8) + 8 - 1)),
				(1<<(int32(sps.BitDepthLumaMinus8)+8-1))-1, i)
		} else {
			infer(&current.DeltaLumaWeightL0[i], 0)
			infer(&current.LumaOffsetL0[i], 0)
		}
		if current.ChromaWeightL0Flag[i] != 0 {
			for j := 0; j < 2; j++ {
				ses(r, "delta_chroma_weight_l0[i][j]", &current.DeltaChromaWeightL0[i][j], -128, +127, i, j)
				ses(r, "chroma_offset_l0[i][j]", &current.ChromaOffsetL0[i][j],
					-(4 << (int32(sps.BitDepthChromaMinus8) + 8 - 1)),
					(4<<(int32(sps.BitDepthChromaMinus8)+8-1))-1, i, j)
			}
		} else {
			for j := 0; j < 2; j++ {
				infer(&current.DeltaChromaWeightL0[i][j], 0)
				infer(&current.ChromaOffsetL0[i][j], 0)
			}
		}
	}

	if current.SliceType == hevcSliceB {
		for i := 0; i <= int(current.NumRefIdxL1ActiveMinus1); i++ {
			// RefPicList1[i] is not CurrPic, nor is it in a different layer
			flags(r, "luma_weight_l1_flag[i]", &current.LumaWeightL1Flag[i], i)
		}
		if chroma {
			for i := 0; i <= int(current.NumRefIdxL1ActiveMinus1); i++ {
				// RefPicList1[i] is not CurrPic, nor is it in a different layer
				flags(r, "chroma_weight_l1_flag[i]", &current.ChromaWeightL1Flag[i], i)
			}
		}

		for i := 0; i <= int(current.NumRefIdxL1ActiveMinus1); i++ {
			if current.LumaWeightL1Flag[i] != 0 {
				ses(r, "delta_luma_weight_l1[i]", &current.DeltaLumaWeightL1[i], -128, +127, i)
				ses(r, "luma_offset_l1[i]", &current.LumaOffsetL1[i],
					-(1 << (int32(sps.BitDepthLumaMinus8) + 8 - 1)),
					(1<<(int32(sps.BitDepthLumaMinus8)+8-1))-1, i)
			} else {
				infer(&current.DeltaLumaWeightL1[i], 0)
				infer(&current.LumaOffsetL1[i], 0)
			}
			if current.ChromaWeightL1Flag[i] != 0 {
				for j := 0; j < 2; j++ {
					ses(r, "delta_chroma_weight_l1[i][j]", &current.DeltaChromaWeightL1[i][j], -128, +127, i, j)
					ses(r, "chroma_offset_l1[i][j]", &current.ChromaOffsetL1[i][j],
						-(4 << (int32(sps.BitDepthChromaMinus8) + 8 - 1)),
						(4<<(int32(sps.BitDepthChromaMinus8)+8-1))-1, i, j)
				}
			} else {
				for j := 0; j < 2; j++ {
					infer(&current.DeltaChromaWeightL1[i][j], 0)
					infer(&current.ChromaOffsetL1[i][j], 0)
				}
			}
		}
	}
}

func (h *H265Context) sliceSegmentHeader(r *Reader, current *H265RawSliceHeader) {
	numPicTotalCurr := 0

	r.header("Slice Segment Header")

	h.nalUnitHeader(r, &current.NalUnitHeader, -1)

	flag(r, "first_slice_segment_in_pic_flag", &current.FirstSliceSegmentInPicFlag)

	if current.NalUnitHeader.NalUnitType >= hevcNALBLAWLP &&
		current.NalUnitHeader.NalUnitType <= hevcNALRsvIrapVcl23 {
		flag(r, "no_output_of_prior_pics_flag", &current.NoOutputOfPriorPicsFlag)
	}

	ue(r, "slice_pic_parameter_set_id", &current.SlicePicParameterSetID, 0, 63)
	pps := h.pps[current.SlicePicParameterSetID]
	if pps == nil {
		r.diag(LevelError, "PPS id %d not available.",
			current.SlicePicParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activePPS = pps

	sps := h.sps[pps.PpsSeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.",
			pps.PpsSeqParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeSPS = sps

	minCbLog2SizeY := int(sps.Log2MinLumaCodingBlockSizeMinus3) + 3
	ctbLog2SizeY := minCbLog2SizeY + int(sps.Log2DiffMaxMinLumaCodingBlockSize)
	ctbSizeY := 1 << ctbLog2SizeY
	picWidthInCtbsY :=
		(int(sps.PicWidthInLumaSamples) + ctbSizeY - 1) / ctbSizeY
	picHeightInCtbsY :=
		(int(sps.PicHeightInLumaSamples) + ctbSizeY - 1) / ctbSizeY
	picSizeInCtbsY := picWidthInCtbsY * picHeightInCtbsY

	if current.FirstSliceSegmentInPicFlag == 0 {
		addressSize := avLog2(uint32(picSizeInCtbsY-1)) + 1
		if pps.DependentSliceSegmentsEnabledFlag != 0 {
			flag(r, "dependent_slice_segment_flag", &current.DependentSliceSegmentFlag)
		} else {
			infer(&current.DependentSliceSegmentFlag, 0)
		}
		u(r, addressSize, "slice_segment_address", &current.SliceSegmentAddress, 0, uint32(picSizeInCtbsY-1))
	} else {
		infer(&current.DependentSliceSegmentFlag, 0)
	}

	if current.DependentSliceSegmentFlag == 0 {
		for i := 0; i < int(pps.NumExtraSliceHeaderBits); i++ {
			flags(r, "slice_reserved_flag[i]", &current.SliceReservedFlag[i], i)
		}

		ue(r, "slice_type", &current.SliceType, 0, 2)

		if pps.OutputFlagPresentFlag != 0 {
			flag(r, "pic_output_flag", &current.PicOutputFlag)
		}

		if sps.SeparateColourPlaneFlag != 0 {
			u(r, 2, "colour_plane_id", &current.ColourPlaneID, 0, 2)
		}

		if current.NalUnitHeader.NalUnitType != hevcNALIDRWRADL &&
			current.NalUnitHeader.NalUnitType != hevcNALIDRNLP {
			var rps *H265RawSTRefPicSet

			ub(r, int(sps.Log2MaxPicOrderCntLsbMinus4)+4, "slice_pic_order_cnt_lsb", &current.SlicePicOrderCntLsb)

			flag(r, "short_term_ref_pic_set_sps_flag", &current.ShortTermRefPicSetSpsFlag)
			if current.ShortTermRefPicSetSpsFlag == 0 {
				h.stRefPicSet(r, &current.ShortTermRefPicSet,
					int(sps.NumShortTermRefPicSets), sps)
				rps = &current.ShortTermRefPicSet
			} else if sps.NumShortTermRefPicSets > 1 {
				idxSize := avLog2(uint32(sps.NumShortTermRefPicSets-1)) + 1
				u(r, idxSize, "short_term_ref_pic_set_idx", &current.ShortTermRefPicSetIdx,
					0, uint32(sps.NumShortTermRefPicSets-1))
				rps = &sps.StRefPicSet[current.ShortTermRefPicSetIdx]
			} else {
				infer(&current.ShortTermRefPicSetIdx, 0)
				rps = &sps.StRefPicSet[0]
			}

			dpbSlotsRemaining := hevcMaxDPBSize - 1 -
				int(rps.NumNegativePics) - int(rps.NumPositivePics)
			if pps.PpsCurrPicRefEnabledFlag != 0 &&
				(sps.SampleAdaptiveOffsetEnabledFlag != 0 ||
					pps.PpsDeblockingFilterDisabledFlag == 0 ||
					pps.DeblockingFilterOverrideEnabledFlag != 0) {
				// This picture will occupy two DPB slots.
				if dpbSlotsRemaining == 0 {
					r.diag(LevelError, "Invalid stream: short-term ref pic set contains too many pictures to use with current picture reference enabled.")
					r.fail(ErrInvalidData)
				}
				dpbSlotsRemaining--
			}

			numPicTotalCurr = 0
			for i := 0; i < int(rps.NumNegativePics); i++ {
				if rps.UsedByCurrPicS0Flag[i] != 0 {
					numPicTotalCurr++
				}
			}
			for i := 0; i < int(rps.NumPositivePics); i++ {
				if rps.UsedByCurrPicS1Flag[i] != 0 {
					numPicTotalCurr++
				}
			}

			if sps.LongTermRefPicsPresentFlag != 0 {
				var idxSize int

				if sps.NumLongTermRefPicsSps > 0 {
					ue(r, "num_long_term_sps", &current.NumLongTermSps,
						0, uint32(min(int(sps.NumLongTermRefPicsSps), dpbSlotsRemaining)))
					idxSize = avLog2(uint32(sps.NumLongTermRefPicsSps-1)) + 1
					dpbSlotsRemaining -= int(current.NumLongTermSps)
				} else {
					infer(&current.NumLongTermSps, 0)
					idxSize = 0
				}
				ue(r, "num_long_term_pics", &current.NumLongTermPics, 0, uint32(dpbSlotsRemaining))

				for i := 0; i < int(current.NumLongTermSps)+
					int(current.NumLongTermPics); i++ {
					if i < int(current.NumLongTermSps) {
						if sps.NumLongTermRefPicsSps > 1 {
							us(r, idxSize, "lt_idx_sps[i]", &current.LtIdxSps[i],
								0, uint32(sps.NumLongTermRefPicsSps-1), i)
						}
						if sps.UsedByCurrPicLtSpsFlag[current.LtIdxSps[i]] != 0 {
							numPicTotalCurr++
						}
					} else {
						ubs(r, int(sps.Log2MaxPicOrderCntLsbMinus4)+4, "poc_lsb_lt[i]", &current.PocLsbLt[i], i)
						flags(r, "used_by_curr_pic_lt_flag[i]", &current.UsedByCurrPicLtFlag[i], i)
						if current.UsedByCurrPicLtFlag[i] != 0 {
							numPicTotalCurr++
						}
					}
					flags(r, "delta_poc_msb_present_flag[i]", &current.DeltaPocMsbPresentFlag[i], i)
					if current.DeltaPocMsbPresentFlag[i] != 0 {
						ues(r, "delta_poc_msb_cycle_lt[i]", &current.DeltaPocMsbCycleLt[i], 0, math.MaxUint32-1, i)
					} else {
						infer(&current.DeltaPocMsbCycleLt[i], 0)
					}
				}
			}

			if sps.SpsTemporalMvpEnabledFlag != 0 {
				flag(r, "slice_temporal_mvp_enabled_flag", &current.SliceTemporalMvpEnabledFlag)
			} else {
				infer(&current.SliceTemporalMvpEnabledFlag, 0)
			}

			if pps.PpsCurrPicRefEnabledFlag != 0 {
				numPicTotalCurr++
			}
		}

		if sps.SampleAdaptiveOffsetEnabledFlag != 0 {
			flag(r, "slice_sao_luma_flag", &current.SliceSaoLumaFlag)
			if sps.SeparateColourPlaneFlag == 0 && sps.ChromaFormatIdc != 0 {
				flag(r, "slice_sao_chroma_flag", &current.SliceSaoChromaFlag)
			} else {
				infer(&current.SliceSaoChromaFlag, 0)
			}
		} else {
			infer(&current.SliceSaoLumaFlag, 0)
			infer(&current.SliceSaoChromaFlag, 0)
		}

		if current.SliceType == hevcSliceP ||
			current.SliceType == hevcSliceB {
			flag(r, "num_ref_idx_active_override_flag", &current.NumRefIdxActiveOverrideFlag)
			if current.NumRefIdxActiveOverrideFlag != 0 {
				ue(r, "num_ref_idx_l0_active_minus1", &current.NumRefIdxL0ActiveMinus1, 0, 14)
				if current.SliceType == hevcSliceB {
					ue(r, "num_ref_idx_l1_active_minus1", &current.NumRefIdxL1ActiveMinus1, 0, 14)
				} else {
					infer(&current.NumRefIdxL1ActiveMinus1, pps.NumRefIdxL1DefaultActiveMinus1)
				}
			} else {
				infer(&current.NumRefIdxL0ActiveMinus1, pps.NumRefIdxL0DefaultActiveMinus1)
				infer(&current.NumRefIdxL1ActiveMinus1, pps.NumRefIdxL1DefaultActiveMinus1)
			}

			if pps.ListsModificationPresentFlag != 0 && numPicTotalCurr > 1 {
				h.refPicListsModification(r, current, numPicTotalCurr)
			}

			if current.SliceType == hevcSliceB {
				flag(r, "mvd_l1_zero_flag", &current.MvdL1ZeroFlag)
			}
			if pps.CabacInitPresentFlag != 0 {
				flag(r, "cabac_init_flag", &current.CabacInitFlag)
			} else {
				infer(&current.CabacInitFlag, 0)
			}
			if current.SliceTemporalMvpEnabledFlag != 0 {
				if current.SliceType == hevcSliceB {
					flag(r, "collocated_from_l0_flag", &current.CollocatedFromL0Flag)
				} else {
					infer(&current.CollocatedFromL0Flag, 1)
				}
				if current.CollocatedFromL0Flag != 0 {
					if current.NumRefIdxL0ActiveMinus1 > 0 {
						ue(r, "collocated_ref_idx", &current.CollocatedRefIdx, 0, uint32(current.NumRefIdxL0ActiveMinus1))
					} else {
						infer(&current.CollocatedRefIdx, 0)
					}
				} else {
					if current.NumRefIdxL1ActiveMinus1 > 0 {
						ue(r, "collocated_ref_idx", &current.CollocatedRefIdx, 0, uint32(current.NumRefIdxL1ActiveMinus1))
					} else {
						infer(&current.CollocatedRefIdx, 0)
					}
				}
			}

			if (pps.WeightedPredFlag != 0 && current.SliceType == hevcSliceP) ||
				(pps.WeightedBipredFlag != 0 && current.SliceType == hevcSliceB) {
				h.predWeightTable(r, current)
			}

			ue(r, "five_minus_max_num_merge_cand", &current.FiveMinusMaxNumMergeCand, 0, 4)
			if sps.MotionVectorResolutionControlIdc == 2 {
				flag(r, "use_integer_mv_flag", &current.UseIntegerMvFlag)
			} else {
				infer(&current.UseIntegerMvFlag, sps.MotionVectorResolutionControlIdc)
			}
		}

		se(r, "slice_qp_delta", &current.SliceQpDelta,
			-6*int32(sps.BitDepthLumaMinus8)-(int32(pps.InitQpMinus26)+26),
			+51-(int32(pps.InitQpMinus26)+26))
		if pps.PpsSliceChromaQpOffsetsPresentFlag != 0 {
			se(r, "slice_cb_qp_offset", &current.SliceCbQpOffset, -12, +12)
			se(r, "slice_cr_qp_offset", &current.SliceCrQpOffset, -12, +12)
		} else {
			infer(&current.SliceCbQpOffset, 0)
			infer(&current.SliceCrQpOffset, 0)
		}
		if pps.PpsSliceActQpOffsetsPresentFlag != 0 {
			se(r, "slice_act_y_qp_offset", &current.SliceActYQpOffset,
				-12-(int32(pps.PpsActYQpOffsetPlus5)-5),
				+12-(int32(pps.PpsActYQpOffsetPlus5)-5))
			se(r, "slice_act_cb_qp_offset", &current.SliceActCbQpOffset,
				-12-(int32(pps.PpsActCbQpOffsetPlus5)-5),
				+12-(int32(pps.PpsActCbQpOffsetPlus5)-5))
			se(r, "slice_act_cr_qp_offset", &current.SliceActCrQpOffset,
				-12-(int32(pps.PpsActCrQpOffsetPlus3)-3),
				+12-(int32(pps.PpsActCrQpOffsetPlus3)-3))
		} else {
			infer(&current.SliceActYQpOffset, 0)
			infer(&current.SliceActCbQpOffset, 0)
			infer(&current.SliceActCrQpOffset, 0)
		}
		if pps.ChromaQpOffsetListEnabledFlag != 0 {
			flag(r, "cu_chroma_qp_offset_enabled_flag", &current.CuChromaQpOffsetEnabledFlag)
		} else {
			infer(&current.CuChromaQpOffsetEnabledFlag, 0)
		}

		if pps.DeblockingFilterOverrideEnabledFlag != 0 {
			flag(r, "deblocking_filter_override_flag", &current.DeblockingFilterOverrideFlag)
		} else {
			infer(&current.DeblockingFilterOverrideFlag, 0)
		}
		if current.DeblockingFilterOverrideFlag != 0 {
			flag(r, "slice_deblocking_filter_disabled_flag", &current.SliceDeblockingFilterDisabledFlag)
			if current.SliceDeblockingFilterDisabledFlag == 0 {
				se(r, "slice_beta_offset_div2", &current.SliceBetaOffsetDiv2, -6, +6)
				se(r, "slice_tc_offset_div2", &current.SliceTcOffsetDiv2, -6, +6)
			} else {
				infer(&current.SliceBetaOffsetDiv2, pps.PpsBetaOffsetDiv2)
				infer(&current.SliceTcOffsetDiv2, pps.PpsTcOffsetDiv2)
			}
		} else {
			infer(&current.SliceDeblockingFilterDisabledFlag,
				pps.PpsDeblockingFilterDisabledFlag)
			infer(&current.SliceBetaOffsetDiv2, pps.PpsBetaOffsetDiv2)
			infer(&current.SliceTcOffsetDiv2, pps.PpsTcOffsetDiv2)
		}
		if pps.PpsLoopFilterAcrossSlicesEnabledFlag != 0 &&
			(current.SliceSaoLumaFlag != 0 || current.SliceSaoChromaFlag != 0 ||
				current.SliceDeblockingFilterDisabledFlag == 0) {
			flag(r, "slice_loop_filter_across_slices_enabled_flag", &current.SliceLoopFilterAcrossSlicesEnabledFlag)
		} else {
			infer(&current.SliceLoopFilterAcrossSlicesEnabledFlag,
				pps.PpsLoopFilterAcrossSlicesEnabledFlag)
		}
	}

	if pps.TilesEnabledFlag != 0 || pps.EntropyCodingSyncEnabledFlag != 0 {
		var numEntryPointOffsetsLimit int
		if pps.TilesEnabledFlag == 0 && pps.EntropyCodingSyncEnabledFlag != 0 {
			numEntryPointOffsetsLimit = picHeightInCtbsY - 1
		} else if pps.TilesEnabledFlag != 0 && pps.EntropyCodingSyncEnabledFlag == 0 {
			numEntryPointOffsetsLimit =
				(int(pps.NumTileColumnsMinus1) + 1) * (int(pps.NumTileRowsMinus1) + 1)
		} else {
			numEntryPointOffsetsLimit =
				(int(pps.NumTileColumnsMinus1)+1)*picHeightInCtbsY - 1
		}
		ue(r, "num_entry_point_offsets", &current.NumEntryPointOffsets, 0, uint32(numEntryPointOffsetsLimit))

		if current.NumEntryPointOffsets > hevcMaxEntryPointOffsets {
			r.diag(LevelError, "Too many entry points: %d.",
				current.NumEntryPointOffsets)
			r.fail(ErrPatchWelcome)
		}

		if current.NumEntryPointOffsets > 0 {
			ue(r, "offset_len_minus1", &current.OffsetLenMinus1, 0, 31)
			for i := 0; i < int(current.NumEntryPointOffsets); i++ {
				ubs(r, int(current.OffsetLenMinus1)+1, "entry_point_offset_minus1[i]", &current.EntryPointOffsetMinus1[i], i)
			}
		}
	}

	if pps.SliceSegmentHeaderExtensionPresentFlag != 0 {
		ue(r, "slice_segment_header_extension_length", &current.SliceSegmentHeaderExtensionLength, 0, 256)
		for i := 0; i < int(current.SliceSegmentHeaderExtensionLength); i++ {
			us(r, 8, "slice_segment_header_extension_data_byte[i]", &current.SliceSegmentHeaderExtensionDataByte[i], 0x00, 0xff, i)
		}
	}

	h.byteAlignment(r)
}

func (h *H265Context) seiBufferingPeriod(r *Reader, current *H265RawSEIBufferingPeriod, sei *SEIMessageState) {
	startPos := r.bitPosition()

	r.header("Buffering Period")

	ue(r, "bp_seq_parameter_set_id", &current.BpSeqParameterSetID, 0, hevcMaxSPSCount-1)

	sps := h.sps[current.BpSeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.",
			current.BpSeqParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeSPS = sps

	if sps.VuiParametersPresentFlag == 0 ||
		sps.Vui.VuiHrdParametersPresentFlag == 0 {
		r.diag(LevelError, "Buffering period SEI requires HRD parameters to be present in SPS.")
		r.fail(ErrInvalidData)
	}
	hrd := &sps.Vui.HrdParameters
	if hrd.NalHrdParametersPresentFlag == 0 &&
		hrd.VclHrdParametersPresentFlag == 0 {
		r.diag(LevelError, "Buffering period SEI requires NAL or VCL HRD parameters to be present.")
		r.fail(ErrInvalidData)
	}

	if hrd.SubPicHrdParamsPresentFlag == 0 {
		flag(r, "irap_cpb_params_present_flag", &current.IrapCpbParamsPresentFlag)
	} else {
		infer(&current.IrapCpbParamsPresentFlag, 0)
	}
	if current.IrapCpbParamsPresentFlag != 0 {
		length := int(hrd.AuCpbRemovalDelayLengthMinus1) + 1
		ub(r, length, "cpb_delay_offset", &current.CpbDelayOffset)
		length = int(hrd.DpbOutputDelayLengthMinus1) + 1
		ub(r, length, "dpb_delay_offset", &current.DpbDelayOffset)
	} else {
		infer(&current.CpbDelayOffset, 0)
		infer(&current.DpbDelayOffset, 0)
	}

	flag(r, "concatenation_flag", &current.ConcatenationFlag)

	length := int(hrd.AuCpbRemovalDelayLengthMinus1) + 1
	ub(r, length, "au_cpb_removal_delay_delta_minus1", &current.AuCpbRemovalDelayDeltaMinus1)

	if hrd.NalHrdParametersPresentFlag != 0 {
		for i := 0; i <= int(hrd.CpbCntMinus1[0]); i++ {
			length := int(hrd.InitialCpbRemovalDelayLengthMinus1) + 1

			ubs(r, length, "nal_initial_cpb_removal_delay[i]", &current.NalInitialCpbRemovalDelay[i], i)
			ubs(r, length, "nal_initial_cpb_removal_offset[i]", &current.NalInitialCpbRemovalOffset[i], i)

			if hrd.SubPicHrdParamsPresentFlag != 0 ||
				current.IrapCpbParamsPresentFlag != 0 {
				ubs(r, length, "nal_initial_alt_cpb_removal_delay[i]", &current.NalInitialAltCpbRemovalDelay[i], i)
				ubs(r, length, "nal_initial_alt_cpb_removal_offset[i]", &current.NalInitialAltCpbRemovalOffset[i], i)
			}
		}
	}
	if hrd.VclHrdParametersPresentFlag != 0 {
		for i := 0; i <= int(hrd.CpbCntMinus1[0]); i++ {
			length := int(hrd.InitialCpbRemovalDelayLengthMinus1) + 1

			ubs(r, length, "vcl_initial_cpb_removal_delay[i]", &current.VclInitialCpbRemovalDelay[i], i)
			ubs(r, length, "vcl_initial_cpb_removal_offset[i]", &current.VclInitialCpbRemovalOffset[i], i)

			if hrd.SubPicHrdParamsPresentFlag != 0 ||
				current.IrapCpbParamsPresentFlag != 0 {
				ubs(r, length, "vcl_initial_alt_cpb_removal_delay[i]", &current.VclInitialAltCpbRemovalDelay[i], i)
				ubs(r, length, "vcl_initial_alt_cpb_removal_offset[i]", &current.VclInitialAltCpbRemovalOffset[i], i)
			}
		}
	}

	endPos := r.bitPosition()
	if payloadExtensionPresent(r, sei.PayloadSize, endPos-startPos) {
		flag(r, "use_alt_cpb_params_flag", &current.UseAltCpbParamsFlag)
	} else {
		infer(&current.UseAltCpbParamsFlag, 0)
	}
}

func (h *H265Context) seiPicTiming(r *Reader, current *H265RawSEIPicTiming, _ *SEIMessageState) {
	r.header("Picture Timing")

	sps := h.activeSPS
	if sps == nil {
		r.diag(LevelError, "No active SPS for pic_timing.")
		r.fail(ErrInvalidData)
	}

	expectedSourceScanType := 2 -
		2*int(sps.ProfileTierLevel.GeneralInterlacedSourceFlag) -
		int(sps.ProfileTierLevel.GeneralProgressiveSourceFlag)

	if sps.Vui.FrameFieldInfoPresentFlag != 0 {
		u(r, 4, "pic_struct", &current.PicStruct, 0, 12)
		u(r, 2, "source_scan_type", &current.SourceScanType,
			uint32(cond(expectedSourceScanType >= 0, expectedSourceScanType, 0)),
			uint32(cond(expectedSourceScanType >= 0, expectedSourceScanType, 2)))
		flag(r, "duplicate_flag", &current.DuplicateFlag)
	} else {
		infer(&current.PicStruct, 0)
		infer(&current.SourceScanType,
			uint8(cond(expectedSourceScanType >= 0, expectedSourceScanType, 2)))
		infer(&current.DuplicateFlag, 0)
	}

	var hrd *H265RawHRDParameters
	if sps.VuiParametersPresentFlag != 0 &&
		sps.Vui.VuiHrdParametersPresentFlag != 0 {
		hrd = &sps.Vui.HrdParameters
	}
	if hrd != nil && (hrd.NalHrdParametersPresentFlag != 0 ||
		hrd.VclHrdParametersPresentFlag != 0) {
		length := int(hrd.AuCpbRemovalDelayLengthMinus1) + 1
		ub(r, length, "au_cpb_removal_delay_minus1", &current.AuCpbRemovalDelayMinus1)

		length = int(hrd.DpbOutputDelayLengthMinus1) + 1
		ub(r, length, "pic_dpb_output_delay", &current.PicDpbOutputDelay)

		if hrd.SubPicHrdParamsPresentFlag != 0 {
			length = int(hrd.DpbOutputDelayDuLengthMinus1) + 1
			ub(r, length, "pic_dpb_output_du_delay", &current.PicDpbOutputDuDelay)
		}

		if hrd.SubPicHrdParamsPresentFlag != 0 &&
			hrd.SubPicCpbParamsInPicTimingSeiFlag != 0 {
			// Each decoding unit must contain at least one slice segment.
			ue(r, "num_decoding_units_minus1", &current.NumDecodingUnitsMinus1, 0, hevcMaxSliceSegments)
			flag(r, "du_common_cpb_removal_delay_flag", &current.DuCommonCpbRemovalDelayFlag)

			length = int(hrd.DuCpbRemovalDelayIncrementLengthMinus1) + 1
			if current.DuCommonCpbRemovalDelayFlag != 0 {
				ub(r, length, "du_common_cpb_removal_delay_increment_minus1", &current.DuCommonCpbRemovalDelayIncrementMinus1)
			}

			for i := 0; i <= int(current.NumDecodingUnitsMinus1); i++ {
				ues(r, "num_nalus_in_du_minus1[i]",
					&current.NumNalusInDuMinus1[i], 0, hevcMaxSliceSegments, i)
				if current.DuCommonCpbRemovalDelayFlag == 0 &&
					i < int(current.NumDecodingUnitsMinus1) {
					ubs(r, length, "du_cpb_removal_delay_increment_minus1[i]", &current.DuCpbRemovalDelayIncrementMinus1[i], i)
				}
			}
		}
	}
}

func (h *H265Context) seiPanScanRect(r *Reader, current *H265RawSEIPanScanRect, _ *SEIMessageState) {
	r.header("Pan-Scan Rectangle")

	ue(r, "pan_scan_rect_id", &current.PanScanRectID, 0, math.MaxUint32-1)
	flag(r, "pan_scan_rect_cancel_flag", &current.PanScanRectCancelFlag)

	if current.PanScanRectCancelFlag == 0 {
		ue(r, "pan_scan_cnt_minus1", &current.PanScanCntMinus1, 0, 2)

		for i := 0; i <= int(current.PanScanCntMinus1); i++ {
			ses(r, "pan_scan_rect_left_offset[i]", &current.PanScanRectLeftOffset[i], int32MinPlus1, math.MaxInt32, i)
			ses(r, "pan_scan_rect_right_offset[i]", &current.PanScanRectRightOffset[i], int32MinPlus1, math.MaxInt32, i)
			ses(r, "pan_scan_rect_top_offset[i]", &current.PanScanRectTopOffset[i], int32MinPlus1, math.MaxInt32, i)
			ses(r, "pan_scan_rect_bottom_offset[i]", &current.PanScanRectBottomOffset[i], int32MinPlus1, math.MaxInt32, i)
		}

		flag(r, "pan_scan_rect_persistence_flag", &current.PanScanRectPersistenceFlag)
	}
}

func (h *H265Context) seiRecoveryPoint(r *Reader, current *H265RawSEIRecoveryPoint, _ *SEIMessageState) {
	r.header("Recovery Point")

	se(r, "recovery_poc_cnt", &current.RecoveryPocCnt, -32768, 32767)

	flag(r, "exact_match_flag", &current.ExactMatchFlag)
	flag(r, "broken_link_flag", &current.BrokenLinkFlag)
}

func (h *H265Context) seiFilmGrainCharacteristics(r *Reader, current *H265RawFilmGrainCharacteristics, _ *SEIMessageState) {
	sps := h.activeSPS

	r.header("Film Grain Characteristics")

	flag(r, "film_grain_characteristics_cancel_flag", &current.FilmGrainCharacteristicsCancelFlag)
	if current.FilmGrainCharacteristicsCancelFlag == 0 {
		var filmGrainBitDepth [3]int

		u(r, 2, "film_grain_model_id", &current.FilmGrainModelID, 0, 1)
		flag(r, "separate_colour_description_present_flag", &current.SeparateColourDescriptionPresentFlag)
		if current.SeparateColourDescriptionPresentFlag != 0 {
			ub(r, 3, "film_grain_bit_depth_luma_minus8", &current.FilmGrainBitDepthLumaMinus8)
			ub(r, 3, "film_grain_bit_depth_chroma_minus8", &current.FilmGrainBitDepthChromaMinus8)
			flag(r, "film_grain_full_range_flag", &current.FilmGrainFullRangeFlag)
			ub(r, 8, "film_grain_colour_primaries", &current.FilmGrainColourPrimaries)
			ub(r, 8, "film_grain_transfer_characteristics", &current.FilmGrainTransferCharacteristics)
			ub(r, 8, "film_grain_matrix_coeffs", &current.FilmGrainMatrixCoeffs)
		} else {
			if sps == nil {
				r.diag(LevelError, "No active SPS for film_grain_characteristics.")
				r.fail(ErrInvalidData)
			}
			infer(&current.FilmGrainBitDepthLumaMinus8, sps.BitDepthLumaMinus8)
			infer(&current.FilmGrainBitDepthChromaMinus8, sps.BitDepthChromaMinus8)
			infer(&current.FilmGrainFullRangeFlag, sps.Vui.VideoFullRangeFlag)
			infer(&current.FilmGrainColourPrimaries, sps.Vui.ColourPrimaries)
			infer(&current.FilmGrainTransferCharacteristics, sps.Vui.TransferCharacteristics)
			infer(&current.FilmGrainMatrixCoeffs, sps.Vui.MatrixCoefficients)
		}

		filmGrainBitDepth[0] = int(current.FilmGrainBitDepthLumaMinus8) + 8
		filmGrainBitDepth[1] = int(current.FilmGrainBitDepthChromaMinus8) + 8
		filmGrainBitDepth[2] = filmGrainBitDepth[1]

		u(r, 2, "blending_mode_id", &current.BlendingModeID, 0, 1)
		ub(r, 4, "log2_scale_factor", &current.Log2ScaleFactor)
		for c := 0; c < 3; c++ {
			flags(r, "comp_model_present_flag[c]", &current.CompModelPresentFlag[c], c)
		}
		for c := 0; c < 3; c++ {
			if current.CompModelPresentFlag[c] != 0 {
				ubs(r, 8, "num_intensity_intervals_minus1[c]", &current.NumIntensityIntervalsMinus1[c], c)
				us(r, 3, "num_model_values_minus1[c]", &current.NumModelValuesMinus1[c], 0, 5, c)
				for i := 0; i <= int(current.NumIntensityIntervalsMinus1[c]); i++ {
					ubs(r, 8, "intensity_interval_lower_bound[c][i]", &current.IntensityIntervalLowerBound[c][i], c, i)
					ubs(r, 8, "intensity_interval_upper_bound[c][i]", &current.IntensityIntervalUpperBound[c][i], c, i)
					for j := 0; j <= int(current.NumModelValuesMinus1[c]); j++ {
						ses(r, "comp_model_value[c][i][j]", &current.CompModelValue[c][i][j],
							0-int32(current.FilmGrainModelID)*(1<<(filmGrainBitDepth[c]-1)),
							int32(1<<filmGrainBitDepth[c]-1)-int32(current.FilmGrainModelID)*(1<<(filmGrainBitDepth[c]-1)),
							c, i, j)
					}
				}
			}
		}
		flag(r, "film_grain_characteristics_persistence_flag", &current.FilmGrainCharacteristicsPersistenceFlag)
	}
}

func (h *H265Context) seiDisplayOrientation(r *Reader, current *H265RawSEIDisplayOrientation, _ *SEIMessageState) {
	r.header("Display Orientation")

	flag(r, "display_orientation_cancel_flag", &current.DisplayOrientationCancelFlag)
	if current.DisplayOrientationCancelFlag == 0 {
		flag(r, "hor_flip", &current.HorFlip)
		flag(r, "ver_flip", &current.VerFlip)
		ub(r, 16, "anticlockwise_rotation", &current.AnticlockwiseRotation)
		flag(r, "display_orientation_persistence_flag", &current.DisplayOrientationPersistenceFlag)
	}
}

func (h *H265Context) seiActiveParameterSets(r *Reader, current *H265RawSEIActiveParameterSets, _ *SEIMessageState) {
	r.header("Active Parameter Sets")

	u(r, 4, "active_video_parameter_set_id", &current.ActiveVideoParameterSetID, 0, hevcMaxVPSCount)
	vps := h.vps[current.ActiveVideoParameterSetID]
	if vps == nil {
		r.diag(LevelError, "VPS id %d not available for active parameter sets.",
			current.ActiveVideoParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeVPS = vps

	flag(r, "self_contained_cvs_flag", &current.SelfContainedCvsFlag)
	flag(r, "no_parameter_set_update_flag", &current.NoParameterSetUpdateFlag)

	ue(r, "num_sps_ids_minus1", &current.NumSpsIdsMinus1, 0, hevcMaxSPSCount-1)
	for i := 0; i <= int(current.NumSpsIdsMinus1); i++ {
		ues(r, "active_seq_parameter_set_id[i]", &current.ActiveSeqParameterSetID[i], 0, hevcMaxSPSCount-1, i)
	}

	for i := int(vps.VpsBaseLayerInternalFlag); i <= min(62, int(vps.VpsMaxLayersMinus1)); i++ {
		ues(r, "layer_sps_idx[i]", &current.LayerSpsIdx[i], 0, uint32(current.NumSpsIdsMinus1), i)

		if i == 0 {
			h.activeSPS = h.sps[current.ActiveSeqParameterSetID[current.LayerSpsIdx[0]]]
		}
	}
}

func (h *H265Context) seiDecodedPictureHash(r *Reader, current *H265RawSEIDecodedPictureHash, _ *SEIMessageState) {
	sps := h.activeSPS

	r.header("Decoded Picture Hash")

	if sps == nil {
		r.diag(LevelError, "No active SPS for decoded picture hash.")
		r.fail(ErrInvalidData)
	}

	u(r, 8, "hash_type", &current.HashType, 0, 2)

	for c := 0; c < cond(sps.ChromaFormatIdc == 0, 1, 3); c++ {
		if current.HashType == 0 {
			for i := 0; i < 16; i++ {
				us(r, 8, "picture_md5[c][i]", &current.PictureMd5[c][i], 0x00, 0xff, c, i)
			}
		} else if current.HashType == 1 {
			us(r, 16, "picture_crc[c]", &current.PictureCrc[c], 0x0000, 0xffff, c)
		} else if current.HashType == 2 {
			us(r, 32, "picture_checksum[c]", &current.PictureChecksum[c], 0x00000000, 0xffffffff, c)
		}
	}
}

func (h *H265Context) seiTimeCode(r *Reader, current *H265RawSEITimeCode, _ *SEIMessageState) {
	r.header("Time Code")

	u(r, 2, "num_clock_ts", &current.NumClockTs, 1, 3)

	for i := 0; i < int(current.NumClockTs); i++ {
		flags(r, "clock_timestamp_flag[i]", &current.ClockTimestampFlag[i], i)

		if current.ClockTimestampFlag[i] != 0 {
			flags(r, "units_field_based_flag[i]", &current.UnitsFieldBasedFlag[i], i)
			us(r, 5, "counting_type[i]", &current.CountingType[i], 0, 6, i)
			flags(r, "full_timestamp_flag[i]", &current.FullTimestampFlag[i], i)
			flags(r, "discontinuity_flag[i]", &current.DiscontinuityFlag[i], i)
			flags(r, "cnt_dropped_flag[i]", &current.CntDroppedFlag[i], i)

			ubs(r, 9, "n_frames[i]", &current.NFrames[i], i)

			if current.FullTimestampFlag[i] != 0 {
				us(r, 6, "seconds_value[i]", &current.SecondsValue[i], 0, 59, i)
				us(r, 6, "minutes_value[i]", &current.MinutesValue[i], 0, 59, i)
				us(r, 5, "hours_value[i]", &current.HoursValue[i], 0, 23, i)
			} else {
				flags(r, "seconds_flag[i]", &current.SecondsFlag[i], i)
				if current.SecondsFlag[i] != 0 {
					us(r, 6, "seconds_value[i]", &current.SecondsValue[i], 0, 59, i)
					flags(r, "minutes_flag[i]", &current.MinutesFlag[i], i)
					if current.MinutesFlag[i] != 0 {
						us(r, 6, "minutes_value[i]", &current.MinutesValue[i], 0, 59, i)
						flags(r, "hours_flag[i]", &current.HoursFlag[i], i)
						if current.HoursFlag[i] != 0 {
							us(r, 5, "hours_value[i]", &current.HoursValue[i], 0, 23, i)
						}
					}
				}
			}

			ubs(r, 5, "time_offset_length[i]", &current.TimeOffsetLength[i], i)
			if current.TimeOffsetLength[i] > 0 {
				ibs(r, int(current.TimeOffsetLength[i]), "time_offset_value[i]", &current.TimeOffsetValue[i], i)
			} else {
				infer(&current.TimeOffsetValue[i], 0)
			}
		}
	}
}

func (h *H265Context) seiAlphaChannelInfo(r *Reader, current *H265RawSEIAlphaChannelInfo, _ *SEIMessageState) {
	r.header("Alpha Channel Information")

	flag(r, "alpha_channel_cancel_flag", &current.AlphaChannelCancelFlag)
	if current.AlphaChannelCancelFlag == 0 {
		ub(r, 3, "alpha_channel_use_idc", &current.AlphaChannelUseIdc)
		ub(r, 3, "alpha_channel_bit_depth_minus8", &current.AlphaChannelBitDepthMinus8)
		length := int(current.AlphaChannelBitDepthMinus8) + 9
		ub(r, length, "alpha_transparent_value", &current.AlphaTransparentValue)
		ub(r, length, "alpha_opaque_value", &current.AlphaOpaqueValue)
		flag(r, "alpha_channel_incr_flag", &current.AlphaChannelIncrFlag)
		flag(r, "alpha_channel_clip_flag", &current.AlphaChannelClipFlag)
		if current.AlphaChannelClipFlag != 0 {
			flag(r, "alpha_channel_clip_type_flag", &current.AlphaChannelClipTypeFlag)
		}
	} else {
		infer(&current.AlphaChannelUseIdc, 2)
		infer(&current.AlphaChannelIncrFlag, 0)
		infer(&current.AlphaChannelClipFlag, 0)
	}
}

func (h *H265Context) sei3DReferenceDisplaysInfo(r *Reader, current *H265RawSEI3DReferenceDisplaysInfo, _ *SEIMessageState) {
	r.header("Three Dimensional Reference Displays Information")

	ue(r, "prec_ref_display_width", &current.PrecRefDisplayWidth, 0, 31)
	flag(r, "ref_viewing_distance_flag", &current.RefViewingDistanceFlag)
	if current.RefViewingDistanceFlag != 0 {
		ue(r, "prec_ref_viewing_dist", &current.PrecRefViewingDist, 0, 31)
	}
	ue(r, "num_ref_displays_minus1", &current.NumRefDisplaysMinus1, 0, 31)
	for i := 0; i <= int(current.NumRefDisplaysMinus1); i++ {
		ues(r, "left_view_id[i]", &current.LeftViewID[i], 0, maxUintBits(15), i)
		ues(r, "right_view_id[i]", &current.RightViewID[i], 0, maxUintBits(15), i)
		us(r, 6, "exponent_ref_display_width[i]", &current.ExponentRefDisplayWidth[i], 0, 62, i)
		var length int
		if current.ExponentRefDisplayWidth[i] == 0 {
			length = max(0, int(current.PrecRefDisplayWidth)-30)
		} else {
			length = max(0, int(current.ExponentRefDisplayWidth[i])+
				int(current.PrecRefDisplayWidth)-31)
		}

		if length > 32 {
			r.diag(LevelError, "refDispWidthBits > 32 is not supported")
			r.fail(ErrPatchWelcome)
		}

		if length != 0 {
			ubs(r, length, "mantissa_ref_display_width[i]", &current.MantissaRefDisplayWidth[i], i)
		} else {
			infer(&current.MantissaRefDisplayWidth[i], 0)
		}
		if current.RefViewingDistanceFlag != 0 {
			us(r, 6, "exponent_ref_viewing_distance[i]", &current.ExponentRefViewingDistance[i], 0, 62, i)
			if current.ExponentRefViewingDistance[i] == 0 {
				length = max(0, int(current.PrecRefViewingDist)-30)
			} else {
				length = max(0, int(current.ExponentRefViewingDistance[i])+
					int(current.PrecRefViewingDist)-31)
			}

			if length > 32 {
				r.diag(LevelError, "refViewDistBits > 32 is not supported")
				r.fail(ErrPatchWelcome)
			}

			if length != 0 {
				ubs(r, length, "mantissa_ref_viewing_distance[i]", &current.MantissaRefViewingDistance[i], i)
			} else {
				infer(&current.MantissaRefViewingDistance[i], 0)
			}
		}
		flags(r, "additional_shift_present_flag[i]", &current.AdditionalShiftPresentFlag[i], i)
		if current.AdditionalShiftPresentFlag[i] != 0 {
			us(r, 10, "num_sample_shift_plus512[i]", &current.NumSampleShiftPlus512[i], 0, 1023, i)
		}
	}
	flag(r, "three_dimensional_reference_displays_extension_flag", &current.ThreeDimensionalReferenceDisplaysExtensionFlag)
}

func (h *H265Context) sei(r *Reader, current *H265RawSEI, prefix bool) {
	if prefix {
		r.header("Prefix Supplemental Enhancement Information")
	} else {
		r.header("Suffix Supplemental Enhancement Information")
	}

	h.nalUnitHeader(r, &current.NalUnitHeader,
		cond(prefix, hevcNALSEIPrefix, hevcNALSEISuffix))

	seiMessageList(h, r, &current.MessageList)

	h.rbspTrailingBits(r)
}

func (h *H265Context) filler(r *Reader, current *H265RawFiller) {
	r.header("Filler Data")

	h.nalUnitHeader(r, &current.NalUnitHeader, hevcNALFDNUT)

	for r.br.showBits(8) == 0xff {
		fixed(r, 8, "ff_byte", 0xff)
		current.FillerSize++
	}

	h.rbspTrailingBits(r)
}
