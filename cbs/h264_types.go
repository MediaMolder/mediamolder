// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the H.264 raw structures and limits (libavcodec/cbs_h264.h,
// libavcodec/h264.h). Field names are the C names in Go casing.

package cbs

// Table 7-1 NAL unit type codes (libavcodec/h264.h).
const (
	h264NALUnspecified     = 0
	h264NALSlice           = 1
	h264NALDPA             = 2
	h264NALDPB             = 3
	h264NALDPC             = 4
	h264NALIDRSlice        = 5
	h264NALSEI             = 6
	h264NALSPS             = 7
	h264NALPPS             = 8
	h264NALAUD             = 9
	h264NALEndSequence     = 10
	h264NALEndStream       = 11
	h264NALFillerData      = 12
	h264NALSPSExt          = 13
	h264NALPrefix          = 14
	h264NALSubSPS          = 15
	h264NALDPS             = 16
	h264NALAuxiliarySlice  = 19
	h264NALExtenSlice      = 20
	h264NALDepthExtenSlice = 21
)

// Limits (libavcodec/h264.h).
const (
	h264MaxSPSCount    = 32
	h264MaxPPSCount    = 256
	h264MaxDPBFrames   = 16
	h264MaxRefs        = 2 * h264MaxDPBFrames
	h264MaxRPLMCount   = h264MaxRefs + 1
	h264MaxMMCOCount   = h264MaxRefs*2 + 3
	h264MaxSliceGroups = 8
	h264MaxCPBCnt      = 32
	h264MaxMBPicSize   = 139264
	h264MaxMBWidth     = 1055
	h264MaxMBHeight    = 1055
	h264MaxWidth       = h264MaxMBWidth * 16
	h264MaxHeight      = h264MaxMBHeight * 16
)

// h264NALUnitName mirrors h264_nal_unit_name (libavcodec/h2645_parse.c).
func h264NALUnitName(typ uint32) string {
	names := [32]string{
		"Unspecified 0", "Coded slice of a non-IDR picture",
		"Coded slice data partition A", "Coded slice data partition B",
		"Coded slice data partition C", "IDR", "SEI", "SPS", "PPS", "AUD",
		"End of sequence", "End of stream", "Filler data",
		"SPS extension", "Prefix", "Subset SPS", "Depth parameter set",
		"Reserved 17", "Reserved 18",
		"Auxiliary coded picture without partitioning",
		"Slice extension", "Slice extension for a depth view",
		"Reserved 22", "Reserved 23", "Unspecified 24", "Unspecified 25",
		"Unspecified 26", "Unspecified 27", "Unspecified 28",
		"Unspecified 29", "Unspecified 30", "Unspecified 31",
	}
	if typ < 32 {
		return names[typ]
	}
	return "unknown"
}

type H264RawNALUnitHeader struct {
	NalRefIdc   uint8
	NalUnitType uint8

	SVCExtensionFlag   uint8
	AVC3DExtensionFlag uint8
}

type H264RawScalingList struct {
	DeltaScale [64]int8
}

type H264RawHRD struct {
	CpbCntMinus1 uint8
	BitRateScale uint8
	CpbSizeScale uint8

	BitRateValueMinus1 [h264MaxCPBCnt]uint32
	CpbSizeValueMinus1 [h264MaxCPBCnt]uint32
	CbrFlag            [h264MaxCPBCnt]uint8

	InitialCpbRemovalDelayLengthMinus1 uint8
	CpbRemovalDelayLengthMinus1        uint8
	DpbOutputDelayLengthMinus1         uint8
	TimeOffsetLength                   uint8
}

