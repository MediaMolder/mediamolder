// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the AV1 raw structures and limits (libavcodec/cbs_av1.h,
// libavcodec/av1.h). Field names are the C names in Go casing.

package cbs

// OBU types (libavcodec/av1.h, section 6.2.2).
const (
	// 0 reserved.
	av1OBUSequenceHeader       = 1
	av1OBUTemporalDelimiter    = 2
	av1OBUFrameHeader          = 3
	av1OBUTileGroup            = 4
	av1OBUMetadata             = 5
	av1OBUFrame                = 6
	av1OBURedundantFrameHeader = 7
	av1OBUTileList             = 8
	// 9-14 reserved.
	av1OBUPadding = 15
)

// Metadata types (section 6.7.1).
const (
	av1MetadataTypeHDRCLL      = 1
	av1MetadataTypeHDRMDCV     = 2
	av1MetadataTypeScalability = 3
	av1MetadataTypeITUTT35     = 4
	av1MetadataTypeTimecode    = 5
)

// Frame types (section 6.8.2).
const (
	av1FrameKey       = 0
	av1FrameInter     = 1
	av1FrameIntraOnly = 2
	av1FrameSwitch    = 3
)

// Reference frames (section 6.10.24).
const (
	av1RefFrameNone    = -1
	av1RefFrameIntra   = 0
	av1RefFrameLast    = 1
	av1RefFrameLast2   = 2
	av1RefFrameLast3   = 3
	av1RefFrameGolden  = 4
	av1RefFrameBwdref  = 5
	av1RefFrameAltref2 = 6
	av1RefFrameAltref  = 7
)

// Constants (section 3).
const (
	av1MaxOperatingPoints = 32

	av1MaxTileWidth = 4096
	av1MaxTileArea  = 4096 * 2304
	av1MaxTileRows  = 64
	av1MaxTileCols  = 64

	av1NumRefFrames      = 8
	av1RefsPerFrame      = 7
	av1TotalRefsPerFrame = 8
	av1PrimaryRefNone    = 7

	av1MaxSegments = 8
	av1SegLvlMax   = 8

	av1SegLvlAltQ = 0

	av1SelectScreenContentTools = 2
	av1SelectIntegerMV          = 2

	av1SuperresNum      = 8
	av1SuperresDenomMin = 9

	av1InterpolationFilterSwitchable = 4

	av1GMAbsAlphaBits      = 12
	av1GMAlphaPrecBits     = 15
	av1GMAbsTransOnlyBits  = 9
	av1GMTransOnlyPrecBits = 3
	av1GMAbsTransBits      = 12
	av1GMTransPrecBits     = 6

	av1WarpModelIdentity    = 0
	av1WarpModelTranslation = 1
	av1WarpModelRotzoom     = 2
	av1WarpModelAffine      = 3
)

// Chroma sample position.
const (
	av1CSPUnknown   = 0
	av1CSPColocated = 2
)

// Scalability modes (section 6.7.5).
const av1ScalabilitySS = 14

// Frame Restoration types (section 6.10.15).
const av1RestoreNone = 0

// TX mode (section 6.8.21).
const (
	av1Only4x4       = 0
	av1TXModeLargest = 1
	av1TXModeSelect  = 2
)

// AV1 profiles (libavcodec/defs.h).
const (
	av1ProfileMain         = 0
	av1ProfileHigh         = 1
	av1ProfileProfessional = 2
)

// Colour description values used by the template (libavutil/pixfmt.h).
const (
	av1ColPriBT709        = 1
	av1ColPriUnspecified  = 2
	av1ColTrcUnspecified  = 2
	av1ColTrcIEC61966_2_1 = 13
	av1ColSpcRGB          = 0
	av1ColSpcUnspecified  = 2
)

