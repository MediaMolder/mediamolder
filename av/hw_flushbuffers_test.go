// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// TestHWDecoderFlushBuffersAllowsDecodeAfterSeek is the hardware twin of
// TestFlushBuffersAllowsDecodeAfterSeek: a seek-and-decode loop must be able to reset a
// HARDWARE decoder with FlushBuffers (avcodec_flush_buffers) and keep decoding — the
// contract seeked multi-frame extraction drives through the shared decoder interface.
// Probes the available device types and skips where none opens (CI runners); on a machine
// with a working device (e.g. VideoToolbox on Apple silicon) it exercises the real path.
func TestHWDecoderFlushBuffersAllowsDecodeAfterSeek(t *testing.T) {
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

	var dev *HWDeviceContext
	for _, dt := range []HWDeviceType{HWDeviceVideoToolbox, HWDeviceCUDA, HWDeviceVAAPI, HWDeviceQSV} {
		if d, e := OpenHWDevice(dt, ""); e == nil {
			dev = d
			break
		}
	}
	if dev == nil {
		t.Skip("no hardware decode device available on this machine")
	}
	defer dev.Close()

	dec, err := OpenHWDecoder(input, vid, dev, HWDecoderOptions{AutoTransfer: true})
	if err != nil {
		t.Skipf("OpenHWDecoder: %v", err)
	}
	defer dec.Close()
	pkt, _ := AllocPacket()
	defer pkt.Close()

	decodeOne := func() bool {
		for i := 0; i < 1000; i++ {
			f, _ := AllocFrame()
			if e := dec.ReceiveFrame(f); e == nil {
				sw := !IsHWFrame(f) // AutoTransfer must hand back software frames
				f.Close()
				if !sw {
					t.Fatal("AutoTransfer returned a hardware frame")
				}
				return true
			}
			f.Close()
			pkt.Unref()
			if e := input.ReadPacket(pkt); e != nil {
				if IsEOF(e) {
					return false
				}
				return false
			}
			if pkt.StreamIndex() == vid {
				_ = dec.SendPacket(pkt)
			}
		}
		return false
	}

	if !decodeOne() {
		t.Fatal("no frame decoded before the seek")
	}
	if err := input.SeekFile(0); err != nil {
		t.Fatalf("seek: %v", err)
	}
	dec.FlushBuffers()
	if !decodeOne() {
		t.Fatal("no frame decoded after seek + FlushBuffers — the reset did not take")
	}
}
