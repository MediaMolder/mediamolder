// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Line-for-line port of libavcodec/cbs_av1_syntax_template.c (READ side).
// Element names, ranges and control flow mirror the C template exactly; see
// that file for the specification references.

package cbs

import "math"

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// clipUintp2 is av_clip_uintp2.
func clipUintp2(a, p int) int {
	if a&^((1<<uint(p))-1) != 0 {
		return (^a >> 63) & ((1 << uint(p)) - 1)
	}
	return a
}

func (h *AV1Context) obuHeader(r *Reader, current *AV1RawOBUHeader) {
	r.header("OBU header")

	u(r, 1, "obu_forbidden_bit", &current.OBUForbiddenBit, 0, 0)

	u(r, 4, "obu_type", &current.OBUType, 0, av1OBUPadding)
	flag(r, "obu_extension_flag", &current.OBUExtensionFlag)
	flag(r, "obu_has_size_field", &current.OBUHasSizeField)

	u(r, 1, "obu_reserved_1bit", &current.OBUReserved1Bit, 0, 0)

	if current.OBUExtensionFlag != 0 {
		ub(r, 3, "temporal_id", &current.TemporalID)
		ub(r, 2, "spatial_id", &current.SpatialID)
		u(r, 3, "extension_header_reserved_3bits", &current.ExtensionHeaderReserved3Bits, 0, 0)
	} else {
		infer(&current.TemporalID, 0)
		infer(&current.SpatialID, 0)
	}

	h.temporalID = int(current.TemporalID)
	h.spatialID = int(current.SpatialID)
}

func (h *AV1Context) trailingBits(r *Reader, nbBits int) {
	fixed(r, 1, "trailing_one_bit", 1)
	nbBits--

	for nbBits > 0 {
		fixed(r, 1, "trailing_zero_bit", 0)
		nbBits--
	}
}

func (h *AV1Context) byteAlignmentOBU(r *Reader) {
	for r.byteAlignment() != 0 {
		fixed(r, 1, "zero_bit", 0)
	}
}

func (h *AV1Context) colorConfig(r *Reader, current *AV1RawColorConfig, seqProfile int) {
	flag(r, "high_bitdepth", &current.HighBitdepth)

	if seqProfile == av1ProfileProfessional &&
		current.HighBitdepth != 0 {
		flag(r, "twelve_bit", &current.TwelveBit)
		h.bitDepth = cond(current.TwelveBit != 0, 12, 10)
	} else {
		h.bitDepth = cond(current.HighBitdepth != 0, 10, 8)
	}

	if seqProfile == av1ProfileHigh {
		infer(&current.MonoChrome, 0)
	} else {
		flag(r, "mono_chrome", &current.MonoChrome)
	}
	h.numPlanes = cond(current.MonoChrome != 0, 1, 3)

	flag(r, "color_description_present_flag", &current.ColorDescriptionPresentFlag)
	if current.ColorDescriptionPresentFlag != 0 {
		ub(r, 8, "color_primaries", &current.ColorPrimaries)
		ub(r, 8, "transfer_characteristics", &current.TransferCharacteristics)
		ub(r, 8, "matrix_coefficients", &current.MatrixCoefficients)
	} else {
		infer(&current.ColorPrimaries, av1ColPriUnspecified)
		infer(&current.TransferCharacteristics, av1ColTrcUnspecified)
		infer(&current.MatrixCoefficients, av1ColSpcUnspecified)
	}

	if current.MonoChrome != 0 {
		flag(r, "color_range", &current.ColorRange)

		infer(&current.SubsamplingX, 1)
		infer(&current.SubsamplingY, 1)
		infer(&current.ChromaSamplePosition, av1CSPUnknown)
		infer(&current.SeparateUVDeltaQ, 0)

	} else if current.ColorPrimaries == av1ColPriBT709 &&
		current.TransferCharacteristics == av1ColTrcIEC61966_2_1 &&
		current.MatrixCoefficients == av1ColSpcRGB {
		infer(&current.ColorRange, 1)
		infer(&current.SubsamplingX, 0)
		infer(&current.SubsamplingY, 0)
		flag(r, "separate_uv_delta_q", &current.SeparateUVDeltaQ)

	} else {
		flag(r, "color_range", &current.ColorRange)

		if seqProfile == av1ProfileMain {
			infer(&current.SubsamplingX, 1)
			infer(&current.SubsamplingY, 1)
		} else if seqProfile == av1ProfileHigh {
			infer(&current.SubsamplingX, 0)
			infer(&current.SubsamplingY, 0)
		} else {
			if h.bitDepth == 12 {
				ub(r, 1, "subsampling_x", &current.SubsamplingX)
				if current.SubsamplingX != 0 {
					ub(r, 1, "subsampling_y", &current.SubsamplingY)
				} else {
					infer(&current.SubsamplingY, 0)
				}
			} else {
				infer(&current.SubsamplingX, 1)
				infer(&current.SubsamplingY, 0)
			}
		}
		if current.SubsamplingX != 0 && current.SubsamplingY != 0 {
			u(r, 2, "chroma_sample_position", &current.ChromaSamplePosition,
				av1CSPUnknown, av1CSPColocated)
		}

		flag(r, "separate_uv_delta_q", &current.SeparateUVDeltaQ)
	}
}

func (h *AV1Context) timingInfo(r *Reader, current *AV1RawTimingInfo) {
	u(r, 32, "num_units_in_display_tick", &current.NumUnitsInDisplayTick, 1, math.MaxUint32)
	u(r, 32, "time_scale", &current.TimeScale, 1, math.MaxUint32)

	flag(r, "equal_picture_interval", &current.EqualPictureInterval)
	if current.EqualPictureInterval != 0 {
		uvlcv(r, "num_ticks_per_picture_minus_1", &current.NumTicksPerPictureMinus1,
			0, math.MaxUint32-1)
	}
}

func (h *AV1Context) decoderModelInfo(r *Reader, current *AV1RawDecoderModelInfo) {
	ub(r, 5, "buffer_delay_length_minus_1", &current.BufferDelayLengthMinus1)
	u(r, 32, "num_units_in_decoding_tick", &current.NumUnitsInDecodingTick, 1, math.MaxUint32)
	ub(r, 5, "buffer_removal_time_length_minus_1", &current.BufferRemovalTimeLengthMinus1)
	ub(r, 5, "frame_presentation_time_length_minus_1", &current.FramePresentationTimeLengthMinus1)
}