// av1OBUName returns a short name for an OBU type.
func av1OBUName(typ uint32) string {
	switch typ {
	case av1OBUSequenceHeader:
		return "SEQUENCE_HEADER"
	case av1OBUTemporalDelimiter:
		return "TEMPORAL_DELIMITER"
	case av1OBUFrameHeader:
		return "FRAME_HEADER"
	case av1OBUTileGroup:
		return "TILE_GROUP"
	case av1OBUMetadata:
		return "METADATA"
	case av1OBUFrame:
		return "FRAME"
	case av1OBURedundantFrameHeader:
		return "REDUNDANT_FRAME_HEADER"
	case av1OBUTileList:
		return "TILE_LIST"
	case av1OBUPadding:
		return "PADDING"
	}
	if typ < 16 {
		return "RESERVED"
	}
	return "unknown"
}

// AV1RawOBUHeader is AV1RawOBUHeader (cbs_av1.h).
type AV1RawOBUHeader struct {
	OBUForbiddenBit  uint8
	OBUType          uint8
	OBUExtensionFlag uint8
	OBUHasSizeField  uint8
	OBUReserved1Bit  uint8

	TemporalID                   uint8
	SpatialID                    uint8
	ExtensionHeaderReserved3Bits uint8
}

// AV1RawColorConfig is AV1RawColorConfig.
type AV1RawColorConfig struct {
	HighBitdepth uint8
	TwelveBit    uint8
	MonoChrome   uint8

	ColorDescriptionPresentFlag uint8
	ColorPrimaries              uint8
	TransferCharacteristics     uint8
	MatrixCoefficients          uint8

	ColorRange           uint8
	SubsamplingX         uint8
	SubsamplingY         uint8
	ChromaSamplePosition uint8
	SeparateUVDeltaQ     uint8
}

// AV1RawTimingInfo is AV1RawTimingInfo.
type AV1RawTimingInfo struct {
	NumUnitsInDisplayTick uint32
	TimeScale             uint32

	EqualPictureInterval     uint8
	NumTicksPerPictureMinus1 uint32
}

// AV1RawDecoderModelInfo is AV1RawDecoderModelInfo.
type AV1RawDecoderModelInfo struct {
	BufferDelayLengthMinus1           uint8
	NumUnitsInDecodingTick            uint32
	BufferRemovalTimeLengthMinus1     uint8
	FramePresentationTimeLengthMinus1 uint8
}

// AV1RawSequenceHeader is AV1RawSequenceHeader.
type AV1RawSequenceHeader struct {
	SeqProfile                uint8
	StillPicture              uint8
	ReducedStillPictureHeader uint8

	TimingInfoPresentFlag          uint8
	DecoderModelInfoPresentFlag    uint8
	InitialDisplayDelayPresentFlag uint8
	OperatingPointsCntMinus1       uint8

	TimingInfo       AV1RawTimingInfo
	DecoderModelInfo AV1RawDecoderModelInfo

	OperatingPointIdc                   [av1MaxOperatingPoints]uint16
	SeqLevelIdx                         [av1MaxOperatingPoints]uint8
	SeqTier                             [av1MaxOperatingPoints]uint8
	DecoderModelPresentForThisOp        [av1MaxOperatingPoints]uint8
	DecoderBufferDelay                  [av1MaxOperatingPoints]uint32
	EncoderBufferDelay                  [av1MaxOperatingPoints]uint32
	LowDelayModeFlag                    [av1MaxOperatingPoints]uint8
	InitialDisplayDelayPresentForThisOp [av1MaxOperatingPoints]uint8
	InitialDisplayDelayMinus1           [av1MaxOperatingPoints]uint8

	FrameWidthBitsMinus1  uint8
	FrameHeightBitsMinus1 uint8
	MaxFrameWidthMinus1   uint16
	MaxFrameHeightMinus1  uint16

	FrameIDNumbersPresentFlag     uint8
	DeltaFrameIDLengthMinus2      uint8
	AdditionalFrameIDLengthMinus1 uint8

	Use128x128Superblock     uint8
	EnableFilterIntra        uint8
	EnableIntraEdgeFilter    uint8
	EnableInterintraCompound uint8
	EnableMaskedCompound     uint8
	EnableWarpedMotion       uint8
	EnableDualFilter         uint8

	EnableOrderHint   uint8
	EnableJntComp     uint8
	EnableRefFrameMvs uint8

	SeqChooseScreenContentTools uint8
	SeqForceScreenContentTools  uint8
	SeqChooseIntegerMV          uint8
	SeqForceIntegerMV           uint8

	OrderHintBitsMinus1 uint8

	EnableSuperres    uint8
	EnableCdef        uint8
	EnableRestoration uint8

	ColorConfig AV1RawColorConfig

	FilmGrainParamsPresent uint8
}