type H264RawVUI struct {
	AspectRatioInfoPresentFlag uint8
	AspectRatioIdc             uint8
	SarWidth                   uint16
	SarHeight                  uint16

	OverscanInfoPresentFlag uint8
	OverscanAppropriateFlag uint8

	VideoSignalTypePresentFlag   uint8
	VideoFormat                  uint8
	VideoFullRangeFlag           uint8
	ColourDescriptionPresentFlag uint8
	ColourPrimaries              uint8
	TransferCharacteristics      uint8
	MatrixCoefficients           uint8

	ChromaLocInfoPresentFlag       uint8
	ChromaSampleLocTypeTopField    uint8
	ChromaSampleLocTypeBottomField uint8

	TimingInfoPresentFlag uint8
	NumUnitsInTick        uint32
	TimeScale             uint32
	FixedFrameRateFlag    uint8

	NalHrdParametersPresentFlag uint8
	NalHrdParameters            H264RawHRD
	VclHrdParametersPresentFlag uint8
	VclHrdParameters            H264RawHRD
	LowDelayHrdFlag             uint8

	PicStructPresentFlag uint8

	BitstreamRestrictionFlag           uint8
	MotionVectorsOverPicBoundariesFlag uint8
	MaxBytesPerPicDenom                uint8
	MaxBitsPerMbDenom                  uint8
	Log2MaxMvLengthHorizontal          uint8
	Log2MaxMvLengthVertical            uint8
	MaxNumReorderFrames                uint8
	MaxDecFrameBuffering               uint8
}

type H264RawSPS struct {
	NalUnitHeader H264RawNALUnitHeader

	ProfileIdc         uint8
	ConstraintSet0Flag uint8
	ConstraintSet1Flag uint8
	ConstraintSet2Flag uint8
	ConstraintSet3Flag uint8
	ConstraintSet4Flag uint8
	ConstraintSet5Flag uint8
	ReservedZero2Bits  uint8
	LevelIdc           uint8

	SeqParameterSetID uint8

	ChromaFormatIdc                 uint8
	SeparateColourPlaneFlag         uint8
	BitDepthLumaMinus8              uint8
	BitDepthChromaMinus8            uint8
	QpprimeYZeroTransformBypassFlag uint8

	SeqScalingMatrixPresentFlag uint8
	SeqScalingListPresentFlag   [12]uint8
	ScalingList4x4              [6]H264RawScalingList
	ScalingList8x8              [6]H264RawScalingList

	Log2MaxFrameNumMinus4          uint8
	PicOrderCntType                uint8
	Log2MaxPicOrderCntLsbMinus4    uint8
	DeltaPicOrderAlwaysZeroFlag    uint8
	OffsetForNonRefPic             int32
	OffsetForTopToBottomField      int32
	NumRefFramesInPicOrderCntCycle uint8
	OffsetForRefFrame              [256]int32

	MaxNumRefFrames           uint8
	GapsInFrameNumAllowedFlag uint8

	PicWidthInMbsMinus1       uint16
	PicHeightInMapUnitsMinus1 uint16

	FrameMbsOnlyFlag         uint8
	MbAdaptiveFrameFieldFlag uint8
	Direct8x8InferenceFlag   uint8

	FrameCroppingFlag     uint8
	FrameCropLeftOffset   uint16
	FrameCropRightOffset  uint16
	FrameCropTopOffset    uint16
	FrameCropBottomOffset uint16

	VuiParametersPresentFlag uint8
	Vui                      H264RawVUI
}

type H264RawSPSExtension struct {
	NalUnitHeader H264RawNALUnitHeader

	SeqParameterSetID uint8

	AuxFormatIdc          uint8
	BitDepthAuxMinus8     uint8
	AlphaIncrFlag         uint8
	AlphaOpaqueValue      uint16
	AlphaTransparentValue uint16

	AdditionalExtensionFlag uint8
}

