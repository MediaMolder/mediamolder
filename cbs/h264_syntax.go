// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Line-for-line port of libavcodec/cbs_h264_syntax_template.c (READ side).
// Element names, ranges and control flow mirror the C template exactly; see
// that file for the specification references.

package cbs

import "math"

const int32MinPlus1 = math.MinInt32 + 1

// ceilLog2 is av_ceil_log2.
func ceilLog2(x uint32) int {
	if x <= 1 {
		return 0
	}
	return log2u32(x-1) + 1
}

func (h *H264Context) rbspTrailingBits(r *Reader) {
	fixed(r, 1, "rbsp_stop_one_bit", 1)
	for r.byteAlignment() != 0 {
		fixed(r, 1, "rbsp_alignment_zero_bit", 0)
	}
}

func (h *H264Context) nalUnitHeader(r *Reader, current *H264RawNALUnitHeader, validTypeMask uint32) {
	fixed(r, 1, "forbidden_zero_bit", 0)
	ub(r, 2, "nal_ref_idc", &current.NalRefIdc)
	ub(r, 5, "nal_unit_type", &current.NalUnitType)

	if 1<<current.NalUnitType&validTypeMask == 0 {
		r.diag(LevelError, "Invalid NAL unit type %d.", current.NalUnitType)
		r.fail(ErrInvalidData)
	}

	if current.NalUnitType == 14 ||
		current.NalUnitType == 20 ||
		current.NalUnitType == 21 {
		if current.NalUnitType != 21 {
			flag(r, "svc_extension_flag", &current.SVCExtensionFlag)
		} else {
			flag(r, "avc_3d_extension_flag", &current.AVC3DExtensionFlag)
		}

		if current.SVCExtensionFlag != 0 {
			r.diag(LevelError, "SVC not supported.")
			r.fail(ErrPatchWelcome)
		} else if current.AVC3DExtensionFlag != 0 {
			r.diag(LevelError, "3DAVC not supported.")
			r.fail(ErrPatchWelcome)
		} else {
			r.diag(LevelError, "MVC not supported.")
			r.fail(ErrPatchWelcome)
		}
	}
}

func (h *H264Context) scalingList(r *Reader, current *H264RawScalingList, sizeOfScalingList int) {
	scale := 8
	for i := 0; i < sizeOfScalingList; i++ {
		ses(r, "delta_scale[i]", &current.DeltaScale[i], -128, +127, i)
		scale = (scale + int(current.DeltaScale[i]) + 256) % 256
		if scale == 0 {
			break
		}
	}
}

func (h *H264Context) hrdParameters(r *Reader, current *H264RawHRD) {
	ue(r, "cpb_cnt_minus1", &current.CpbCntMinus1, 0, 31)
	ub(r, 4, "bit_rate_scale", &current.BitRateScale)
	ub(r, 4, "cpb_size_scale", &current.CpbSizeScale)

	for i := 0; i <= int(current.CpbCntMinus1); i++ {
		ues(r, "bit_rate_value_minus1[i]", &current.BitRateValueMinus1[i], 0, math.MaxUint32-1, i)
		ues(r, "cpb_size_value_minus1[i]", &current.CpbSizeValueMinus1[i], 0, math.MaxUint32-1, i)
		flags(r, "cbr_flag[i]", &current.CbrFlag[i], i)
	}

	ub(r, 5, "initial_cpb_removal_delay_length_minus1", &current.InitialCpbRemovalDelayLengthMinus1)
	ub(r, 5, "cpb_removal_delay_length_minus1", &current.CpbRemovalDelayLengthMinus1)
	ub(r, 5, "dpb_output_delay_length_minus1", &current.DpbOutputDelayLengthMinus1)
	ub(r, 5, "time_offset_length", &current.TimeOffsetLength)
}

func (h *H264Context) vuiParameters(r *Reader, current *H264RawVUI, sps *H264RawSPS) {
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

	flag(r, "timing_info_present_flag", &current.TimingInfoPresentFlag)
	if current.TimingInfoPresentFlag != 0 {
		u(r, 32, "num_units_in_tick", &current.NumUnitsInTick, 1, math.MaxUint32)
		u(r, 32, "time_scale", &current.TimeScale, 1, math.MaxUint32)
		flag(r, "fixed_frame_rate_flag", &current.FixedFrameRateFlag)
	} else {
		infer(&current.FixedFrameRateFlag, 0)
	}

	flag(r, "nal_hrd_parameters_present_flag", &current.NalHrdParametersPresentFlag)
	if current.NalHrdParametersPresentFlag != 0 {
		h.hrdParameters(r, &current.NalHrdParameters)
	}

	flag(r, "vcl_hrd_parameters_present_flag", &current.VclHrdParametersPresentFlag)
	if current.VclHrdParametersPresentFlag != 0 {
		h.hrdParameters(r, &current.VclHrdParameters)
	}

	if current.NalHrdParametersPresentFlag != 0 ||
		current.VclHrdParametersPresentFlag != 0 {
		flag(r, "low_delay_hrd_flag", &current.LowDelayHrdFlag)
	} else {
		infer(&current.LowDelayHrdFlag, 1-current.FixedFrameRateFlag)
	}

	flag(r, "pic_struct_present_flag", &current.PicStructPresentFlag)

	flag(r, "bitstream_restriction_flag", &current.BitstreamRestrictionFlag)
	if current.BitstreamRestrictionFlag != 0 {
		flag(r, "motion_vectors_over_pic_boundaries_flag", &current.MotionVectorsOverPicBoundariesFlag)
		ue(r, "max_bytes_per_pic_denom", &current.MaxBytesPerPicDenom, 0, 16)
		ue(r, "max_bits_per_mb_denom", &current.MaxBitsPerMbDenom, 0, 16)
		// The current version of the standard constrains this to be in
		// [0,15], but older versions allow 16.
		ue(r, "log2_max_mv_length_horizontal", &current.Log2MaxMvLengthHorizontal, 0, 16)
		ue(r, "log2_max_mv_length_vertical", &current.Log2MaxMvLengthVertical, 0, 16)
		ue(r, "max_num_reorder_frames", &current.MaxNumReorderFrames, 0, h264MaxDPBFrames)
		ue(r, "max_dec_frame_buffering", &current.MaxDecFrameBuffering, 0, h264MaxDPBFrames)
	} else {
		infer(&current.MotionVectorsOverPicBoundariesFlag, 1)
		infer(&current.MaxBytesPerPicDenom, 2)
		infer(&current.MaxBitsPerMbDenom, 1)
		infer(&current.Log2MaxMvLengthHorizontal, 15)
		infer(&current.Log2MaxMvLengthVertical, 15)

		if (sps.ProfileIdc == 44 || sps.ProfileIdc == 86 ||
			sps.ProfileIdc == 100 || sps.ProfileIdc == 110 ||
			sps.ProfileIdc == 122 || sps.ProfileIdc == 244) &&
			sps.ConstraintSet3Flag != 0 {
			infer(&current.MaxNumReorderFrames, 0)
			infer(&current.MaxDecFrameBuffering, 0)
		} else {
			infer(&current.MaxNumReorderFrames, h264MaxDPBFrames)
			infer(&current.MaxDecFrameBuffering, h264MaxDPBFrames)
		}
	}
}