// AV1RawFilmGrainParams is AV1RawFilmGrainParams.
type AV1RawFilmGrainParams struct {
	ApplyGrain            uint8
	GrainSeed             uint16
	UpdateGrain           uint8
	FilmGrainParamsRefIdx uint8
	NumYPoints            uint8
	PointYValue           [14]uint8
	PointYScaling         [14]uint8
	ChromaScalingFromLuma uint8
	NumCbPoints           uint8
	PointCbValue          [10]uint8
	PointCbScaling        [10]uint8
	NumCrPoints           uint8
	PointCrValue          [10]uint8
	PointCrScaling        [10]uint8
	GrainScalingMinus8    uint8
	ArCoeffLag            uint8
	ArCoeffsYPlus128      [24]uint8
	ArCoeffsCbPlus128     [25]uint8
	ArCoeffsCrPlus128     [25]uint8
	ArCoeffShiftMinus6    uint8
	GrainScaleShift       uint8
	CbMult                uint8
	CbLumaMult            uint8
	CbOffset              uint16
	CrMult                uint8
	CrLumaMult            uint8
	CrOffset              uint16
	OverlapFlag           uint8
	ClipToRestrictedRange uint8
}

// AV1RawFrameHeader is AV1RawFrameHeader.
type AV1RawFrameHeader struct {
	ShowExistingFrame     uint8
	FrameToShowMapIdx     uint8
	FramePresentationTime uint32
	DisplayFrameID        uint32

	FrameType     uint8
	ShowFrame     uint8
	ShowableFrame uint8

	ErrorResilientMode      uint8
	DisableCdfUpdate        uint8
	AllowScreenContentTools uint8
	ForceIntegerMV          uint8

	CurrentFrameID        uint32
	FrameSizeOverrideFlag uint8
	OrderHint             uint8

	BufferRemovalTimePresentFlag uint8
	BufferRemovalTime            [av1MaxOperatingPoints]uint32

	PrimaryRefFrame             uint8
	FrameWidthMinus1            uint16
	FrameHeightMinus1           uint16
	UseSuperres                 uint8
	CodedDenom                  uint8
	RenderAndFrameSizeDifferent uint8
	RenderWidthMinus1           uint16
	RenderHeightMinus1          uint16

	FoundRef [av1RefsPerFrame]uint8

	RefreshFrameFlags       uint8
	AllowIntrabc            uint8
	RefOrderHint            [av1NumRefFrames]uint8
	FrameRefsShortSignaling uint8
	LastFrameIdx            uint8
	GoldenFrameIdx          uint8
	RefFrameIdx             [av1RefsPerFrame]int8
	DeltaFrameIDMinus1      [av1RefsPerFrame]uint32

	AllowHighPrecisionMV   uint8
	IsFilterSwitchable     uint8
	InterpolationFilter    uint8
	IsMotionModeSwitchable uint8
	UseRefFrameMvs         uint8

	DisableFrameEndUpdateCdf uint8

	UniformTileSpacingFlag uint8
	TileColsLog2           uint8
	TileRowsLog2           uint8
	TileStartColSb         [av1MaxTileCols]uint8
	TileStartRowSb         [av1MaxTileCols]uint8
	WidthInSbsMinus1       [av1MaxTileCols]uint8
	HeightInSbsMinus1      [av1MaxTileRows]uint8
	ContextUpdateTileID    uint16
	TileSizeBytesMinus1    uint8

	// These are derived values, but it's very unhelpful to have to
	// recalculate them all the time so we store them here.
	TileCols uint16
	TileRows uint16

	BaseQIdx     uint8
	DeltaQYDc    int8
	DiffUVDelta  uint8
	DeltaQUDc    int8
	DeltaQUAc    int8
	DeltaQVDc    int8
	DeltaQVAc    int8
	UsingQmatrix uint8
	QmY          uint8
	QmU          uint8
	QmV          uint8

	SegmentationEnabled        uint8
	SegmentationUpdateMap      uint8
	SegmentationTemporalUpdate uint8
	SegmentationUpdateData     uint8
	FeatureEnabled             [av1MaxSegments][av1SegLvlMax]uint8
	FeatureValue               [av1MaxSegments][av1SegLvlMax]int16

	DeltaQPresent  uint8
	DeltaQRes      uint8
	DeltaLfPresent uint8
	DeltaLfRes     uint8
	DeltaLfMulti   uint8

	LoopFilterLevel        [4]uint8
	LoopFilterSharpness    uint8
	LoopFilterDeltaEnabled uint8
	LoopFilterDeltaUpdate  uint8
	UpdateRefDelta         [av1TotalRefsPerFrame]uint8
	LoopFilterRefDeltas    [av1TotalRefsPerFrame]int8
	UpdateModeDelta        [2]uint8
	LoopFilterModeDeltas   [2]int8

	CdefDampingMinus3 uint8
	CdefBits          uint8
	CdefYPriStrength  [8]uint8
	CdefYSecStrength  [8]uint8
	CdefUVPriStrength [8]uint8
	CdefUVSecStrength [8]uint8

	LrType      [3]uint8
	LrUnitShift uint8
	LrUVShift   uint8

	TxMode          uint8
	ReferenceSelect uint8
	SkipModePresent uint8

	AllowWarpedMotion uint8
	ReducedTxSet      uint8

	IsGlobal      [av1TotalRefsPerFrame]uint8
	IsRotZoom     [av1TotalRefsPerFrame]uint8
	IsTranslation [av1TotalRefsPerFrame]uint8
	GmParams      [av1TotalRefsPerFrame][6]uint32

	FilmGrain AV1RawFilmGrainParams
}

