// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"log"
	"os"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/cbs"
	"github.com/MediaMolder/MediaMolder/graph"
)

// smartCopyMode says where the boundary encoder's parameter sets live in
// the output and how its packets are framed to match the copied interior.
type smartCopyMode int

const (
	// smartCopyModeConfigPS: the boundary SPS/PPS use ids the source does
	// not, and the output avcC carries the source's and the boundary's
	// parameter sets side by side. Copied packets stay byte-identical and
	// nothing travels in-band (H.264 + libx264 + an avcC source).
	smartCopyModeConfigPS smartCopyMode = iota
	// smartCopyModeInBand: the boundary encoder repeats its parameter sets
	// before every IDR, and the source's parameter sets are re-inserted on
	// every copied keyframe, since both use the same ids. ISOBMFF outputs
	// get the in-band sample entry (hev1 / avc3).
	smartCopyModeInBand
	// smartCopyModePassthrough: not a NAL-unit codec; packets are forwarded
	// as the encoder emits them (headers in-band).
	smartCopyModePassthrough
)

// smartCopyPlan is the per-node decision shared by the sink (which
// registers the output stream) and the handler (which re-encodes boundary
// GOPs).
type smartCopyPlan struct {
	codec      cbs.CodecID // 0 for non-NAL codecs
	mode       smartCopyMode
	lengthSize int // source NAL length-prefix size; 0 = Annex B
	encOpts    av.EncoderOptions

	extradata    []byte   // replacement output extradata (nil = keep the source's)
	codecTag     string   // sample entry for ISOBMFF outputs ("" = keep)
	srcParamSets [][]byte // source VPS/SPS/PPS NALs, re-inserted on copied keyframes (in-band mode)
	encParamSets [][]byte // boundary SPS/PPS expected from the encoder (config mode)

	// srcReorder is the source's decode-to-display delay in frames (the
	// B-frame depth its DTS timeline assumes), or -1 when it could not be
	// probed. The boundary encoder is kept at or below it so re-encoded
	// packets slot into the copied DTS timeline without collisions.
	srcReorder int

	// H.264 POC continuity across an open-GOP head splice (see
	// smartcopy_h264poc.go). pocFix is false when the source or the
	// boundary encoder does not use pic_order_cnt_type 0 or when the
	// boundary MaxPicOrderCntLsb could not be brought under the source's.
	pocFix    bool
	srcMaxLsb int32

	notes []string
}

// smartCopyPlanFor returns the cached plan for a smartcopy video node,
// computing it on first use. Audio nodes have no plan (nil, nil).
func (r *graphRunner) smartCopyPlanFor(node *graph.Node) (*smartCopyPlan, error) {
	if v, ok := r.smartCopyPlans.Load(node.ID); ok {
		return v.(*smartCopyPlan), nil
	}
	input, srcIdx, srcTB, err := r.copySourceFor(node)
	if err != nil {
		return nil, err
	}
	si, err := input.StreamInfo(srcIdx)
	if err != nil {
		return nil, err
	}
	if si.Type != av.MediaTypeVideo {
		return nil, nil
	}
	reorder := -1
	if url := r.sourceURLFor(node); url != "" {
		reorder = probeSourceReorder(url, srcIdx, si, srcTB)
	}
	plan, err := planSmartCopyVideo(si, srcTB, input.StreamExtraData(srcIdx), reorder, node.Params)
	if err != nil {
		return nil, err
	}
	if v, loaded := r.smartCopyPlans.LoadOrStore(node.ID, plan); loaded {
		return v.(*smartCopyPlan), nil
	}
	for _, n := range plan.notes {
		log.Printf("smartcopy node %q: %s", node.ID, n)
	}
	return plan, nil
}

// sourceURLFor returns the URL of the source feeding a copy/smartcopy node
// when it is a regular file (probe-able with a second demuxer), else "".
func (r *graphRunner) sourceURLFor(node *graph.Node) string {
	if len(node.Inbound) != 1 {
		return ""
	}
	from := node.Inbound[0].From
	for from != nil && from.Kind == graph.KindGoProcessor && len(from.Inbound) == 1 {
		from = from.Inbound[0].From
	}
	if from == nil || from.Kind != graph.KindSource {
		return ""
	}
	src := r.sources[from.ID]
	if src == nil || src.cfg.Kind != "" && src.cfg.Kind != "file" {
		return ""
	}
	if st, err := os.Stat(src.cfg.URL); err != nil || !st.Mode().IsRegular() {
		return ""
	}
	return src.cfg.URL
}