func (h *H264Context) vuiParametersDefault(r *Reader, current *H264RawVUI, sps *H264RawSPS) {
	infer(&current.AspectRatioIdc, 0)

	infer(&current.VideoFormat, 5)
	infer(&current.VideoFullRangeFlag, 0)
	infer(&current.ColourPrimaries, 2)
	infer(&current.TransferCharacteristics, 2)
	infer(&current.MatrixCoefficients, 2)

	infer(&current.ChromaSampleLocTypeTopField, 0)
	infer(&current.ChromaSampleLocTypeBottomField, 0)

	infer(&current.FixedFrameRateFlag, 0)
	infer(&current.LowDelayHrdFlag, 1)

	infer(&current.PicStructPresentFlag, 0)

	infer(&current.MotionVectorsOverPicBoundariesFlag, 1)
	infer(&current.MaxBytesPerPicDenom, 2)
	infer(&current.MaxBitsPerMbDenom, 1)
	infer(&current.Log2MaxMvLengthHorizontal, 15)
	infer(&current.Log2MaxMvLengthVertical, 15)

	if (sps.ProfileIdc == 44 || sps.ProfileIdc == 86 ||
		sps.ProfileIdc == 100 || sps.ProfileIdc == 110 ||
		sps.ProfileIdc == 122 || sps.ProfileIdc == 244) &&
		sps.ConstraintSet3Flag != 0 {
		infer(&current.MaxNumReorderFrames, 0)
		infer(&current.MaxDecFrameBuffering, 0)
	} else {
		infer(&current.MaxNumReorderFrames, h264MaxDPBFrames)
		infer(&current.MaxDecFrameBuffering, h264MaxDPBFrames)
	}
}

func (h *H264Context) readSPS(r *Reader, current *H264RawSPS) {
	r.header("Sequence Parameter Set")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALSPS)

	ub(r, 8, "profile_idc", &current.ProfileIdc)

	flag(r, "constraint_set0_flag", &current.ConstraintSet0Flag)
	flag(r, "constraint_set1_flag", &current.ConstraintSet1Flag)
	flag(r, "constraint_set2_flag", &current.ConstraintSet2Flag)
	flag(r, "constraint_set3_flag", &current.ConstraintSet3Flag)
	flag(r, "constraint_set4_flag", &current.ConstraintSet4Flag)
	flag(r, "constraint_set5_flag", &current.ConstraintSet5Flag)

	u(r, 2, "reserved_zero_2bits", &current.ReservedZero2Bits, 0, 0)

	ub(r, 8, "level_idc", &current.LevelIdc)

	ue(r, "seq_parameter_set_id", &current.SeqParameterSetID, 0, 31)

	if current.ProfileIdc == 100 || current.ProfileIdc == 110 ||
		current.ProfileIdc == 122 || current.ProfileIdc == 244 ||
		current.ProfileIdc == 44 || current.ProfileIdc == 83 ||
		current.ProfileIdc == 86 || current.ProfileIdc == 118 ||
		current.ProfileIdc == 128 || current.ProfileIdc == 138 {
		ue(r, "chroma_format_idc", &current.ChromaFormatIdc, 0, 3)

		if current.ChromaFormatIdc == 3 {
			flag(r, "separate_colour_plane_flag", &current.SeparateColourPlaneFlag)
		} else {
			infer(&current.SeparateColourPlaneFlag, 0)
		}

		ue(r, "bit_depth_luma_minus8", &current.BitDepthLumaMinus8, 0, 6)
		ue(r, "bit_depth_chroma_minus8", &current.BitDepthChromaMinus8, 0, 6)

		flag(r, "qpprime_y_zero_transform_bypass_flag", &current.QpprimeYZeroTransformBypassFlag)

		flag(r, "seq_scaling_matrix_present_flag", &current.SeqScalingMatrixPresentFlag)
		if current.SeqScalingMatrixPresentFlag != 0 {
			for i := 0; i < cond(current.ChromaFormatIdc != 3, 8, 12); i++ {
				flags(r, "seq_scaling_list_present_flag[i]", &current.SeqScalingListPresentFlag[i], i)
				if current.SeqScalingListPresentFlag[i] != 0 {
					if i < 6 {
						h.scalingList(r, &current.ScalingList4x4[i], 16)
					} else {
						h.scalingList(r, &current.ScalingList8x8[i-6], 64)
					}
				}
			}
		}
	} else {
		infer(&current.ChromaFormatIdc, cond[uint8](current.ProfileIdc == 183, 0, 1))

		infer(&current.SeparateColourPlaneFlag, 0)
		infer(&current.BitDepthLumaMinus8, 0)
		infer(&current.BitDepthChromaMinus8, 0)
	}

	ue(r, "log2_max_frame_num_minus4", &current.Log2MaxFrameNumMinus4, 0, 12)
	ue(r, "pic_order_cnt_type", &current.PicOrderCntType, 0, 2)

	if current.PicOrderCntType == 0 {
		ue(r, "log2_max_pic_order_cnt_lsb_minus4", &current.Log2MaxPicOrderCntLsbMinus4, 0, 12)
	} else if current.PicOrderCntType == 1 {
		flag(r, "delta_pic_order_always_zero_flag", &current.DeltaPicOrderAlwaysZeroFlag)
		se(r, "offset_for_non_ref_pic", &current.OffsetForNonRefPic, int32MinPlus1, math.MaxInt32)
		se(r, "offset_for_top_to_bottom_field", &current.OffsetForTopToBottomField, int32MinPlus1, math.MaxInt32)
		ue(r, "num_ref_frames_in_pic_order_cnt_cycle", &current.NumRefFramesInPicOrderCntCycle, 0, 255)

		for i := 0; i < int(current.NumRefFramesInPicOrderCntCycle); i++ {
			ses(r, "offset_for_ref_frame[i]", &current.OffsetForRefFrame[i], int32MinPlus1, math.MaxInt32, i)
		}
	}

	ue(r, "max_num_ref_frames", &current.MaxNumRefFrames, 0, h264MaxDPBFrames)
	flag(r, "gaps_in_frame_num_allowed_flag", &current.GapsInFrameNumAllowedFlag)

	ue(r, "pic_width_in_mbs_minus1", &current.PicWidthInMbsMinus1, 0, h264MaxMBWidth)
	ue(r, "pic_height_in_map_units_minus1", &current.PicHeightInMapUnitsMinus1, 0, h264MaxMBHeight)

	flag(r, "frame_mbs_only_flag", &current.FrameMbsOnlyFlag)
	if current.FrameMbsOnlyFlag == 0 {
		flag(r, "mb_adaptive_frame_field_flag", &current.MbAdaptiveFrameFieldFlag)
	}

	flag(r, "direct_8x8_inference_flag", &current.Direct8x8InferenceFlag)

	flag(r, "frame_cropping_flag", &current.FrameCroppingFlag)
	if current.FrameCroppingFlag != 0 {
		ue(r, "frame_crop_left_offset", &current.FrameCropLeftOffset, 0, h264MaxWidth)
		ue(r, "frame_crop_right_offset", &current.FrameCropRightOffset, 0, h264MaxWidth)
		ue(r, "frame_crop_top_offset", &current.FrameCropTopOffset, 0, h264MaxHeight)
		ue(r, "frame_crop_bottom_offset", &current.FrameCropBottomOffset, 0, h264MaxHeight)
	}

	flag(r, "vui_parameters_present_flag", &current.VuiParametersPresentFlag)
	if current.VuiParametersPresentFlag != 0 {
		h.vuiParameters(r, &current.Vui, current)
	} else {
		h.vuiParametersDefault(r, &current.Vui, current)
	}

	h.rbspTrailingBits(r)
}

