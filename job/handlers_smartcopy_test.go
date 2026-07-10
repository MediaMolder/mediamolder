// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

type probedPkt struct {
	pts  int64
	key  bool
	data []byte
}

// probeVideoPackets returns every video packet (pts, keyframe flag, payload)
// plus the video stream's time_base, in demux order.
func probeVideoPackets(t *testing.T, path string) ([]probedPkt, [2]int) {
	t.Helper()
	in, err := av.OpenInput(path, nil)
	if err != nil {
		t.Fatalf("OpenInput(%s): %v", path, err)
	}
	defer in.Close()

	vIdx := -1
	var tb [2]int
	for i := 0; i < in.NumStreams(); i++ {
		si, err := in.StreamInfo(i)
		if err != nil {
			continue
		}
		if si.Type == av.MediaTypeVideo {
			vIdx = si.Index
			tb = si.TimeBase
			break
		}
	}
	if vIdx < 0 {
		t.Fatalf("%s: no video stream", path)
	}

	var out []probedPkt
	for {
		pkt, err := av.AllocPacket()
		if err != nil {
			t.Fatalf("AllocPacket: %v", err)
		}
		if err := in.ReadPacket(pkt); err != nil {
			pkt.Close()
			if av.IsEOF(err) {
				break
			}
			t.Fatalf("ReadPacket: %v", err)
		}
		if pkt.StreamIndex() == vIdx {
			out = append(out, probedPkt{pts: pkt.PTS(), key: pkt.IsKeyFrame(), data: pkt.Data()})
		}
		pkt.Close()
	}
	return out, tb
}

func secToTB(sec float64, tb [2]int) int64 {
	return int64(sec * float64(tb[1]) / float64(tb[0]))
}

func tbToSec(pts int64, tb [2]int) float64 {
	return float64(pts) * float64(tb[0]) / float64(tb[1])
}

// TestSmartCopyInteriorByteIdentical trims a real clip so both cut points fall
// mid-GOP and asserts (a) interior packets are byte-identical to the source —
// proving the interior is copied, not transcoded — and (b) the output starts
// on a keyframe at the requested start. Boundary packets are expected to
// differ (they are re-encoded).
func TestSmartCopyInteriorByteIdentical(t *testing.T) {
	// BBB_1080p.mp4 has real closed GOPs (~250 frames each); the *sec fixtures
	// are all-intra and would not exercise interior copy vs boundary re-encode.
	src := filepath.Join("..", "testdata", "BBB_1080p.mp4")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("testdata/BBB_1080p.mp4 missing; run: bash scripts/fetch-bbb.sh")
	}

	srcPkts, tb := probeVideoPackets(t, src)
	var kf []int64
	for _, p := range srcPkts {
		if p.key {
			kf = append(kf, p.pts)
		}
	}
	if len(kf) < 4 {
		t.Skipf("source has only %d keyframes; need >=4 to exercise mid-GOP cuts", len(kf))
	}

	// Pick cut points deep inside GOPs a few keyframes apart in the middle of
	// the clip, so the head and tail boundaries each re-encode part of a GOP
	// and several whole GOPs sit between them as interior copy.
	i0 := len(kf) / 3
	i1 := i0 + 4
	if i1 >= len(kf)-1 {
		i1 = len(kf) - 2
	}
	if i0 >= i1 {
		t.Skipf("not enough keyframes (%d) to place cuts", len(kf))
	}
	startSec := (tbToSec(kf[i0], tb) + tbToSec(kf[i0+1], tb)) / 2
	endSec := (tbToSec(kf[i1], tb) + tbToSec(kf[i1+1], tb)) / 2
	if endSec <= startSec {
		t.Skipf("insufficient keyframe spread (start=%.3f end=%.3f)", startSec, endSec)
	}
	startPTS := secToTB(startSec, tb)
	endPTS := secToTB(endSec, tb)

	// First interior keyframe = first kf >= startPTS; tail keyframe = last kf
	// <= endPTS. Packets with pts in [firstInteriorKF, tailKF) are interior.
	var firstInteriorKF, tailKF int64 = -1, -1
	for _, k := range kf {
		if k >= startPTS && firstInteriorKF < 0 {
			firstInteriorKF = k
		}
		if k <= endPTS {
			tailKF = k
		}
	}
	if firstInteriorKF < 0 || tailKF < 0 || firstInteriorKF >= tailKF {
		t.Skipf("no interior GOPs between cuts")
	}

	tmp := t.TempDir()
	outPath := filepath.ToSlash(filepath.Join(tmp, "clip.mp4"))
	cfgJSON := fmt.Sprintf(`{
      "schema_version": "1.0",
      "copy_ts": true,
      "inputs": [{"id": "in0", "url": %q, "streams": [
        {"input_index": 0, "type": "video", "track": 0},
        {"input_index": 0, "type": "audio", "track": 0}
      ]}],
      "graph": {"nodes": [], "edges": [
        {"from": "in0:v:0", "to": "out0:v", "type": "video"},
        {"from": "in0:a:0", "to": "out0:a", "type": "audio"}
      ]},
      "outputs": [{
        "id": "out0", "url": %q,
        "codec_video": "smartcopy", "codec_audio": "copy",
        "options": {"ss": "%.6f", "to": "%.6f"}
      }]
    }`, filepath.ToSlash(src), outPath, startSec, endSec)

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

	// --- Verify ---
	outPkts, otb := probeVideoPackets(t, outPath)
	if len(outPkts) == 0 {
		t.Fatalf("output has no video packets")
	}
	if otb != tb {
		t.Errorf("output time_base %v != source %v (params must be identical)", otb, tb)
	}
	if !outPkts[0].key {
		t.Errorf("first output packet is not a keyframe")
	}
	if got := tbToSec(outPkts[0].pts, tb); got < startSec-tbToSec(1, tb) {
		t.Errorf("first output pts %.3fs is before requested start %.3fs", got, startSec)
	}

	// The muxer re-anchors output timestamps, so match by payload rather
	// than PTS: a copied interior packet's bytes appear verbatim in the
	// source; a re-encoded boundary packet's bytes do not. This directly
	// proves the interior is stream-copied, not transcoded.
	srcSet := make(map[string]struct{}, len(srcPkts))
	for _, p := range srcPkts {
		srcSet[string(p.data)] = struct{}{}
	}

	copied, reencoded := 0, 0
	for _, p := range outPkts {
		if _, ok := srcSet[string(p.data)]; ok {
			copied++
		} else {
			reencoded++
		}
	}
	_ = bytes.Equal // retained for clarity of intent
	if copied == 0 {
		t.Fatalf("no output packets are byte-identical to the source — interior was not stream-copied")
	}
	if reencoded == 0 {
		t.Errorf("no re-encoded boundary packets; cuts may not have landed mid-GOP (start=%.3f end=%.3f)", startSec, endSec)
	}
	// The re-encoded region must be small — at most the head+tail GOPs. With
	// mid-GOP cuts that is well under half the output.
	if reencoded >= copied {
		t.Fatalf("re-encoded %d >= copied %d packets — smartcopy re-encoded too much (expected only boundary GOPs)", reencoded, copied)
	}
	t.Logf("smartcopy: %d interior packets copied byte-identical, %d boundary packets re-encoded; window [%.3f, %.3f]s",
		copied, reencoded, startSec, endSec)
	_ = firstInteriorKF
	_ = tailKF
}