// probeSourceReorder demuxes the first GOP of stream srcIdx with a second
// demuxer and returns its decode-to-display delay in frames, or -1.
func probeSourceReorder(url string, srcIdx int, si av.StreamInfo, tb [2]int) int {
	in, err := av.OpenInput(url, nil)
	if err != nil {
		return -1
	}
	defer in.Close()
	var pts, dts []int64
	keys := 0
	for n := 0; n < 2000 && keys < 2; n++ {
		pkt, err := av.AllocPacket()
		if err != nil {
			return -1
		}
		if err := in.ReadPacket(pkt); err != nil {
			pkt.Close()
			break
		}
		if pkt.StreamIndex() == srcIdx {
			if pkt.IsKeyFrame() {
				keys++
			}
			if keys == 1 {
				pts = append(pts, pkt.PTS())
				dts = append(dts, pkt.DTS())
			}
		}
		pkt.Close()
	}
	if len(pts) == 0 {
		return -1
	}
	return reorderDepth(pts, dts, frameDurationTB(si.FrameRate, tb))
}

// reorderDepth derives a GOP's decode-to-display delay in frames from its
// packets in decode order (keyframe first). A stream's DTS timeline is its
// display-ordered PTS sequence shifted by that delay, so for the keyframe
// PTS − DTS = (delay + leading pictures) × frame duration. Without usable
// DTS, the smallest delay that keeps every DTS at or below its PTS is
// returned instead.
func reorderDepth(pts, dts []int64, dur int64) int {
	if len(pts) == 0 {
		return -1
	}
	sorted := append([]int64(nil), pts...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i] < sorted[j] })
	if dur <= 0 && len(sorted) > 1 {
		deltas := make([]int64, 0, len(sorted)-1)
		for i := 1; i < len(sorted); i++ {
			deltas = append(deltas, sorted[i]-sorted[i-1])
		}
		sort.Slice(deltas, func(i, j int) bool { return deltas[i] < deltas[j] })
		dur = deltas[len(deltas)/2]
	}
	key := pts[0]
	leading := 0
	for _, p := range pts {
		if validPTS(p) && p < key {
			leading++
		}
	}
	if dur > 0 && len(dts) > 0 && validPTS(dts[0]) && validPTS(key) {
		d := int((key-dts[0]+dur/2)/dur) - leading
		if d >= 0 {
			return d
		}
	}
	rank := make(map[int64]int, len(sorted))
	for i, p := range sorted {
		rank[p] = i
	}
	depth := 0
	for k, p := range pts {
		if d := k - rank[p]; d > depth {
			depth = d
		}
	}
	return depth
}

// planSmartCopyVideo derives the boundary encoder configuration and the
// output stream adjustments for one source video stream.
func planSmartCopyVideo(si av.StreamInfo, srcTB [2]int, extradata []byte, srcReorder int, params map[string]any) (*smartCopyPlan, error) {
	opts, err := buildBoundaryEncoderOptions(si, srcTB, params)
	if err != nil {
		return nil, err
	}
	plan := &smartCopyPlan{encOpts: opts, mode: smartCopyModePassthrough, srcReorder: srcReorder}
	codec, isNAL := cbs.CodecFromName(av.CodecName(si.CodecID))
	if !isNAL || codec == cbs.CodecAV1 {
		plan.encOpts.GlobalHeader = false
		return plan, nil
	}
	plan.codec = codec
	plan.lengthSize = isomNALLengthSize(codec, extradata)
	if _, set := params["smartcopy_global_header"]; set {
		plan.notes = append(plan.notes, "smartcopy_global_header is ignored: parameter-set placement is derived from the source")
	}
	if srcReorder < 0 {
		plan.notes = append(plan.notes, "source reorder depth unknown (not a probe-able file); boundary packet DTS are derived from the re-encoded run alone")
	}

	switch codec {
	case cbs.CodecH265:
		plan.mode = smartCopyModeInBand
		plan.encOpts.GlobalHeader = false
		if plan.lengthSize > 0 {
			nals, err := hvcCParamSets(extradata)
			if err != nil {
				return nil, fmt.Errorf("source hvcC: %w", err)
			}
			for _, n := range nals {
				if nalIsParamSet(codec, n) {
					plan.srcParamSets = append(plan.srcParamSets, n)
				}
			}
			plan.codecTag = "hev1"
		}
		if plan.encOpts.CodecName == "libx265" {
			var kv []string
			switch srcReorder {
			case 0:
				kv = []string{"bframes=0"}
			case 1:
				kv = []string{"b-pyramid=0"}
			}
			if len(kv) > 0 {
				plan.encOpts.ExtraOpts = mergeCodecParams(plan.encOpts.ExtraOpts, "x265-params", kv)
				plan.notes = append(plan.notes, "boundary x265 reorder depth limited to the source's ("+strings.Join(kv, ", ")+")")
			}
		}
		plan.notes = append(plan.notes, "HEVC boundary parameter sets travel in-band; copied keyframes carry the source's (hev1)")
		return plan, nil

	case cbs.CodecH264:
		return planSmartCopyH264(plan, extradata)
	}
	return plan, nil
}

