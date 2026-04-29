// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"strings"
)

// validColorRangeNames mirrors libavutil/pixdesc.c::av_color_range_name
// minus AVCOL_RANGE_UNSPECIFIED, which the validator forbids (callers
// who want "leave alone" should leave the field empty instead). The
// authoritative parser is av_color_range_from_name; this duplication
// only exists so the schema-time validator does not need to call into
// cgo on every parse. Keep in sync with libavutil/pixdesc.c.
var validColorRangeNames = map[string]bool{
	"tv":      true,
	"mpeg":    true,
	"limited": true,
	"pc":      true,
	"jpeg":    true,
	"full":    true,
}

// validColorPrimariesNames mirrors libavutil/pixdesc.c::color_primaries_names.
var validColorPrimariesNames = map[string]bool{
	"bt709":      true,
	"bt470m":     true,
	"bt470bg":    true,
	"smpte170m":  true,
	"smpte240m":  true,
	"film":       true,
	"bt2020":     true,
	"smpte428":   true,
	"smpte428_1": true,
	"smpte431":   true,
	"smpte432":   true,
	"ebu3213":    true,
	"jedec-p22":  true,
}

// validColorTransferNames mirrors libavutil/pixdesc.c::color_transfer_names.
var validColorTransferNames = map[string]bool{
	"bt709":        true,
	"gamma22":      true,
	"gamma28":      true,
	"smpte170m":    true,
	"smpte240m":    true,
	"linear":       true,
	"log100":       true,
	"log316":       true,
	"iec61966-2-4": true,
	"bt1361e":      true,
	"iec61966-2-1": true,
	"bt2020-10":    true,
	"bt2020-12":    true,
	"smpte2084":    true,
	"smpte428":     true,
	"smpte428_1":   true,
	"arib-std-b67": true,
}

// validColorSpaceNames mirrors libavutil/pixdesc.c::color_space_names.
var validColorSpaceNames = map[string]bool{
	"rgb":               true,
	"bt709":             true,
	"fcc":               true,
	"bt470bg":           true,
	"smpte170m":         true,
	"smpte240m":         true,
	"ycgco":             true,
	"ycgco-re":          true,
	"ycgco-ro":          true,
	"bt2020nc":          true,
	"bt2020c":           true,
	"smpte2085":         true,
	"chroma-derived-nc": true,
	"chroma-derived-c":  true,
	"ictcp":             true,
	"ipt-c2":            true,
	"ycgco-re2":         true,
}

// validChromaLocationNames mirrors libavutil/pixdesc.c::chroma_location_names.
var validChromaLocationNames = map[string]bool{
	"left":       true,
	"center":     true,
	"topleft":    true,
	"top":        true,
	"bottomleft": true,
	"bottom":     true,
}

// hdrCapableCodecs is the closed set of video codecs whose bitstreams
// carry the SEI / OBU containers needed to express HDR10 mastering
// display + content-light-level metadata. Other codecs (mpeg4, h263,
// vp8, mjpeg, ...) cannot signal HDR10 and a configuration that pairs
// them with `Output.HDR` is rejected at validate time.
var hdrCapableCodecs = map[string]bool{
	"hevc":       true,
	"libx265":    true,
	"av1":        true,
	"libsvtav1":  true,
	"libaom-av1": true,
	"vp9":        true,
	"libvpx-vp9": true,
}

// hdrCapableContainers is the closed set of muxers known to write the
// HDR10 side-data through to the produced bitstream (mp4/mov: `mdcv`/
// `clli` boxes; matroska/webm: MasteringMetadata + MaxCLL/MaxFALL;
// mpegts: SEI passthrough). AVI / FLV / WAV / MP3 / MPEG-PS strip
// the side data so the validator rejects HDR + those containers.
var hdrCapableContainers = map[string]bool{
	"mp4":      true,
	"mov":      true,
	"matroska": true,
	"mkv":      true,
	"webm":     true,
	"mpegts":   true,
}

