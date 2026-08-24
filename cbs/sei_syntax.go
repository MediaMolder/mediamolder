// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the common SEI payload readers
// (libavcodec/cbs_sei_syntax_template.c, READ side). The h274 payloads
// (H.266-only) are not ported.

package cbs

func seiReadFillerPayload(r *Reader, curAny any, state *SEIMessageState) {
	current := curAny.(*SEIRawFillerPayload)

	r.header("Filler Payload")

	current.PayloadSize = state.PayloadSize

	for i := 0; i < int(current.PayloadSize); i++ {
		fixed(r, 8, "ff_byte", 0xff)
	}
}

func seiReadUserDataRegistered(r *Reader, curAny any, state *SEIMessageState) {
	current := curAny.(*SEIRawUserDataRegistered)

	r.header("User Data Registered ITU-T T.35")

	var i int
	u(r, 8, "itu_t_t35_country_code", &current.ItuTT35CountryCode, 0x00, 0xff)
	if current.ItuTT35CountryCode != 0xff {
		i = 1
	} else {
		u(r, 8, "itu_t_t35_country_code_extension_byte",
			&current.ItuTT35CountryCodeExtensionByte, 0x00, 0xff)
		i = 2
	}

	if int(state.PayloadSize) < i {
		r.diag(LevelError, "Invalid SEI user data registered payload.")
		r.fail(ErrInvalidData)
	}
	dataLength := int(state.PayloadSize) - i

	current.Data = make([]uint8, dataLength)
	for j := 0; j < dataLength; j++ {
		current.Data[j] = uint8(readUnsignedRaw(r, 8,
			"itu_t_t35_payload_byte[]", []int{i + j}, 0x00, 0xff))
	}
}

func seiReadUserDataUnregistered(r *Reader, curAny any, state *SEIMessageState) {
	current := curAny.(*SEIRawUserDataUnregistered)

	r.header("User Data Unregistered")

	if state.PayloadSize < 16 {
		r.diag(LevelError, "Invalid SEI user data unregistered payload.")
		r.fail(ErrInvalidData)
	}
	dataLength := int(state.PayloadSize) - 16

	for i := 0; i < 16; i++ {
		us(r, 8, "uuid_iso_iec_11578[i]", &current.UuidIsoIec11578[i],
			0x00, 0xff, i)
	}

	current.Data = make([]uint8, dataLength)
	for i := 0; i < dataLength; i++ {
		current.Data[i] = uint8(readUnsignedRaw(r, 8,
			"user_data_payload_byte[i]", []int{i}, 0x00, 0xff))
	}
}

func seiReadFramePackingArrangement(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawFramePackingArrangement)

	r.header("Frame Packing Arrangement")

	ue(r, "fp_arrangement_id", &current.FpArrangementID, 0, maxUintBits(31))
	flag(r, "fp_arrangement_cancel_flag", &current.FpArrangementCancelFlag)
	if current.FpArrangementCancelFlag == 0 {
		u(r, 7, "fp_arrangement_type", &current.FpArrangementType, 3, 5)
		flag(r, "fp_quincunx_sampling_flag", &current.FpQuincunxSamplingFlag)
		u(r, 6, "fp_content_interpretation_type", &current.FpContentInterpretationType, 0, 2)
		flag(r, "fp_spatial_flipping_flag", &current.FpSpatialFlippingFlag)
		flag(r, "fp_frame0_flipped_flag", &current.FpFrame0FlippedFlag)
		flag(r, "fp_field_views_flag", &current.FpFieldViewsFlag)
		flag(r, "fp_current_frame_is_frame0_flag", &current.FpCurrentFrameIsFrame0Flag)
		flag(r, "fp_frame0_self_contained_flag", &current.FpFrame0SelfContainedFlag)
		flag(r, "fp_frame1_self_contained_flag", &current.FpFrame1SelfContainedFlag)
		if current.FpQuincunxSamplingFlag == 0 && current.FpArrangementType != 5 {
			ub(r, 4, "fp_frame0_grid_position_x", &current.FpFrame0GridPositionX)
			ub(r, 4, "fp_frame0_grid_position_y", &current.FpFrame0GridPositionY)
			ub(r, 4, "fp_frame1_grid_position_x", &current.FpFrame1GridPositionX)
			ub(r, 4, "fp_frame1_grid_position_y", &current.FpFrame1GridPositionY)
		}
		fixed(r, 8, "fp_arrangement_reserved_byte", 0)
		flag(r, "fp_arrangement_persistence_flag", &current.FpArrangementPersistenceFlag)
	}
	flag(r, "fp_upsampled_aspect_ratio_flag", &current.FpUpsampledAspectRatioFlag)
}

func seiReadDecodedPictureHash(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawDecodedPictureHash)

	r.header("Decoded Picture Hash")

	u(r, 8, "dph_sei_hash_type", &current.DphSeiHashType, 0, 2)
	flag(r, "dph_sei_single_component_flag", &current.DphSeiSingleComponentFlag)
	ub(r, 7, "dph_sei_reserved_zero_7bits", &current.DphSeiReservedZero7Bits)

	for cIdx := 0; cIdx < cond(current.DphSeiSingleComponentFlag != 0, 1, 3); cIdx++ {
		if current.DphSeiHashType == 0 {
			for i := 0; i < 16; i++ {
				us(r, 8, "dph_sei_picture_md5[c_idx][i]",
					&current.DphSeiPictureMd5[cIdx][i], 0x00, 0xff, cIdx, i)
			}
		} else if current.DphSeiHashType == 1 {
			us(r, 16, "dph_sei_picture_crc[c_idx]",
				&current.DphSeiPictureCrc[cIdx], 0x0000, 0xffff, cIdx)
		} else if current.DphSeiHashType == 2 {
			us(r, 32, "dph_sei_picture_checksum[c_idx]",
				&current.DphSeiPictureChecksum[cIdx], 0x00000000, 0xffffffff, cIdx)
		}
	}
}

func seiReadMasteringDisplayColourVolume(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawMasteringDisplayColourVolume)

	r.header("Mastering Display Colour Volume")

	for c := 0; c < 3; c++ {
		ubs(r, 16, "display_primaries_x[c]", &current.DisplayPrimariesX[c], c)
		ubs(r, 16, "display_primaries_y[c]", &current.DisplayPrimariesY[c], c)
	}

	ub(r, 16, "white_point_x", &current.WhitePointX)
	ub(r, 16, "white_point_y", &current.WhitePointY)

	ub(r, 32, "max_display_mastering_luminance", &current.MaxDisplayMasteringLuminance)
	ub(r, 32, "min_display_mastering_luminance", &current.MinDisplayMasteringLuminance)
}

func seiReadContentLightLevelInfo(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawContentLightLevelInfo)

	r.header("Content Light Level Information")

	ub(r, 16, "max_content_light_level", &current.MaxContentLightLevel)
	ub(r, 16, "max_pic_average_light_level", &current.MaxPicAverageLightLevel)
}

func seiReadAlternativeTransferCharacteristics(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawAlternativeTransferCharacteristics)

	r.header("Alternative Transfer Characteristics")

	ub(r, 8, "preferred_transfer_characteristics", &current.PreferredTransferCharacteristics)
}

func seiReadAmbientViewingEnvironment(r *Reader, curAny any, _ *SEIMessageState) {
	current := curAny.(*SEIRawAmbientViewingEnvironment)

	const maxAmbientLightValue = 50000

	r.header("Ambient Viewing Environment")

	u(r, 32, "ambient_illuminance", &current.AmbientIlluminance, 1, maxUintBits(32))
	u(r, 16, "ambient_light_x", &current.AmbientLightX, 0, maxAmbientLightValue)
	u(r, 16, "ambient_light_y", &current.AmbientLightY, 0, maxAmbientLightValue)
}