// AV1RawTileData is AV1RawTileData.
type AV1RawTileData struct {
	Data []byte
}

// AV1RawTileGroup is AV1RawTileGroup.
type AV1RawTileGroup struct {
	Data []byte

	TileStartAndEndPresentFlag uint8
	TgStart                    uint16
	TgEnd                      uint16

	TileData AV1RawTileData
}

// AV1RawFrame is AV1RawFrame.
type AV1RawFrame struct {
	Header    AV1RawFrameHeader
	TileGroup AV1RawTileGroup
}

// AV1RawTileList is AV1RawTileList.
type AV1RawTileList struct {
	OutputFrameWidthInTilesMinus1  uint8
	OutputFrameHeightInTilesMinus1 uint8
	TileCountMinus1                uint16

	TileData AV1RawTileData
}

// AV1RawMetadataHDRCLL is AV1RawMetadataHDRCLL.
type AV1RawMetadataHDRCLL struct {
	MaxCLL  uint16
	MaxFALL uint16
}

// AV1RawMetadataHDRMDCV is AV1RawMetadataHDRMDCV.
type AV1RawMetadataHDRMDCV struct {
	PrimaryChromaticityX    [3]uint16
	PrimaryChromaticityY    [3]uint16
	WhitePointChromaticityX uint16
	WhitePointChromaticityY uint16
	LuminanceMax            uint32
	LuminanceMin            uint32
}

