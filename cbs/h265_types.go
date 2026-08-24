// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the H.265 raw structures and limits (libavcodec/cbs_h265.h,
// libavcodec/hevc/hevc.h) and the name table from h2645_parse.c. Field
// names are the C names in Go casing.

package cbs

const (
	hevcNALTrailN       = 0
	hevcNALTrailR       = 1
	hevcNALTSAN         = 2
	hevcNALTSAR         = 3
	hevcNALSTSAN        = 4
	hevcNALSTSAR        = 5
	hevcNALRADLN        = 6
	hevcNALRADLR        = 7
	hevcNALRASLN        = 8
	hevcNALRASLR        = 9
	hevcNALBLAWLP       = 16
	hevcNALBLAWRADL     = 17
	hevcNALBLANLP       = 18
	hevcNALIDRWRADL     = 19
	hevcNALIDRNLP       = 20
	hevcNALCRANUT       = 21
	hevcNALRsvIrapVcl22 = 22
	hevcNALRsvIrapVcl23 = 23
	hevcNALVPS          = 32
	hevcNALSPS          = 33
	hevcNALPPS          = 34
	hevcNALAUD          = 35
	hevcNALEOSNUT       = 36
	hevcNALEOBNUT       = 37
	hevcNALFDNUT        = 38
	hevcNALSEIPrefix    = 39
	hevcNALSEISuffix    = 40
)

// hevcNALUnitName mirrors hevc_nal_type_name (libavcodec/h2645_parse.c).
func hevcNALUnitName(typ uint32) string {
	names := [64]string{
		"TRAIL_N", "TRAIL_R", "TSA_N", "TSA_R", "STSA_N", "STSA_R",
		"RADL_N", "RADL_R", "RASL_N", "RASL_R",
		"RSV_VCL_N10", "RSV_VCL_R11", "RSV_VCL_N12", "RSV_VLC_R13",
		"RSV_VCL_N14", "RSV_VCL_R15",
		"BLA_W_LP", "BLA_W_RADL", "BLA_N_LP", "IDR_W_RADL", "IDR_N_LP",
		"CRA_NUT", "RSV_IRAP_VCL22", "RSV_IRAP_VCL23",
		"RSV_VCL24", "RSV_VCL25", "RSV_VCL26", "RSV_VCL27", "RSV_VCL28",
		"RSV_VCL29", "RSV_VCL30", "RSV_VCL31",
		"VPS", "SPS", "PPS", "AUD", "EOS_NUT", "EOB_NUT", "FD_NUT",
		"SEI_PREFIX", "SEI_SUFFIX",
		"RSV_NVCL41", "RSV_NVCL42", "RSV_NVCL43", "RSV_NVCL44",
		"RSV_NVCL45", "RSV_NVCL46", "RSV_NVCL47",
		"UNSPEC48", "UNSPEC49", "UNSPEC50", "UNSPEC51", "UNSPEC52",
		"UNSPEC53", "UNSPEC54", "UNSPEC55", "UNSPEC56", "UNSPEC57",
		"UNSPEC58", "UNSPEC59", "UNSPEC60", "UNSPEC61", "UNSPEC62",
		"UNSPEC63",
	}
	if typ < 64 {
		return names[typ]
	}
	return "unknown"
}

// Limits (libavcodec/hevc/hevc.h).
const (
	hevcMaxLayers    = 63
	hevcMaxSubLayers = 7
	hevcMaxLayerSets = 1024
	hevcMaxLayerID   = 63

	hevcMaxVPSCount = 16
	hevcMaxSPSCount = 16
	hevcMaxPPSCount = 64

	hevcMaxDPBSize = 16
	hevcMaxRefs    = hevcMaxDPBSize

	hevcMaxShortTermRefPicSets = 64
	hevcMaxLongTermRefPics     = 32

	hevcMinLog2CTBSize = 4
	hevcMaxLog2CTBSize = 6

	hevcMaxCPBCnt = 32

	hevcMaxLumaPS = 35651584

	hevcMaxWidth  = 16888
	hevcMaxHeight = 16888

	hevcMaxTileRows    = 22
	hevcMaxTileColumns = 20

	hevcMaxSliceSegments = 600

	hevcMaxEntryPointOffsets = hevcMaxTileColumns * 135

	hevcMaxPalettePredictorSize = 128
)