func planSmartCopyH264(plan *smartCopyPlan, extradata []byte) (*smartCopyPlan, error) {
	src, err := newH264POCState(extradata, plan.lengthSize)
	if err != nil {
		return nil, fmt.Errorf("source H.264 parameter sets: %w", err)
	}
	srcSPS := src.firstSPS()
	srcPocType := -1
	srcRefs := 0
	if srcSPS != nil {
		srcPocType = int(srcSPS.PicOrderCntType)
		srcRefs = int(srcSPS.MaxNumRefFrames)
		if srcPocType == 0 {
			plan.srcMaxLsb = int32(1) << (srcSPS.Log2MaxPicOrderCntLsbMinus4 + 4)
		}
	}
	isX264 := plan.encOpts.CodecName == "libx264" || plan.encOpts.CodecName == "libx264rgb"

	// Base x264 parameters: match the source's reorder depth (so the
	// re-encoded DTS slot into the copied timeline), its POC type and, in
	// config mode, free parameter-set ids.
	base := []string{}
	if isX264 {
		switch {
		case srcPocType == 2:
			// pic_order_cnt_type 2 derives POC from frame_num: the boundary
			// run must use it too (x264 picks it without B-frames) so the
			// copied keyframe's POC follows the run's.
			base = append(base, "bframes=0")
			plan.notes = append(plan.notes, "source uses pic_order_cnt_type 2; boundary re-encode runs without B-frames")
		case plan.srcReorder == 0:
			base = append(base, "bframes=0")
			plan.notes = append(plan.notes, "source has no B-frames; boundary re-encode runs without them")
		case plan.srcReorder == 1:
			base = append(base, "b-pyramid=none")
			plan.notes = append(plan.notes, "boundary x264 B-pyramid disabled to match the source's reorder depth")
		}
	}

	if plan.lengthSize > 0 && isX264 {
		plan.mode = smartCopyModeConfigPS
		plan.encOpts.GlobalHeader = true
		used := map[int]bool{}
		for id, sp := range src.sps {
			if sp != nil {
				used[int(id)] = true
			}
		}
		for id, pp := range src.pps {
			if pp != nil {
				used[int(id)] = true
			}
		}
		spsID := -1
		for id := 0; id < 32; id++ {
			if !used[id] {
				spsID = id
				break
			}
		}
		if spsID < 0 {
			return nil, fmt.Errorf("source avcC uses every SPS id; no free id for the boundary encoder")
		}
		base = append(base, "sps-id="+strconv.Itoa(spsID))
		plan.notes = append(plan.notes, fmt.Sprintf("boundary SPS/PPS use id %d and are appended to the output avcC", spsID))
	} else {
		plan.mode = smartCopyModeInBand
		plan.encOpts.GlobalHeader = false
		if plan.lengthSize > 0 {
			sps, pps, _, err := avcCParamSets(extradata)
			if err != nil {
				return nil, fmt.Errorf("source avcC: %w", err)
			}
			plan.srcParamSets = append(append(plan.srcParamSets, sps...), pps...)
			plan.codecTag = "avc3"
		}
		plan.notes = append(plan.notes, fmt.Sprintf("H.264 boundary parameter sets travel in-band (%s is not libx264 or the source is Annex B)", plan.encOpts.CodecName))
	}

	// Open a trial encoder to learn the boundary parameter sets. For the
	// splice to decode cleanly the boundary SPS must not keep more
	// reference frames than the source's, and for pic_order_cnt_type 0 its
	// MaxPicOrderCntLsb must not exceed the source's (the renumbering is
	// derived in the source's modulus). x264 sizes both from its B-frame
	// layout and reference count, so tighten those until they fit.
	candidates := [][]string{nil}
	if isX264 {
		refKV := []string{}
		if srcRefs > 0 {
			refKV = append(refKV, "ref="+strconv.Itoa(srcRefs))
		}
		candidates = append(candidates,
			refKV,
			append([]string{"b-pyramid=none"}, refKV...),
			append([]string{"b-pyramid=none", "bframes=3"}, refKV...),
			append([]string{"b-pyramid=none", "bframes=2"}, refKV...),
			append([]string{"b-pyramid=none", "bframes=1"}, refKV...),
		)
	}
	var encSPS, encPPS [][]byte
	var encHdr *cbs.H264RawSPS
	for _, extra := range candidates {
		opts := plan.encOpts
		opts.GlobalHeader = true
		opts.ExtraOpts = mergeCodecParams(plan.encOpts.ExtraOpts, "x264-params", append(append([]string{}, base...), extra...))
		enc, err := av.OpenEncoder(opts)
		if err != nil {
			return nil, fmt.Errorf("open trial boundary encoder: %w", err)
		}
		ed := enc.ExtraData()
		enc.Close()
		encSPS, encPPS = nil, nil
		for _, n := range splitAnnexB(ed) {
			switch h264NALType(n) {
			case h264NALSPS:
				encSPS = append(encSPS, n)
			case h264NALPPS:
				encPPS = append(encPPS, n)
			}
		}
		encState, err := newH264POCState(ed, 0)
		if err != nil {
			return nil, fmt.Errorf("boundary encoder parameter sets: %w", err)
		}
		encHdr = encState.firstSPS()
		plan.encOpts.ExtraOpts = opts.ExtraOpts
		plan.encOpts.GlobalHeader = plan.mode == smartCopyModeConfigPS
		if encHdr == nil {
			break
		}
		refsOK := srcRefs == 0 || int(encHdr.MaxNumRefFrames) <= srcRefs
		pocOK := srcPocType != 0 || encHdr.PicOrderCntType != 0 ||
			int32(1)<<(encHdr.Log2MaxPicOrderCntLsbMinus4+4) <= plan.srcMaxLsb
		if refsOK && pocOK {
			if len(extra) > 0 {
				plan.notes = append(plan.notes, "boundary x264 layout tightened ("+strings.Join(extra, ", ")+") to fit the source's reference and POC limits")
			}
			break
		}
	}
	if encHdr == nil {
		return nil, fmt.Errorf("boundary encoder %s produced no SPS", plan.encOpts.CodecName)
	}
	plan.pocFix = srcPocType == 0 && encHdr.PicOrderCntType == 0 &&
		int32(1)<<(encHdr.Log2MaxPicOrderCntLsbMinus4+4) <= plan.srcMaxLsb
	if srcRefs > 0 && int(encHdr.MaxNumRefFrames) > srcRefs {
		plan.notes = append(plan.notes, fmt.Sprintf("boundary encoder keeps %d reference frames, source %d; decoders may trim the reference buffer at the head cut", encHdr.MaxNumRefFrames, srcRefs))
	}
	switch {
	case srcPocType == 0 && !plan.pocFix:
		plan.notes = append(plan.notes, "open-GOP H.264 splices cannot be renumbered: boundary encoder POC layout is incompatible with the source's (output order may be wrong at the head cut)")
	case srcPocType == 1:
		plan.notes = append(plan.notes, "source uses pic_order_cnt_type 1; open-GOP H.264 head splices are not renumbered")
	}

	if plan.mode == smartCopyModeConfigPS {
		plan.encParamSets = append(append([][]byte{}, encSPS...), encPPS...)
		merged, err := avcCWithParamSets(extradata, encSPS, encPPS)
		if err != nil {
			return nil, fmt.Errorf("merge boundary parameter sets into avcC: %w", err)
		}
		plan.extradata = merged
	}
	return plan, nil
}

