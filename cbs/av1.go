// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the AV1 codec glue and element primitives: cbs_av1_read_uvlc,
// cbs_av1_read_leb128, cbs_av1_read_ns, cbs_av1_read_increment,
// cbs_av1_read_subexp, cbs_av1_split_fragment, cbs_av1_read_unit and
// cbs_av1_flush (libavcodec/cbs_av1.c).

package cbs

import (
	"errors"
	"math"
)

// --- AV1-specific element primitives (cbs_av1.c) ---

// readUvlc is cbs_av1_read_uvlc.
func readUvlc(r *Reader, name string, rangeMin, rangeMax uint32) uint32 {
	start := r.br.count()

	zeroes := 0
	for zeroes < 32 {
		if r.br.bitsLeft() < 1 {
			r.diag(LevelError, "Invalid uvlc code at %s: bitstream ended.", name)
			r.fail(ErrInvalidData)
		}
		if r.br.getBit() != 0 {
			break
		}
		zeroes++
	}

	var value uint32
	if zeroes >= 32 {
		// The spec allows at least thirty-two zero bits followed by a
		// one to mean 2^32-1, with no constraint on the number of
		// zeroes.  The libaom reference decoder does not match this,
		// instead reading thirty-two zeroes but not the following one
		// to mean 2^32-1.  These two interpretations are incompatible
		// and other implementations may follow one or the other.
		// Therefore we reject thirty-two zeroes because the intended
		// behaviour is not clear.
		r.diag(LevelError, "Thirty-two zero bits in %s uvlc code: considered invalid due to conflicting standard and reference decoder behaviour.", name)
		r.fail(ErrInvalidData)
	} else {
		if r.br.bitsLeft() < zeroes {
			r.diag(LevelError, "Invalid uvlc code at %s: bitstream ended.", name)
			r.fail(ErrInvalidData)
		}

		bitsValue := r.br.getBits(zeroes)
		value = bitsValue + uint32(1)<<uint(zeroes) - 1
	}

	r.traceEnd(start, name, nil, int64(value))

	if value < rangeMin || value > rangeMax {
		r.diag(LevelError, "%s out of range: %d, but must be in [%d,%d].",
			name, value, rangeMin, rangeMax)
		r.fail(ErrInvalidData)
	}

	return value
}

// readLeb128 is cbs_av1_read_leb128.
func readLeb128(r *Reader, name string) uint64 {
	start := r.br.count()

	var value uint64
	for i := 0; i < 8; i++ {
		if r.br.bitsLeft() < 8 {
			r.diag(LevelError, "Invalid leb128 at %s: bitstream ended.", name)
			r.fail(ErrInvalidData)
		}
		byteValue := r.br.getBits(8)
		value |= uint64(byteValue&0x7f) << uint(i*7)
		if byteValue&0x80 == 0 {
			break
		}
	}

	if value > math.MaxUint32 {
		r.fail(ErrInvalidData)
	}

	r.traceEnd(start, name, nil, int64(value))

	return value
}

// readNs is cbs_av1_read_ns.
func readNs(r *Reader, n uint32, name string, subs []int) uint32 {
	start := r.br.count()

	w := log2u32(n) + 1
	m := uint32(1)<<uint(w) - n

	if r.br.bitsLeft() < w {
		r.diag(LevelError, "Invalid non-symmetric value at %s: bitstream ended.", name)
		r.fail(ErrInvalidData)
	}

	var v uint32
	if w-1 > 0 {
		v = r.br.getBits(w - 1)
	} else {
		v = 0
	}

	var value uint32
	if v < m {
		value = v
	} else {
		extraBit := r.br.getBit()
		value = v<<1 - m + extraBit
	}

	r.traceEnd(start, name, subs, int64(value))

	return value
}

// readIncrement is cbs_av1_read_increment.
func readIncrement(r *Reader, rangeMin, rangeMax uint32, name string) uint32 {
	start := r.br.count()

	value := rangeMin
	for value < rangeMax {
		if r.br.bitsLeft() < 1 {
			r.diag(LevelError, "Invalid increment value at %s: bitstream ended.", name)
			r.fail(ErrInvalidData)
		}
		if r.br.getBit() != 0 {
			value++
		} else {
			break
		}
	}

	r.traceEnd(start, name, nil, int64(value))

	return value
}

// readSubexp is cbs_av1_read_subexp.
func readSubexp(r *Reader, rangeMax uint32, name string, subs []int) uint32 {
	maxLen := uint32(log2u32(rangeMax-1) - 3)

	length := readIncrement(r, 0, maxLen, "subexp_more_bits")

	var rangeBits, rangeOffset uint32
	if length != 0 {
		rangeBits = 2 + length
		rangeOffset = 1 << uint(rangeBits)
	} else {
		rangeBits = 3
		rangeOffset = 0
	}

	var value uint32
	if length < maxLen {
		value = readUnsignedRaw(r, int(rangeBits), "subexp_bits", nil,
			0, math.MaxUint32)
	} else {
		value = readNs(r, rangeMax-rangeOffset, "subexp_final_bits", nil)
	}
	value += rangeOffset

	r.traceValueOnly(name, subs, int64(value))

	return value
}

// --- Go analogues of the AV1 template macros (cbs_av1.c:496-590) ---
//
// fb/fc/fbs/fcs/flag/flags/su/sus/fixed map onto the shared reader
// helpers ub/u/ubs/us/flag/flags/ib/ibs/fixed; the AV1-only composites
// follow here.

// uvlcv is uvlc(name, range_min, range_max).
func uvlcv[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax uint32) {
	*dst = T(readUvlc(r, name, rangeMin, rangeMax))
}

// ns is ns(max_value, name, subs...).
func ns[T integer](r *Reader, maxValue uint32, name string, dst *T, subs ...int) {
	*dst = T(readNs(r, maxValue, name, subs))
}

// increment is increment(name, min, max).
func increment[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax uint32) {
	*dst = T(readIncrement(r, rangeMin, rangeMax, name))
}

// subexpv is subexp(name, max, subs...).
func subexpv[T integer](r *Reader, name string, dst *T, maxValue uint32, subs ...int) {
	*dst = T(readSubexp(r, maxValue, name, subs))
}

// deltaQ is delta_q(name): a 1-bit delta_coded flag followed by su(1+6).
func deltaQ(r *Reader, name string, dst *int8) {
	deltaCoded := readUnsignedRaw(r, 1, name+".delta_coded", nil, 0, 1)
	var dq int32
	if deltaCoded != 0 {
		dq = readSignedRaw(r, 1+6, name+".delta_q", nil,
			minIntBits(1+6), maxIntBits(1+6))
	} else {
		dq = 0
	}
	*dst = int8(dq)
}

// leb128v is leb128(name).
func leb128v[T integer](r *Reader, name string, dst *T) {
	*dst = T(readLeb128(r, name))
}

// av1TileLog2 is cbs_av1_tile_log2.
func av1TileLog2(blksize, target int) int {
	k := 0
	for ; blksize<<uint(k) < target; k++ {
	}
	return k
}

// av1GetRelativeDist is cbs_av1_get_relative_dist.
func av1GetRelativeDist(seq *AV1RawSequenceHeader, a, b uint32) int {
	if seq.EnableOrderHint == 0 {
		return 0
	}
	diff := a - b
	m := uint32(1) << uint(seq.OrderHintBitsMinus1)
	diff = (diff & (m - 1)) - (diff & m)
	return int(int32(diff))
}

// av1PayloadBytesLeft is cbs_av1_get_payload_bytes_left.
func av1PayloadBytesLeft(r *Reader) int {
	tmp := r.br
	size := 0
	for i := 0; tmp.bitsLeft() >= 8; i++ {
		if tmp.getBits(8) != 0 {
			size = i
		}
	}
	return size
}

// AV1Context is CodedBitstreamAV1Context.
type AV1Context struct {
	tr Tracer

	sequenceHeader *AV1RawSequenceHeader

	seenFrameHeader int
	frameHeader     []byte
	frameHeaderSize int // bits

	temporalID        int
	spatialID         int
	operatingPointIDC int

	bitDepth      int
	orderHint     int
	frameWidth    int
	frameHeight   int
	upscaledWidth int
	renderWidth   int
	renderHeight  int

	numPlanes     int
	codedLossless int
	allLossless   int
	tileCols      int
	tileRows      int
	tileNum       int

	orderHints       [av1TotalRefsPerFrame]int // OrderHints
	refFrameSignBias [av1TotalRefsPerFrame]int // RefFrameSignBias

	ref [av1NumRefFrames]AV1ReferenceFrameState

	// AVOptions (fixed_obu_size_length is write-only in C and not ported)
	operatingPoint int

	loopFilterRefDeltas  [av1TotalRefsPerFrame]int8
	loopFilterModeDeltas [2]int8
	featureEnabled       [av1MaxSegments][av1SegLvlMax]uint8
	featureValue         [av1MaxSegments][av1SegLvlMax]int16
}