func (h *AV1Context) sequenceHeaderOBU(r *Reader, current *AV1RawSequenceHeader) {
	r.header("Sequence Header")

	u(r, 3, "seq_profile", &current.SeqProfile,
		av1ProfileMain, av1ProfileProfessional)
	flag(r, "still_picture", &current.StillPicture)
	flag(r, "reduced_still_picture_header", &current.ReducedStillPictureHeader)

	if current.ReducedStillPictureHeader != 0 {
		infer(&current.TimingInfoPresentFlag, 0)
		infer(&current.DecoderModelInfoPresentFlag, 0)
		infer(&current.InitialDisplayDelayPresentFlag, 0)
		infer(&current.OperatingPointsCntMinus1, 0)
		infer(&current.OperatingPointIdc[0], 0)

		ub(r, 5, "seq_level_idx[0]", &current.SeqLevelIdx[0])

		infer(&current.SeqTier[0], 0)
		infer(&current.DecoderModelPresentForThisOp[0], 0)
		infer(&current.InitialDisplayDelayPresentForThisOp[0], 0)

	} else {
		flag(r, "timing_info_present_flag", &current.TimingInfoPresentFlag)
		if current.TimingInfoPresentFlag != 0 {
			h.timingInfo(r, &current.TimingInfo)

			flag(r, "decoder_model_info_present_flag", &current.DecoderModelInfoPresentFlag)
			if current.DecoderModelInfoPresentFlag != 0 {
				h.decoderModelInfo(r, &current.DecoderModelInfo)
			}
		} else {
			infer(&current.DecoderModelInfoPresentFlag, 0)
		}

		flag(r, "initial_display_delay_present_flag", &current.InitialDisplayDelayPresentFlag)

		ub(r, 5, "operating_points_cnt_minus_1", &current.OperatingPointsCntMinus1)
		for i := 0; i <= int(current.OperatingPointsCntMinus1); i++ {
			ubs(r, 12, "operating_point_idc[i]", &current.OperatingPointIdc[i], i)
			ubs(r, 5, "seq_level_idx[i]", &current.SeqLevelIdx[i], i)

			if current.SeqLevelIdx[i] > 7 {
				flags(r, "seq_tier[i]", &current.SeqTier[i], i)
			} else {
				infer(&current.SeqTier[i], 0)
			}

			if current.DecoderModelInfoPresentFlag != 0 {
				flags(r, "decoder_model_present_for_this_op[i]", &current.DecoderModelPresentForThisOp[i], i)
				if current.DecoderModelPresentForThisOp[i] != 0 {
					n := int(current.DecoderModelInfo.BufferDelayLengthMinus1) + 1
					ubs(r, n, "decoder_buffer_delay[i]", &current.DecoderBufferDelay[i], i)
					ubs(r, n, "encoder_buffer_delay[i]", &current.EncoderBufferDelay[i], i)
					flags(r, "low_delay_mode_flag[i]", &current.LowDelayModeFlag[i], i)
				}
			} else {
				infer(&current.DecoderModelPresentForThisOp[i], 0)
			}

			if current.InitialDisplayDelayPresentFlag != 0 {
				flags(r, "initial_display_delay_present_for_this_op[i]", &current.InitialDisplayDelayPresentForThisOp[i], i)
				if current.InitialDisplayDelayPresentForThisOp[i] != 0 {
					ubs(r, 4, "initial_display_delay_minus_1[i]", &current.InitialDisplayDelayMinus1[i], i)
				}
			}
		}
	}

	ub(r, 4, "frame_width_bits_minus_1", &current.FrameWidthBitsMinus1)
	ub(r, 4, "frame_height_bits_minus_1", &current.FrameHeightBitsMinus1)

	ub(r, int(current.FrameWidthBitsMinus1)+1, "max_frame_width_minus_1", &current.MaxFrameWidthMinus1)
	ub(r, int(current.FrameHeightBitsMinus1)+1, "max_frame_height_minus_1", &current.MaxFrameHeightMinus1)

	if current.ReducedStillPictureHeader != 0 {
		infer(&current.FrameIDNumbersPresentFlag, 0)
	} else {
		flag(r, "frame_id_numbers_present_flag", &current.FrameIDNumbersPresentFlag)
	}
	if current.FrameIDNumbersPresentFlag != 0 {
		ub(r, 4, "delta_frame_id_length_minus_2", &current.DeltaFrameIDLengthMinus2)
		ub(r, 3, "additional_frame_id_length_minus_1", &current.AdditionalFrameIDLengthMinus1)
	}

	flag(r, "use_128x128_superblock", &current.Use128x128Superblock)
	flag(r, "enable_filter_intra", &current.EnableFilterIntra)
	flag(r, "enable_intra_edge_filter", &current.EnableIntraEdgeFilter)

	if current.ReducedStillPictureHeader != 0 {
		infer(&current.EnableInterintraCompound, 0)
		infer(&current.EnableMaskedCompound, 0)
		infer(&current.EnableWarpedMotion, 0)
		infer(&current.EnableDualFilter, 0)
		infer(&current.EnableOrderHint, 0)
		infer(&current.EnableJntComp, 0)
		infer(&current.EnableRefFrameMvs, 0)

		infer(&current.SeqForceScreenContentTools,
			av1SelectScreenContentTools)
		infer(&current.SeqForceIntegerMV,
			av1SelectIntegerMV)
	} else {
		flag(r, "enable_interintra_compound", &current.EnableInterintraCompound)
		flag(r, "enable_masked_compound", &current.EnableMaskedCompound)
		flag(r, "enable_warped_motion", &current.EnableWarpedMotion)
		flag(r, "enable_dual_filter", &current.EnableDualFilter)

		flag(r, "enable_order_hint", &current.EnableOrderHint)
		if current.EnableOrderHint != 0 {
			flag(r, "enable_jnt_comp", &current.EnableJntComp)
			flag(r, "enable_ref_frame_mvs", &current.EnableRefFrameMvs)
		} else {
			infer(&current.EnableJntComp, 0)
			infer(&current.EnableRefFrameMvs, 0)
		}

		flag(r, "seq_choose_screen_content_tools", &current.SeqChooseScreenContentTools)
		if current.SeqChooseScreenContentTools != 0 {
			infer(&current.SeqForceScreenContentTools,
				av1SelectScreenContentTools)
		} else {
			ub(r, 1, "seq_force_screen_content_tools", &current.SeqForceScreenContentTools)
		}
		if current.SeqForceScreenContentTools > 0 {
			flag(r, "seq_choose_integer_mv", &current.SeqChooseIntegerMV)
			if current.SeqChooseIntegerMV != 0 {
				infer(&current.SeqForceIntegerMV,
					av1SelectIntegerMV)
			} else {
				ub(r, 1, "seq_force_integer_mv", &current.SeqForceIntegerMV)
			}
		} else {
			infer(&current.SeqForceIntegerMV, av1SelectIntegerMV)
		}

		if current.EnableOrderHint != 0 {
			ub(r, 3, "order_hint_bits_minus_1", &current.OrderHintBitsMinus1)
		}
	}

	flag(r, "enable_superres", &current.EnableSuperres)
	flag(r, "enable_cdef", &current.EnableCdef)
	flag(r, "enable_restoration", &current.EnableRestoration)

	h.colorConfig(r, &current.ColorConfig, int(current.SeqProfile))

	flag(r, "film_grain_params_present", &current.FilmGrainParamsPresent)
}

func (h *AV1Context) temporalDelimiterOBU(r *Reader) {
	r.header("Temporal Delimiter")

	h.seenFrameHeader = 0
}

func (h *AV1Context) setFrameRefs(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	refFrameList := [av1NumRefFrames - 3]uint8{
		av1RefFrameLast2, av1RefFrameLast3, av1RefFrameBwdref,
		av1RefFrameAltref2, av1RefFrameAltref,
	}
	var refFrameIdx [av1RefsPerFrame]int8
	var usedFrame [av1NumRefFrames]int8
	var shiftedOrderHints [av1NumRefFrames]int16
	var curFrameHint, latestOrderHint, earliestOrderHint, ref int

	for i := 0; i < av1RefsPerFrame; i++ {
		refFrameIdx[i] = av1RefFrameNone
	}
	refFrameIdx[0] = int8(current.LastFrameIdx) // [AV1_REF_FRAME_LAST - AV1_REF_FRAME_LAST]
	refFrameIdx[av1RefFrameGolden-av1RefFrameLast] = int8(current.GoldenFrameIdx)

	for i := 0; i < av1NumRefFrames; i++ {
		usedFrame[i] = 0
	}
	usedFrame[current.LastFrameIdx] = 1
	usedFrame[current.GoldenFrameIdx] = 1

	curFrameHint = 1 << uint(seq.OrderHintBitsMinus1)
	for i := 0; i < av1NumRefFrames; i++ {
		shiftedOrderHints[i] = int16(curFrameHint +
			av1GetRelativeDist(seq, uint32(h.ref[i].OrderHint),
				uint32(h.orderHint)))
	}

	latestOrderHint = int(shiftedOrderHints[current.LastFrameIdx])
	earliestOrderHint = int(shiftedOrderHints[current.GoldenFrameIdx])

	ref = av1RefFrameNone
	for i := 0; i < av1NumRefFrames; i++ {
		hint := int(shiftedOrderHints[i])
		if usedFrame[i] == 0 && hint >= curFrameHint &&
			(ref < 0 || hint >= latestOrderHint) {
			ref = i
			latestOrderHint = hint
		}
	}
	if ref >= 0 {
		refFrameIdx[av1RefFrameAltref-av1RefFrameLast] = int8(ref)
		usedFrame[ref] = 1
	}

	ref = av1RefFrameNone
	for i := 0; i < av1NumRefFrames; i++ {
		hint := int(shiftedOrderHints[i])
		if usedFrame[i] == 0 && hint >= curFrameHint &&
			(ref < 0 || hint < earliestOrderHint) {
			ref = i
			earliestOrderHint = hint
		}
	}
	if ref >= 0 {
		refFrameIdx[av1RefFrameBwdref-av1RefFrameLast] = int8(ref)
		usedFrame[ref] = 1
	}

	ref = av1RefFrameNone
	for i := 0; i < av1NumRefFrames; i++ {
		hint := int(shiftedOrderHints[i])
		if usedFrame[i] == 0 && hint >= curFrameHint &&
			(ref < 0 || hint < earliestOrderHint) {
			ref = i
			earliestOrderHint = hint
		}
	}
	if ref >= 0 {
		refFrameIdx[av1RefFrameAltref2-av1RefFrameLast] = int8(ref)
		usedFrame[ref] = 1
	}

	for i := 0; i < av1RefsPerFrame-2; i++ {
		refFrame := int(refFrameList[i])
		if refFrameIdx[refFrame-av1RefFrameLast] < 0 {
			ref = av1RefFrameNone
			for j := 0; j < av1NumRefFrames; j++ {
				hint := int(shiftedOrderHints[j])
				if usedFrame[j] == 0 && hint < curFrameHint &&
					(ref < 0 || hint >= latestOrderHint) {
					ref = j
					latestOrderHint = hint
				}
			}
			if ref >= 0 {
				refFrameIdx[refFrame-av1RefFrameLast] = int8(ref)
				usedFrame[ref] = 1
			}
		}
	}

	ref = av1RefFrameNone
	for i := 0; i < av1NumRefFrames; i++ {
		hint := int(shiftedOrderHints[i])
		if ref < 0 || hint < earliestOrderHint {
			ref = i
			earliestOrderHint = hint
		}
	}
	for i := 0; i < av1RefsPerFrame; i++ {
		if refFrameIdx[i] < 0 {
			refFrameIdx[i] = int8(ref)
		}
		infer(&current.RefFrameIdx[i], refFrameIdx[i])
	}
}

func (h *AV1Context) superresParams(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	var denom int

	if seq.EnableSuperres != 0 {
		flag(r, "use_superres", &current.UseSuperres)
	} else {
		infer(&current.UseSuperres, 0)
	}

	if current.UseSuperres != 0 {
		ub(r, 3, "coded_denom", &current.CodedDenom)
		denom = int(current.CodedDenom) + av1SuperresDenomMin
	} else {
		denom = av1SuperresNum
	}

	h.upscaledWidth = h.frameWidth
	h.frameWidth = (h.upscaledWidth*av1SuperresNum +
		denom/2) / denom
}

func (h *AV1Context) frameSize(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader

	if current.FrameSizeOverrideFlag != 0 {
		ub(r, int(seq.FrameWidthBitsMinus1)+1, "frame_width_minus_1", &current.FrameWidthMinus1)
		ub(r, int(seq.FrameHeightBitsMinus1)+1, "frame_height_minus_1", &current.FrameHeightMinus1)
	} else {
		infer(&current.FrameWidthMinus1, seq.MaxFrameWidthMinus1)
		infer(&current.FrameHeightMinus1, seq.MaxFrameHeightMinus1)
	}

	h.frameWidth = int(current.FrameWidthMinus1) + 1
	h.frameHeight = int(current.FrameHeightMinus1) + 1

	h.superresParams(r, current)
}