func (h *H264Context) spsExtension(r *Reader, current *H264RawSPSExtension) {
	r.header("Sequence Parameter Set Extension")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALSPSExt)

	ue(r, "seq_parameter_set_id", &current.SeqParameterSetID, 0, 31)

	ue(r, "aux_format_idc", &current.AuxFormatIdc, 0, 3)

	if current.AuxFormatIdc != 0 {
		ue(r, "bit_depth_aux_minus8", &current.BitDepthAuxMinus8, 0, 4)
		flag(r, "alpha_incr_flag", &current.AlphaIncrFlag)

		bits := int(current.BitDepthAuxMinus8) + 9
		ub(r, bits, "alpha_opaque_value", &current.AlphaOpaqueValue)
		ub(r, bits, "alpha_transparent_value", &current.AlphaTransparentValue)
	}

	flag(r, "additional_extension_flag", &current.AdditionalExtensionFlag)

	h.rbspTrailingBits(r)
}

func (h *H264Context) readPPS(r *Reader, current *H264RawPPS) {
	r.header("Picture Parameter Set")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALPPS)

	ue(r, "pic_parameter_set_id", &current.PicParameterSetID, 0, 255)
	ue(r, "seq_parameter_set_id", &current.SeqParameterSetID, 0, 31)

	sps := h.sps[current.SeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.", current.SeqParameterSetID)
		r.fail(ErrInvalidData)
	}

	flag(r, "entropy_coding_mode_flag", &current.EntropyCodingModeFlag)
	flag(r, "bottom_field_pic_order_in_frame_present_flag", &current.BottomFieldPicOrderInFramePresentFlag)

	ue(r, "num_slice_groups_minus1", &current.NumSliceGroupsMinus1, 0, 7)
	if current.NumSliceGroupsMinus1 > 0 {
		picSize := (uint32(sps.PicWidthInMbsMinus1) + 1) *
			(uint32(sps.PicHeightInMapUnitsMinus1) + 1)

		ue(r, "slice_group_map_type", &current.SliceGroupMapType, 0, 6)

		if current.SliceGroupMapType == 0 {
			for iGroup := 0; iGroup <= int(current.NumSliceGroupsMinus1); iGroup++ {
				ues(r, "run_length_minus1[iGroup]", &current.RunLengthMinus1[iGroup], 0, picSize-1, iGroup)
			}
		} else if current.SliceGroupMapType == 2 {
			for iGroup := 0; iGroup < int(current.NumSliceGroupsMinus1); iGroup++ {
				ues(r, "top_left[iGroup]", &current.TopLeft[iGroup], 0, picSize-1, iGroup)
				ues(r, "bottom_right[iGroup]", &current.BottomRight[iGroup],
					uint32(current.TopLeft[iGroup]), picSize-1, iGroup)
			}
		} else if current.SliceGroupMapType == 3 ||
			current.SliceGroupMapType == 4 ||
			current.SliceGroupMapType == 5 {
			flag(r, "slice_group_change_direction_flag", &current.SliceGroupChangeDirectionFlag)
			ue(r, "slice_group_change_rate_minus1", &current.SliceGroupChangeRateMinus1, 0, picSize-1)
		} else if current.SliceGroupMapType == 6 {
			ue(r, "pic_size_in_map_units_minus1", &current.PicSizeInMapUnitsMinus1, picSize-1, picSize-1)

			current.SliceGroupID = make([]uint8, int(current.PicSizeInMapUnitsMinus1)+1)
			for i := 0; i <= int(current.PicSizeInMapUnitsMinus1); i++ {
				us(r, log2u32(2*uint32(current.NumSliceGroupsMinus1)+1),
					"slice_group_id[i]", &current.SliceGroupID[i],
					0, uint32(current.NumSliceGroupsMinus1), i)
			}
		}
	}

	ue(r, "num_ref_idx_l0_default_active_minus1", &current.NumRefIdxL0DefaultActiveMinus1, 0, 31)
	ue(r, "num_ref_idx_l1_default_active_minus1", &current.NumRefIdxL1DefaultActiveMinus1, 0, 31)

	flag(r, "weighted_pred_flag", &current.WeightedPredFlag)
	u(r, 2, "weighted_bipred_idc", &current.WeightedBipredIdc, 0, 2)

	se(r, "pic_init_qp_minus26", &current.PicInitQpMinus26, -26-6*int32(sps.BitDepthLumaMinus8), +25)
	se(r, "pic_init_qs_minus26", &current.PicInitQsMinus26, -26, +25)
	se(r, "chroma_qp_index_offset", &current.ChromaQpIndexOffset, -12, +12)

	flag(r, "deblocking_filter_control_present_flag", &current.DeblockingFilterControlPresentFlag)
	flag(r, "constrained_intra_pred_flag", &current.ConstrainedIntraPredFlag)
	flag(r, "redundant_pic_cnt_present_flag", &current.RedundantPicCntPresentFlag)

	if current.MoreRbspData = b2u8(r.moreRBSPData()); current.MoreRbspData != 0 {
		flag(r, "transform_8x8_mode_flag", &current.Transform8x8ModeFlag)

		flag(r, "pic_scaling_matrix_present_flag", &current.PicScalingMatrixPresentFlag)
		if current.PicScalingMatrixPresentFlag != 0 {
			for i := 0; i < 6+cond(sps.ChromaFormatIdc != 3, 2, 6)*int(current.Transform8x8ModeFlag); i++ {
				flags(r, "pic_scaling_list_present_flag[i]", &current.PicScalingListPresentFlag[i], i)
				if current.PicScalingListPresentFlag[i] != 0 {
					if i < 6 {
						h.scalingList(r, &current.ScalingList4x4[i], 16)
					} else {
						h.scalingList(r, &current.ScalingList8x8[i-6], 64)
					}
				}
			}
		}

		se(r, "second_chroma_qp_index_offset", &current.SecondChromaQpIndexOffset, -12, +12)
	} else {
		infer(&current.Transform8x8ModeFlag, 0)
		infer(&current.PicScalingMatrixPresentFlag, 0)
		infer(&current.SecondChromaQpIndexOffset, current.ChromaQpIndexOffset)
	}

	h.rbspTrailingBits(r)
}