// Slice types (libavcodec/hevc/hevc.h).
const (
	hevcSliceB = 0
	hevcSliceP = 1
	hevcSliceI = 2
)

type H265RawNALUnitHeader struct {
	NalUnitType        uint8
	NuhLayerID         uint8
	NuhTemporalIDPlus1 uint8
}

type H265RawProfileTierLevel struct {
	GeneralProfileSpace uint8
	GeneralTierFlag     uint8
	GeneralProfileIdc   uint8

	GeneralProfileCompatibilityFlag [32]uint8

	GeneralProgressiveSourceFlag   uint8
	GeneralInterlacedSourceFlag    uint8
	GeneralNonPackedConstraintFlag uint8
	GeneralFrameOnlyConstraintFlag uint8

	GeneralMax12bitConstraintFlag       uint8
	GeneralMax10bitConstraintFlag       uint8
	GeneralMax8bitConstraintFlag        uint8
	GeneralMax422chromaConstraintFlag   uint8
	GeneralMax420chromaConstraintFlag   uint8
	GeneralMaxMonochromeConstraintFlag  uint8
	GeneralIntraConstraintFlag          uint8
	GeneralOnePictureOnlyConstraintFlag uint8
	GeneralLowerBitRateConstraintFlag   uint8
	GeneralMax14bitConstraintFlag       uint8

	GeneralInbldFlag uint8

	GeneralLevelIdc uint8

	SubLayerProfilePresentFlag [hevcMaxSubLayers]uint8
	SubLayerLevelPresentFlag   [hevcMaxSubLayers]uint8

	SubLayerProfileSpace [hevcMaxSubLayers]uint8
	SubLayerTierFlag     [hevcMaxSubLayers]uint8
	SubLayerProfileIdc   [hevcMaxSubLayers]uint8

	SubLayerProfileCompatibilityFlag [hevcMaxSubLayers][32]uint8

	SubLayerProgressiveSourceFlag   [hevcMaxSubLayers]uint8
	SubLayerInterlacedSourceFlag    [hevcMaxSubLayers]uint8
	SubLayerNonPackedConstraintFlag [hevcMaxSubLayers]uint8
	SubLayerFrameOnlyConstraintFlag [hevcMaxSubLayers]uint8

	SubLayerMax12bitConstraintFlag       [hevcMaxSubLayers]uint8
	SubLayerMax10bitConstraintFlag       [hevcMaxSubLayers]uint8
	SubLayerMax8bitConstraintFlag        [hevcMaxSubLayers]uint8
	SubLayerMax422chromaConstraintFlag   [hevcMaxSubLayers]uint8
	SubLayerMax420chromaConstraintFlag   [hevcMaxSubLayers]uint8
	SubLayerMaxMonochromeConstraintFlag  [hevcMaxSubLayers]uint8
	SubLayerIntraConstraintFlag          [hevcMaxSubLayers]uint8
	SubLayerOnePictureOnlyConstraintFlag [hevcMaxSubLayers]uint8
	SubLayerLowerBitRateConstraintFlag   [hevcMaxSubLayers]uint8
	SubLayerMax14bitConstraintFlag       [hevcMaxSubLayers]uint8

	SubLayerInbldFlag [hevcMaxSubLayers]uint8

	SubLayerLevelIdc [hevcMaxSubLayers]uint8
}

type H265RawSubLayerHRDParameters struct {
	BitRateValueMinus1   [hevcMaxCPBCnt]uint32
	CpbSizeValueMinus1   [hevcMaxCPBCnt]uint32
	CpbSizeDuValueMinus1 [hevcMaxCPBCnt]uint32
	BitRateDuValueMinus1 [hevcMaxCPBCnt]uint32
	CbrFlag              [hevcMaxCPBCnt]uint8
}

