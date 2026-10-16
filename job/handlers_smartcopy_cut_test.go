// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/cbs"
)

// Decode-based smartcopy tests on generated fixtures: 80 frames, 64x64,
// 30000/1001 fps, GOP 12 with two B-frames, each frame a flat luma that
// identifies it, so a decoded frame names its source frame. Mid-GOP cuts at
// both ends are checked by decoding the output: frame count, order,
// content and the decoder's error flags.

const (
	cutFixtureFrames = 80
	cutFixtureTB     = 30000 // mp4/mov timescale for 30000/1001 fps; one frame = 1001 ticks
)

func cutFixtureLuma(i int) uint8 { return uint8(16 + (i*37)%200) }

func cutFixtureSeconds(frame float64) float64 { return frame * 1001 / cutFixtureTB }

// writeCutFixture encodes the fixture with codecName (libx264 / libx265)
// into path. openGOP makes every keyframe after the first a non-IDR I
// (H.264) / CRA (HEVC) with leading B-frames.
func writeCutFixture(t *testing.T, path, format, codecName string, openGOP bool) {
	t.Helper()
	if !av.FindEncoder(codecName) {
		t.Skipf("encoder %s not available", codecName)
	}
	extra := map[string]string{"crf": "18", "preset": "medium"}
	switch codecName {
	case "libx264":
		p := "keyint=12:min-keyint=12:scenecut=0:bframes=2"
		if openGOP {
			p += ":open-gop=1"
		}
		extra["x264-params"] = p
	case "libx265":
		p := "keyint=12:min-keyint=12:scenecut=0:bframes=2:log-level=error:open-gop=0"
		if openGOP {
			p = p[:len(p)-1] + "1"
		}
		extra["x265-params"] = p
	}
	enc, err := av.OpenEncoder(av.EncoderOptions{
		CodecName:    codecName,
		Width:        64,
		Height:       64,
		PixFmt:       0, // AV_PIX_FMT_YUV420P
		FrameRate:    [2]int{30000, 1001},
		TimeBase:     [2]int{1001, 30000},
		GOPSize:      12,
		GlobalHeader: true,
		ExtraOpts:    extra,
	})
	if err != nil {
		t.Fatalf("OpenEncoder(%s): %v", codecName, err)
	}
	defer enc.Close()

	out, err := av.OpenOutputWithFormat(path, format)
	if err != nil {
		t.Fatalf("OpenOutputWithFormat(%s): %v", format, err)
	}
	idx, err := out.AddStream(enc)
	if err != nil {
		t.Fatalf("AddStream: %v", err)
	}
	if codecName == "libx265" {
		if err := out.SetStreamCodecTag(idx, "hvc1"); err != nil {
			t.Fatalf("SetStreamCodecTag: %v", err)
		}
	}
	if err := out.WriteHeader(); err != nil {
		t.Fatalf("WriteHeader: %v", err)
	}
	pkt, err := av.AllocPacket()
	if err != nil {
		t.Fatal(err)
	}
	defer pkt.Close()
	drain := func() {
		for enc.ReceivePacket(pkt) == nil {
			pkt.SetStreamIndex(idx)
			pkt.Rescale(enc.TimeBase(), out.StreamTimeBase(idx))
			if err := out.WritePacket(pkt); err != nil {
				t.Fatalf("WritePacket: %v", err)
			}
			pkt.Unref()
		}
	}
	fr := av.NewTestFrame(t, 64, 64, 0)
	defer fr.Close()
	for i := 0; i < cutFixtureFrames; i++ {
		av.FillTestFrameYFlat(fr, cutFixtureLuma(i))
		fr.SetPTS(int64(i))
		if err := enc.SendFrame(fr); err != nil {
			t.Fatalf("SendFrame(%d): %v", i, err)
		}
		drain()
	}
	if err := enc.Flush(); err != nil {
		t.Fatalf("Flush: %v", err)
	}
	drain()
	if err := out.WriteTrailer(); err != nil {
		t.Fatalf("WriteTrailer: %v", err)
	}
	if err := out.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

type decodedPic struct {
	pts      int64
	luma     float64
	errFlags int
}

// decodeVideo decodes every video frame of path with the stream's own
// extradata, in output order.
func decodeVideo(t *testing.T, path string) []decodedPic {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()
	vIdx := -1
	for i := 0; i < in.NumStreams(); i++ {
		if si, err := in.StreamInfo(i); err == nil && si.Type == av.MediaTypeVideo {
			vIdx = i
			break
		}
	}
	if vIdx < 0 {
		t.Fatalf("%s: no video stream", path)
	}
	dec, err := av.OpenDecoder(in, vIdx)
	if err != nil {
		t.Fatalf("OpenDecoder: %v", err)
	}
	defer dec.Close()

	var pics []decodedPic
	receive := func() {
		for {
			f, err := av.AllocFrame()
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.ReceiveFrame(f); err != nil {
				f.Close()
				if av.IsEAgain(err) || av.IsEOF(err) {
					return
				}
				t.Fatalf("ReceiveFrame: %v", err)
			}
			w, h, ls := f.PlaneWidth(0), f.PlaneHeight(0), f.Linesize(0)
			data := f.Plane(0)
			sum := 0
			for y := 0; y < h; y++ {
				for x := 0; x < w; x++ {
					sum += int(data[y*ls+x])
				}
			}
			pics = append(pics, decodedPic{pts: f.PTS(), luma: float64(sum) / float64(w*h), errFlags: f.DecodeErrorFlags()})
			f.Close()
		}
	}
	for {
		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatal(err)
		}
		if err := in.ReadPacket(pkt); err != nil {
			pkt.Close()
			if av.IsEOF(err) {
				break
			}
			t.Fatalf("ReadPacket: %v", err)
		}
		if pkt.StreamIndex() == vIdx {
			if err := dec.SendPacket(pkt); err != nil {
				t.Fatalf("SendPacket: %v", err)
			}
			receive()
		}
		pkt.Close()
	}
	if err := dec.Flush(); err != nil && !av.IsEOF(err) && !av.IsEAgain(err) {
		t.Fatalf("Flush: %v", err)
	}
	receive()
	return pics
}