func (h *H264Context) seiBufferingPeriod(r *Reader, current *H264RawSEIBufferingPeriod, _ *SEIMessageState) {
	r.header("Buffering Period")

	ue(r, "seq_parameter_set_id", &current.SeqParameterSetID, 0, 31)

	sps := h.sps[current.SeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.", current.SeqParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeSPS = sps

	if sps.Vui.NalHrdParametersPresentFlag != 0 {
		for i := 0; i <= int(sps.Vui.NalHrdParameters.CpbCntMinus1); i++ {
			length := int(sps.Vui.NalHrdParameters.InitialCpbRemovalDelayLengthMinus1) + 1
			current.Nal.InitialCpbRemovalDelay[i] = readUnsignedRaw(r,
				length, "initial_cpb_removal_delay[SchedSelIdx]", []int{i},
				1, maxUintBits(length))
			current.Nal.InitialCpbRemovalDelayOffset[i] = readUnsignedRaw(r,
				length, "initial_cpb_removal_delay_offset[SchedSelIdx]", []int{i},
				0, maxUintBits(length))
		}
	}

	if sps.Vui.VclHrdParametersPresentFlag != 0 {
		for i := 0; i <= int(sps.Vui.VclHrdParameters.CpbCntMinus1); i++ {
			length := int(sps.Vui.VclHrdParameters.InitialCpbRemovalDelayLengthMinus1) + 1
			current.Vcl.InitialCpbRemovalDelay[i] = readUnsignedRaw(r,
				length, "initial_cpb_removal_delay[SchedSelIdx]", []int{i},
				1, maxUintBits(length))
			current.Vcl.InitialCpbRemovalDelayOffset[i] = readUnsignedRaw(r,
				length, "initial_cpb_removal_delay_offset[SchedSelIdx]", []int{i},
				0, maxUintBits(length))
		}
	}
}

func (h *H264Context) seiPicTimestamp(r *Reader, current *H264RawSEIPicTimestamp, sps *H264RawSPS) {
	u(r, 2, "ct_type", &current.CtType, 0, 2)
	flag(r, "nuit_field_based_flag", &current.NuitFieldBasedFlag)
	u(r, 5, "counting_type", &current.CountingType, 0, 6)
	flag(r, "full_timestamp_flag", &current.FullTimestampFlag)
	flag(r, "discontinuity_flag", &current.DiscontinuityFlag)
	flag(r, "cnt_dropped_flag", &current.CntDroppedFlag)
	ub(r, 8, "n_frames", &current.NFrames)
	if current.FullTimestampFlag != 0 {
		u(r, 6, "seconds_value", &current.SecondsValue, 0, 59)
		u(r, 6, "minutes_value", &current.MinutesValue, 0, 59)
		u(r, 5, "hours_value", &current.HoursValue, 0, 23)
	} else {
		flag(r, "seconds_flag", &current.SecondsFlag)
		if current.SecondsFlag != 0 {
			u(r, 6, "seconds_value", &current.SecondsValue, 0, 59)
			flag(r, "minutes_flag", &current.MinutesFlag)
			if current.MinutesFlag != 0 {
				u(r, 6, "minutes_value", &current.MinutesValue, 0, 59)
				flag(r, "hours_flag", &current.HoursFlag)
				if current.HoursFlag != 0 {
					u(r, 5, "hours_value", &current.HoursValue, 0, 23)
				}
			}
		}
	}

	var timeOffsetLength uint8
	if sps.Vui.NalHrdParametersPresentFlag != 0 {
		timeOffsetLength = sps.Vui.NalHrdParameters.TimeOffsetLength
	} else if sps.Vui.VclHrdParametersPresentFlag != 0 {
		timeOffsetLength = sps.Vui.VclHrdParameters.TimeOffsetLength
	} else {
		timeOffsetLength = 24
	}

	if timeOffsetLength > 0 {
		ib(r, int(timeOffsetLength), "time_offset", &current.TimeOffset)
	} else {
		infer(&current.TimeOffset, 0)
	}
}

// h264GuessActiveSPS implements the "exactly one possible SPS" fallback
// shared by pic_timing and film_grain_characteristics.
func (h *H264Context) h264GuessActiveSPS() *H264RawSPS {
	sps := h.activeSPS
	if sps == nil {
		k := -1
		for i := 0; i < h264MaxSPSCount; i++ {
			if h.sps[i] != nil {
				if k >= 0 {
					k = -1
					break
				}
				k = i
			}
		}
		if k >= 0 {
			sps = h.sps[k]
		}
	}
	return sps
}

func (h *H264Context) seiPicTiming(r *Reader, current *H264RawSEIPicTiming, _ *SEIMessageState) {
	r.header("Picture Timing")

	sps := h.h264GuessActiveSPS()
	if sps == nil {
		r.diag(LevelError, "No active SPS for pic_timing.")
		r.fail(ErrInvalidData)
	}

	if sps.Vui.NalHrdParametersPresentFlag != 0 ||
		sps.Vui.VclHrdParametersPresentFlag != 0 {
		var hrd *H264RawHRD
		if sps.Vui.NalHrdParametersPresentFlag != 0 {
			hrd = &sps.Vui.NalHrdParameters
		} else {
			hrd = &sps.Vui.VclHrdParameters
		}

		ub(r, int(hrd.CpbRemovalDelayLengthMinus1)+1, "cpb_removal_delay", &current.CpbRemovalDelay)
		ub(r, int(hrd.DpbOutputDelayLengthMinus1)+1, "dpb_output_delay", &current.DpbOutputDelay)
	}

	if sps.Vui.PicStructPresentFlag != 0 {
		numClockTs := [9]uint8{1, 1, 1, 2, 2, 3, 3, 2, 3}

		u(r, 4, "pic_struct", &current.PicStruct, 0, 8)
		if current.PicStruct > 8 {
			r.fail(ErrInvalidData)
		}

		for i := 0; i < int(numClockTs[current.PicStruct]); i++ {
			flags(r, "clock_timestamp_flag[i]", &current.ClockTimestampFlag[i], i)
			if current.ClockTimestampFlag[i] != 0 {
				h.seiPicTimestamp(r, &current.Timestamp[i], sps)
			}
		}
	}
}

func (h *H264Context) seiPanScanRect(r *Reader, current *H264RawSEIPanScanRect, _ *SEIMessageState) {
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

		ue(r, "pan_scan_rect_repetition_period", &current.PanScanRectRepetitionPeriod, 0, 16384)
	}
}