func (h *AV1Context) renderSize(r *Reader, current *AV1RawFrameHeader) {
	flag(r, "render_and_frame_size_different", &current.RenderAndFrameSizeDifferent)

	if current.RenderAndFrameSizeDifferent != 0 {
		ub(r, 16, "render_width_minus_1", &current.RenderWidthMinus1)
		ub(r, 16, "render_height_minus_1", &current.RenderHeightMinus1)
	} else {
		infer(&current.RenderWidthMinus1, current.FrameWidthMinus1)
		infer(&current.RenderHeightMinus1, current.FrameHeightMinus1)
	}

	h.renderWidth = int(current.RenderWidthMinus1) + 1
	h.renderHeight = int(current.RenderHeightMinus1) + 1
}

func (h *AV1Context) frameSizeWithRefs(r *Reader, current *AV1RawFrameHeader) {
	var i int

	for i = 0; i < av1RefsPerFrame; i++ {
		flags(r, "found_ref[i]", &current.FoundRef[i], i)
		if current.FoundRef[i] != 0 {
			ref := &h.ref[current.RefFrameIdx[i]]

			if ref.Valid == 0 {
				r.diag(LevelError,
					"Missing reference frame needed for frame size (ref = %d, ref_frame_idx = %d).",
					i, current.RefFrameIdx[i])
				r.fail(ErrInvalidData)
			}

			infer(&current.FrameWidthMinus1, uint16(ref.UpscaledWidth-1))
			infer(&current.FrameHeightMinus1, uint16(ref.FrameHeight-1))
			infer(&current.RenderWidthMinus1, uint16(ref.RenderWidth-1))
			infer(&current.RenderHeightMinus1, uint16(ref.RenderHeight-1))

			h.upscaledWidth = ref.UpscaledWidth
			h.frameWidth = h.upscaledWidth
			h.frameHeight = ref.FrameHeight
			h.renderWidth = ref.RenderWidth
			h.renderHeight = ref.RenderHeight
			break
		}
	}

	if i >= av1RefsPerFrame {
		h.frameSize(r, current)
		h.renderSize(r, current)
	} else {
		h.superresParams(r, current)
	}
}

func (h *AV1Context) interpolationFilter(r *Reader, current *AV1RawFrameHeader) {
	flag(r, "is_filter_switchable", &current.IsFilterSwitchable)
	if current.IsFilterSwitchable != 0 {
		infer(&current.InterpolationFilter,
			av1InterpolationFilterSwitchable)
	} else {
		ub(r, 2, "interpolation_filter", &current.InterpolationFilter)
	}
}

func (h *AV1Context) tileInfo(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	var miCols, miRows, sbCols, sbRows, sbShift, sbSize int
	var maxTileWidthSb, maxTileHeightSb, maxTileAreaSb int
	var minLog2TileCols, maxLog2TileCols, maxLog2TileRows int
	var minLog2Tiles, minLog2TileRows int

	miCols = 2 * ((h.frameWidth + 7) >> 3)
	miRows = 2 * ((h.frameHeight + 7) >> 3)

	sbCols = cond(seq.Use128x128Superblock != 0, (miCols+31)>>5, (miCols+15)>>4)
	sbRows = cond(seq.Use128x128Superblock != 0, (miRows+31)>>5, (miRows+15)>>4)

	sbShift = cond(seq.Use128x128Superblock != 0, 5, 4)
	sbSize = sbShift + 2

	maxTileWidthSb = av1MaxTileWidth >> uint(sbSize)
	maxTileAreaSb = av1MaxTileArea >> uint(2*sbSize)

	minLog2TileCols = av1TileLog2(maxTileWidthSb, sbCols)
	maxLog2TileCols = av1TileLog2(1, min(sbCols, av1MaxTileCols))
	maxLog2TileRows = av1TileLog2(1, min(sbRows, av1MaxTileRows))
	minLog2Tiles = max(minLog2TileCols,
		av1TileLog2(maxTileAreaSb, sbRows*sbCols))

	flag(r, "uniform_tile_spacing_flag", &current.UniformTileSpacingFlag)

	if current.UniformTileSpacingFlag != 0 {
		var tileWidthSb, tileHeightSb int

		increment(r, "tile_cols_log2", &current.TileColsLog2,
			uint32(minLog2TileCols), uint32(maxLog2TileCols))

		tileWidthSb = (sbCols + (1 << uint(current.TileColsLog2)) - 1) >>
			uint(current.TileColsLog2)

		for off, j := 0, 0; off < sbCols; off += tileWidthSb {
			current.TileStartColSb[j] = uint8(off)
			j++
		}

		current.TileCols = uint16((sbCols + tileWidthSb - 1) / tileWidthSb)

		minLog2TileRows = max(minLog2Tiles-int(current.TileColsLog2), 0)

		increment(r, "tile_rows_log2", &current.TileRowsLog2,
			uint32(minLog2TileRows), uint32(maxLog2TileRows))

		tileHeightSb = (sbRows + (1 << uint(current.TileRowsLog2)) - 1) >>
			uint(current.TileRowsLog2)

		for off, j := 0, 0; off < sbRows; off += tileHeightSb {
			current.TileStartRowSb[j] = uint8(off)
			j++
		}

		current.TileRows = uint16((sbRows + tileHeightSb - 1) / tileHeightSb)

		var i int
		for i = 0; i < int(current.TileCols)-1; i++ {
			infer(&current.WidthInSbsMinus1[i], uint8(tileWidthSb-1))
		}
		infer(&current.WidthInSbsMinus1[i],
			uint8(sbCols-(int(current.TileCols)-1)*tileWidthSb-1))
		for i = 0; i < int(current.TileRows)-1; i++ {
			infer(&current.HeightInSbsMinus1[i], uint8(tileHeightSb-1))
		}
		infer(&current.HeightInSbsMinus1[i],
			uint8(sbRows-(int(current.TileRows)-1)*tileHeightSb-1))

	} else {
		var widestTileSb, startSb, sizeSb, maxWidth, maxHeight, i int

		widestTileSb = 0

		startSb = 0
		for i = 0; startSb < sbCols && i < av1MaxTileCols; i++ {
			current.TileStartColSb[i] = uint8(startSb)
			maxWidth = min(sbCols-startSb, maxTileWidthSb)
			ns(r, uint32(maxWidth), "width_in_sbs_minus_1[i]", &current.WidthInSbsMinus1[i], i)
			sizeSb = int(current.WidthInSbsMinus1[i]) + 1
			widestTileSb = max(sizeSb, widestTileSb)
			startSb += sizeSb
		}
		current.TileColsLog2 = uint8(av1TileLog2(1, i))
		current.TileCols = uint16(i)

		if minLog2Tiles > 0 {
			maxTileAreaSb = (sbRows * sbCols) >> uint(minLog2Tiles+1)
		} else {
			maxTileAreaSb = sbRows * sbCols
		}
		maxTileHeightSb = max(maxTileAreaSb/widestTileSb, 1)

		startSb = 0
		for i = 0; startSb < sbRows && i < av1MaxTileRows; i++ {
			current.TileStartRowSb[i] = uint8(startSb)
			maxHeight = min(sbRows-startSb, maxTileHeightSb)
			ns(r, uint32(maxHeight), "height_in_sbs_minus_1[i]", &current.HeightInSbsMinus1[i], i)
			sizeSb = int(current.HeightInSbsMinus1[i]) + 1
			startSb += sizeSb
		}
		current.TileRowsLog2 = uint8(av1TileLog2(1, i))
		current.TileRows = uint16(i)
	}

	if current.TileColsLog2 > 0 ||
		current.TileRowsLog2 > 0 {
		ub(r, int(current.TileColsLog2)+int(current.TileRowsLog2),
			"context_update_tile_id", &current.ContextUpdateTileID)
		ub(r, 2, "tile_size_bytes_minus1", &current.TileSizeBytesMinus1)
	} else {
		infer(&current.ContextUpdateTileID, 0)
	}

	h.tileCols = int(current.TileCols)
	h.tileRows = int(current.TileRows)
}

func (h *AV1Context) quantizationParams(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader

	ub(r, 8, "base_q_idx", &current.BaseQIdx)

	deltaQ(r, "delta_q_y_dc", &current.DeltaQYDc)

	if h.numPlanes > 1 {
		if seq.ColorConfig.SeparateUVDeltaQ != 0 {
			flag(r, "diff_uv_delta", &current.DiffUVDelta)
		} else {
			infer(&current.DiffUVDelta, 0)
		}

		deltaQ(r, "delta_q_u_dc", &current.DeltaQUDc)
		deltaQ(r, "delta_q_u_ac", &current.DeltaQUAc)

		if current.DiffUVDelta != 0 {
			deltaQ(r, "delta_q_v_dc", &current.DeltaQVDc)
			deltaQ(r, "delta_q_v_ac", &current.DeltaQVAc)
		} else {
			infer(&current.DeltaQVDc, current.DeltaQUDc)
			infer(&current.DeltaQVAc, current.DeltaQUAc)
		}
	} else {
		infer(&current.DeltaQUDc, 0)
		infer(&current.DeltaQUAc, 0)
		infer(&current.DeltaQVDc, 0)
		infer(&current.DeltaQVAc, 0)
	}

	flag(r, "using_qmatrix", &current.UsingQmatrix)
	if current.UsingQmatrix != 0 {
		ub(r, 4, "qm_y", &current.QmY)
		ub(r, 4, "qm_u", &current.QmU)
		if seq.ColorConfig.SeparateUVDeltaQ != 0 {
			ub(r, 4, "qm_v", &current.QmV)
		} else {
			infer(&current.QmV, current.QmU)
		}
	}
}