type H265RawHRDParameters struct {
	NalHrdParametersPresentFlag uint8
	VclHrdParametersPresentFlag uint8

	SubPicHrdParamsPresentFlag             uint8
	TickDivisorMinus2                      uint8
	DuCpbRemovalDelayIncrementLengthMinus1 uint8
	SubPicCpbParamsInPicTimingSeiFlag      uint8
	DpbOutputDelayDuLengthMinus1           uint8

	BitRateScale   uint8
	CpbSizeScale   uint8
	CpbSizeDuScale uint8

	InitialCpbRemovalDelayLengthMinus1 uint8
	AuCpbRemovalDelayLengthMinus1      uint8
	DpbOutputDelayLengthMinus1         uint8

	FixedPicRateGeneralFlag     [hevcMaxSubLayers]uint8
	FixedPicRateWithinCvsFlag   [hevcMaxSubLayers]uint8
	ElementalDurationInTcMinus1 [hevcMaxSubLayers]uint16
	LowDelayHrdFlag             [hevcMaxSubLayers]uint8
	CpbCntMinus1                [hevcMaxSubLayers]uint8
	NalSubLayerHrdParameters    [hevcMaxSubLayers]H265RawSubLayerHRDParameters
	VclSubLayerHrdParameters    [hevcMaxSubLayers]H265RawSubLayerHRDParameters
}

type H265RawVUI struct {
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

	NeutralChromaIndicationFlag uint8
	FieldSeqFlag                uint8
	FrameFieldInfoPresentFlag   uint8

	DefaultDisplayWindowFlag uint8
	DefDispWinLeftOffset     uint16
	DefDispWinRightOffset    uint16
	DefDispWinTopOffset      uint16
	DefDispWinBottomOffset   uint16

	VuiTimingInfoPresentFlag       uint8
	VuiNumUnitsInTick              uint32
	VuiTimeScale                   uint32
	VuiPocProportionalToTimingFlag uint8
	VuiNumTicksPocDiffOneMinus1    uint32
	VuiHrdParametersPresentFlag    uint8
	HrdParameters                  H265RawHRDParameters

	BitstreamRestrictionFlag           uint8
	TilesFixedStructureFlag            uint8
	MotionVectorsOverPicBoundariesFlag uint8
	RestrictedRefPicListsFlag          uint8
	MinSpatialSegmentationIdc          uint16
	MaxBytesPerPicDenom                uint8
	MaxBitsPerMinCuDenom               uint8
	Log2MaxMvLengthHorizontal          uint8
	Log2MaxMvLengthVertical            uint8
}

type H265RawExtensionData struct {
	Data      []uint8
	BitLength int
}

type H265RawVPS struct {
	NalUnitHeader H265RawNALUnitHeader

	VpsVideoParameterSetID uint8

	VpsBaseLayerInternalFlag  uint8
	VpsBaseLayerAvailableFlag uint8
	VpsMaxLayersMinus1        uint8
	VpsMaxSubLayersMinus1     uint8
	VpsTemporalIDNestingFlag  uint8

	ProfileTierLevel H265RawProfileTierLevel

	VpsSubLayerOrderingInfoPresentFlag uint8
	VpsMaxDecPicBufferingMinus1        [hevcMaxSubLayers]uint8
	VpsMaxNumReorderPics               [hevcMaxSubLayers]uint8
	VpsMaxLatencyIncreasePlus1         [hevcMaxSubLayers]uint32

	VpsMaxLayerID         uint8
	VpsNumLayerSetsMinus1 uint16
	LayerIDIncludedFlag   [hevcMaxLayerSets][hevcMaxLayers]uint8

	VpsTimingInfoPresentFlag       uint8
	VpsNumUnitsInTick              uint32
	VpsTimeScale                   uint32
	VpsPocProportionalToTimingFlag uint8
	VpsNumTicksPocDiffOneMinus1    uint32
	VpsNumHrdParameters            uint16
	HrdLayerSetIdx                 [hevcMaxLayerSets]uint16
	CprmsPresentFlag               [hevcMaxLayerSets]uint8
	HrdParameters                  [hevcMaxLayerSets]H265RawHRDParameters

	VpsExtensionFlag uint8
	ExtensionData    H265RawExtensionData
}

type H265RawSTRefPicSet struct {
	InterRefPicSetPredictionFlag uint8

	DeltaIdxMinus1    uint8
	DeltaRpsSign      uint8
	AbsDeltaRpsMinus1 uint16

	UsedByCurrPicFlag [hevcMaxRefs]uint8
	UseDeltaFlag      [hevcMaxRefs]uint8

	NumNegativePics     uint8
	NumPositivePics     uint8
	DeltaPocS0Minus1    [hevcMaxRefs]uint16
	UsedByCurrPicS0Flag [hevcMaxRefs]uint8
	DeltaPocS1Minus1    [hevcMaxRefs]uint16
	UsedByCurrPicS1Flag [hevcMaxRefs]uint8
}