func (h *H264Context) seiRecoveryPoint(r *Reader, current *H264RawSEIRecoveryPoint, _ *SEIMessageState) {
	r.header("Recovery Point")

	ue(r, "recovery_frame_cnt", &current.RecoveryFrameCnt, 0, 65535)
	flag(r, "exact_match_flag", &current.ExactMatchFlag)
	flag(r, "broken_link_flag", &current.BrokenLinkFlag)
	u(r, 2, "changing_slice_group_idc", &current.ChangingSliceGroupIdc, 0, 2)
}

func (h *H264Context) seiFilmGrainCharacteristics(r *Reader, current *H264RawFilmGrainCharacteristics, _ *SEIMessageState) {
	r.header("Film Grain Characteristics")

	sps := h.h264GuessActiveSPS()

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
			ub(r, 8, "film_grain_matrix_coefficients", &current.FilmGrainMatrixCoefficients)
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
			infer(&current.FilmGrainMatrixCoefficients, sps.Vui.MatrixCoefficients)
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
		ue(r, "film_grain_characteristics_repetition_period", &current.FilmGrainCharacteristicsRepetitionPeriod, 0, 16384)
	}
}

func (h *H264Context) seiFramePackingArrangement(r *Reader, current *H264RawSEIFramePackingArrangement, _ *SEIMessageState) {
	r.header("Frame Packing Arrangement")

	ue(r, "frame_packing_arrangement_id", &current.FramePackingArrangementID, 0, maxUintBits(31))
	flag(r, "frame_packing_arrangement_cancel_flag", &current.FramePackingArrangementCancelFlag)
	if current.FramePackingArrangementCancelFlag == 0 {
		u(r, 7, "frame_packing_arrangement_type", &current.FramePackingArrangementType, 0, 7)
		flag(r, "quincunx_sampling_flag", &current.QuincunxSamplingFlag)
		u(r, 6, "content_interpretation_type", &current.ContentInterpretationType, 0, 2)
		flag(r, "spatial_flipping_flag", &current.SpatialFlippingFlag)
		flag(r, "frame0_flipped_flag", &current.Frame0FlippedFlag)
		flag(r, "field_views_flag", &current.FieldViewsFlag)
		flag(r, "current_frame_is_frame0_flag", &current.CurrentFrameIsFrame0Flag)
		flag(r, "frame0_self_contained_flag", &current.Frame0SelfContainedFlag)
		flag(r, "frame1_self_contained_flag", &current.Frame1SelfContainedFlag)
		if current.QuincunxSamplingFlag == 0 && current.FramePackingArrangementType != 5 {
			ub(r, 4, "frame0_grid_position_x", &current.Frame0GridPositionX)
			ub(r, 4, "frame0_grid_position_y", &current.Frame0GridPositionY)
			ub(r, 4, "frame1_grid_position_x", &current.Frame1GridPositionX)
			ub(r, 4, "frame1_grid_position_y", &current.Frame1GridPositionY)
		}
		fixed(r, 8, "frame_packing_arrangement_reserved_byte", 0)
		ue(r, "frame_packing_arrangement_repetition_period", &current.FramePackingArrangementRepetitionPeriod, 0, 16384)
	}
	flag(r, "frame_packing_arrangement_extension_flag", &current.FramePackingArrangementExtensionFlag)
}