func (h *AV1Context) segmentationParams(r *Reader, current *AV1RawFrameHeader) {
	bits := [av1SegLvlMax]uint8{8, 6, 6, 6, 6, 3, 0, 0}
	sign := [av1SegLvlMax]uint8{1, 1, 1, 1, 1, 0, 0, 0}
	defaultFeatureEnabled := [av1SegLvlMax]uint8{}
	defaultFeatureValue := [av1SegLvlMax]int16{}

	flag(r, "segmentation_enabled", &current.SegmentationEnabled)

	if current.SegmentationEnabled != 0 {
		if current.PrimaryRefFrame == av1PrimaryRefNone {
			infer(&current.SegmentationUpdateMap, 1)
			infer(&current.SegmentationTemporalUpdate, 0)
			infer(&current.SegmentationUpdateData, 1)
		} else {
			flag(r, "segmentation_update_map", &current.SegmentationUpdateMap)
			if current.SegmentationUpdateMap != 0 {
				flag(r, "segmentation_temporal_update", &current.SegmentationTemporalUpdate)
			} else {
				infer(&current.SegmentationTemporalUpdate, 0)
			}
			flag(r, "segmentation_update_data", &current.SegmentationUpdateData)
		}

		for i := 0; i < av1MaxSegments; i++ {
			var refFeatureEnabled [av1SegLvlMax]uint8
			var refFeatureValue [av1SegLvlMax]int16

			if current.PrimaryRefFrame == av1PrimaryRefNone {
				refFeatureEnabled = defaultFeatureEnabled
				refFeatureValue = defaultFeatureValue
			} else {
				refFeatureEnabled =
					h.ref[current.RefFrameIdx[current.PrimaryRefFrame]].FeatureEnabled[i]
				refFeatureValue =
					h.ref[current.RefFrameIdx[current.PrimaryRefFrame]].FeatureValue[i]
			}

			for j := 0; j < av1SegLvlMax; j++ {
				if current.SegmentationUpdateData != 0 {
					flags(r, "feature_enabled[i][j]", &current.FeatureEnabled[i][j], i, j)

					if current.FeatureEnabled[i][j] != 0 && bits[j] > 0 {
						if sign[j] != 0 {
							ibs(r, 1+int(bits[j]), "feature_value[i][j]", &current.FeatureValue[i][j], i, j)
						} else {
							ubs(r, int(bits[j]), "feature_value[i][j]", &current.FeatureValue[i][j], i, j)
						}
					} else {
						infer(&current.FeatureValue[i][j], 0)
					}
				} else {
					infer(&current.FeatureEnabled[i][j], refFeatureEnabled[j])
					infer(&current.FeatureValue[i][j], refFeatureValue[j])
				}
			}
		}
	} else {
		for i := 0; i < av1MaxSegments; i++ {
			for j := 0; j < av1SegLvlMax; j++ {
				infer(&current.FeatureEnabled[i][j], 0)
				infer(&current.FeatureValue[i][j], 0)
			}
		}
	}
}

func (h *AV1Context) deltaQParams(r *Reader, current *AV1RawFrameHeader) {
	if current.BaseQIdx > 0 {
		flag(r, "delta_q_present", &current.DeltaQPresent)
	} else {
		infer(&current.DeltaQPresent, 0)
	}

	if current.DeltaQPresent != 0 {
		ub(r, 2, "delta_q_res", &current.DeltaQRes)
	}
}

func (h *AV1Context) deltaLfParams(r *Reader, current *AV1RawFrameHeader) {
	if current.DeltaQPresent != 0 {
		if current.AllowIntrabc == 0 {
			flag(r, "delta_lf_present", &current.DeltaLfPresent)
		} else {
			infer(&current.DeltaLfPresent, 0)
		}
		if current.DeltaLfPresent != 0 {
			ub(r, 2, "delta_lf_res", &current.DeltaLfRes)
			flag(r, "delta_lf_multi", &current.DeltaLfMulti)
		} else {
			infer(&current.DeltaLfRes, 0)
			infer(&current.DeltaLfMulti, 0)
		}
	} else {
		infer(&current.DeltaLfPresent, 0)
		infer(&current.DeltaLfRes, 0)
		infer(&current.DeltaLfMulti, 0)
	}
}

func (h *AV1Context) loopFilterParams(r *Reader, current *AV1RawFrameHeader) {
	defaultLoopFilterRefDeltas := [av1TotalRefsPerFrame]int8{1, 0, 0, 0, -1, 0, -1, -1}
	defaultLoopFilterModeDeltas := [2]int8{0, 0}

	if h.codedLossless != 0 || current.AllowIntrabc != 0 {
		infer(&current.LoopFilterLevel[0], 0)
		infer(&current.LoopFilterLevel[1], 0)
		infer(&current.LoopFilterRefDeltas[av1RefFrameIntra], 1)
		infer(&current.LoopFilterRefDeltas[av1RefFrameLast], 0)
		infer(&current.LoopFilterRefDeltas[av1RefFrameLast2], 0)
		infer(&current.LoopFilterRefDeltas[av1RefFrameLast3], 0)
		infer(&current.LoopFilterRefDeltas[av1RefFrameBwdref], 0)
		infer(&current.LoopFilterRefDeltas[av1RefFrameGolden], -1)
		infer(&current.LoopFilterRefDeltas[av1RefFrameAltref], -1)
		infer(&current.LoopFilterRefDeltas[av1RefFrameAltref2], -1)
		for i := 0; i < 2; i++ {
			infer(&current.LoopFilterModeDeltas[i], 0)
		}
		return
	}

	ub(r, 6, "loop_filter_level[0]", &current.LoopFilterLevel[0])
	ub(r, 6, "loop_filter_level[1]", &current.LoopFilterLevel[1])

	if h.numPlanes > 1 {
		if current.LoopFilterLevel[0] != 0 ||
			current.LoopFilterLevel[1] != 0 {
			ub(r, 6, "loop_filter_level[2]", &current.LoopFilterLevel[2])
			ub(r, 6, "loop_filter_level[3]", &current.LoopFilterLevel[3])
		}
	}

	ub(r, 3, "loop_filter_sharpness", &current.LoopFilterSharpness)

	flag(r, "loop_filter_delta_enabled", &current.LoopFilterDeltaEnabled)
	if current.LoopFilterDeltaEnabled != 0 {
		var refLoopFilterRefDeltas [av1TotalRefsPerFrame]int8
		var refLoopFilterModeDeltas [2]int8

		if current.PrimaryRefFrame == av1PrimaryRefNone {
			refLoopFilterRefDeltas = defaultLoopFilterRefDeltas
			refLoopFilterModeDeltas = defaultLoopFilterModeDeltas
		} else {
			refLoopFilterRefDeltas =
				h.ref[current.RefFrameIdx[current.PrimaryRefFrame]].LoopFilterRefDeltas
			refLoopFilterModeDeltas =
				h.ref[current.RefFrameIdx[current.PrimaryRefFrame]].LoopFilterModeDeltas
		}

		flag(r, "loop_filter_delta_update", &current.LoopFilterDeltaUpdate)
		for i := 0; i < av1TotalRefsPerFrame; i++ {
			if current.LoopFilterDeltaUpdate != 0 {
				flags(r, "update_ref_delta[i]", &current.UpdateRefDelta[i], i)
			} else {
				infer(&current.UpdateRefDelta[i], 0)
			}
			if current.UpdateRefDelta[i] != 0 {
				ibs(r, 1+6, "loop_filter_ref_deltas[i]", &current.LoopFilterRefDeltas[i], i)
			} else {
				infer(&current.LoopFilterRefDeltas[i], refLoopFilterRefDeltas[i])
			}
		}
		for i := 0; i < 2; i++ {
			if current.LoopFilterDeltaUpdate != 0 {
				flags(r, "update_mode_delta[i]", &current.UpdateModeDelta[i], i)
			} else {
				infer(&current.UpdateModeDelta[i], 0)
			}
			if current.UpdateModeDelta[i] != 0 {
				ibs(r, 1+6, "loop_filter_mode_deltas[i]", &current.LoopFilterModeDeltas[i], i)
			} else {
				infer(&current.LoopFilterModeDeltas[i], refLoopFilterModeDeltas[i])
			}
		}
	} else {
		for i := 0; i < av1TotalRefsPerFrame; i++ {
			infer(&current.LoopFilterRefDeltas[i], defaultLoopFilterRefDeltas[i])
		}
		for i := 0; i < 2; i++ {
			infer(&current.LoopFilterModeDeltas[i], defaultLoopFilterModeDeltas[i])
		}
	}
}

func (h *AV1Context) cdefParams(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader

	if h.codedLossless != 0 || current.AllowIntrabc != 0 ||
		seq.EnableCdef == 0 {
		infer(&current.CdefDampingMinus3, 0)
		infer(&current.CdefBits, 0)
		infer(&current.CdefYPriStrength[0], 0)
		infer(&current.CdefYSecStrength[0], 0)
		infer(&current.CdefUVPriStrength[0], 0)
		infer(&current.CdefUVSecStrength[0], 0)

		return
	}

	ub(r, 2, "cdef_damping_minus_3", &current.CdefDampingMinus3)
	ub(r, 2, "cdef_bits", &current.CdefBits)

	for i := 0; i < 1<<uint(current.CdefBits); i++ {
		ubs(r, 4, "cdef_y_pri_strength[i]", &current.CdefYPriStrength[i], i)
		ubs(r, 2, "cdef_y_sec_strength[i]", &current.CdefYSecStrength[i], i)

		if h.numPlanes > 1 {
			ubs(r, 4, "cdef_uv_pri_strength[i]", &current.CdefUVPriStrength[i], i)
			ubs(r, 2, "cdef_uv_sec_strength[i]", &current.CdefUVSecStrength[i], i)
		}
	}
}