type H265RawScalingList struct {
	ScalingListPredModeFlag      [4][6]uint8
	ScalingListPredMatrixIDDelta [4][6]uint8
	ScalingListDcCoefMinus8      [4][6]int16
	ScalingListDeltaCoeff        [4][6][64]int8
}

type H265RawSPS struct {
	NalUnitHeader H265RawNALUnitHeader

	SpsVideoParameterSetID uint8

	SpsMaxSubLayersMinus1      uint8
	SpsExtOrMaxSubLayersMinus1 uint8
	SpsTemporalIDNestingFlag   uint8

	ProfileTierLevel H265RawProfileTierLevel

	SpsSeqParameterSetID uint8

	UpdateRepFormatFlag uint8
	SpsRepFormatIdx     uint8

	ChromaFormatIdc         uint8
	SeparateColourPlaneFlag uint8

	PicWidthInLumaSamples  uint16
	PicHeightInLumaSamples uint16

	ConformanceWindowFlag uint8
	ConfWinLeftOffset     uint16
	ConfWinRightOffset    uint16
	ConfWinTopOffset      uint16
	ConfWinBottomOffset   uint16

	BitDepthLumaMinus8   uint8
	BitDepthChromaMinus8 uint8

	Log2MaxPicOrderCntLsbMinus4 uint8

	SpsSubLayerOrderingInfoPresentFlag uint8
	SpsMaxDecPicBufferingMinus1        [hevcMaxSubLayers]uint8
	SpsMaxNumReorderPics               [hevcMaxSubLayers]uint8
	SpsMaxLatencyIncreasePlus1         [hevcMaxSubLayers]uint32

	Log2MinLumaCodingBlockSizeMinus3     uint8
	Log2DiffMaxMinLumaCodingBlockSize    uint8
	Log2MinLumaTransformBlockSizeMinus2  uint8
	Log2DiffMaxMinLumaTransformBlockSize uint8
	MaxTransformHierarchyDepthInter      uint8
	MaxTransformHierarchyDepthIntra      uint8

	ScalingListEnabledFlag        uint8
	SpsInferScalingListFlag       uint8
	SpsScalingListRefLayerID      uint8
	SpsScalingListDataPresentFlag uint8
	ScalingList                   H265RawScalingList

	AmpEnabledFlag                  uint8
	SampleAdaptiveOffsetEnabledFlag uint8

	PcmEnabledFlag                       uint8
	PcmSampleBitDepthLumaMinus1          uint8
	PcmSampleBitDepthChromaMinus1        uint8
	Log2MinPcmLumaCodingBlockSizeMinus3  uint8
	Log2DiffMaxMinPcmLumaCodingBlockSize uint8
	PcmLoopFilterDisabledFlag            uint8

	NumShortTermRefPicSets uint8
	StRefPicSet            [hevcMaxShortTermRefPicSets]H265RawSTRefPicSet

	LongTermRefPicsPresentFlag uint8
	NumLongTermRefPicsSps      uint8
	LtRefPicPocLsbSps          [hevcMaxLongTermRefPics]uint16
	UsedByCurrPicLtSpsFlag     [hevcMaxLongTermRefPics]uint8

	SpsTemporalMvpEnabledFlag       uint8
	StrongIntraSmoothingEnabledFlag uint8

	VuiParametersPresentFlag uint8
	Vui                      H265RawVUI

	SpsExtensionPresentFlag    uint8
	SpsRangeExtensionFlag      uint8
	SpsMultilayerExtensionFlag uint8
	Sps3DExtensionFlag         uint8
	SpsSccExtensionFlag        uint8
	SpsExtension4Bits          uint8

	ExtensionData H265RawExtensionData

	// Range extension.
	TransformSkipRotationEnabledFlag    uint8
	TransformSkipContextEnabledFlag     uint8
	ImplicitRdpcmEnabledFlag            uint8
	ExplicitRdpcmEnabledFlag            uint8
	ExtendedPrecisionProcessingFlag     uint8
	IntraSmoothingDisabledFlag          uint8
	HighPrecisionOffsetsEnabledFlag     uint8
	PersistentRiceAdaptationEnabledFlag uint8
	CabacBypassAlignmentEnabledFlag     uint8

	// Screen content coding extension.
	SpsCurrPicRefEnabledFlag                  uint8
	PaletteModeEnabledFlag                    uint8
	PaletteMaxSize                            uint8
	DeltaPaletteMaxPredictorSize              uint8
	SpsPalettePredictorInitializerPresentFlag uint8
	SpsNumPalettePredictorInitializerMinus1   uint8
	SpsPalettePredictorInitializers           [3][128]uint16

	MotionVectorResolutionControlIdc  uint8
	IntraBoundaryFilteringDisableFlag uint8

	// Multilayer extension.
	InterViewMvVertConstraintFlag uint8
}