func (h *H264Context) seiDisplayOrientation(r *Reader, current *H264RawSEIDisplayOrientation, _ *SEIMessageState) {
	r.header("Display Orientation")

	flag(r, "display_orientation_cancel_flag", &current.DisplayOrientationCancelFlag)
	if current.DisplayOrientationCancelFlag == 0 {
		flag(r, "hor_flip", &current.HorFlip)
		flag(r, "ver_flip", &current.VerFlip)
		ub(r, 16, "anticlockwise_rotation", &current.AnticlockwiseRotation)
		ue(r, "display_orientation_repetition_period", &current.DisplayOrientationRepetitionPeriod, 0, 16384)
		flag(r, "display_orientation_extension_flag", &current.DisplayOrientationExtensionFlag)
	}
}

func (h *H264Context) sei(r *Reader, current *H264RawSEI) {
	r.header("Supplemental Enhancement Information")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALSEI)

	seiMessageList(h, r, &current.MessageList)

	h.rbspTrailingBits(r)
}

func (h *H264Context) aud(r *Reader, current *H264RawAUD) {
	r.header("Access Unit Delimiter")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALAUD)

	ub(r, 3, "primary_pic_type", &current.PrimaryPicType)

	h.rbspTrailingBits(r)
}

func (h *H264Context) refPicListModification(r *Reader, current *H264RawSliceHeader) {
	sps := h.activeSPS

	if current.SliceType%5 != 2 &&
		current.SliceType%5 != 4 {
		flag(r, "ref_pic_list_modification_flag_l0", &current.RefPicListModificationFlagL0)
		if current.RefPicListModificationFlagL0 != 0 {
			for i := 0; i < h264MaxRPLMCount; i++ {
				ue(r, "modification_of_pic_nums_idc",
					&current.RplmL0[i].ModificationOfPicNumsIdc, 0, 3)

				mopn := int(current.RplmL0[i].ModificationOfPicNumsIdc)
				if mopn == 3 {
					break
				}

				if mopn == 0 || mopn == 1 {
					ue(r, "abs_diff_pic_num_minus1",
						&current.RplmL0[i].AbsDiffPicNumMinus1,
						0, uint32(1+current.FieldPicFlag)*
							(1<<(sps.Log2MaxFrameNumMinus4+4)))
				} else if mopn == 2 {
					ue(r, "long_term_pic_num",
						&current.RplmL0[i].LongTermPicNum,
						0, uint32(sps.MaxNumRefFrames)-1)
				}
			}
		}
	}

	if current.SliceType%5 == 1 {
		flag(r, "ref_pic_list_modification_flag_l1", &current.RefPicListModificationFlagL1)
		if current.RefPicListModificationFlagL1 != 0 {
			for i := 0; i < h264MaxRPLMCount; i++ {
				ue(r, "modification_of_pic_nums_idc",
					&current.RplmL1[i].ModificationOfPicNumsIdc, 0, 3)

				mopn := int(current.RplmL1[i].ModificationOfPicNumsIdc)
				if mopn == 3 {
					break
				}

				if mopn == 0 || mopn == 1 {
					ue(r, "abs_diff_pic_num_minus1",
						&current.RplmL1[i].AbsDiffPicNumMinus1,
						0, uint32(1+current.FieldPicFlag)*
							(1<<(sps.Log2MaxFrameNumMinus4+4)))
				} else if mopn == 2 {
					ue(r, "long_term_pic_num",
						&current.RplmL1[i].LongTermPicNum,
						0, uint32(sps.MaxNumRefFrames)-1)
				}
			}
		}
	}
}

func (h *H264Context) predWeightTable(r *Reader, current *H264RawSliceHeader) {
	sps := h.activeSPS

	ue(r, "luma_log2_weight_denom", &current.LumaLog2WeightDenom, 0, 7)

	chroma := sps.SeparateColourPlaneFlag == 0 && sps.ChromaFormatIdc != 0
	if chroma {
		ue(r, "chroma_log2_weight_denom", &current.ChromaLog2WeightDenom, 0, 7)
	}

	for i := 0; i <= int(current.NumRefIdxL0ActiveMinus1); i++ {
		flags(r, "luma_weight_l0_flag[i]", &current.LumaWeightL0Flag[i], i)
		if current.LumaWeightL0Flag[i] != 0 {
			ses(r, "luma_weight_l0[i]", &current.LumaWeightL0[i], -128, +127, i)
			ses(r, "luma_offset_l0[i]", &current.LumaOffsetL0[i], -128, +127, i)
		}
		if chroma {
			flags(r, "chroma_weight_l0_flag[i]", &current.ChromaWeightL0Flag[i], i)
			if current.ChromaWeightL0Flag[i] != 0 {
				for j := 0; j < 2; j++ {
					ses(r, "chroma_weight_l0[i][j]", &current.ChromaWeightL0[i][j], -128, +127, i, j)
					ses(r, "chroma_offset_l0[i][j]", &current.ChromaOffsetL0[i][j], -128, +127, i, j)
				}
			}
		}
	}

	if current.SliceType%5 == 1 {
		for i := 0; i <= int(current.NumRefIdxL1ActiveMinus1); i++ {
			flags(r, "luma_weight_l1_flag[i]", &current.LumaWeightL1Flag[i], i)
			if current.LumaWeightL1Flag[i] != 0 {
				ses(r, "luma_weight_l1[i]", &current.LumaWeightL1[i], -128, +127, i)
				ses(r, "luma_offset_l1[i]", &current.LumaOffsetL1[i], -128, +127, i)
			}
			if chroma {
				flags(r, "chroma_weight_l1_flag[i]", &current.ChromaWeightL1Flag[i], i)
				if current.ChromaWeightL1Flag[i] != 0 {
					for j := 0; j < 2; j++ {
						ses(r, "chroma_weight_l1[i][j]", &current.ChromaWeightL1[i][j], -128, +127, i, j)
						ses(r, "chroma_offset_l1[i][j]", &current.ChromaOffsetL1[i][j], -128, +127, i, j)
					}
				}
			}
		}
	}
}