// AV1RawMetadataScalability is AV1RawMetadataScalability.
type AV1RawMetadataScalability struct {
	ScalabilityModeIdc                        uint8
	SpatialLayersCntMinus1                    uint8
	SpatialLayerDimensionsPresentFlag         uint8
	SpatialLayerDescriptionPresentFlag        uint8
	TemporalGroupDescriptionPresentFlag       uint8
	ScalabilityStructureReserved3Bits         uint8
	SpatialLayerMaxWidth                      [4]uint16
	SpatialLayerMaxHeight                     [4]uint16
	SpatialLayerRefID                         [4]uint8
	TemporalGroupSize                         uint8
	TemporalGroupTemporalID                   [255]uint8
	TemporalGroupTemporalSwitchingUpPointFlag [255]uint8
	TemporalGroupSpatialSwitchingUpPointFlag  [255]uint8
	TemporalGroupRefCnt                       [255]uint8
	TemporalGroupRefPicDiff                   [255][7]uint8
}

// AV1RawMetadataITUTT35 is AV1RawMetadataITUTT35.
type AV1RawMetadataITUTT35 struct {
	ItuTT35CountryCode              uint8
	ItuTT35CountryCodeExtensionByte uint8

	Payload []byte
}

// AV1RawMetadataTimecode is AV1RawMetadataTimecode.
type AV1RawMetadataTimecode struct {
	CountingType      uint8
	FullTimestampFlag uint8
	DiscontinuityFlag uint8
	CntDroppedFlag    uint8
	NFrames           uint16
	SecondsValue      uint8
	MinutesValue      uint8
	HoursValue        uint8
	SecondsFlag       uint8
	MinutesFlag       uint8
	HoursFlag         uint8
	TimeOffsetLength  uint8
	TimeOffsetValue   uint32
}

// AV1RawMetadataUnknown is AV1RawMetadataUnknown.
type AV1RawMetadataUnknown struct {
	Payload []byte
}

// AV1RawMetadata is AV1RawMetadata; the C union members are separate
// fields (only the one matching MetadataType is populated).
type AV1RawMetadata struct {
	MetadataType uint64

	HDRCLL      AV1RawMetadataHDRCLL
	HDRMDCV     AV1RawMetadataHDRMDCV
	Scalability AV1RawMetadataScalability
	ITUTT35     AV1RawMetadataITUTT35
	Timecode    AV1RawMetadataTimecode
	Unknown     AV1RawMetadataUnknown
}

// AV1RawPadding is AV1RawPadding.
type AV1RawPadding struct {
	Payload []byte
}

// AV1RawOBU is AV1RawOBU; the C union members are separate fields (only
// the one matching Header.OBUType is populated).
type AV1RawOBU struct {
	Header AV1RawOBUHeader

	OBUSize uint64

	SequenceHeader AV1RawSequenceHeader
	FrameHeader    AV1RawFrameHeader
	Frame          AV1RawFrame
	TileGroup      AV1RawTileGroup
	TileList       AV1RawTileList
	Metadata       AV1RawMetadata
	Padding        AV1RawPadding
}

// AV1ReferenceFrameState is AV1ReferenceFrameState (cbs_av1.h:435-455).
type AV1ReferenceFrameState struct {
	Valid         int // RefValid
	FrameID       int // RefFrameId
	UpscaledWidth int // RefUpscaledWidth
	FrameWidth    int // RefFrameWidth
	FrameHeight   int // RefFrameHeight
	RenderWidth   int // RefRenderWidth
	RenderHeight  int // RefRenderHeight
	FrameType     int // RefFrameType
	SubsamplingX  int // RefSubsamplingX
	SubsamplingY  int // RefSubsamplingY
	BitDepth      int // RefBitDepth
	OrderHint     int // RefOrderHint

	SavedOrderHints [av1TotalRefsPerFrame]int // SavedOrderHints[ref]

	LoopFilterRefDeltas  [av1TotalRefsPerFrame]int8
	LoopFilterModeDeltas [2]int8
	FeatureEnabled       [av1MaxSegments][av1SegLvlMax]uint8
	FeatureValue         [av1MaxSegments][av1SegLvlMax]int16
}