type H265RawPPS struct {
	NalUnitHeader H265RawNALUnitHeader

	PpsPicParameterSetID uint8
	PpsSeqParameterSetID uint8

	DependentSliceSegmentsEnabledFlag uint8
	OutputFlagPresentFlag             uint8
	NumExtraSliceHeaderBits           uint8
	SignDataHidingEnabledFlag         uint8
	CabacInitPresentFlag              uint8

	NumRefIdxL0DefaultActiveMinus1 uint8
	NumRefIdxL1DefaultActiveMinus1 uint8

	InitQpMinus26 int8

	ConstrainedIntraPredFlag uint8
	TransformSkipEnabledFlag uint8
	CuQpDeltaEnabledFlag     uint8
	DiffCuQpDeltaDepth       uint8

	PpsCbQpOffset                      int8
	PpsCrQpOffset                      int8
	PpsSliceChromaQpOffsetsPresentFlag uint8

	WeightedPredFlag   uint8
	WeightedBipredFlag uint8

	TransquantBypassEnabledFlag  uint8
	TilesEnabledFlag             uint8
	EntropyCodingSyncEnabledFlag uint8

	NumTileColumnsMinus1             uint8
	NumTileRowsMinus1                uint8
	UniformSpacingFlag               uint8
	ColumnWidthMinus1                [hevcMaxTileColumns]uint16
	RowHeightMinus1                  [hevcMaxTileRows]uint16
	LoopFilterAcrossTilesEnabledFlag uint8

	PpsLoopFilterAcrossSlicesEnabledFlag uint8
	DeblockingFilterControlPresentFlag   uint8
	DeblockingFilterOverrideEnabledFlag  uint8
	PpsDeblockingFilterDisabledFlag      uint8
	PpsBetaOffsetDiv2                    int8
	PpsTcOffsetDiv2                      int8

	PpsScalingListDataPresentFlag uint8
	ScalingList                   H265RawScalingList

	ListsModificationPresentFlag uint8
	Log2ParallelMergeLevelMinus2 uint8

	SliceSegmentHeaderExtensionPresentFlag uint8

	PpsExtensionPresentFlag    uint8
	PpsRangeExtensionFlag      uint8
	PpsMultilayerExtensionFlag uint8
	Pps3DExtensionFlag         uint8
	PpsSccExtensionFlag        uint8
	PpsExtension4Bits          uint8

	ExtensionData H265RawExtensionData

	// Range extension.
	Log2MaxTransformSkipBlockSizeMinus2 uint8
	CrossComponentPredictionEnabledFlag uint8
	ChromaQpOffsetListEnabledFlag       uint8
	DiffCuChromaQpOffsetDepth           uint8
	ChromaQpOffsetListLenMinus1         uint8
	CbQpOffsetList                      [6]int8
	CrQpOffsetList                      [6]int8
	Log2SaoOffsetScaleLuma              uint8
	Log2SaoOffsetScaleChroma            uint8

	// Screen content coding extension.
	PpsCurrPicRefEnabledFlag                   uint8
	ResidualAdaptiveColourTransformEnabledFlag uint8
	PpsSliceActQpOffsetsPresentFlag            uint8
	PpsActYQpOffsetPlus5                       int8
	PpsActCbQpOffsetPlus5                      int8
	PpsActCrQpOffsetPlus3                      int8

	PpsPalettePredictorInitializerPresentFlag uint8
	PpsNumPalettePredictorInitializer         uint8
	MonochromePaletteFlag                     uint8
	LumaBitDepthEntryMinus8                   uint8
	ChromaBitDepthEntryMinus8                 uint8
	PpsPalettePredictorInitializers           [3][128]uint16

	// Multilayer extension.
	PocResetInfoPresentFlag         uint8
	PpsInferScalingListFlag         uint8
	PpsScalingListRefLayerID        uint8
	NumRefLocOffsets                uint8
	RefLocOffsetLayerID             [64]uint8
	ScaledRefLayerOffsetPresentFlag [64]uint8
	ScaledRefLayerLeftOffset        [64]int16
	ScaledRefLayerTopOffset         [64]int16
	ScaledRefLayerRightOffset       [64]int16
	ScaledRefLayerBottomOffset      [64]int16
	RefRegionOffsetPresentFlag      [64]uint8
	RefRegionLeftOffset             [64]int16
	RefRegionTopOffset              [64]int16
	RefRegionRightOffset            [64]int16
	RefRegionBottomOffset           [64]int16
	ResamplePhaseSetPresentFlag     [64]uint8
	PhaseHorLuma                    [64]uint8
	PhaseVerLuma                    [64]uint8
	PhaseHorChromaPlus8             [64]uint8
	PhaseVerChromaPlus8             [64]uint8
	ColourMappingEnabledFlag        uint8
	NumCmRefLayersMinus1            uint8
	CmRefLayerID                    [62]uint8
	CmOctantDepth                   uint8
	CmYPartNumLog2                  uint8
	LumaBitDepthCmInputMinus8       uint8
	ChromaBitDepthCmInputMinus8     uint8
	LumaBitDepthCmOutputMinus8      uint8
	ChromaBitDepthCmOutputMinus8    uint8
	CmResQuantBits                  uint8
	CmDeltaFlcBitsMinus1            uint8
	CmAdaptThresholdUDelta          int16
	CmAdaptThresholdVDelta          int16
	SplitOctantFlag                 [2]uint8
	CodedResFlag                    [12][2][2][4]uint8
	ResCoeffQ                       [12][2][2][4][3]uint8
	ResCoeffS                       [12][2][2][4][3]uint32
	ResCoeffR                       [12][2][2][4][3]uint8
}