// runSmartCut trims src to frames [first, last] (last < 0: to the end)
// through a codec_video:"smartcopy" job and returns the output path.
func runSmartCut(t *testing.T, src, outPath string, first, last int) {
	t.Helper()
	ss := cutFixtureSeconds(max(float64(first)-0.5, 0))
	timing := fmt.Sprintf(`"ss": "%.6f"`, ss)
	if last >= 0 {
		timing += fmt.Sprintf(`, "to": "%.6f"`, cutFixtureSeconds(float64(last)+0.5))
	}
	cfgJSON := fmt.Sprintf(`{
      "schema_version": "1.0",
      "copy_ts": true,
      "inputs": [{"id": "in0", "url": %q, "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
      "graph": {"nodes": [], "edges": [{"from": "in0:v:0", "to": "out0:v", "type": "video"}]},
      "outputs": [{"id": "out0", "url": %q, "codec_video": "smartcopy", "options": {%s}}]
    }`, filepath.ToSlash(src), filepath.ToSlash(outPath), timing)
	cfg, err := ParseConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// verifySmartCut decodes outPath and checks it holds exactly source frames
// first..last, in order, with the right content and no decoder errors.
// Adjacent fixture frames differ by 37 luma levels, so a tolerance well
// under half of that still identifies every frame.
func verifySmartCut(t *testing.T, outPath string, first, last int) {
	t.Helper()
	verifySmartCutTol(t, outPath, first, last, 6)
}

func verifySmartCutTol(t *testing.T, outPath string, first, last int, tol float64) {
	t.Helper()
	if last < 0 {
		last = cutFixtureFrames - 1
	}
	want := last - first + 1
	pics := decodeVideo(t, outPath)
	if len(pics) != want {
		t.Errorf("decoded %d frames, want %d (source %d..%d)", len(pics), want, first, last)
	}
	n := min(len(pics), want)
	for i := 0; i < n; i++ {
		exp := float64(cutFixtureLuma(first + i))
		if d := pics[i].luma - exp; d > tol || d < -tol {
			t.Errorf("frame %d (source %d): luma %.1f, want %.0f", i, first+i, pics[i].luma, exp)
		}
		if pics[i].errFlags != 0 {
			t.Errorf("frame %d (source %d): decode_error_flags %#x", i, first+i, pics[i].errFlags)
		}
		if i > 0 && pics[i].pts <= pics[i-1].pts {
			t.Errorf("frame %d: pts %d not after %d", i, pics[i].pts, pics[i-1].pts)
		}
	}
	pkts, _ := probeVideoPackets(t, outPath)
	if len(pkts) != want {
		t.Errorf("output has %d video packets, want %d", len(pkts), want)
	}
	if len(pkts) > 0 && !pkts[0].key {
		t.Errorf("first output packet is not a keyframe")
	}
	// A constant-rate source must give a constant-rate cut: every DTS step
	// (what MP4 stores in stts) is one frame, at the head, at the joins and
	// at the tail. A single odd step makes probes call the file variable
	// frame rate.
	for i := 1; i < len(pkts); i++ {
		if d := pkts[i].dts - pkts[i-1].dts; d != 1001 {
			t.Errorf("packet %d: DTS step %d, want 1001 (constant frame rate)", i, d)
		}
	}
}

// slicePayloadKeys returns, per video packet of path, a key made of its
// slice payloads: for HEVC the VCL NAL bytes, for H.264 the slice data after
// the header (an open-GOP splice renumbers pic_order_cnt_lsb in copied
// slices, so the header may legitimately differ). Parameter sets inserted
// in front of a keyframe never affect the key.
func slicePayloadKeys(t *testing.T, codec cbs.CodecID, path string) []string {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatal(err)
	}
	ed := in.StreamExtraData(0)
	in.Close()
	lengthSize := isomNALLengthSize(codec, ed)
	pkts, _ := probeVideoPackets(t, path)
	keys := make([]string, 0, len(pkts))
	if codec == cbs.CodecH265 {
		for _, p := range pkts {
			nals, err := splitNALs(p.data, lengthSize)
			if err != nil {
				t.Fatal(err)
			}
			var key []byte
			for _, n := range nals {
				if nalIsVCL(codec, n) {
					key = append(key, n...)
				}
			}
			keys = append(keys, string(key))
		}
		return keys
	}
	c, err := cbs.New(cbs.CodecH264, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := c.ReadExtradata(ed); err != nil {
		t.Fatal(err)
	}
	for _, p := range pkts {
		frag, err := c.ReadPacket(p.data)
		if err != nil {
			t.Fatal(err)
		}
		var key []byte
		for i := range frag.Units {
			if sl, ok := frag.Units[i].Content.(*cbs.H264RawSlice); ok {
				key = append(key, byte(sl.DataBitStart))
				key = append(key, sl.Data...)
			}
		}
		keys = append(keys, string(key))
	}
	return keys
}

// countCopied reports how many output packets carry slice payloads that are
// byte-identical to a source packet's.
func countCopied(t *testing.T, codec cbs.CodecID, src, out string) (copied, total int) {
	t.Helper()
	set := map[string]bool{}
	for _, k := range slicePayloadKeys(t, codec, src) {
		set[k] = true
	}
	outKeys := slicePayloadKeys(t, codec, out)
	for _, k := range outKeys {
		if set[k] {
			copied++
		}
	}
	return copied, len(outKeys)
}

func TestSmartCopyCutDecodes(t *testing.T) {
	cases := []struct {
		name    string
		codec   string
		format  string
		openGOP bool
	}{
		{"h264-closed-mp4", "libx264", "mp4", false},
		{"h264-closed-mov", "libx264", "mov", false},
		{"h264-open-mp4", "libx264", "mp4", true},
		{"h264-open-mov", "libx264", "mov", true},
		{"hevc-closed-mp4", "libx265", "mp4", false},
		{"hevc-open-mp4", "libx265", "mp4", true},
		{"hevc-open-mov", "libx265", "mov", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src."+tc.format)
			writeCutFixture(t, src, tc.format, tc.codec, tc.openGOP)
			codec := cbs.CodecH264
			if tc.codec == "libx265" {
				codec = cbs.CodecH265
			}

			// Frames 17..58: both cuts mid-GOP, two whole GOPs (24, 36) inside.
			out := filepath.Join(dir, "cut."+tc.format)
			runSmartCut(t, src, out, 17, 58)
			verifySmartCut(t, out, 17, 58)
			copied, total := countCopied(t, codec, src, out)
			if copied < 20 || copied >= total {
				t.Errorf("copied %d of %d packets; want the two interior GOPs copied and the boundaries re-encoded", copied, total)
			}

			in, err := av.OpenInput(out, nil)
			if err != nil {
				t.Fatal(err)
			}
			si, _ := in.StreamInfo(0)
			ed := in.StreamExtraData(0)
			in.Close()
			pkts, _ := probeVideoPackets(t, out)
			tag := string([]byte{byte(si.CodecTag), byte(si.CodecTag >> 8), byte(si.CodecTag >> 16), byte(si.CodecTag >> 24)})
			switch codec {
			case cbs.CodecH264:
				if tag != "avc1" {
					t.Errorf("codec tag %q, want avc1", tag)
				}
				sps, pps, _, err := avcCParamSets(ed)
				if err != nil || len(sps) != 2 || len(pps) != 2 {
					t.Errorf("output avcC: %d SPS, %d PPS (%v); want the source's and the boundary's", len(sps), len(pps), err)
				}
			case cbs.CodecH265:
				if tag != "hvc1" {
					t.Errorf("codec tag %q, want hvc1 (source sample entry kept; parameter sets in hvcC)", tag)
				}
				nals, err := hvcCParamSets(ed)
				if err != nil {
					t.Fatalf("output hvcC: %v", err)
				}
				counts := map[int]int{}
				for _, n := range nals {
					counts[hevcNALType(n)]++
				}
				if counts[hevcNALVPS] != 2 || counts[hevcNALSPS] != 2 || counts[hevcNALPPS] != 2 {
					t.Errorf("output hvcC holds %d VPS, %d SPS, %d PPS; want the source's and the boundary's", counts[hevcNALVPS], counts[hevcNALSPS], counts[hevcNALPPS])
				}
				if packetHasParamSets(cbs.CodecH265, pkts[0].data, 4) {
					t.Errorf("first output packet carries in-band parameter sets; config mode should keep them in hvcC")
				}
			}

			// Head only: frame 17 to the end of the clip.
			tail := filepath.Join(dir, "tail."+tc.format)
			runSmartCut(t, src, tail, 17, -1)
			verifySmartCut(t, tail, 17, -1)
		})
	}
}