// validateColorHDR enforces per-output Color / HDR rules at schema
// time. Empty Color/HDR blocks are no-ops. Unknown enum names, missing
// HDR transfer (must be PQ / HLG), missing video stream, non-HDR-
// capable codec, or non-HDR-capable container produce an explicit
// error so misconfiguration never produces a silently-stripped output.
func validateColorHDR(out Output) error {
	if out.Color != nil {
		c := out.Color
		if c.Range != "" && !validColorRangeNames[c.Range] {
			return fmt.Errorf("output %q: color.range %q is not a recognised av_color_range name", out.ID, c.Range)
		}
		if c.Primaries != "" && !validColorPrimariesNames[c.Primaries] {
			return fmt.Errorf("output %q: color.primaries %q is not a recognised av_color_primaries name", out.ID, c.Primaries)
		}
		if c.Transfer != "" && !validColorTransferNames[c.Transfer] {
			return fmt.Errorf("output %q: color.transfer %q is not a recognised av_color_transfer name", out.ID, c.Transfer)
		}
		if c.Space != "" && !validColorSpaceNames[c.Space] {
			return fmt.Errorf("output %q: color.space %q is not a recognised av_color_space name", out.ID, c.Space)
		}
		if c.ChromaLocation != "" && !validChromaLocationNames[c.ChromaLocation] {
			return fmt.Errorf("output %q: color.chroma_location %q is not a recognised av_chroma_location name", out.ID, c.ChromaLocation)
		}
	}
	if out.HDR == nil {
		return nil
	}
	// HDR requires a video stream.
	if out.CodecVideo == "" || strings.EqualFold(out.CodecVideo, "none") {
		return fmt.Errorf("output %q: hdr requires a video stream (codec_video unset)", out.ID)
	}
	codec := strings.ToLower(out.CodecVideo)
	if codec != "copy" && !hdrCapableCodecs[codec] {
		return fmt.Errorf("output %q: hdr requires an HDR-capable video codec (have codec_video=%q; want hevc, av1, vp9, or stream-copy)", out.ID, out.CodecVideo)
	}
	// Container must carry the side data through.
	if out.Format != "" && !hdrCapableContainers[strings.ToLower(out.Format)] {
		return fmt.Errorf("output %q: hdr requires a container that carries SMPTE ST 2086 metadata (have format=%q; want mp4, mov, matroska, webm, or mpegts)", out.ID, out.Format)
	}
	// HDR transfer must be PQ or HLG when Color is provided. (Color
	// itself is optional — bare mastering display + CLL on a Rec.709
	// transfer is technically possible but almost always a mistake, so
	// require an explicit HDR transfer when Color is set.)
	if out.Color != nil && out.Color.Transfer != "" {
		switch out.Color.Transfer {
		case "smpte2084", "arib-std-b67":
		default:
			return fmt.Errorf("output %q: hdr requires color.transfer=\"smpte2084\" (PQ) or \"arib-std-b67\" (HLG); have %q", out.ID, out.Color.Transfer)
		}
	}
	if md := out.HDR.MasteringDisplay; md != nil {
		// Primaries are all-or-nothing.
		primSet := md.DisplayPrimariesRX != 0 || md.DisplayPrimariesRY != 0 ||
			md.DisplayPrimariesGX != 0 || md.DisplayPrimariesGY != 0 ||
			md.DisplayPrimariesBX != 0 || md.DisplayPrimariesBY != 0 ||
			md.WhitePointX != 0 || md.WhitePointY != 0
		if primSet {
			if md.DisplayPrimariesRX == 0 || md.DisplayPrimariesRY == 0 ||
				md.DisplayPrimariesGX == 0 || md.DisplayPrimariesGY == 0 ||
				md.DisplayPrimariesBX == 0 || md.DisplayPrimariesBY == 0 ||
				md.WhitePointX == 0 || md.WhitePointY == 0 {
				return fmt.Errorf("output %q: hdr.mastering_display primaries + white_point must all be set together", out.ID)
			}
		}
		if md.MinLuminance != 0 || md.MaxLuminance != 0 {
			if md.MaxLuminance <= 0 {
				return fmt.Errorf("output %q: hdr.mastering_display.max_luminance must be > 0 when set", out.ID)
			}
			if md.MinLuminance < 0 {
				return fmt.Errorf("output %q: hdr.mastering_display.min_luminance must be >= 0", out.ID)
			}
			if md.MinLuminance >= md.MaxLuminance {
				return fmt.Errorf("output %q: hdr.mastering_display.min_luminance (%d) must be < max_luminance (%d)", out.ID, md.MinLuminance, md.MaxLuminance)
			}
		}
	}
	if cll := out.HDR.ContentLightLevel; cll != nil {
		if cll.MaxFALL > cll.MaxCLL && cll.MaxCLL != 0 {
			return fmt.Errorf("output %q: hdr.content_light_level.max_fall (%d) must be <= max_cll (%d)", out.ID, cll.MaxFALL, cll.MaxCLL)
		}
	}
	return nil
}