type H265RawAUD struct {
	NalUnitHeader H265RawNALUnitHeader

	PicType uint8
}

type H265RawSliceHeader struct {
	NalUnitHeader H265RawNALUnitHeader

	FirstSliceSegmentInPicFlag uint8
	NoOutputOfPriorPicsFlag    uint8
	SlicePicParameterSetID     uint8

	DependentSliceSegmentFlag uint8
	SliceSegmentAddress       uint16

	SliceReservedFlag [8]uint8
	SliceType         uint8

	PicOutputFlag uint8
	ColourPlaneID uint8

	SlicePicOrderCntLsb uint16

	ShortTermRefPicSetSpsFlag uint8
	ShortTermRefPicSet        H265RawSTRefPicSet
	ShortTermRefPicSetIdx     uint8

	NumLongTermSps         uint8
	NumLongTermPics        uint8
	LtIdxSps               [hevcMaxRefs]uint8
	PocLsbLt               [hevcMaxRefs]uint8
	UsedByCurrPicLtFlag    [hevcMaxRefs]uint8
	DeltaPocMsbPresentFlag [hevcMaxRefs]uint8
	DeltaPocMsbCycleLt     [hevcMaxRefs]uint32

	SliceTemporalMvpEnabledFlag uint8

	SliceSaoLumaFlag   uint8
	SliceSaoChromaFlag uint8

	NumRefIdxActiveOverrideFlag uint8
	NumRefIdxL0ActiveMinus1     uint8
	NumRefIdxL1ActiveMinus1     uint8

	RefPicListModificationFlagL0 uint8
	ListEntryL0                  [hevcMaxRefs]uint8
	RefPicListModificationFlagL1 uint8
	ListEntryL1                  [hevcMaxRefs]uint8

	MvdL1ZeroFlag        uint8
	CabacInitFlag        uint8
	CollocatedFromL0Flag uint8
	CollocatedRefIdx     uint8

	LumaLog2WeightDenom        uint8
	DeltaChromaLog2WeightDenom int8
	LumaWeightL0Flag           [hevcMaxRefs]uint8
	ChromaWeightL0Flag         [hevcMaxRefs]uint8
	DeltaLumaWeightL0          [hevcMaxRefs]int8
	LumaOffsetL0               [hevcMaxRefs]int16
	DeltaChromaWeightL0        [hevcMaxRefs][2]int8
	ChromaOffsetL0             [hevcMaxRefs][2]int16
	LumaWeightL1Flag           [hevcMaxRefs]uint8
	ChromaWeightL1Flag         [hevcMaxRefs]uint8
	DeltaLumaWeightL1          [hevcMaxRefs]int8
	LumaOffsetL1               [hevcMaxRefs]int16
	DeltaChromaWeightL1        [hevcMaxRefs][2]int8
	ChromaOffsetL1             [hevcMaxRefs][2]int16

	FiveMinusMaxNumMergeCand uint8
	UseIntegerMvFlag         uint8

	SliceQpDelta                int8
	SliceCbQpOffset             int8
	SliceCrQpOffset             int8
	SliceActYQpOffset           int8
	SliceActCbQpOffset          int8
	SliceActCrQpOffset          int8
	CuChromaQpOffsetEnabledFlag uint8

	DeblockingFilterOverrideFlag           uint8
	SliceDeblockingFilterDisabledFlag      uint8
	SliceBetaOffsetDiv2                    int8
	SliceTcOffsetDiv2                      int8
	SliceLoopFilterAcrossSlicesEnabledFlag uint8

	NumEntryPointOffsets   uint16
	OffsetLenMinus1        uint8
	EntryPointOffsetMinus1 [hevcMaxEntryPointOffsets]uint32

	SliceSegmentHeaderExtensionLength   uint16
	SliceSegmentHeaderExtensionDataByte [256]uint8
}