// TestSmartCopyOpenGOPSplices sweeps head and tail cut positions over an
// open-GOP source so the splice lands before, inside and after the leading
// pictures of the following keyframe, for both codecs.
func TestSmartCopyOpenGOPSplices(t *testing.T) {
	for _, codec := range []string{"libx264", "libx265"} {
		t.Run(codec, func(t *testing.T) {
			dir := t.TempDir()
			src := filepath.Join(dir, "src.mp4")
			writeCutFixture(t, src, "mp4", codec, true)
			for _, c := range [][2]int{{13, 58}, {21, 58}, {22, 58}, {23, 58}, {24, 58}, {17, 46}, {17, 47}, {17, 48}, {17, 59}, {17, 60}, {0, 70}, {12, 23}} {
				out := filepath.Join(dir, fmt.Sprintf("cut-%d-%d.mp4", c[0], c[1]))
				runSmartCut(t, src, out, c[0], c[1])
				verifySmartCut(t, out, c[0], c[1])
			}
		})
	}
}

// TestSmartCopyInBandH264 exercises the in-band H.264 path (a boundary
// encoder other than libx264 cannot pick parameter-set ids, so its SPS/PPS
// travel in-band and the output becomes avc3) with the VideoToolbox encoder
// where available.
func TestSmartCopyInBandH264(t *testing.T) {
	if !av.FindEncoder("h264_videotoolbox") {
		t.Skip("h264_videotoolbox not available")
	}
	dir := t.TempDir()
	src := filepath.Join(dir, "src.mp4")
	writeCutFixture(t, src, "mp4", "libx264", false)
	out := filepath.Join(dir, "cut.mp4")
	cfgJSON := fmt.Sprintf(`{
      "schema_version": "1.0",
      "copy_ts": true,
      "inputs": [{"id": "in0", "url": %q, "streams": [{"input_index": 0, "type": "video", "track": 0}]}],
      "graph": {
        "nodes": [{"id": "sc_v", "type": "smartcopy", "params": {"smartcopy_encoder": "h264_videotoolbox"}}],
        "edges": [{"from": "in0:v:0", "to": "sc_v", "type": "video"}, {"from": "sc_v", "to": "out0", "type": "video"}]
      },
      "outputs": [{"id": "out0", "url": %q, "options": {"ss": "%.6f", "to": "%.6f"}}]
    }`, filepath.ToSlash(src), filepath.ToSlash(out), cutFixtureSeconds(16.5), cutFixtureSeconds(58.5))
	cfg, err := ParseConfig([]byte(cfgJSON))
	if err != nil {
		t.Fatalf("ParseConfig: %v", err)
	}
	eng, err := NewPipeline(cfg)
	if err != nil {
		t.Fatalf("NewPipeline: %v", err)
	}
	if err := eng.Run(context.Background()); err != nil {
		t.Skipf("Run with h264_videotoolbox: %v", err)
	}
	// The hardware encoder is driven by the source's (tiny) bit rate rather
	// than a quality target, so flat frames come back a few levels off.
	verifySmartCutTol(t, out, 17, 58, 16)
	in, err := av.OpenInput(out, nil)
	if err != nil {
		t.Fatal(err)
	}
	si, _ := in.StreamInfo(0)
	in.Close()
	tag := string([]byte{byte(si.CodecTag), byte(si.CodecTag >> 8), byte(si.CodecTag >> 16), byte(si.CodecTag >> 24)})
	if tag != "avc3" {
		t.Errorf("codec tag %q, want avc3 (in-band parameter sets)", tag)
	}
}