type H264RawPPS struct {
	NalUnitHeader H264RawNALUnitHeader

	PicParameterSetID uint8
	SeqParameterSetID uint8

	EntropyCodingModeFlag                 uint8
	BottomFieldPicOrderInFramePresentFlag uint8

	NumSliceGroupsMinus1          uint8
	SliceGroupMapType             uint8
	RunLengthMinus1               [h264MaxSliceGroups]uint16
	TopLeft                       [h264MaxSliceGroups]uint16
	BottomRight                   [h264MaxSliceGroups]uint16
	SliceGroupChangeDirectionFlag uint8
	SliceGroupChangeRateMinus1    uint16
	PicSizeInMapUnitsMinus1       uint16

	SliceGroupID []uint8

	NumRefIdxL0DefaultActiveMinus1 uint8
	NumRefIdxL1DefaultActiveMinus1 uint8

	WeightedPredFlag  uint8
	WeightedBipredIdc uint8

	PicInitQpMinus26    int8
	PicInitQsMinus26    int8
	ChromaQpIndexOffset int8

	DeblockingFilterControlPresentFlag uint8
	ConstrainedIntraPredFlag           uint8

	MoreRbspData uint8

	RedundantPicCntPresentFlag uint8
	Transform8x8ModeFlag       uint8

	PicScalingMatrixPresentFlag uint8
	PicScalingListPresentFlag   [12]uint8
	ScalingList4x4              [6]H264RawScalingList
	ScalingList8x8              [6]H264RawScalingList

	SecondChromaQpIndexOffset int8
}

type H264RawAUD struct {
	NalUnitHeader H264RawNALUnitHeader

	PrimaryPicType uint8
}

type H264RawSEIBufferingPeriodHRD struct {
	InitialCpbRemovalDelay       [h264MaxCPBCnt]uint32
	InitialCpbRemovalDelayOffset [h264MaxCPBCnt]uint32
}

type H264RawSEIBufferingPeriod struct {
	SeqParameterSetID uint8
	Nal, Vcl          H264RawSEIBufferingPeriodHRD
}

type H264RawSEIPicTimestamp struct {
	CtType             uint8
	NuitFieldBasedFlag uint8
	CountingType       uint8
	FullTimestampFlag  uint8
	DiscontinuityFlag  uint8
	CntDroppedFlag     uint8
	NFrames            uint8
	SecondsFlag        uint8
	SecondsValue       uint8
	MinutesFlag        uint8
	MinutesValue       uint8
	HoursFlag          uint8
	HoursValue         uint8
	TimeOffset         int32
}

type H264RawSEIPicTiming struct {
	CpbRemovalDelay    uint32
	DpbOutputDelay     uint32
	PicStruct          uint8
	ClockTimestampFlag [3]uint8
	Timestamp          [3]H264RawSEIPicTimestamp
}

type H264RawSEIPanScanRect struct {
	PanScanRectID               uint32
	PanScanRectCancelFlag       uint8
	PanScanCntMinus1            uint8
	PanScanRectLeftOffset       [3]int32
	PanScanRectRightOffset      [3]int32
	PanScanRectTopOffset        [3]int32
	PanScanRectBottomOffset     [3]int32
	PanScanRectRepetitionPeriod uint16
}

type H264RawSEIRecoveryPoint struct {
	RecoveryFrameCnt      uint16
	ExactMatchFlag        uint8
	BrokenLinkFlag        uint8
	ChangingSliceGroupIdc uint8
}

type H264RawFilmGrainCharacteristics struct {
	FilmGrainCharacteristicsCancelFlag       uint8
	FilmGrainModelID                         uint8
	SeparateColourDescriptionPresentFlag     uint8
	FilmGrainBitDepthLumaMinus8              uint8
	FilmGrainBitDepthChromaMinus8            uint8
	FilmGrainFullRangeFlag                   uint8
	FilmGrainColourPrimaries                 uint8
	FilmGrainTransferCharacteristics         uint8
	FilmGrainMatrixCoefficients              uint8
	BlendingModeID                           uint8
	Log2ScaleFactor                          uint8
	CompModelPresentFlag                     [3]uint8
	NumIntensityIntervalsMinus1              [3]uint8
	NumModelValuesMinus1                     [3]uint8
	IntensityIntervalLowerBound              [3][256]uint8
	IntensityIntervalUpperBound              [3][256]uint8
	CompModelValue                           [3][256][6]int16
	FilmGrainCharacteristicsRepetitionPeriod uint8
}

