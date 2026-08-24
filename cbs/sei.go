// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the SEI message machinery: message() and message_list() from
// libavcodec/cbs_sei_syntax_template.c (read side) and the descriptor
// lookup from libavcodec/cbs_sei.c. The h274 descriptor list is H.266-only
// and is not ported.

package cbs

import "math/bits"

// seiHost resolves payload types to descriptors; implemented per codec
// (codec-specific list first, then the common list, as in
// ff_cbs_sei_find_type).
type seiHost interface {
	seiFindType(payloadType int) *seiTypeDescriptor
}

func seiFindIn(list []seiTypeDescriptor, payloadType int) *seiTypeDescriptor {
	for i := range list {
		if list[i].typ == payloadType {
			return &list[i]
		}
	}
	return nil
}

// seiMessage is FUNC(message) (cbs_sei_syntax_template.c).
func seiMessage(host seiHost, r *Reader, current *SEIRawMessage) {
	desc := host.seiFindType(int(current.PayloadType))
	if desc != nil {
		state := SEIMessageState{
			PayloadType: current.PayloadType,
			PayloadSize: current.PayloadSize,
		}
		if current.ExtensionBitLength > 0 {
			state.ExtensionPresent = 1
		}

		current.Payload = desc.alloc()

		startPosition := r.bitPosition()

		desc.read(r, current.Payload, &state)

		currentPosition := r.bitPosition()
		bitsWritten := currentPosition - startPosition

		if r.byteAlignment() != 0 || state.ExtensionPresent != 0 ||
			bitsWritten < 8*int(current.PayloadSize) {
			bitsLeft := 8*int(current.PayloadSize) - bitsWritten
			if bitsLeft < 0 {
				r.fail(ErrInvalidData)
			}

			tmp := r.br
			if bitsLeft > 8 {
				tmp.skipBits(bitsLeft - 8)
			}
			trailingBits := tmp.getBits(min(bitsLeft, 8))
			if trailingBits == 0 {
				// The trailing bits must contain a bit_equal_to_one, so
				// they can't all be zero.
				r.fail(ErrInvalidData)
			}
			trailingZeroBits := bits.TrailingZeros32(trailingBits)
			current.ExtensionBitLength = bitsLeft - 1 - trailingZeroBits

			if current.ExtensionBitLength > 0 {
				current.ExtensionData = make([]uint8, (current.ExtensionBitLength+7)/8)
				bl := current.ExtensionBitLength
				for i := 0; bl > 0; i++ {
					length := min(bl, 8)
					current.ExtensionData[i] = uint8(readUnsignedRaw(r,
						length, "reserved_payload_extension_data", nil,
						0, maxUintBits(length)))
					bl -= length
				}
			}

			fixed(r, 1, "bit_equal_to_one", 1)
			for r.byteAlignment() != 0 {
				fixed(r, 1, "bit_equal_to_zero", 0)
			}
		}
	} else {
		data := make([]uint8, current.PayloadSize)
		for i := 0; i < int(current.PayloadSize); i++ {
			data[i] = uint8(readUnsignedRaw(r, 8, "payload_byte[i]",
				[]int{i}, 0, 255))
		}
		current.Payload = data
	}
}

// seiMessageList is FUNC(message_list) (cbs_sei_syntax_template.c, READ).
func seiMessageList(host seiHost, r *Reader, current *SEIRawMessageList) {
	for k := 0; ; k++ {
		var payloadType, payloadSize, tmp uint32

		for r.br.showBits(8) == 0xff {
			fixed(r, 8, "ff_byte", 0xff)
			payloadType += 255
		}
		u(r, 8, "last_payload_type_byte", &tmp, 0, 254)
		payloadType += tmp

		for r.br.showBits(8) == 0xff {
			fixed(r, 8, "ff_byte", 0xff)
			payloadSize += 255
		}
		u(r, 8, "last_payload_size_byte", &tmp, 0, 254)
		payloadSize += tmp

		// There must be space remaining for both the payload and
		// the trailing bits on the SEI NAL unit.
		if int(payloadSize)+1 > r.br.bitsLeft()/8 {
			r.diag(LevelError,
				"Invalid SEI message: payload_size too large (%d bytes).",
				payloadSize)
			r.fail(ErrInvalidData)
		}
		payloadReader := r.sub(8 * int(payloadSize))

		current.Messages = append(current.Messages, SEIRawMessage{
			PayloadType: payloadType,
			PayloadSize: payloadSize,
		})
		message := &current.Messages[k]

		seiMessage(host, &payloadReader, message)

		r.br.skipBits(8 * int(payloadSize))

		if !r.moreRBSPData() {
			break
		}
	}
}

// payloadExtensionPresent is ff_cbs_h2645_payload_extension_present.
func payloadExtensionPresent(r *Reader, payloadSize uint32, curPos int) bool {
	bitsLeft := int(payloadSize)*8 - curPos
	return bitsLeft > 0 &&
		(bitsLeft > 7 || r.br.showBits(bitsLeft)&maxUintBits(bitsLeft-1) != 0)
}

// seiCommonTypes is cbs_sei_common_types (cbs_sei.c).
var seiCommonTypes = []seiTypeDescriptor{
	{seiTypeFillerPayload, true, true,
		func() any { return new(SEIRawFillerPayload) }, seiReadFillerPayload},
	{seiTypeUserDataRegisteredITUTT35, true, true,
		func() any { return new(SEIRawUserDataRegistered) }, seiReadUserDataRegistered},
	{seiTypeUserDataUnregistered, true, true,
		func() any { return new(SEIRawUserDataUnregistered) }, seiReadUserDataUnregistered},
	{seiTypeFramePackingArrangement, true, false,
		func() any { return new(SEIRawFramePackingArrangement) }, seiReadFramePackingArrangement},
	{seiTypeDecodedPictureHash, false, true,
		func() any { return new(SEIRawDecodedPictureHash) }, seiReadDecodedPictureHash},
	{seiTypeMasteringDisplayColourVolume, true, false,
		func() any { return new(SEIRawMasteringDisplayColourVolume) }, seiReadMasteringDisplayColourVolume},
	{seiTypeContentLightLevelInfo, true, false,
		func() any { return new(SEIRawContentLightLevelInfo) }, seiReadContentLightLevelInfo},
	{seiTypeAlternativeTransferCharacteristics, true, false,
		func() any { return new(SEIRawAlternativeTransferCharacteristics) }, seiReadAlternativeTransferCharacteristics},
	{seiTypeAmbientViewingEnvironment, true, false,
		func() any { return new(SEIRawAmbientViewingEnvironment) }, seiReadAmbientViewingEnvironment},
}
