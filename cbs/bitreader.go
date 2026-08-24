// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Port of the GetBitContext subset CBS uses (libavcodec/get_bits.h).
// Reads past the end of the buffer return zero bits, matching FFmpeg's
// padded-buffer semantics; bitsLeft may go negative, like get_bits_left.

package cbs

// BitReader is a big-endian bit reader over a byte slice.
//
// pos and end are absolute bit offsets into data. A sub-reader (see
// Reader.sub) shares data and pos with a reduced end, so bit positions
// reported by tracing stay relative to the unit start, exactly as FFmpeg's
// SEI payload_gbc behaves.
type BitReader struct {
	data []byte
	pos  int
	end  int // exclusive
}

func newBitReader(data []byte) BitReader {
	return BitReader{data: data, end: 8 * len(data)}
}

// count is get_bits_count: bits consumed since the start of the unit.
func (b *BitReader) count() int { return b.pos }

// bitsLeft is get_bits_left; it may be negative after over-reads.
func (b *BitReader) bitsLeft() int { return b.end - b.pos }

// peek returns n bits (0 <= n <= 32) starting at absolute bit offset pos,
// zero-padding past the end of data.
func (b *BitReader) peek(pos, n int) uint32 {
	var v uint32
	for n > 0 {
		byteIdx := pos >> 3
		var cur byte
		if byteIdx >= 0 && byteIdx < len(b.data) {
			cur = b.data[byteIdx]
		}
		bitOff := pos & 7
		take := 8 - bitOff
		if take > n {
			take = n
		}
		bits := (cur << uint(bitOff)) >> uint(8-take)
		v = v<<uint(take) | uint32(bits)
		pos += take
		n -= take
	}
	return v
}

// getBits is get_bits_long: read and consume n bits, 0 <= n <= 32.
func (b *BitReader) getBits(n int) uint32 {
	v := b.peek(b.pos, n)
	b.pos += n
	return v
}

// getBit reads a single bit (get_bits1).
func (b *BitReader) getBit() uint32 { return b.getBits(1) }

// getSBits is get_sbits_long: read n bits, sign-extended, 0 < n <= 32.
func (b *BitReader) getSBits(n int) int32 {
	v := b.getBits(n)
	return int32(v<<uint(32-n)) >> uint(32-n)
}

// showBits is show_bits_long: peek n bits without consuming, 0 <= n <= 32.
func (b *BitReader) showBits(n int) uint32 { return b.peek(b.pos, n) }

// skipBits is skip_bits_long.
func (b *BitReader) skipBits(n int) { b.pos += n }