func (h *H264Context) decRefPicMarking(r *Reader, current *H264RawSliceHeader, idrPicFlag bool) {
	sps := h.activeSPS

	if idrPicFlag {
		flag(r, "no_output_of_prior_pics_flag", &current.NoOutputOfPriorPicsFlag)
		flag(r, "long_term_reference_flag", &current.LongTermReferenceFlag)
	} else {
		flag(r, "adaptive_ref_pic_marking_mode_flag", &current.AdaptiveRefPicMarkingModeFlag)
		if current.AdaptiveRefPicMarkingModeFlag != 0 {
			var i int
			for i = 0; i < h264MaxMMCOCount; i++ {
				ue(r, "memory_management_control_operation",
					&current.Mmco[i].MemoryManagementControlOperation, 0, 6)

				mmco := current.Mmco[i].MemoryManagementControlOperation
				if mmco == 0 {
					break
				}

				if mmco == 1 || mmco == 3 {
					ue(r, "difference_of_pic_nums_minus1",
						&current.Mmco[i].DifferenceOfPicNumsMinus1,
						0, math.MaxInt32)
				}
				if mmco == 2 {
					ue(r, "long_term_pic_num",
						&current.Mmco[i].LongTermPicNum,
						0, uint32(sps.MaxNumRefFrames)-1)
				}
				if mmco == 3 || mmco == 6 {
					ue(r, "long_term_frame_idx",
						&current.Mmco[i].LongTermFrameIdx,
						0, uint32(sps.MaxNumRefFrames)-1)
				}
				if mmco == 4 {
					ue(r, "max_long_term_frame_idx_plus1",
						&current.Mmco[i].MaxLongTermFrameIdxPlus1,
						0, uint32(sps.MaxNumRefFrames))
				}
			}
			if i == h264MaxMMCOCount {
				r.diag(LevelError, "Too many memory management control operations.")
				r.fail(ErrInvalidData)
			}
		}
	}
}