type H265RawSlice struct {
	Header H265RawSliceHeader

	Data         []byte
	DataBitStart int
}

type H265RawSEIBufferingPeriod struct {
	BpSeqParameterSetID          uint8
	IrapCpbParamsPresentFlag     uint8
	CpbDelayOffset               uint32
	DpbDelayOffset               uint32
	ConcatenationFlag            uint8
	AuCpbRemovalDelayDeltaMinus1 uint32

	NalInitialCpbRemovalDelay     [hevcMaxCPBCnt]uint32
	NalInitialCpbRemovalOffset    [hevcMaxCPBCnt]uint32
	NalInitialAltCpbRemovalDelay  [hevcMaxCPBCnt]uint32
	NalInitialAltCpbRemovalOffset [hevcMaxCPBCnt]uint32

	VclInitialCpbRemovalDelay     [hevcMaxCPBCnt]uint32
	VclInitialCpbRemovalOffset    [hevcMaxCPBCnt]uint32
	VclInitialAltCpbRemovalDelay  [hevcMaxCPBCnt]uint32
	VclInitialAltCpbRemovalOffset [hevcMaxCPBCnt]uint32

	UseAltCpbParamsFlag uint8
}

type H265RawSEIPicTiming struct {
	PicStruct      uint8
	SourceScanType uint8
	DuplicateFlag  uint8

	AuCpbRemovalDelayMinus1 uint32
	PicDpbOutputDelay       uint32
	PicDpbOutputDuDelay     uint32

	NumDecodingUnitsMinus1                 uint16
	DuCommonCpbRemovalDelayFlag            uint8
	DuCommonCpbRemovalDelayIncrementMinus1 uint32
	NumNalusInDuMinus1                     [hevcMaxSliceSegments]uint16
	DuCpbRemovalDelayIncrementMinus1       [hevcMaxSliceSegments]uint32
}

type H265RawSEIPanScanRect struct {
	PanScanRectID              uint32
	PanScanRectCancelFlag      uint8
	PanScanCntMinus1           uint8
	PanScanRectLeftOffset      [3]int32
	PanScanRectRightOffset     [3]int32
	PanScanRectTopOffset       [3]int32
	PanScanRectBottomOffset    [3]int32
	PanScanRectPersistenceFlag uint16
}