func newAV1Context(tr Tracer) *AV1Context {
	return &AV1Context{tr: tr, operatingPoint: -1}
}

func (a *AV1Context) diag(level Level, format string, args ...any) {
	if a.tr != nil {
		a.tr.Diag(level, sprintf(format, args...))
	}
}

// splitFragment is cbs_av1_split_fragment.
func (a *AV1Context) splitFragment(frag *Fragment, header bool) error {
	data := frag.Data
	size := len(data)
	off := 0

	if math.MaxInt32/8 < size {
		a.diag(LevelError, "Invalid fragment: too large (%d bytes).", size)
		return ErrInvalidData
	}

	if header && size > 0 && data[0]&0x80 != 0 {
		// first bit is nonzero, the extradata does not consist purely of
		// OBUs. Expect MP4/Matroska AV1CodecConfigurationRecord
		configRecordVersion := int(data[0] & 0x7f)

		if configRecordVersion != 1 {
			a.diag(LevelError, "Unknown version %d of AV1CodecConfigurationRecord found!",
				configRecordVersion)
			return ErrInvalidData
		}

		if size <= 4 {
			if size < 4 {
				a.diag(LevelWarning, "Undersized AV1CodecConfigurationRecord v%d found!",
					configRecordVersion)
				return ErrInvalidData
			}
			return nil
		}

		// In AV1CodecConfigurationRecord v1, actual OBUs start after
		// four bytes. Thus set the offset as required for properly
		// parsing them.
		off = 4
		size -= 4
	}

	for size > 0 {
		var obuHeader AV1RawOBUHeader
		var obuSize uint64
		var pos int

		// Don't include this parsing in trace output.
		err := func() (err error) {
			defer func() {
				if p := recover(); p != nil {
					if ra, ok := p.(readAbort); ok {
						err = ra.err
						return
					}
					panic(p)
				}
			}()
			r := newReader(data[off:off+size], nil)
			r.tr = nil
			a.obuHeader(&r, &obuHeader)

			if obuHeader.OBUHasSizeField != 0 {
				if r.br.bitsLeft() < 8 {
					a.diag(LevelError, "Invalid OBU: fragment too short (%d bytes).", size)
					return ErrInvalidData
				}
				obuSize = readLeb128(&r, "obu_size")
			} else {
				obuSize = uint64(size) - 1 - uint64(obuHeader.OBUExtensionFlag)
			}

			pos = r.bitPosition()
			return nil
		}()
		if err != nil {
			return err
		}

		obuLength := uint64(pos/8) + obuSize

		if uint64(size) < obuLength {
			a.diag(LevelError, "Invalid OBU length: %d, but only %d bytes remaining in fragment.",
				obuLength, size)
			return ErrInvalidData
		}

		frag.Units = append(frag.Units, Unit{
			Type:       uint32(obuHeader.OBUType),
			TypeName:   av1OBUName(uint32(obuHeader.OBUType)),
			Offset:     off,
			PrefixSize: 0,
			RawSize:    int(obuLength),
			RBSP:       data[off : off+int(obuLength)],
			TemporalID: obuHeader.TemporalID,
			SpatialID:  obuHeader.SpatialID,
		})

		off += int(obuLength)
		size -= int(obuLength)
	}

	return nil
}

// refTileData is cbs_av1_ref_tile_data.
func (a *AV1Context) refTileData(r *Reader, unit *Unit) []byte {
	pos := r.br.count()
	if pos >= 8*len(unit.RBSP) {
		r.diag(LevelError, "Bitstream ended before any data in tile group (%d bits read).", pos)
		r.fail(ErrInvalidData)
	}
	// Must be byte-aligned at this point.
	return unit.RBSP[pos/8:]
}

