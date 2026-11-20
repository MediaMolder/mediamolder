// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

type cudaTestProcessor struct{ processors.Null }

func (p *cudaTestProcessor) Process(f *av.Frame, ctx processors.ProcessorContext) (*av.Frame, *processors.Metadata, error) {
	if ctx.HWDevice == nil || ctx.HWDevice.Type() != av.HWDeviceCUDA {
		return nil, nil, fmt.Errorf("expected a named CUDA device")
	}
	view, ok := f.CUDAFrame()
	if !ok || view.PixFmt != av.PixFmtByName("nv12") || view.NumPlanes != 2 {
		return nil, nil, fmt.Errorf("expected a CUDA NV12 frame, got %+v", view)
	}
	// Return a new reference to the same device buffers. This exercises the
	// replacement-frame path without downloading or uploading any pixels.
	out, err := f.Clone()
	return out, nil, err
}

func init() {
	processors.Register("test_cuda_frame", func() processors.Processor { return &cudaTestProcessor{} })
}

func TestGoProcessorCUDAEncode(t *testing.T) {
	av.RequireHWDevice(t, av.HWDeviceCUDA)
	probe, err := av.OpenEncoder(av.EncoderOptions{
		CodecName: "h264_nvenc", Width: 320, Height: 240, FrameRate: [2]int{25, 1},
	})
	if err != nil {
		t.Skipf("NVENC unavailable: %v", err)
	}
	probe.Close()

	input := filepath.Join("..", "av", "testdata", "tiny.mp4")
	output := filepath.Join(t.TempDir(), "out.mp4")
	raw := fmt.Sprintf(`{
		"schema_version": "1.1",
		"hardware_devices": [{"name": "gpu0", "type": "cuda", "device": "0"}],
		"inputs": [{
			"id": "src", "url": %q,
			"hwaccel": "cuda", "hwaccel_device": "gpu0", "hwaccel_output_format": "cuda",
			"streams": [{"input_index": 0, "type": "video", "track": 0}]
		}],
		"graph": {
			"nodes": [
				{"id": "proc", "type": "go_processor", "processor": "test_cuda_frame", "device": "gpu0"},
				{"id": "enc", "type": "encoder", "device": "gpu0",
				 "params": {"codec": "h264_nvenc", "pix_fmt": "cuda", "sw_pix_fmt": "nv12"}}
			],
			"edges": [
				{"from": "src:v:0", "to": "proc", "type": "video"},
				{"from": "proc", "to": "enc", "type": "video"},
				{"from": "enc", "to": "out:v", "type": "video"}
			]
		},
		"outputs": [{"id": "out", "url": %q}]
	}`, input, output)
	cfg, err := ParseConfig([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	p, err := NewPipeline(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.Run(t.Context()); err != nil {
		t.Fatal(err)
	}

	in, err := av.OpenInput(output, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	count := 0
	for {
		err := in.ReadPacket(pkt)
		if av.IsEOF(err) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		count++
		pkt.Unref()
	}
	if count != 20 {
		t.Errorf("encoded %d packets, want 20", count)
	}
}