func (h *AV1Context) lrParams(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	var usesLr, usesChromaLr int

	if h.allLossless != 0 || current.AllowIntrabc != 0 ||
		seq.EnableRestoration == 0 {
		return
	}

	usesLr, usesChromaLr = 0, 0
	for i := 0; i < h.numPlanes; i++ {
		ubs(r, 2, "lr_type[i]", &current.LrType[i], i)

		if current.LrType[i] != av1RestoreNone {
			usesLr = 1
			if i > 0 {
				usesChromaLr = 1
			}
		}
	}

	if usesLr != 0 {
		if seq.Use128x128Superblock != 0 {
			increment(r, "lr_unit_shift", &current.LrUnitShift, 1, 2)
		} else {
			increment(r, "lr_unit_shift", &current.LrUnitShift, 0, 2)
		}

		if seq.ColorConfig.SubsamplingX != 0 &&
			seq.ColorConfig.SubsamplingY != 0 && usesChromaLr != 0 {
			ub(r, 1, "lr_uv_shift", &current.LrUVShift)
		} else {
			infer(&current.LrUVShift, 0)
		}
	}
}

func (h *AV1Context) readTxMode(r *Reader, current *AV1RawFrameHeader) {
	if h.codedLossless != 0 {
		infer(&current.TxMode, av1Only4x4)
	} else {
		increment(r, "tx_mode", &current.TxMode, av1TXModeLargest, av1TXModeSelect)
	}
}

func (h *AV1Context) frameReferenceMode(r *Reader, current *AV1RawFrameHeader) {
	if current.FrameType == av1FrameIntraOnly ||
		current.FrameType == av1FrameKey {
		infer(&current.ReferenceSelect, 0)
	} else {
		flag(r, "reference_select", &current.ReferenceSelect)
	}
}

func (h *AV1Context) skipModeParams(r *Reader, current *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	var skipModeAllowed int

	if current.FrameType == av1FrameKey ||
		current.FrameType == av1FrameIntraOnly ||
		current.ReferenceSelect == 0 || seq.EnableOrderHint == 0 {
		skipModeAllowed = 0
	} else {
		var forwardIdx, backwardIdx int
		var forwardHint, backwardHint int
		var refHint, dist int

		forwardIdx = -1
		backwardIdx = -1
		for i := 0; i < av1RefsPerFrame; i++ {
			refHint = h.ref[current.RefFrameIdx[i]].OrderHint
			dist = av1GetRelativeDist(seq, uint32(refHint),
				uint32(h.orderHint))
			if dist < 0 {
				if forwardIdx < 0 ||
					av1GetRelativeDist(seq, uint32(refHint),
						uint32(forwardHint)) > 0 {
					forwardIdx = i
					forwardHint = refHint
				}
			} else if dist > 0 {
				if backwardIdx < 0 ||
					av1GetRelativeDist(seq, uint32(refHint),
						uint32(backwardHint)) < 0 {
					backwardIdx = i
					backwardHint = refHint
				}
			}
		}

		if forwardIdx < 0 {
			skipModeAllowed = 0
		} else if backwardIdx >= 0 {
			skipModeAllowed = 1
			// Frames for skip mode are forward_idx and backward_idx.
		} else {
			var secondForwardIdx int
			var secondForwardHint int

			secondForwardIdx = -1
			for i := 0; i < av1RefsPerFrame; i++ {
				refHint = h.ref[current.RefFrameIdx[i]].OrderHint
				if av1GetRelativeDist(seq, uint32(refHint),
					uint32(forwardHint)) < 0 {
					if secondForwardIdx < 0 ||
						av1GetRelativeDist(seq, uint32(refHint),
							uint32(secondForwardHint)) > 0 {
						secondForwardIdx = i
						secondForwardHint = refHint
					}
				}
			}

			if secondForwardIdx < 0 {
				skipModeAllowed = 0
			} else {
				skipModeAllowed = 1
				// Frames for skip mode are forward_idx and second_forward_idx.
			}
		}
	}

	if skipModeAllowed != 0 {
		flag(r, "skip_mode_present", &current.SkipModePresent)
	} else {
		infer(&current.SkipModePresent, 0)
	}
}

func (h *AV1Context) globalMotionParam(r *Reader, current *AV1RawFrameHeader, typ, ref, idx int) {
	var absBits, precBits uint32

	if idx < 2 {
		if typ == av1WarpModelTranslation {
			absBits = av1GMAbsTransOnlyBits - uint32(b2i(current.AllowHighPrecisionMV == 0))
			precBits = av1GMTransOnlyPrecBits - uint32(b2i(current.AllowHighPrecisionMV == 0))
		} else {
			absBits = av1GMAbsTransBits
			precBits = av1GMTransPrecBits
		}
	} else {
		absBits = av1GMAbsAlphaBits
		precBits = av1GMAlphaPrecBits
	}

	numSyms := 2*(uint32(1)<<absBits) + 1
	subexpv(r, "gm_params[ref][idx]", &current.GmParams[ref][idx], numSyms, ref, idx)

	// Actual gm_params value is not reconstructed here.
	_ = precBits
}

func (h *AV1Context) globalMotionParams(r *Reader, current *AV1RawFrameHeader) {
	var typ int

	if current.FrameType == av1FrameKey ||
		current.FrameType == av1FrameIntraOnly {
		return
	}

	for ref := av1RefFrameLast; ref <= av1RefFrameAltref; ref++ {
		flags(r, "is_global[ref]", &current.IsGlobal[ref], ref)
		if current.IsGlobal[ref] != 0 {
			flags(r, "is_rot_zoom[ref]", &current.IsRotZoom[ref], ref)
			if current.IsRotZoom[ref] != 0 {
				typ = av1WarpModelRotzoom
			} else {
				flags(r, "is_translation[ref]", &current.IsTranslation[ref], ref)
				typ = cond(current.IsTranslation[ref] != 0, av1WarpModelTranslation,
					av1WarpModelAffine)
			}
		} else {
			typ = av1WarpModelIdentity
		}

		if typ >= av1WarpModelRotzoom {
			h.globalMotionParam(r, current, typ, ref, 2)
			h.globalMotionParam(r, current, typ, ref, 3)
			// else: gm_params[ref][4] = -gm_params[ref][3] and
			// gm_params[ref][5] = gm_params[ref][2] (decoder-derived).
			if typ == av1WarpModelAffine {
				h.globalMotionParam(r, current, typ, ref, 4)
				h.globalMotionParam(r, current, typ, ref, 5)
			}
		}
		if typ >= av1WarpModelTranslation {
			h.globalMotionParam(r, current, typ, ref, 0)
			h.globalMotionParam(r, current, typ, ref, 1)
		}
	}
}

func (h *AV1Context) filmGrainParams(r *Reader, current *AV1RawFilmGrainParams, frameHeader *AV1RawFrameHeader) {
	seq := h.sequenceHeader
	var numPosLuma, numPosChroma int
	var i int

	if seq.FilmGrainParamsPresent == 0 ||
		(frameHeader.ShowFrame == 0 && frameHeader.ShowableFrame == 0) {
		return
	}

	flag(r, "apply_grain", &current.ApplyGrain)

	if current.ApplyGrain == 0 {
		return
	}

	ub(r, 16, "grain_seed", &current.GrainSeed)

	if frameHeader.FrameType == av1FrameInter {
		flag(r, "update_grain", &current.UpdateGrain)
	} else {
		infer(&current.UpdateGrain, 1)
	}

	if current.UpdateGrain == 0 {
		ub(r, 3, "film_grain_params_ref_idx", &current.FilmGrainParamsRefIdx)
		return
	}

	u(r, 4, "num_y_points", &current.NumYPoints, 0, 14)
	for i = 0; i < int(current.NumYPoints); i++ {
		us(r, 8, "point_y_value[i]", &current.PointYValue[i],
			cond(i != 0, uint32(current.PointYValue[max(i-1, 0)])+1, 0),
			maxUintBits(8)-uint32(int(current.NumYPoints)-i-1),
			i)
		ubs(r, 8, "point_y_scaling[i]", &current.PointYScaling[i], i)
	}

	if seq.ColorConfig.MonoChrome != 0 {
		infer(&current.ChromaScalingFromLuma, 0)
	} else {
		flag(r, "chroma_scaling_from_luma", &current.ChromaScalingFromLuma)
	}

	if seq.ColorConfig.MonoChrome != 0 ||
		current.ChromaScalingFromLuma != 0 ||
		(seq.ColorConfig.SubsamplingX == 1 &&
			seq.ColorConfig.SubsamplingY == 1 &&
			current.NumYPoints == 0) {
		infer(&current.NumCbPoints, 0)
		infer(&current.NumCrPoints, 0)
	} else {
		u(r, 4, "num_cb_points", &current.NumCbPoints, 0, 10)
		for i = 0; i < int(current.NumCbPoints); i++ {
			us(r, 8, "point_cb_value[i]", &current.PointCbValue[i],
				cond(i != 0, uint32(current.PointCbValue[max(i-1, 0)])+1, 0),
				maxUintBits(8)-uint32(int(current.NumCbPoints)-i-1),
				i)
			ubs(r, 8, "point_cb_scaling[i]", &current.PointCbScaling[i], i)
		}
		u(r, 4, "num_cr_points", &current.NumCrPoints, 0, 10)
		for i = 0; i < int(current.NumCrPoints); i++ {
			us(r, 8, "point_cr_value[i]", &current.PointCrValue[i],
				cond(i != 0, uint32(current.PointCrValue[max(i-1, 0)])+1, 0),
				maxUintBits(8)-uint32(int(current.NumCrPoints)-i-1),
				i)
			ubs(r, 8, "point_cr_scaling[i]", &current.PointCrScaling[i], i)
		}
	}

	ub(r, 2, "grain_scaling_minus_8", &current.GrainScalingMinus8)
	ub(r, 2, "ar_coeff_lag", &current.ArCoeffLag)
	numPosLuma = 2 * int(current.ArCoeffLag) * (int(current.ArCoeffLag) + 1)
	if current.NumYPoints != 0 {
		numPosChroma = numPosLuma + 1
		for i = 0; i < numPosLuma; i++ {
			ubs(r, 8, "ar_coeffs_y_plus_128[i]", &current.ArCoeffsYPlus128[i], i)
		}
	} else {
		numPosChroma = numPosLuma
	}
	if current.ChromaScalingFromLuma != 0 || current.NumCbPoints != 0 {
		for i = 0; i < numPosChroma; i++ {
			ubs(r, 8, "ar_coeffs_cb_plus_128[i]", &current.ArCoeffsCbPlus128[i], i)
		}
	}
	if current.ChromaScalingFromLuma != 0 || current.NumCrPoints != 0 {
		for i = 0; i < numPosChroma; i++ {
			ubs(r, 8, "ar_coeffs_cr_plus_128[i]", &current.ArCoeffsCrPlus128[i], i)
		}
	}
	ub(r, 2, "ar_coeff_shift_minus_6", &current.ArCoeffShiftMinus6)
	ub(r, 2, "grain_scale_shift", &current.GrainScaleShift)
	if current.NumCbPoints != 0 {
		ub(r, 8, "cb_mult", &current.CbMult)
		ub(r, 8, "cb_luma_mult", &current.CbLumaMult)
		ub(r, 9, "cb_offset", &current.CbOffset)
	}
	if current.NumCrPoints != 0 {
		ub(r, 8, "cr_mult", &current.CrMult)
		ub(r, 8, "cr_luma_mult", &current.CrLumaMult)
		ub(r, 9, "cr_offset", &current.CrOffset)
	}

	flag(r, "overlap_flag", &current.OverlapFlag)
	flag(r, "clip_to_restricted_range", &current.ClipToRestrictedRange)
}