func (h *H264Context) sliceHeader(r *Reader, current *H264RawSliceHeader) {
	r.header("Slice Header")

	h.nalUnitHeader(r, &current.NalUnitHeader,
		1<<h264NALSlice|1<<h264NALIDRSlice|1<<h264NALAuxiliarySlice)

	var idrPicFlag bool
	if current.NalUnitHeader.NalUnitType == h264NALAuxiliarySlice {
		if h.lastSliceNALUnitType == 0 {
			r.diag(LevelError, "Auxiliary slice is not decodable without the main picture in the same access unit.")
			r.fail(ErrInvalidData)
		}
		idrPicFlag = h.lastSliceNALUnitType == h264NALIDRSlice
	} else {
		idrPicFlag = current.NalUnitHeader.NalUnitType == h264NALIDRSlice
	}

	ue(r, "first_mb_in_slice", &current.FirstMbInSlice, 0, h264MaxMBPicSize-1)
	ue(r, "slice_type", &current.SliceType, 0, 9)

	sliceTypeI := current.SliceType%5 == 2
	sliceTypeP := current.SliceType%5 == 0
	sliceTypeB := current.SliceType%5 == 1
	sliceTypeSI := current.SliceType%5 == 4
	sliceTypeSP := current.SliceType%5 == 3

	if idrPicFlag && !(sliceTypeI || sliceTypeSI) {
		r.diag(LevelError, "Invalid slice type %d for IDR picture.", current.SliceType)
		r.fail(ErrInvalidData)
	}

	ue(r, "pic_parameter_set_id", &current.PicParameterSetID, 0, 255)

	pps := h.pps[current.PicParameterSetID]
	if pps == nil {
		r.diag(LevelError, "PPS id %d not available.", current.PicParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activePPS = pps

	sps := h.sps[pps.SeqParameterSetID]
	if sps == nil {
		r.diag(LevelError, "SPS id %d not available.", pps.SeqParameterSetID)
		r.fail(ErrInvalidData)
	}
	h.activeSPS = sps

	if sps.SeparateColourPlaneFlag != 0 {
		u(r, 2, "colour_plane_id", &current.ColourPlaneID, 0, 2)
	}

	ub(r, int(sps.Log2MaxFrameNumMinus4)+4, "frame_num", &current.FrameNum)

	if sps.FrameMbsOnlyFlag == 0 {
		flag(r, "field_pic_flag", &current.FieldPicFlag)
		if current.FieldPicFlag != 0 {
			flag(r, "bottom_field_flag", &current.BottomFieldFlag)
		} else {
			infer(&current.BottomFieldFlag, 0)
		}
	} else {
		infer(&current.FieldPicFlag, 0)
		infer(&current.BottomFieldFlag, 0)
	}

	if idrPicFlag {
		ue(r, "idr_pic_id", &current.IdrPicID, 0, 65535)
	}

	if sps.PicOrderCntType == 0 {
		ub(r, int(sps.Log2MaxPicOrderCntLsbMinus4)+4, "pic_order_cnt_lsb", &current.PicOrderCntLsb)
		if pps.BottomFieldPicOrderInFramePresentFlag != 0 &&
			current.FieldPicFlag == 0 {
			se(r, "delta_pic_order_cnt_bottom", &current.DeltaPicOrderCntBottom, int32MinPlus1, math.MaxInt32)
		}
	} else if sps.PicOrderCntType == 1 {
		if sps.DeltaPicOrderAlwaysZeroFlag == 0 {
			se(r, "delta_pic_order_cnt[0]", &current.DeltaPicOrderCnt[0], int32MinPlus1, math.MaxInt32)
			if pps.BottomFieldPicOrderInFramePresentFlag != 0 &&
				current.FieldPicFlag == 0 {
				se(r, "delta_pic_order_cnt[1]", &current.DeltaPicOrderCnt[1], int32MinPlus1, math.MaxInt32)
			} else {
				infer(&current.DeltaPicOrderCnt[1], 0)
			}
		} else {
			infer(&current.DeltaPicOrderCnt[0], 0)
			infer(&current.DeltaPicOrderCnt[1], 0)
		}
	}

	if pps.RedundantPicCntPresentFlag != 0 {
		ue(r, "redundant_pic_cnt", &current.RedundantPicCnt, 0, 127)
	} else {
		infer(&current.RedundantPicCnt, 0)
	}

	if current.NalUnitHeader.NalUnitType != h264NALAuxiliarySlice &&
		current.RedundantPicCnt == 0 {
		h.lastSliceNALUnitType = current.NalUnitHeader.NalUnitType
	}

	if sliceTypeB {
		flag(r, "direct_spatial_mv_pred_flag", &current.DirectSpatialMvPredFlag)
	}

	if sliceTypeP || sliceTypeSP || sliceTypeB {
		flag(r, "num_ref_idx_active_override_flag", &current.NumRefIdxActiveOverrideFlag)
		if current.NumRefIdxActiveOverrideFlag != 0 {
			ue(r, "num_ref_idx_l0_active_minus1", &current.NumRefIdxL0ActiveMinus1, 0, 31)
			if sliceTypeB {
				ue(r, "num_ref_idx_l1_active_minus1", &current.NumRefIdxL1ActiveMinus1, 0, 31)
			}
		} else {
			infer(&current.NumRefIdxL0ActiveMinus1, pps.NumRefIdxL0DefaultActiveMinus1)
			infer(&current.NumRefIdxL1ActiveMinus1, pps.NumRefIdxL1DefaultActiveMinus1)
		}
	}

	if current.NalUnitHeader.NalUnitType == 20 ||
		current.NalUnitHeader.NalUnitType == 21 {
		r.diag(LevelError, "MVC / 3DAVC not supported.")
		r.fail(ErrPatchWelcome)
	} else {
		h.refPicListModification(r, current)
	}

	if (pps.WeightedPredFlag != 0 && (sliceTypeP || sliceTypeSP)) ||
		(pps.WeightedBipredIdc == 1 && sliceTypeB) {
		h.predWeightTable(r, current)
	}

	if current.NalUnitHeader.NalRefIdc != 0 {
		h.decRefPicMarking(r, current, idrPicFlag)
	}

	if pps.EntropyCodingModeFlag != 0 &&
		!sliceTypeI && !sliceTypeSI {
		ue(r, "cabac_init_idc", &current.CabacInitIdc, 0, 2)
	}

	se(r, "slice_qp_delta", &current.SliceQpDelta,
		-51-6*int32(sps.BitDepthLumaMinus8),
		+51+6*int32(sps.BitDepthLumaMinus8))
	if sliceTypeSP || sliceTypeSI {
		if sliceTypeSP {
			flag(r, "sp_for_switch_flag", &current.SpForSwitchFlag)
		}
		se(r, "slice_qs_delta", &current.SliceQsDelta, -51, +51)
	}

	if pps.DeblockingFilterControlPresentFlag != 0 {
		ue(r, "disable_deblocking_filter_idc", &current.DisableDeblockingFilterIdc, 0, 2)
		if current.DisableDeblockingFilterIdc != 1 {
			se(r, "slice_alpha_c0_offset_div2", &current.SliceAlphaC0OffsetDiv2, -6, +6)
			se(r, "slice_beta_offset_div2", &current.SliceBetaOffsetDiv2, -6, +6)
		} else {
			infer(&current.SliceAlphaC0OffsetDiv2, 0)
			infer(&current.SliceBetaOffsetDiv2, 0)
		}
	} else {
		infer(&current.DisableDeblockingFilterIdc, 0)
		infer(&current.SliceAlphaC0OffsetDiv2, 0)
		infer(&current.SliceBetaOffsetDiv2, 0)
	}

	if pps.NumSliceGroupsMinus1 > 0 &&
		pps.SliceGroupMapType >= 3 &&
		pps.SliceGroupMapType <= 5 {
		picSize := (uint32(sps.PicWidthInMbsMinus1) + 1) *
			(uint32(sps.PicHeightInMapUnitsMinus1) + 1)
		max := (picSize + uint32(pps.SliceGroupChangeRateMinus1)) /
			(uint32(pps.SliceGroupChangeRateMinus1) + 1)
		bits := ceilLog2(max + 1)

		u(r, bits, "slice_group_change_cycle", &current.SliceGroupChangeCycle, 0, max)
	}

	if pps.EntropyCodingModeFlag != 0 {
		for r.byteAlignment() != 0 {
			fixed(r, 1, "cabac_alignment_one_bit", 1)
		}
	}
}

func (h *H264Context) filler(r *Reader, current *H264RawFiller) {
	r.header("Filler Data")

	h.nalUnitHeader(r, &current.NalUnitHeader, 1<<h264NALFillerData)

	for r.br.showBits(8) == 0xff {
		fixed(r, 8, "ff_byte", 0xff)
		current.FillerSize++
	}

	h.rbspTrailingBits(r)
}

func (h *H264Context) endOfSequence(r *Reader, current *H264RawNALUnitHeader) {
	r.header("End of Sequence")

	h.nalUnitHeader(r, current, 1<<h264NALEndSequence)
}

func (h *H264Context) endOfStream(r *Reader, current *H264RawNALUnitHeader) {
	r.header("End of Stream")

	h.nalUnitHeader(r, current, 1<<h264NALEndStream)
}

func b2u8(b bool) uint8 {
	if b {
		return 1
	}
	return 0
}
