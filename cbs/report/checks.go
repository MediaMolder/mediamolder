// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Layer-1 stream-structure checks: named, opt-in inspections over the
// decode-order unit walk the summarizer already performs. They are
// deliberately a curated set — many perfectly legal streams fail the
// strict ones (no AUD, parameter sets only in extradata), which is why
// nothing here is always-on.

package report

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// Check ids. Presets: "default" = the ingest-QC set that only flags
// broken streams; "strict" = default plus conventions.
const (
	checkVCLBeforePS = "vcl_before_ps" // picture data before any parameter sets (default)
	checkFrameNumGap = "frame_num_gap" // H.264 frame_num gap with gaps disallowed (default)
	checkNoAUD       = "no_aud"        // packet with VCL but no AUD (strict)
	checkSEIAfterVCL = "sei_after_vcl" // prefix SEI after the first VCL of a packet (strict)
)

var checkPresets = map[string][]string{
	"default": {checkVCLBeforePS, checkFrameNumGap},
	"strict":  {checkVCLBeforePS, checkFrameNumGap, checkNoAUD, checkSEIAfterVCL},
}

// ResolveChecks expands presets and validates check ids.
func ResolveChecks(spec []string) ([]string, error) {
	var out []string
	seen := map[string]bool{}
	add := func(id string) {
		if !seen[id] {
			seen[id] = true
			out = append(out, id)
		}
	}
	for _, s := range spec {
		if preset, ok := checkPresets[s]; ok {
			for _, id := range preset {
				add(id)
			}
			continue
		}
		switch s {
		case checkVCLBeforePS, checkFrameNumGap, checkNoAUD, checkSEIAfterVCL:
			add(s)
		default:
			return nil, fmt.Errorf("report: unknown check %q (presets: default, strict; ids: %s, %s, %s, %s)",
				s, checkVCLBeforePS, checkFrameNumGap, checkNoAUD, checkSEIAfterVCL)
		}
	}
	return out, nil
}

// checker runs the enabled structure checks; writers feed it every unit
// in decode order (filters do not apply) plus packet boundaries.
type checker struct {
	enabled map[string]bool
	codec   string
	sum     *summarizer
	vlog    *violationLog

	// vcl_before_ps: emitted once, until parameter sets appear.
	reportedNoPS bool

	// frame_num_gap state (H.264).
	havePrevRef bool
	prevRefFN   int32

	// per-packet state (no_aud, sei_after_vcl).
	pktIndex   int64
	sawAUD     bool
	sawVCL     bool
	reportedCC map[string]bool // per-packet dedupe
}

func newChecker(spec []string, codec string, sum *summarizer, vlog *violationLog) *checker {
	if len(spec) == 0 {
		return nil
	}
	c := &checker{enabled: map[string]bool{}, codec: codec, sum: sum, vlog: vlog}
	for _, id := range spec {
		c.enabled[id] = true
	}
	return c
}

func (c *checker) violation(severity, check string, packet int64, unit int, format string, args ...any) {
	c.vlog.add(Violation{
		Severity: severity,
		Kind:     "structure",
		Check:    check,
		Spec:     specName(c.codec),
		Packet:   packet,
		Unit:     unit,
		Message:  fmt.Sprintf(format, args...),
	})
}

func (c *checker) beginPacket(index int64) {
	if c == nil {
		return
	}
	c.pktIndex = index
	c.sawAUD = false
	c.sawVCL = false
	c.reportedCC = nil
}

func (c *checker) endPacket() {
	if c == nil {
		return
	}
	if c.enabled[checkNoAUD] && c.sawVCL && !c.sawAUD &&
		(c.codec == "h264" || c.codec == "hevc" || c.codec == "h265") {
		c.violation("warning", checkNoAUD, c.pktIndex, -1,
			"packet carries picture data but no access unit delimiter")
	}
}

func (c *checker) once(key string) bool {
	if c.reportedCC == nil {
		c.reportedCC = map[string]bool{}
	}
	if c.reportedCC[key] {
		return false
	}
	c.reportedCC[key] = true
	return true
}

// observe runs after summarizer.advance for every unit, in decode order.
// packet is -1 for extradata.
func (c *checker) observe(packet int64, unit int, u *cbs.Unit, pic *pictureJSON) {
	if c == nil {
		return
	}
	class := classify(c.codec, u)
	isAUD := (c.codec == "h264" && u.Type == 9) ||
		((c.codec == "hevc" || c.codec == "h265") && u.Type == 35)
	isPrefixSEI := (c.codec == "h264" && u.Type == 6) ||
		((c.codec == "hevc" || c.codec == "h265") && u.Type == 39)

	if isAUD {
		c.sawAUD = true
	}

	if c.enabled[checkSEIAfterVCL] && isPrefixSEI && c.sawVCL && c.once("sei_after_vcl") {
		c.violation("warning", checkSEIAfterVCL, packet, unit,
			"prefix SEI after the first VCL unit of the access unit")
	}

	if class == classVCL {
		c.sawVCL = true

		if c.enabled[checkVCLBeforePS] && !c.reportedNoPS && !c.paramSetsSeen() {
			c.reportedNoPS = true
			c.violation("error", checkVCLBeforePS, packet, unit,
				"picture data before any parameter sets (extradata or in-band)")
		}

		if c.enabled[checkFrameNumGap] && c.codec == "h264" && pic != nil && pic.FrameNum != nil {
			c.checkFrameNum(packet, unit, u, pic)
		}
	}
}

func (c *checker) paramSetsSeen() bool {
	switch c.codec {
	case "h264":
		return len(c.sum.poc.h264SPS) > 0 && len(c.sum.poc.h264PPS) > 0
	case "hevc", "h265":
		return len(c.sum.poc.h265SPS) > 0 && len(c.sum.poc.h265PPS) > 0
	}
	// AV1: a frame without a sequence header in force is a parse error
	// already (layer 0).
	return true
}

// checkFrameNum flags H.264 frame_num gaps when the active SPS forbids
// them (§7.4.3: frame_num of a non-IDR picture is prev or prev+1 mod
// MaxFrameNum unless gaps_in_frame_num_allowed_flag).
func (c *checker) checkFrameNum(packet int64, unit int, u *cbs.Unit, pic *pictureJSON) {
	slice, ok := u.Content.(*cbs.H264RawSlice)
	if !ok {
		return
	}
	sh := &slice.Header
	pps := c.sum.poc.h264PPS[sh.PicParameterSetID]
	if pps == nil {
		return
	}
	sps := c.sum.poc.h264SPS[pps.SeqParameterSetID]
	if sps == nil {
		return
	}
	fn := int32(*pic.FrameNum)
	idr := sh.NalUnitHeader.NalUnitType == 5

	if !idr && c.havePrevRef && sps.GapsInFrameNumAllowedFlag == 0 {
		maxFN := int32(1) << (sps.Log2MaxFrameNumMinus4 + 4)
		if fn != c.prevRefFN && fn != (c.prevRefFN+1)%maxFN && c.once("frame_num_gap") {
			c.violation("error", checkFrameNumGap, packet, unit,
				"frame_num gap: %d after reference frame_num %d (gaps_in_frame_num_allowed_flag is 0)",
				fn, c.prevRefFN)
		}
	}
	if idr {
		c.havePrevRef, c.prevRefFN = true, fn
	} else if sh.NalUnitHeader.NalRefIdc != 0 {
		c.havePrevRef, c.prevRefFN = true, fn
	}
}
