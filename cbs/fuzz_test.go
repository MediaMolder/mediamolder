// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package cbs

import (
	"errors"
	"io"
	"os"
	"testing"
)

// fuzzCodec asserts the hostile-input contract: no *InternalError (a
// parser bug), unit geometry stays inside the input, and the text tracer
// never crashes rendering whatever was traced.
func fuzzCodec(f *testing.F, codec CodecID, seedFiles ...string) {
	for _, sf := range seedFiles {
		if data, err := os.ReadFile(sf); err == nil {
			f.Add(data)
			if len(data) > 512 {
				f.Add(data[:512])
			}
		}
	}
	f.Add([]byte{0x00, 0x00, 0x01, 0x67})
	f.Add([]byte{0x00, 0x00, 0x00, 0x01})
	f.Add([]byte{})

	f.Fuzz(func(t *testing.T, data []byte) {
		for _, tr := range []Tracer{nil, NewTextTracer(io.Discard)} {
			c, err := New(codec, tr)
			if err != nil {
				t.Fatal(err)
			}
			for _, header := range []bool{true, false} {
				var frag *Fragment
				var rerr error
				if header {
					frag, rerr = c.ReadExtradata(data)
				} else {
					frag, rerr = c.ReadPacket(data)
				}
				_ = rerr
				if frag == nil {
					t.Fatal("nil fragment")
				}
				for i := range frag.Units {
					u := &frag.Units[i]
					var ie *InternalError
					if errors.As(u.Err, &ie) {
						t.Fatalf("unit %d: internal parser error: %v\n%s", i, ie.Panic, ie.Stack)
					}
					if u.Offset < 0 || u.Offset+u.RawSize > len(frag.Data) {
						t.Fatalf("unit %d: geometry out of bounds: offset %d raw %d len %d",
							i, u.Offset, u.RawSize, len(frag.Data))
					}
				}
			}
		}
	})
}

func FuzzH264ReadPacket(f *testing.F) {
	fuzzCodec(f, CodecH264, "testdata/tiny.h264")
}

func FuzzH265ReadPacket(f *testing.F) {
	fuzzCodec(f, CodecH265, "testdata/tiny.hevc")
}

func FuzzAV1ReadPacket(f *testing.F) {
	fuzzCodec(f, CodecAV1, "testdata/tiny.obu")
}
