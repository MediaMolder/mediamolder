// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Element-read primitives: ports of cbs_read_unsigned / ff_cbs_read_signed
// (libavcodec/cbs.c), ff_cbs_read_ue_golomb / ff_cbs_read_se_golomb and
// ff_cbs_h2645_read_more_rbsp_data (libavcodec/cbs_h2645.c), plus the Go
// analogues of the syntax-template convenience macros (cbs_h264.c:56-88).
//
// Error propagation: r.fail panics with an internal sentinel; readUnit
// recovers it into the unit's error, mirroring C's `return err` unwinding
// without an `if err != nil` on every template line.

package cbs

import (
	"fmt"
	"math"
	"math/bits"
)

type unsigned interface {
	~uint8 | ~uint16 | ~uint32 | ~uint64
}

type sigd interface {
	~int8 | ~int16 | ~int32 | ~int64
}

// integer covers both: the C macros freely assign the read value to
// whatever integer field the struct declares.
type integer interface {
	~uint8 | ~uint16 | ~uint32 | ~uint64 | ~int8 | ~int16 | ~int32 | ~int64
}

// readAbort is the panic sentinel carrying the parse error.
type readAbort struct{ err error }

// Reader wraps a BitReader with the tracer, giving the syntax templates
// the same surface as the C RWContext + CodedBitstreamContext pair.
type Reader struct {
	br BitReader
	tr Tracer
}

func newReader(data []byte, tr Tracer) Reader {
	return Reader{br: newBitReader(data), tr: tr}
}

// sub returns the SEI payload sub-reader: same data and position, with the
// end capped limitBits past the current position (init_get_bits over the
// same buffer + skip, as in cbs_sei message_list).
func (r *Reader) sub(limitBits int) Reader {
	nr := *r
	nr.br.end = nr.br.pos + limitBits
	return nr
}

// fail aborts the current unit's parse; recovered in readUnit.
func (r *Reader) fail(err error) {
	panic(readAbort{err})
}

// header is the HEADER() macro (ff_cbs_trace_header).
func (r *Reader) header(name string) {
	if r.tr != nil {
		r.tr.Header(name)
	}
}

// diag is an av_log mirror; the message text must match C for goldens.
func (r *Reader) diag(level Level, format string, args ...any) {
	if r.tr != nil {
		r.tr.Diag(level, fmt.Sprintf(format, args...))
	}
}

// traceEnd is CBS_TRACE_READ_END: emit the element that started at bit
// offset start and ended at the current position.
func (r *Reader) traceEnd(start int, name string, subs []int, value int64) {
	if r.tr == nil {
		return
	}
	length := r.br.pos - start
	bs := make([]byte, length)
	for i := 0; i < length; i++ {
		if r.br.peek(start+i, 1) != 0 {
			bs[i] = '1'
		} else {
			bs[i] = '0'
		}
	}
	r.tr.Element(Element{
		Position:   start,
		Length:     length,
		Name:       name,
		Subscripts: subs,
		Bits:       string(bs),
		Value:      value,
	})
}

// traceValueOnly is CBS_TRACE_READ_END_VALUE_ONLY: a zero-length element
// whose sub-elements were already traced (AV1 subexp, leb128 wrappers).
func (r *Reader) traceValueOnly(name string, subs []int, value int64) {
	if r.tr == nil {
		return
	}
	r.tr.Element(Element{
		Position:   r.br.pos,
		Name:       name,
		Subscripts: subs,
		Value:      value,
	})
}

// bitPosition is the bit_position(rw) macro.
func (r *Reader) bitPosition() int { return r.br.count() }

// byteAlignment is the byte_alignment(rw) macro.
func (r *Reader) byteAlignment() int { return r.br.count() % 8 }

// moreRBSPData is ff_cbs_h2645_read_more_rbsp_data.
func (r *Reader) moreRBSPData() bool {
	bitsLeft := r.br.bitsLeft()
	if bitsLeft > 8 {
		return true
	}
	if bitsLeft <= 0 {
		return false
	}
	return r.br.showBits(bitsLeft)&maxUintBits(bitsLeft-1) != 0
}

// maxUintBits is MAX_UINT_BITS(length): the largest value in length bits.
func maxUintBits(length int) uint32 {
	return uint32((uint64(1) << uint(length)) - 1)
}

