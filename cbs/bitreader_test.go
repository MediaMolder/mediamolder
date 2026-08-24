// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import "testing"

func TestBitReaderBasics(t *testing.T) {
	br := newBitReader([]byte{0b1011_0011, 0b1100_0001, 0xff, 0x00})
	if got := br.getBits(1); got != 1 {
		t.Fatalf("bit 0: got %d", got)
	}
	if got := br.getBits(3); got != 0b011 {
		t.Fatalf("bits 1-3: got %b", got)
	}
	if got := br.showBits(8); got != 0b0011_1100 {
		t.Fatalf("show 8: got %08b", got)
	}
	if got := br.getBits(12); got != 0b0011_1100_0001 {
		t.Fatalf("cross-byte 12: got %012b", got)
	}
	if got := br.count(); got != 16 {
		t.Fatalf("count: got %d", got)
	}
	if got := br.bitsLeft(); got != 16 {
		t.Fatalf("bitsLeft: got %d", got)
	}
	if got := br.getBits(16); got != 0xff00 {
		t.Fatalf("last 16: got %x", got)
	}
	// Past-the-end reads return zero bits and drive bitsLeft negative,
	// like FFmpeg's padded GetBitContext.
	if got := br.getBits(8); got != 0 {
		t.Fatalf("past end: got %x", got)
	}
	if got := br.bitsLeft(); got != -8 {
		t.Fatalf("bitsLeft past end: got %d", got)
	}
}

func TestBitReader32BitRead(t *testing.T) {
	br := newBitReader([]byte{0xde, 0xad, 0xbe, 0xef, 0x80})
	br.skipBits(1)
	if got := br.getBits(32); got != 0xbd5b7ddf {
		t.Fatalf("unaligned 32: got %08x", got)
	}
}

func TestBitReaderSigned(t *testing.T) {
	br := newBitReader([]byte{0b1110_0000})
	if got := br.getSBits(3); got != -1 {
		t.Fatalf("sbits(111): got %d", got)
	}
	br = newBitReader([]byte{0b0110_0000})
	if got := br.getSBits(3); got != 3 {
		t.Fatalf("sbits(011): got %d", got)
	}
}

func TestGolombCodes(t *testing.T) {
	// Table of exp-Golomb codes: bits → value.
	cases := []struct {
		bits []byte
		n    int // number of meaningful bits
		ue   uint32
	}{
		{[]byte{0b1000_0000}, 1, 0},
		{[]byte{0b0100_0000}, 3, 1},
		{[]byte{0b0110_0000}, 3, 2},
		{[]byte{0b0010_0000}, 5, 3},
		{[]byte{0b0011_1000}, 5, 6},
		{[]byte{0b0001_0000}, 7, 7},
	}
	for _, c := range cases {
		r := newReader(c.bits, nil)
		var got uint32
		ue(&r, "x", &got, 0, 1000)
		if got != c.ue {
			t.Errorf("ue(%08b): got %d, want %d", c.bits[0], got, c.ue)
		}
		if r.br.count() != c.n {
			t.Errorf("ue(%08b): consumed %d bits, want %d", c.bits[0], r.br.count(), c.n)
		}
	}

	// Signed mapping: 1→0, 010→1, 011→-1, 00100→2, 00101→-2.
	se_ := []struct {
		bits byte
		want int32
	}{
		{0b1000_0000, 0},
		{0b0100_0000, 1},
		{0b0110_0000, -1},
		{0b0010_0000, 2},
		{0b0010_1000, -2},
	}
	for _, c := range se_ {
		r := newReader([]byte{c.bits}, nil)
		var got int32
		se(&r, "x", &got, -1000, 1000)
		if got != c.want {
			t.Errorf("se(%08b): got %d, want %d", c.bits, got, c.want)
		}
	}
}

func TestGolombLimits(t *testing.T) {
	// 32 leading zero bits: "more than 31 zeroes".
	r := newReader([]byte{0, 0, 0, 0, 0xff}, nil)
	if err := catchAbort(func() {
		var v uint32
		ue(&r, "x", &v, 0, 10)
	}); err == nil {
		t.Fatal("expected failure for >31 zeroes")
	}

	// Truncated code: bitstream ended.
	r = newReader([]byte{0b0000_0001}, nil) // promises 7 zeroes + needs 8 more bits
	if err := catchAbort(func() {
		var v uint32
		ue(&r, "x", &v, 0, 1<<30)
	}); err == nil {
		t.Fatal("expected failure for truncated code")
	}
}

func TestRangeValidation(t *testing.T) {
	r := newReader([]byte{0xff}, nil)
	err := catchAbort(func() {
		var v uint8
		u(&r, 8, "x", &v, 0, 254)
	})
	if err == nil {
		t.Fatal("expected out-of-range failure")
	}
}

// catchAbort runs f, converting a readAbort panic into its error.
func catchAbort(f func()) (err error) {
	defer func() {
		if p := recover(); p != nil {
			if ra, ok := p.(readAbort); ok {
				err = ra.err
				return
			}
			panic(p)
		}
	}()
	f()
	return nil
}
