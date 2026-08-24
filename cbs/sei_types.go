// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the common SEI raw structures (libavcodec/cbs_sei.h) and the SEI
// payload type codes (libavcodec/sei.h) used by H.264 and H.265.

package cbs

// SEI payload type codes (libavcodec/sei.h), subset used by the ported
// descriptor tables.
const (
	seiTypeBufferingPeriod                       = 0
	seiTypePicTiming                             = 1
	seiTypePanScanRect                           = 2
	seiTypeFillerPayload                         = 3
	seiTypeUserDataRegisteredITUTT35             = 4
	seiTypeUserDataUnregistered                  = 5
	seiTypeRecoveryPoint                         = 6
	seiTypeFilmGrainCharacteristics              = 19
	seiTypeFramePackingArrangement               = 45
	seiTypeDisplayOrientation                    = 47
	seiTypeActiveParameterSets                   = 129
	seiTypeDecodedPictureHash                    = 132
	seiTypeTimeCode                              = 136
	seiTypeMasteringDisplayColourVolume          = 137
	seiTypeContentLightLevelInfo                 = 144
	seiTypeAlternativeTransferCharacteristics    = 147
	seiTypeAmbientViewingEnvironment             = 148
	seiTypeAlphaChannelInfo                      = 165
	seiTypeThreeDimensionalReferenceDisplaysInfo = 176
)

type SEIRawFillerPayload struct {
	PayloadSize uint32
}

type SEIRawUserDataRegistered struct {
	ItuTT35CountryCode              uint8
	ItuTT35CountryCodeExtensionByte uint8
	Data                            []uint8
}

type SEIRawUserDataUnregistered struct {
	UuidIsoIec11578 [16]uint8
	Data            []uint8
}

type SEIRawFramePackingArrangement struct {
	FpArrangementID              uint32
	FpArrangementCancelFlag      uint8
	FpArrangementType            uint8
	FpQuincunxSamplingFlag       uint8
	FpContentInterpretationType  uint8
	FpSpatialFlippingFlag        uint8
	FpFrame0FlippedFlag          uint8
	FpFieldViewsFlag             uint8
	FpCurrentFrameIsFrame0Flag   uint8
	FpFrame0SelfContainedFlag    uint8
	FpFrame1SelfContainedFlag    uint8
	FpFrame0GridPositionX        uint8
	FpFrame0GridPositionY        uint8
	FpFrame1GridPositionX        uint8
	FpFrame1GridPositionY        uint8
	FpArrangementPersistenceFlag uint8
	FpUpsampledAspectRatioFlag   uint8
}

type SEIRawDecodedPictureHash struct {
	DphSeiHashType            uint8
	DphSeiSingleComponentFlag uint8
	DphSeiPictureMd5          [3][16]uint8
	DphSeiPictureCrc          [3]uint16
	DphSeiPictureChecksum     [3]uint32

	DphSeiReservedZero7Bits uint8
}

type SEIRawMasteringDisplayColourVolume struct {
	DisplayPrimariesX            [3]uint16
	DisplayPrimariesY            [3]uint16
	WhitePointX                  uint16
	WhitePointY                  uint16
	MaxDisplayMasteringLuminance uint32
	MinDisplayMasteringLuminance uint32
}

type SEIRawContentLightLevelInfo struct {
	MaxContentLightLevel    uint16
	MaxPicAverageLightLevel uint16
}

type SEIRawAlternativeTransferCharacteristics struct {
	PreferredTransferCharacteristics uint8
}

type SEIRawAmbientViewingEnvironment struct {
	AmbientIlluminance uint32
	AmbientLightX      uint16
	AmbientLightY      uint16
}

// SEIRawMessage is one SEI message (payload type, size, decomposed payload
// when a descriptor exists, and any reserved payload extension bits).
type SEIRawMessage struct {
	PayloadType        uint32
	PayloadSize        uint32
	Payload            any
	ExtensionData      []uint8
	ExtensionBitLength int
}

// SEIRawMessageList is the message list of one SEI NAL unit.
type SEIRawMessageList struct {
	Messages []SEIRawMessage
}

// SEIMessageState mirrors the C struct passed to payload readers.
type SEIMessageState struct {
	PayloadType      uint32
	PayloadSize      uint32
	ExtensionPresent uint8
}

// seiTypeDescriptor mirrors SEIMessageTypeDescriptor (read side only).
type seiTypeDescriptor struct {
	typ    int
	prefix bool
	suffix bool
	alloc  func() any
	read   func(r *Reader, cur any, state *SEIMessageState)
}