// maxIntBits / minIntBits are MAX_INT_BITS / MIN_INT_BITS.
func maxIntBits(length int) int32 { return int32((uint64(1) << uint(length-1)) - 1) }
func minIntBits(length int) int32 { return -int32(uint64(1) << uint(length-1)) }

// av_log2: floor(log2(v)) for v > 0.
func log2u32(v uint32) int { return 31 - bits.LeadingZeros32(v) }

// readUnsignedRaw is cbs_read_unsigned. Order matters for goldens:
// bits-left check (no trace on failure), read, trace, then range check.
func readUnsignedRaw(r *Reader, width int, name string, subs []int, rangeMin, rangeMax uint32) uint32 {
	start := r.br.count()
	if r.br.bitsLeft() < width {
		r.diag(LevelError, "Invalid value at %s: bitstream ended.", name)
		r.fail(ErrInvalidData)
	}
	value := r.br.getBits(width)
	r.traceEnd(start, name, subs, int64(value))
	if value < rangeMin || value > rangeMax {
		r.diag(LevelError, "%s out of range: %d, but must be in [%d,%d].",
			name, value, rangeMin, rangeMax)
		r.fail(ErrInvalidData)
	}
	return value
}

// readSignedRaw is ff_cbs_read_signed.
func readSignedRaw(r *Reader, width int, name string, subs []int, rangeMin, rangeMax int32) int32 {
	start := r.br.count()
	if r.br.bitsLeft() < width {
		r.diag(LevelError, "Invalid value at %s: bitstream ended.", name)
		r.fail(ErrInvalidData)
	}
	value := r.br.getSBits(width)
	r.traceEnd(start, name, subs, int64(value))
	if value < rangeMin || value > rangeMax {
		r.diag(LevelError, "%s out of range: %d, but must be in [%d,%d].",
			name, value, rangeMin, rangeMax)
		r.fail(ErrInvalidData)
	}
	return value
}

// readUEGolomb is ff_cbs_read_ue_golomb.
func readUEGolomb(r *Reader, name string, subs []int, rangeMin, rangeMax uint32) uint32 {
	start := r.br.count()
	maxLength := min(r.br.bitsLeft(), 32)
	var leadingBits uint32
	if maxLength > 0 {
		leadingBits = r.br.showBits(maxLength)
	}
	if leadingBits == 0 {
		if maxLength >= 32 {
			r.diag(LevelError, "Invalid ue-golomb code at %s: more than 31 zeroes.", name)
		} else {
			r.diag(LevelError, "Invalid ue-golomb code at %s: bitstream ended.", name)
		}
		r.fail(ErrInvalidData)
	}
	leadingZeroes := maxLength - 1 - log2u32(leadingBits)
	r.br.skipBits(leadingZeroes)
	if r.br.bitsLeft() < leadingZeroes+1 {
		r.diag(LevelError, "Invalid ue-golomb code at %s: bitstream ended.", name)
		r.fail(ErrInvalidData)
	}
	value := r.br.getBits(leadingZeroes+1) - 1
	r.traceEnd(start, name, subs, int64(value))
	if value < rangeMin || value > rangeMax {
		r.diag(LevelError, "%s out of range: %d, but must be in [%d,%d].",
			name, value, rangeMin, rangeMax)
		r.fail(ErrInvalidData)
	}
	return value
}

// readSEGolomb is ff_cbs_read_se_golomb.
func readSEGolomb(r *Reader, name string, subs []int, rangeMin, rangeMax int32) int32 {
	start := r.br.count()
	maxLength := min(r.br.bitsLeft(), 32)
	var leadingBits uint32
	if maxLength > 0 {
		leadingBits = r.br.showBits(maxLength)
	}
	if leadingBits == 0 {
		if maxLength >= 32 {
			r.diag(LevelError, "Invalid se-golomb code at %s: more than 31 zeroes.", name)
		} else {
			r.diag(LevelError, "Invalid se-golomb code at %s: bitstream ended.", name)
		}
		r.fail(ErrInvalidData)
	}
	leadingZeroes := maxLength - 1 - log2u32(leadingBits)
	r.br.skipBits(leadingZeroes)
	if r.br.bitsLeft() < leadingZeroes+1 {
		r.diag(LevelError, "Invalid se-golomb code at %s: bitstream ended.", name)
		r.fail(ErrInvalidData)
	}
	unsignedValue := r.br.getBits(leadingZeroes + 1)
	var value int32
	if unsignedValue&1 != 0 {
		value = -int32(unsignedValue / 2)
	} else {
		value = int32(unsignedValue / 2)
	}
	r.traceEnd(start, name, subs, int64(value))
	if value < rangeMin || value > rangeMax {
		r.diag(LevelError, "%s out of range: %d, but must be in [%d,%d].",
			name, value, rangeMin, rangeMax)
		r.fail(ErrInvalidData)
	}
	return value
}