// av1UpdateRefs is the update_refs tail of uncompressed_header.
func (h *AV1Context) av1UpdateRefs(current *AV1RawFrameHeader, seq *AV1RawSequenceHeader) {
	for i := 0; i < av1NumRefFrames; i++ {
		if current.RefreshFrameFlags&(1<<uint(i)) != 0 {
			h.ref[i] = AV1ReferenceFrameState{
				Valid:         1,
				FrameID:       int(current.CurrentFrameID),
				UpscaledWidth: h.upscaledWidth,
				FrameWidth:    h.frameWidth,
				FrameHeight:   h.frameHeight,
				RenderWidth:   h.renderWidth,
				RenderHeight:  h.renderHeight,
				FrameType:     int(current.FrameType),
				SubsamplingX:  int(seq.ColorConfig.SubsamplingX),
				SubsamplingY:  int(seq.ColorConfig.SubsamplingY),
				BitDepth:      h.bitDepth,
				OrderHint:     h.orderHint,
			}

			for j := 0; j < av1RefsPerFrame; j++ {
				h.ref[i].SavedOrderHints[j+av1RefFrameLast] =
					h.orderHints[j+av1RefFrameLast]
			}

			if current.ShowExistingFrame != 0 {
				h.ref[i].LoopFilterRefDeltas = h.loopFilterRefDeltas
				h.ref[i].LoopFilterModeDeltas = h.loopFilterModeDeltas
				h.ref[i].FeatureEnabled = h.featureEnabled
				h.ref[i].FeatureValue = h.featureValue
			} else {
				h.ref[i].LoopFilterRefDeltas = current.LoopFilterRefDeltas
				h.ref[i].LoopFilterModeDeltas = current.LoopFilterModeDeltas
				h.ref[i].FeatureEnabled = current.FeatureEnabled
				h.ref[i].FeatureValue = current.FeatureValue
			}
		}
	}
}

func (h *AV1Context) uncompressedHeader(r *Reader, current *AV1RawFrameHeader) {
	var idLen, diffLen, allFrames, frameIsIntra, orderHintBits int

	if h.sequenceHeader == nil {
		r.diag(LevelError, "No sequence header available: unable to decode frame header.")
		r.fail(ErrInvalidData)
	}
	seq := h.sequenceHeader

	idLen = int(seq.AdditionalFrameIDLengthMinus1) +
		int(seq.DeltaFrameIDLengthMinus2) + 3
	allFrames = 1<<av1NumRefFrames - 1

	if seq.ReducedStillPictureHeader != 0 {
		infer(&current.ShowExistingFrame, 0)
		infer(&current.FrameType, av1FrameKey)
		infer(&current.ShowFrame, 1)
		infer(&current.ShowableFrame, 0)
		frameIsIntra = 1

	} else {
		flag(r, "show_existing_frame", &current.ShowExistingFrame)

		if current.ShowExistingFrame != 0 {
			ub(r, 3, "frame_to_show_map_idx", &current.FrameToShowMapIdx)
			ref := &h.ref[current.FrameToShowMapIdx]

			if ref.Valid == 0 {
				r.diag(LevelError, "Missing reference frame needed for show_existing_frame (frame_to_show_map_idx = %d).",
					current.FrameToShowMapIdx)
				r.fail(ErrInvalidData)
			}

			if seq.DecoderModelInfoPresentFlag != 0 &&
				seq.TimingInfo.EqualPictureInterval == 0 {
				ub(r, int(seq.DecoderModelInfo.FramePresentationTimeLengthMinus1)+1,
					"frame_presentation_time", &current.FramePresentationTime)
			}

			if seq.FrameIDNumbersPresentFlag != 0 {
				ub(r, idLen, "display_frame_id", &current.DisplayFrameID)
			}

			infer(&current.FrameType, uint8(ref.FrameType))
			if current.FrameType == av1FrameKey {
				infer(&current.RefreshFrameFlags, uint8(allFrames))

				// Section 7.21
				infer(&current.CurrentFrameID, uint32(ref.FrameID))
				h.upscaledWidth = ref.UpscaledWidth
				h.frameWidth = ref.FrameWidth
				h.frameHeight = ref.FrameHeight
				h.renderWidth = ref.RenderWidth
				h.renderHeight = ref.RenderHeight
				h.bitDepth = ref.BitDepth
				h.orderHint = ref.OrderHint

				h.loopFilterRefDeltas = ref.LoopFilterRefDeltas
				h.loopFilterModeDeltas = ref.LoopFilterModeDeltas
				h.featureEnabled = ref.FeatureEnabled
				h.featureValue = ref.FeatureValue
			} else {
				infer(&current.RefreshFrameFlags, 0)
			}

			infer(&current.FrameWidthMinus1, uint16(ref.UpscaledWidth-1))
			infer(&current.FrameHeightMinus1, uint16(ref.FrameHeight-1))
			infer(&current.RenderWidthMinus1, uint16(ref.RenderWidth-1))
			infer(&current.RenderHeightMinus1, uint16(ref.RenderHeight-1))

			// Section 7.20
			h.av1UpdateRefs(current, seq)
			return
		}

		ub(r, 2, "frame_type", &current.FrameType)
		frameIsIntra = b2i(current.FrameType == av1FrameIntraOnly ||
			current.FrameType == av1FrameKey)

		flag(r, "show_frame", &current.ShowFrame)
		if current.ShowFrame != 0 &&
			seq.DecoderModelInfoPresentFlag != 0 &&
			seq.TimingInfo.EqualPictureInterval == 0 {
			ub(r, int(seq.DecoderModelInfo.FramePresentationTimeLengthMinus1)+1,
				"frame_presentation_time", &current.FramePresentationTime)
		}
		if current.ShowFrame != 0 {
			infer(&current.ShowableFrame, uint8(b2i(current.FrameType != av1FrameKey)))
		} else {
			flag(r, "showable_frame", &current.ShowableFrame)
		}

		if current.FrameType == av1FrameSwitch ||
			(current.FrameType == av1FrameKey && current.ShowFrame != 0) {
			infer(&current.ErrorResilientMode, 1)
		} else {
			flag(r, "error_resilient_mode", &current.ErrorResilientMode)
		}
	}

	if current.FrameType == av1FrameKey && current.ShowFrame != 0 {
		for i := 0; i < av1NumRefFrames; i++ {
			h.ref[i].Valid = 0
			h.ref[i].OrderHint = 0
		}
		for i := 0; i < av1RefsPerFrame; i++ {
			h.orderHints[i+av1RefFrameLast] = 0
		}
	}

	flag(r, "disable_cdf_update", &current.DisableCdfUpdate)

	if seq.SeqForceScreenContentTools ==
		av1SelectScreenContentTools {
		flag(r, "allow_screen_content_tools", &current.AllowScreenContentTools)
	} else {
		infer(&current.AllowScreenContentTools,
			seq.SeqForceScreenContentTools)
	}
	if current.AllowScreenContentTools != 0 {
		if seq.SeqForceIntegerMV == av1SelectIntegerMV {
			flag(r, "force_integer_mv", &current.ForceIntegerMV)
		} else {
			infer(&current.ForceIntegerMV, seq.SeqForceIntegerMV)
		}
	} else {
		infer(&current.ForceIntegerMV, 0)
	}

	if seq.FrameIDNumbersPresentFlag != 0 {
		ub(r, idLen, "current_frame_id", &current.CurrentFrameID)

		diffLen = int(seq.DeltaFrameIDLengthMinus2) + 2
		for i := 0; i < av1NumRefFrames; i++ {
			if int64(current.CurrentFrameID) > int64(1)<<uint(diffLen) {
				if int64(h.ref[i].FrameID) > int64(current.CurrentFrameID) ||
					int64(h.ref[i].FrameID) < int64(current.CurrentFrameID)-
						int64(1)<<uint(diffLen) {
					h.ref[i].Valid = 0
				}
			} else {
				if int64(h.ref[i].FrameID) > int64(current.CurrentFrameID) &&
					int64(h.ref[i].FrameID) < int64(1)<<uint(idLen)+
						int64(current.CurrentFrameID)-
						int64(1)<<uint(diffLen) {
					h.ref[i].Valid = 0
				}
			}
		}
	} else {
		infer(&current.CurrentFrameID, 0)
	}

	if current.FrameType == av1FrameSwitch {
		infer(&current.FrameSizeOverrideFlag, 1)
	} else if seq.ReducedStillPictureHeader != 0 {
		infer(&current.FrameSizeOverrideFlag, 0)
	} else {
		flag(r, "frame_size_override_flag", &current.FrameSizeOverrideFlag)
	}

	orderHintBits = cond(seq.EnableOrderHint != 0, int(seq.OrderHintBitsMinus1)+1, 0)
	if orderHintBits > 0 {
		ub(r, orderHintBits, "order_hint", &current.OrderHint)
	} else {
		infer(&current.OrderHint, 0)
	}
	h.orderHint = int(current.OrderHint)

	if frameIsIntra != 0 || current.ErrorResilientMode != 0 {
		infer(&current.PrimaryRefFrame, av1PrimaryRefNone)
	} else {
		ub(r, 3, "primary_ref_frame", &current.PrimaryRefFrame)
	}

	if seq.DecoderModelInfoPresentFlag != 0 {
		flag(r, "buffer_removal_time_present_flag", &current.BufferRemovalTimePresentFlag)
		if current.BufferRemovalTimePresentFlag != 0 {
			for i := 0; i <= int(seq.OperatingPointsCntMinus1); i++ {
				if seq.DecoderModelPresentForThisOp[i] != 0 {
					opPtIdc := int(seq.OperatingPointIdc[i])
					inTemporalLayer := (opPtIdc >> uint(h.temporalID)) & 1
					inSpatialLayer := (opPtIdc >> uint(h.spatialID+8)) & 1
					if seq.OperatingPointIdc[i] == 0 ||
						(inTemporalLayer != 0 && inSpatialLayer != 0) {
						ubs(r, int(seq.DecoderModelInfo.BufferRemovalTimeLengthMinus1)+1,
							"buffer_removal_time[i]", &current.BufferRemovalTime[i], i)
					}
				}
			}
		}
	}

	if current.FrameType == av1FrameSwitch ||
		(current.FrameType == av1FrameKey && current.ShowFrame != 0) {
		infer(&current.RefreshFrameFlags, uint8(allFrames))
	} else {
		ub(r, 8, "refresh_frame_flags", &current.RefreshFrameFlags)
	}

	if frameIsIntra == 0 || int(current.RefreshFrameFlags) != allFrames {
		if seq.EnableOrderHint != 0 {
			for i := 0; i < av1NumRefFrames; i++ {
				if current.ErrorResilientMode != 0 {
					ubs(r, orderHintBits, "ref_order_hint[i]", &current.RefOrderHint[i], i)
				} else {
					infer(&current.RefOrderHint[i], uint8(h.ref[i].OrderHint))
				}
				if int(current.RefOrderHint[i]) != h.ref[i].OrderHint {
					h.ref[i].Valid = 0
				}
			}
		}
	}

	if current.FrameType == av1FrameKey ||
		current.FrameType == av1FrameIntraOnly {
		h.frameSize(r, current)
		h.renderSize(r, current)

		if current.AllowScreenContentTools != 0 &&
			h.upscaledWidth == h.frameWidth {
			flag(r, "allow_intrabc", &current.AllowIntrabc)
		} else {
			infer(&current.AllowIntrabc, 0)
		}

	} else {
		if seq.EnableOrderHint == 0 {
			infer(&current.FrameRefsShortSignaling, 0)
		} else {
			flag(r, "frame_refs_short_signaling", &current.FrameRefsShortSignaling)
			if current.FrameRefsShortSignaling != 0 {
				ub(r, 3, "last_frame_idx", &current.LastFrameIdx)
				ub(r, 3, "golden_frame_idx", &current.GoldenFrameIdx)
				h.setFrameRefs(r, current)
			}
		}

		for i := 0; i < av1RefsPerFrame; i++ {
			if current.FrameRefsShortSignaling == 0 {
				ubs(r, 3, "ref_frame_idx[i]", &current.RefFrameIdx[i], i)
			}
			if seq.FrameIDNumbersPresentFlag != 0 {
				ubs(r, int(seq.DeltaFrameIDLengthMinus2)+2,
					"delta_frame_id_minus1[i]", &current.DeltaFrameIDMinus1[i], i)
			}
		}

		if current.FrameSizeOverrideFlag != 0 &&
			current.ErrorResilientMode == 0 {
			h.frameSizeWithRefs(r, current)
		} else {
			h.frameSize(r, current)
			h.renderSize(r, current)
		}

		if current.ForceIntegerMV != 0 {
			infer(&current.AllowHighPrecisionMV, 0)
		} else {
			flag(r, "allow_high_precision_mv", &current.AllowHighPrecisionMV)
		}

		h.interpolationFilter(r, current)

		flag(r, "is_motion_mode_switchable", &current.IsMotionModeSwitchable)

		if current.ErrorResilientMode != 0 ||
			seq.EnableRefFrameMvs == 0 {
			infer(&current.UseRefFrameMvs, 0)
		} else {
			flag(r, "use_ref_frame_mvs", &current.UseRefFrameMvs)
		}

		for i := 0; i < av1RefsPerFrame; i++ {
			refFrame := av1RefFrameLast + i
			hint := h.ref[current.RefFrameIdx[i]].OrderHint
			h.orderHints[refFrame] = hint
			if seq.EnableOrderHint == 0 {
				h.refFrameSignBias[refFrame] = 0
			} else {
				h.refFrameSignBias[refFrame] =
					b2i(av1GetRelativeDist(seq, uint32(hint),
						uint32(current.OrderHint)) > 0)
			}
		}

		infer(&current.AllowIntrabc, 0)
	}

	if seq.ReducedStillPictureHeader != 0 || current.DisableCdfUpdate != 0 {
		infer(&current.DisableFrameEndUpdateCdf, 1)
	} else {
		flag(r, "disable_frame_end_update_cdf", &current.DisableFrameEndUpdateCdf)
	}

	// primary_ref_frame == PRIMARY_REF_NONE: init non-coeff CDFs, setup
	// past independence; else: load CDF tables and params from the
	// previous frame. use_ref_frame_mvs: perform the motion field
	// estimation process. All decoder-side steps — no bits read.

	h.tileInfo(r, current)

	h.quantizationParams(r, current)

	h.segmentationParams(r, current)

	h.deltaQParams(r, current)

	h.deltaLfParams(r, current)

	// Init coeff CDFs / load previous segments.

	h.codedLossless = 1
	for i := 0; i < av1MaxSegments; i++ {
		var qindex int
		if current.FeatureEnabled[i][av1SegLvlAltQ] != 0 {
			qindex = int(current.BaseQIdx) +
				int(current.FeatureValue[i][av1SegLvlAltQ])
		} else {
			qindex = int(current.BaseQIdx)
		}
		qindex = clipUintp2(qindex, 8)

		if qindex != 0 || current.DeltaQYDc != 0 ||
			current.DeltaQUAc != 0 || current.DeltaQUDc != 0 ||
			current.DeltaQVAc != 0 || current.DeltaQVDc != 0 {
			h.codedLossless = 0
		}
	}
	h.allLossless = b2i(h.codedLossless != 0 &&
		h.frameWidth == h.upscaledWidth)

	h.loopFilterParams(r, current)

	h.cdefParams(r, current)

	h.lrParams(r, current)

	h.readTxMode(r, current)

	h.frameReferenceMode(r, current)

	h.skipModeParams(r, current)

	if frameIsIntra != 0 || current.ErrorResilientMode != 0 ||
		seq.EnableWarpedMotion == 0 {
		infer(&current.AllowWarpedMotion, 0)
	} else {
		flag(r, "allow_warped_motion", &current.AllowWarpedMotion)
	}

	flag(r, "reduced_tx_set", &current.ReducedTxSet)

	h.globalMotionParams(r, current)

	h.filmGrainParams(r, &current.FilmGrain, current)

	r.diag(LevelDebug, "Frame %d:  size %dx%d  upscaled %d  render %dx%d  subsample %dx%d  bitdepth %d  tiles %dx%d.",
		h.orderHint,
		h.frameWidth, h.frameHeight, h.upscaledWidth,
		h.renderWidth, h.renderHeight,
		seq.ColorConfig.SubsamplingX+1,
		seq.ColorConfig.SubsamplingY+1, h.bitDepth,
		h.tileRows, h.tileCols)

	h.av1UpdateRefs(current, seq)
}

func (h *AV1Context) frameHeaderOBU(r *Reader, current *AV1RawFrameHeader, redundant bool) {
	if h.seenFrameHeader != 0 {
		if !redundant {
			r.diag(LevelError, "Invalid repeated frame header OBU.")
			r.fail(ErrInvalidData)
		} else {
			r.header("Redundant Frame Header")

			if h.frameHeader == nil {
				r.fail(ErrInvalidData)
			}

			fh := newBitReader(h.frameHeader)
			fh.end = h.frameHeaderSize
			for i := 0; i < h.frameHeaderSize; i += 8 {
				b := min(h.frameHeaderSize-i, 8)
				val := fh.getBits(b)
				readUnsignedRaw(r, b, "frame_header_copy[i]",
					[]int{i / 8}, val, val)
			}
		}
	} else {
		if redundant {
			r.header("Redundant Frame Header (used as Frame Header)")
		} else {
			r.header("Frame Header")
		}

		startPos := r.bitPosition()

		h.uncompressedHeader(r, current)

		h.tileNum = 0

		if current.ShowExistingFrame != 0 {
			h.seenFrameHeader = 0
		} else {
			h.seenFrameHeader = 1

			fhBits := r.bitPosition() - startPos
			fhBytes := (fhBits + 7) / 8

			h.frameHeaderSize = fhBits

			h.frameHeader = append([]byte(nil),
				r.br.data[startPos/8:startPos/8+fhBytes]...)
		}
	}
}