// mergeCodecParams returns a copy of extra with the given key=value pairs
// appended to its colon-separated codec parameter list under key
// ("x264-params" / "x265-params"); later entries override earlier ones in
// the wrappers' parse loops.
func mergeCodecParams(extra map[string]string, key string, kv []string) map[string]string {
	out := make(map[string]string, len(extra)+1)
	for k, v := range extra {
		out[k] = v
	}
	if len(kv) == 0 {
		return out
	}
	parts := []string{}
	if cur := strings.TrimSpace(out[key]); cur != "" {
		parts = append(parts, cur)
	}
	parts = append(parts, kv...)
	out[key] = strings.Join(parts, ":")
	return out
}

func mergeX264Params(extra map[string]string, kv []string) map[string]string {
	return mergeCodecParams(extra, "x264-params", kv)
}

// isISOBMFFMuxer reports whether the muxer writes MP4/MOV-family sample
// entries, where the avc3 / hev1 tags select in-band parameter sets.
func isISOBMFFMuxer(name string) bool {
	switch name {
	case "mov", "mp4", "ipod", "psp", "3gp", "3g2", "f4v", "ismv":
		return true
	}
	return false
}

// applySmartCopyStreamParams adjusts an output stream registered from a
// smartcopy node's source: the merged avcC (config mode) and the in-band
// sample entry (in-band mode on ISOBMFF). Called after AddStreamFromInput
// and before any user codec_tag override, which wins.
func (r *graphRunner) applySmartCopyStreamParams(muxer *av.OutputFormatContext, outIdx int, node *graph.Node) error {
	plan, err := r.smartCopyPlanFor(node)
	if err != nil || plan == nil {
		return err
	}
	if plan.extradata != nil {
		if err := muxer.SetStreamExtradata(outIdx, plan.extradata); err != nil {
			return err
		}
	}
	if plan.codecTag != "" && isISOBMFFMuxer(muxer.FormatName()) {
		if err := muxer.SetStreamCodecTag(outIdx, plan.codecTag); err != nil {
			return err
		}
	}
	return nil
}