type H264RawSEIFramePackingArrangement struct {
	FramePackingArrangementID               uint32
	FramePackingArrangementCancelFlag       uint8
	FramePackingArrangementType             uint8
	QuincunxSamplingFlag                    uint8
	ContentInterpretationType               uint8
	SpatialFlippingFlag                     uint8
	Frame0FlippedFlag                       uint8
	FieldViewsFlag                          uint8
	CurrentFrameIsFrame0Flag                uint8
	Frame0SelfContainedFlag                 uint8
	Frame1SelfContainedFlag                 uint8
	Frame0GridPositionX                     uint8
	Frame0GridPositionY                     uint8
	Frame1GridPositionX                     uint8
	Frame1GridPositionY                     uint8
	FramePackingArrangementRepetitionPeriod uint16
	FramePackingArrangementExtensionFlag    uint8
}

type H264RawSEIDisplayOrientation struct {
	DisplayOrientationCancelFlag       uint8
	HorFlip                            uint8
	VerFlip                            uint8
	AnticlockwiseRotation              uint16
	DisplayOrientationRepetitionPeriod uint16
	DisplayOrientationExtensionFlag    uint8
}

type H264RawSEI struct {
	NalUnitHeader H264RawNALUnitHeader
	MessageList   SEIRawMessageList
}

type H264RawSliceHeaderRPLM struct {
	ModificationOfPicNumsIdc uint8
	AbsDiffPicNumMinus1      int32
	LongTermPicNum           uint8
}

type H264RawSliceHeaderMMCO struct {
	MemoryManagementControlOperation uint8
	DifferenceOfPicNumsMinus1        int32
	LongTermPicNum                   uint8
	LongTermFrameIdx                 uint8
	MaxLongTermFrameIdxPlus1         uint8
}

type H264RawSliceHeader struct {
	NalUnitHeader H264RawNALUnitHeader

	FirstMbInSlice uint32
	SliceType      uint8

	PicParameterSetID uint8

	ColourPlaneID uint8

	FrameNum        uint16
	FieldPicFlag    uint8
	BottomFieldFlag uint8

	IdrPicID uint16

	PicOrderCntLsb         uint16
	DeltaPicOrderCntBottom int32
	DeltaPicOrderCnt       [2]int32

	RedundantPicCnt         uint8
	DirectSpatialMvPredFlag uint8

	NumRefIdxActiveOverrideFlag uint8
	NumRefIdxL0ActiveMinus1     uint8
	NumRefIdxL1ActiveMinus1     uint8

	RefPicListModificationFlagL0 uint8
	RefPicListModificationFlagL1 uint8
	RplmL0                       [h264MaxRPLMCount]H264RawSliceHeaderRPLM
	RplmL1                       [h264MaxRPLMCount]H264RawSliceHeaderRPLM

	LumaLog2WeightDenom   uint8
	ChromaLog2WeightDenom uint8

	LumaWeightL0Flag   [h264MaxRefs]uint8
	LumaWeightL0       [h264MaxRefs]int8
	LumaOffsetL0       [h264MaxRefs]int8
	ChromaWeightL0Flag [h264MaxRefs]uint8
	ChromaWeightL0     [h264MaxRefs][2]int8
	ChromaOffsetL0     [h264MaxRefs][2]int8

	LumaWeightL1Flag   [h264MaxRefs]uint8
	LumaWeightL1       [h264MaxRefs]int8
	LumaOffsetL1       [h264MaxRefs]int8
	ChromaWeightL1Flag [h264MaxRefs]uint8
	ChromaWeightL1     [h264MaxRefs][2]int8
	ChromaOffsetL1     [h264MaxRefs][2]int8

	NoOutputOfPriorPicsFlag uint8
	LongTermReferenceFlag   uint8

	AdaptiveRefPicMarkingModeFlag uint8
	Mmco                          [h264MaxMMCOCount]H264RawSliceHeaderMMCO

	CabacInitIdc uint8

	SliceQpDelta int8

	SpForSwitchFlag uint8
	SliceQsDelta    int8

	DisableDeblockingFilterIdc uint8
	SliceAlphaC0OffsetDiv2     int8
	SliceBetaOffsetDiv2        int8

	SliceGroupChangeCycle uint16
}

type H264RawSlice struct {
	Header H264RawSliceHeader

	Data         []byte
	DataBitStart int
}

type H264RawFiller struct {
	NalUnitHeader H264RawNALUnitHeader

	FillerSize uint32
}