func (h *AV1Context) tileGroupOBU(r *Reader, current *AV1RawTileGroup) {
	var numTiles, tileBits int

	r.header("Tile Group")

	numTiles = h.tileCols * h.tileRows
	if numTiles > 1 {
		flag(r, "tile_start_and_end_present_flag", &current.TileStartAndEndPresentFlag)
	} else {
		infer(&current.TileStartAndEndPresentFlag, 0)
	}

	if numTiles == 1 || current.TileStartAndEndPresentFlag == 0 {
		infer(&current.TgStart, 0)
		infer(&current.TgEnd, uint16(numTiles-1))
	} else {
		tileBits = av1TileLog2(1, h.tileCols) +
			av1TileLog2(1, h.tileRows)
		u(r, tileBits, "tg_start", &current.TgStart,
			uint32(h.tileNum), uint32(numTiles-1))
		u(r, tileBits, "tg_end", &current.TgEnd,
			uint32(current.TgStart), uint32(numTiles-1))
	}

	h.tileNum = int(current.TgEnd) + 1

	h.byteAlignmentOBU(r)

	// Reset header for next frame.
	if int(current.TgEnd) == numTiles-1 {
		h.seenFrameHeader = 0
	}

	// Tile data follows.
}

func (h *AV1Context) frameOBU(r *Reader, current *AV1RawFrame) {
	h.frameHeaderOBU(r, &current.Header, false)

	h.byteAlignmentOBU(r)
}

func (h *AV1Context) tileListOBU(r *Reader, current *AV1RawTileList) {
	ub(r, 8, "output_frame_width_in_tiles_minus_1", &current.OutputFrameWidthInTilesMinus1)
	ub(r, 8, "output_frame_height_in_tiles_minus_1", &current.OutputFrameHeightInTilesMinus1)

	ub(r, 16, "tile_count_minus_1", &current.TileCountMinus1)

	// Tile data follows.
}

func (h *AV1Context) metadataHdrCll(r *Reader, current *AV1RawMetadataHDRCLL) {
	r.header("HDR CLL Metadata")

	ub(r, 16, "max_cll", &current.MaxCLL)
	ub(r, 16, "max_fall", &current.MaxFALL)
}

func (h *AV1Context) metadataHdrMdcv(r *Reader, current *AV1RawMetadataHDRMDCV) {
	r.header("HDR MDCV Metadata")

	for i := 0; i < 3; i++ {
		ubs(r, 16, "primary_chromaticity_x[i]", &current.PrimaryChromaticityX[i], i)
		ubs(r, 16, "primary_chromaticity_y[i]", &current.PrimaryChromaticityY[i], i)
	}

	ub(r, 16, "white_point_chromaticity_x", &current.WhitePointChromaticityX)
	ub(r, 16, "white_point_chromaticity_y", &current.WhitePointChromaticityY)

	ub(r, 32, "luminance_max", &current.LuminanceMax)
	ub(r, 32, "luminance_min", &current.LuminanceMin)
}

func (h *AV1Context) scalabilityStructure(r *Reader, current *AV1RawMetadataScalability) {
	if h.sequenceHeader == nil {
		r.diag(LevelError, "No sequence header available: unable to parse scalability metadata.")
		r.fail(ErrInvalidData)
	}
	seq := h.sequenceHeader

	ub(r, 2, "spatial_layers_cnt_minus_1", &current.SpatialLayersCntMinus1)
	flag(r, "spatial_layer_dimensions_present_flag", &current.SpatialLayerDimensionsPresentFlag)
	flag(r, "spatial_layer_description_present_flag", &current.SpatialLayerDescriptionPresentFlag)
	flag(r, "temporal_group_description_present_flag", &current.TemporalGroupDescriptionPresentFlag)
	u(r, 3, "scalability_structure_reserved_3bits", &current.ScalabilityStructureReserved3Bits, 0, 0)
	if current.SpatialLayerDimensionsPresentFlag != 0 {
		for i := 0; i <= int(current.SpatialLayersCntMinus1); i++ {
			us(r, 16, "spatial_layer_max_width[i]", &current.SpatialLayerMaxWidth[i],
				0, uint32(seq.MaxFrameWidthMinus1)+1, i)
			us(r, 16, "spatial_layer_max_height[i]", &current.SpatialLayerMaxHeight[i],
				0, uint32(seq.MaxFrameHeightMinus1)+1, i)
		}
	}
	if current.SpatialLayerDescriptionPresentFlag != 0 {
		for i := 0; i <= int(current.SpatialLayersCntMinus1); i++ {
			ubs(r, 8, "spatial_layer_ref_id[i]", &current.SpatialLayerRefID[i], i)
		}
	}
	if current.TemporalGroupDescriptionPresentFlag != 0 {
		ub(r, 8, "temporal_group_size", &current.TemporalGroupSize)
		for i := 0; i < int(current.TemporalGroupSize); i++ {
			ubs(r, 3, "temporal_group_temporal_id[i]", &current.TemporalGroupTemporalID[i], i)
			flags(r, "temporal_group_temporal_switching_up_point_flag[i]",
				&current.TemporalGroupTemporalSwitchingUpPointFlag[i], i)
			flags(r, "temporal_group_spatial_switching_up_point_flag[i]",
				&current.TemporalGroupSpatialSwitchingUpPointFlag[i], i)
			ubs(r, 3, "temporal_group_ref_cnt[i]", &current.TemporalGroupRefCnt[i], i)
			for j := 0; j < int(current.TemporalGroupRefCnt[i]); j++ {
				ubs(r, 8, "temporal_group_ref_pic_diff[i][j]",
					&current.TemporalGroupRefPicDiff[i][j], i, j)
			}
		}
	}
}

func (h *AV1Context) metadataScalability(r *Reader, current *AV1RawMetadataScalability) {
	r.header("Scalability Metadata")

	ub(r, 8, "scalability_mode_idc", &current.ScalabilityModeIdc)

	if current.ScalabilityModeIdc == av1ScalabilitySS {
		h.scalabilityStructure(r, current)
	}
}

func (h *AV1Context) metadataItutT35(r *Reader, current *AV1RawMetadataITUTT35) {
	r.header("ITU-T T.35 Metadata")

	ub(r, 8, "itu_t_t35_country_code", &current.ItuTT35CountryCode)
	if current.ItuTT35CountryCode == 0xff {
		ub(r, 8, "itu_t_t35_country_code_extension_byte", &current.ItuTT35CountryCodeExtensionByte)
	}

	// The payload runs up to the start of the trailing bits, but there might
	// be arbitrarily many trailing zeroes so we need to read through twice.
	payloadSize := av1PayloadBytesLeft(r)

	current.Payload = make([]byte, payloadSize)

	for i := 0; i < payloadSize; i++ {
		current.Payload[i] = uint8(readUnsignedRaw(r, 8, "itu_t_t35_payload_bytes[i]",
			[]int{i}, 0x00, 0xff))
	}
}

func (h *AV1Context) metadataTimecode(r *Reader, current *AV1RawMetadataTimecode) {
	r.header("Timecode Metadata")

	ub(r, 5, "counting_type", &current.CountingType)
	flag(r, "full_timestamp_flag", &current.FullTimestampFlag)
	flag(r, "discontinuity_flag", &current.DiscontinuityFlag)
	flag(r, "cnt_dropped_flag", &current.CntDroppedFlag)
	ub(r, 9, "n_frames", &current.NFrames)

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

	ub(r, 5, "time_offset_length", &current.TimeOffsetLength)
	if current.TimeOffsetLength > 0 {
		ub(r, int(current.TimeOffsetLength), "time_offset_value", &current.TimeOffsetValue)
	} else {
		infer(&current.TimeOffsetLength, 0)
	}
}

func (h *AV1Context) metadataUnknown(r *Reader, current *AV1RawMetadataUnknown) {
	r.header("Unknown Metadata")

	payloadSize := av1PayloadBytesLeft(r)

	current.Payload = make([]byte, payloadSize)

	for i := 0; i < payloadSize; i++ {
		ubs(r, 8, "payload[i]", &current.Payload[i], i)
	}
}

func (h *AV1Context) metadataOBU(r *Reader, current *AV1RawMetadata) {
	leb128v(r, "metadata_type", &current.MetadataType)

	switch current.MetadataType {
	case av1MetadataTypeHDRCLL:
		h.metadataHdrCll(r, &current.HDRCLL)
	case av1MetadataTypeHDRMDCV:
		h.metadataHdrMdcv(r, &current.HDRMDCV)
	case av1MetadataTypeScalability:
		h.metadataScalability(r, &current.Scalability)
	case av1MetadataTypeITUTT35:
		h.metadataItutT35(r, &current.ITUTT35)
	case av1MetadataTypeTimecode:
		h.metadataTimecode(r, &current.Timecode)
	default:
		h.metadataUnknown(r, &current.Unknown)
	}
}

func (h *AV1Context) paddingOBU(r *Reader, current *AV1RawPadding) {
	r.header("Padding")

	// The payload runs up to the start of the trailing bits, but there might
	// be arbitrarily many trailing zeroes so we need to read through twice.
	payloadSize := av1PayloadBytesLeft(r)

	current.Payload = make([]byte, payloadSize)

	for i := 0; i < payloadSize; i++ {
		current.Payload[i] = uint8(readUnsignedRaw(r, 8, "obu_padding_byte[i]",
			[]int{i}, 0x00, 0xff))
	}
}