type H265RawSEIRecoveryPoint struct {
	RecoveryPocCnt uint16
	ExactMatchFlag uint8
	BrokenLinkFlag uint8
}

type H265RawFilmGrainCharacteristics struct {
	FilmGrainCharacteristicsCancelFlag      uint8
	FilmGrainModelID                        uint8
	SeparateColourDescriptionPresentFlag    uint8
	FilmGrainBitDepthLumaMinus8             uint8
	FilmGrainBitDepthChromaMinus8           uint8
	FilmGrainFullRangeFlag                  uint8
	FilmGrainColourPrimaries                uint8
	FilmGrainTransferCharacteristics        uint8
	FilmGrainMatrixCoeffs                   uint8
	BlendingModeID                          uint8
	Log2ScaleFactor                         uint8
	CompModelPresentFlag                    [3]uint8
	NumIntensityIntervalsMinus1             [3]uint8
	NumModelValuesMinus1                    [3]uint8
	IntensityIntervalLowerBound             [3][256]uint8
	IntensityIntervalUpperBound             [3][256]uint8
	CompModelValue                          [3][256][6]int16
	FilmGrainCharacteristicsPersistenceFlag uint8
}

type H265RawSEIDisplayOrientation struct {
	DisplayOrientationCancelFlag       uint8
	HorFlip                            uint8
	VerFlip                            uint8
	AnticlockwiseRotation              uint16
	DisplayOrientationRepetitionPeriod uint16
	DisplayOrientationPersistenceFlag  uint8
}

type H265RawSEIActiveParameterSets struct {
	ActiveVideoParameterSetID uint8
	SelfContainedCvsFlag      uint8
	NoParameterSetUpdateFlag  uint8
	NumSpsIdsMinus1           uint8
	ActiveSeqParameterSetID   [hevcMaxSPSCount]uint8
	LayerSpsIdx               [hevcMaxLayers]uint8
}

type H265RawSEIDecodedPictureHash struct {
	HashType        uint8
	PictureMd5      [3][16]uint8
	PictureCrc      [3]uint16
	PictureChecksum [3]uint32
}

type H265RawSEITimeCode struct {
	NumClockTs          uint8
	ClockTimestampFlag  [3]uint8
	UnitsFieldBasedFlag [3]uint8
	CountingType        [3]uint8
	FullTimestampFlag   [3]uint8
	DiscontinuityFlag   [3]uint8
	CntDroppedFlag      [3]uint8
	NFrames             [3]uint16
	SecondsValue        [3]uint8
	MinutesValue        [3]uint8
	HoursValue          [3]uint8
	SecondsFlag         [3]uint8
	MinutesFlag         [3]uint8
	HoursFlag           [3]uint8
	TimeOffsetLength    [3]uint8
	TimeOffsetValue     [3]int32
}

type H265RawSEIAlphaChannelInfo struct {
	AlphaChannelCancelFlag     uint8
	AlphaChannelUseIdc         uint8
	AlphaChannelBitDepthMinus8 uint8
	AlphaTransparentValue      uint16
	AlphaOpaqueValue           uint16
	AlphaChannelIncrFlag       uint8
	AlphaChannelClipFlag       uint8
	AlphaChannelClipTypeFlag   uint8
}

type H265RawSEI3DReferenceDisplaysInfo struct {
	PrecRefDisplayWidth                            uint8
	RefViewingDistanceFlag                         uint8
	PrecRefViewingDist                             uint8
	NumRefDisplaysMinus1                           uint8
	LeftViewID                                     [32]uint16
	RightViewID                                    [32]uint16
	ExponentRefDisplayWidth                        [32]uint8
	MantissaRefDisplayWidth                        [32]uint8
	ExponentRefViewingDistance                     [32]uint8
	MantissaRefViewingDistance                     [32]uint8
	AdditionalShiftPresentFlag                     [32]uint8
	NumSampleShiftPlus512                          [32]uint16
	ThreeDimensionalReferenceDisplaysExtensionFlag uint8
}

type H265RawSEI struct {
	NalUnitHeader H265RawNALUnitHeader
	MessageList   SEIRawMessageList
}

type H265RawFiller struct {
	NalUnitHeader H265RawNALUnitHeader

	FillerSize uint32
}