// readUnit is cbs_av1_read_unit.
func (a *AV1Context) readUnit(unit *Unit) {
	defer recoverUnit(unit)

	r := newReader(unit.RBSP, a.tr)
	obu := new(AV1RawOBU)
	unit.Content = obu

	a.obuHeader(&r, &obu.Header)

	if obu.Header.OBUHasSizeField != 0 {
		obuSize := readLeb128(&r, "obu_size")
		obu.OBUSize = obuSize
	} else {
		if len(unit.RBSP) < 1+int(obu.Header.OBUExtensionFlag) {
			r.diag(LevelError, "Invalid OBU length: unit too short (%d).", len(unit.RBSP))
			r.fail(ErrInvalidData)
		}
		obu.OBUSize = uint64(len(unit.RBSP) - 1 - int(obu.Header.OBUExtensionFlag))
	}

	startPos := r.bitPosition()

	if obu.Header.OBUExtensionFlag != 0 {
		if obu.Header.OBUType != av1OBUSequenceHeader &&
			obu.Header.OBUType != av1OBUTemporalDelimiter &&
			a.operatingPointIDC != 0 {
			inTemporalLayer := (a.operatingPointIDC >> uint(a.temporalID)) & 1
			inSpatialLayer := (a.operatingPointIDC >> uint(a.spatialID+8)) & 1
			if inTemporalLayer == 0 || inSpatialLayer == 0 {
				unit.Err = ErrSkipped // drop_obu()
				return
			}
		}
	}

	switch obu.Header.OBUType {
	case av1OBUSequenceHeader:
		a.sequenceHeaderOBU(&r, &obu.SequenceHeader)

		if a.operatingPoint >= 0 {
			sequenceHeader := &obu.SequenceHeader

			if a.operatingPoint > int(sequenceHeader.OperatingPointsCntMinus1) {
				r.diag(LevelError, "Invalid Operating Point %d requested. Must not be higher than %d.",
					a.operatingPoint, sequenceHeader.OperatingPointsCntMinus1)
				r.fail(errors.New("Invalid argument"))
			}
			a.operatingPointIDC = int(sequenceHeader.OperatingPointIdc[a.operatingPoint])
		}

		a.sequenceHeader = &obu.SequenceHeader

	case av1OBUTemporalDelimiter:
		a.temporalDelimiterOBU(&r)

	case av1OBUFrameHeader, av1OBURedundantFrameHeader:
		a.frameHeaderOBU(&r, &obu.FrameHeader,
			obu.Header.OBUType == av1OBURedundantFrameHeader)

	case av1OBUFrame, av1OBUTileGroup:
		var tileGroup *AV1RawTileGroup
		if obu.Header.OBUType == av1OBUFrame {
			a.frameOBU(&r, &obu.Frame)
			tileGroup = &obu.Frame.TileGroup
		} else {
			tileGroup = &obu.TileGroup
		}

		tileGroup.Data = a.refTileData(&r, unit)

		a.tileGroupOBU(&r, tileGroup)

		tileGroup.TileData.Data = a.refTileData(&r, unit)

	case av1OBUTileList:
		a.tileListOBU(&r, &obu.TileList)

		obu.TileList.TileData.Data = a.refTileData(&r, unit)

	case av1OBUMetadata:
		a.metadataOBU(&r, &obu.Metadata)

	case av1OBUPadding:
		a.paddingOBU(&r, &obu.Padding)

	default:
		unit.Skip = SkipUnimplemented
		unit.Err = ErrUnsupported
		return
	}

	endPos := r.bitPosition()

	if obu.OBUSize > 0 &&
		obu.Header.OBUType != av1OBUTileGroup &&
		obu.Header.OBUType != av1OBUTileList &&
		obu.Header.OBUType != av1OBUFrame {
		nbBits := int64(obu.OBUSize)*8 + int64(startPos) - int64(endPos)

		if nbBits <= 0 {
			r.fail(ErrInvalidData)
		}

		a.trailingBits(&r, int(nbBits))
	}

	unit.Decomposed = true
}

func (a *AV1Context) readFragmentContent(frag *Fragment) {
	readFragmentUnits(frag, a.tr, func(u *Unit) { a.readUnit(u) })
}

func (a *AV1Context) ReadExtradata(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := a.splitFragment(frag, true); err != nil {
		return frag, err
	}
	a.readFragmentContent(frag)
	return frag, nil
}

func (a *AV1Context) ReadPacket(data []byte) (*Fragment, error) {
	frag := &Fragment{Data: data}
	if err := a.splitFragment(frag, false); err != nil {
		return frag, err
	}
	a.readFragmentContent(frag)
	return frag, nil
}

// Flush is cbs_av1_flush.
func (a *AV1Context) Flush() {
	a.sequenceHeader = nil
	a.frameHeader = nil
	a.frameHeaderSize = 0

	a.ref = [av1NumRefFrames]AV1ReferenceFrameState{}
	a.operatingPointIDC = 0
	a.seenFrameHeader = 0
	a.tileNum = 0
}
