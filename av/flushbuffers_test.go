// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// TestFlushBuffersAllowsDecodeAfterSeek guards the windowed re-decode used by
// scene_change_mc's full-res refinement: after seeking an open decoder, the
// decoder must be reset with FlushBuffers (avcodec_flush_buffers), NOT Flush
// (avcodec_send_packet(NULL), the drain/EOF signal that leaves the decoder
// refusing further packets). The bug this guards produced zero frames after
// every seek, so the full-res stage skipped every dissolve.
func TestFlushBuffersAllowsDecodeAfterSeek(t *testing.T) {
	input, err := OpenInput("testdata/tiny.mp4", nil)
	if err != nil {
		t.Skip("testdata/tiny.mp4 not available:", err)
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
		t.Skip("no video stream in fixture")
	}
	dec, err := OpenDecoder(input, vid)
	if err != nil {
		t.Fatal(err)
	}
	defer dec.Close()
	pkt, _ := AllocPacket()
	defer pkt.Close()

	decodeOne := func() bool {
		for i := 0; i < 1000; i++ {
			f, _ := AllocFrame()
			if e := dec.ReceiveFrame(f); e == nil {
				f.Close()
				return true
			}
			pkt.Unref()
			if e := input.ReadPacket(pkt); e != nil {
				f.Close()
				if IsEOF(e) {
					return false
				}
				return false
			}
			if pkt.StreamIndex() != vid {
				f.Close()
				continue
			}
			_ = dec.SendPacket(pkt)
			f.Close()
		}
		return false
	}

	if !decodeOne() {
		t.Fatal("could not decode initial frame")
	}
	// Seek back to the start and reset with FlushBuffers — decoding MUST
	// resume. (With Flush()/drain it would not.)
	if err := input.SeekFile(0); err != nil {
		t.Fatal(err)
	}
	dec.FlushBuffers()
	if !decodeOne() {
		t.Fatal("decode did not resume after SeekFile + FlushBuffers")
	}
}
