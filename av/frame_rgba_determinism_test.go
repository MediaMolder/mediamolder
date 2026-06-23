// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"testing"
)

// writeTestJPEG encodes a w×h gradient JPEG (4:2:0) to path.
func writeTestJPEG(t *testing.T, path string, w, h int) {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x * 7), uint8(y * 5), uint8((x + y) * 3), 255})
		}
	}
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := jpeg.Encode(f, img, &jpeg.Options{Quality: 88}); err != nil {
		t.Fatal(err)
	}
}

// decodeFileToRGBA decodes the first video frame of path and returns its packed RGBA bytes,
// closing every resource before returning (so each call is a fresh decode + allocation cycle).
func decodeFileToRGBA(t *testing.T, path string) []byte {
	t.Helper()
	input, err := OpenInput(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()

	vid := -1
	for i := 0; i < input.NumStreams(); i++ {
		if si, e := input.StreamInfo(i); e == nil && si.Type == MediaTypeVideo {
			vid = i
			break
		}
	}
	if vid < 0 {
		t.Fatalf("no video stream in %s", path)
	}
	dec, err := OpenDecoder(input, vid)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()

	pkt, err := AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	for {
		f, err := AllocFrame()
		if err != nil {
			t.Fatal(err)
		}
		if dec.ReceiveFrame(f) == nil {
			img, err := f.ToRGBA()
			f.Close()
			if err != nil {
				t.Fatal(err)
			}
			return append([]byte(nil), img.Pix...)
		}
		f.Close()
		pkt.Unref()
		if e := input.ReadPacket(pkt); e != nil {
			t.Fatalf("no frame decoded from %s: %v", path, e)
		}
		if pkt.StreamIndex() == vid {
			_ = dec.SendPacket(pkt)
		}
	}
}

// TestToRGBA_Deterministic is a regression test for non-deterministic RGBA output:
// frame_to_rgba allocated its destination with av_malloc (uninitialized) and sws_scale does
// not write every byte of the RGBA destination for certain widths (here 737, whose right
// edge is not block-aligned), so the unwritten bytes were heap garbage that varied between
// decodes — the same file could convert to different pixels run to run, breaking byte-stable
// downstream uses such as pixel hashing.
//
// Decode the same file repeatedly (a fresh decode each time churns the allocator) and require
// byte-identical RGBA output every time.
func TestToRGBA_Deterministic(t *testing.T) {
	path := filepath.Join(t.TempDir(), "gradient.jpg")
	writeTestJPEG(t, path, 737, 252)

	var first []byte
	for i := 0; i < 32; i++ {
		pix := decodeFileToRGBA(t, path)
		if i == 0 {
			first = pix
		} else if !bytes.Equal(pix, first) {
			t.Fatalf("decode+ToRGBA produced different output on decode %d — conversion is not deterministic", i)
		}
	}
}