// --- Go analogues of the H.26x template macros (cbs_h264.c:56-88) ---
//
// Each call reads one syntax element into *dst; names must be the exact C
// identifier strings, brackets included, for golden parity.

// ub is ub(width, name): unrestricted range (read_simple_unsigned).
func ub[T integer](r *Reader, width int, name string, dst *T) {
	*dst = T(readUnsignedRaw(r, width, name, nil, 0, math.MaxUint32))
}

// ubs is ubs(width, name, subs...): unrestricted, subscripted.
// Mirrors the C macro: range 0..MAX_UINT_BITS(width).
func ubs[T integer](r *Reader, width int, name string, dst *T, subs ...int) {
	*dst = T(readUnsignedRaw(r, width, name, subs, 0, maxUintBits(width)))
}

// u is u(width, name, range_min, range_max).
func u[T integer](r *Reader, width int, name string, dst *T, rangeMin, rangeMax uint32) {
	*dst = T(readUnsignedRaw(r, width, name, nil, rangeMin, rangeMax))
}

// us is us(width, name, range_min, range_max, subs...).
func us[T integer](r *Reader, width int, name string, dst *T, rangeMin, rangeMax uint32, subs ...int) {
	*dst = T(readUnsignedRaw(r, width, name, subs, rangeMin, rangeMax))
}

// flag is flag(name) == ub(1, name).
func flag[T integer](r *Reader, name string, dst *T) {
	*dst = T(readUnsignedRaw(r, 1, name, nil, 0, math.MaxUint32))
}

// flags is flags(name, subs...) == xu(1, name, 0, 1, subs...).
func flags[T integer](r *Reader, name string, dst *T, subs ...int) {
	*dst = T(readUnsignedRaw(r, 1, name, subs, 0, 1))
}

// ue is ue(name, range_min, range_max).
func ue[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax uint32) {
	*dst = T(readUEGolomb(r, name, nil, rangeMin, rangeMax))
}

// ues is ues(name, range_min, range_max, subs...).
func ues[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax uint32, subs ...int) {
	*dst = T(readUEGolomb(r, name, subs, rangeMin, rangeMax))
}

// se is se(name, range_min, range_max).
func se[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax int32) {
	*dst = T(readSEGolomb(r, name, nil, rangeMin, rangeMax))
}

// ses is ses(name, range_min, range_max, subs...).
func ses[T integer](r *Reader, name string, dst *T, rangeMin, rangeMax int32, subs ...int) {
	*dst = T(readSEGolomb(r, name, subs, rangeMin, rangeMax))
}

// iv is i(width, name, range_min, range_max) (renamed: `i` is the
// conventional loop variable in the templates).
func iv[T integer](r *Reader, width int, name string, dst *T, rangeMin, rangeMax int32) {
	*dst = T(readSignedRaw(r, width, name, nil, rangeMin, rangeMax))
}

// ib is ib(width, name).
func ib[T integer](r *Reader, width int, name string, dst *T) {
	*dst = T(readSignedRaw(r, width, name, nil, minIntBits(width), maxIntBits(width)))
}

// ibs is ibs(width, name, subs...).
func ibs[T integer](r *Reader, width int, name string, dst *T, subs ...int) {
	*dst = T(readSignedRaw(r, width, name, subs, minIntBits(width), maxIntBits(width)))
}

// fixed is fixed(width, name, value): traced and validated constant.
func fixed(r *Reader, width int, name string, value uint32) {
	readUnsignedRaw(r, width, name, nil, value, value)
}

// infer is infer(name, value): assignment only, no bits, no trace.
func infer[T any](dst *T, value T) { *dst = value }

// cond is the C ternary, for template lines like
// `(chroma_format_idc != 3) ? 8 : 12`.
func cond[T any](c bool, a, b T) T {
	if c {
		return a
	}
	return b
}
func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }
