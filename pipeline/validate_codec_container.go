// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"path/filepath"
	"strings"
)

// ---------- Codec / container compatibility ----------

// containerVideoCodecs maps normalised container format names to allowed video
// codecs. A nil value means the container accepts any codec (e.g. MKV).
var containerVideoCodecs = map[string]map[string]bool{
	"mp4": {
		"h264": true, "hevc": true, "h265": true, "av1": true,
		"libx264": true, "libx265": true, "libsvtav1": true,
		"mpeg4": true, "mjpeg": true,
		"h264_nvenc": true, "hevc_nvenc": true, "av1_nvenc": true,
		"h264_videotoolbox": true, "hevc_videotoolbox": true,
		"h264_vaapi": true, "hevc_vaapi": true, "av1_vaapi": true,
		"h264_amf": true, "hevc_amf": true,
	},
	"mov": {
		"h264": true, "hevc": true, "h265": true, "av1": true,
		"libx264": true, "libx265": true, "libsvtav1": true,
		"mpeg4": true, "prores": true, "prores_ks": true, "mjpeg": true,
		"h264_nvenc": true, "hevc_nvenc": true,
		"h264_videotoolbox": true, "hevc_videotoolbox": true,
		"h264_vaapi": true, "hevc_vaapi": true,
		"h264_amf": true, "hevc_amf": true,
	},
	"mkv":      nil, // MKV accepts virtually all codecs
	"matroska": nil,
	"webm":     {"libvpx": true, "libvpx-vp8": true, "libvpx-vp9": true, "av1": true, "libaom-av1": true, "libsvtav1": true},
	"mpegts":   {"h264": true, "hevc": true, "h265": true, "libx264": true, "libx265": true, "mpeg2video": true, "mpeg4": true, "h264_nvenc": true, "hevc_nvenc": true, "h264_videotoolbox": true, "hevc_videotoolbox": true},
	"ts":       {"h264": true, "hevc": true, "h265": true, "libx264": true, "libx265": true, "mpeg2video": true, "mpeg4": true, "h264_nvenc": true, "hevc_nvenc": true, "h264_videotoolbox": true, "hevc_videotoolbox": true},
	"hls":      {"h264": true, "hevc": true, "libx264": true, "libx265": true, "h264_nvenc": true, "hevc_nvenc": true, "h264_videotoolbox": true, "hevc_videotoolbox": true, "h264_vaapi": true, "hevc_vaapi": true},
	"dash":     {"h264": true, "hevc": true, "libx264": true, "libx265": true, "libvpx-vp9": true, "av1": true, "libaom-av1": true, "libsvtav1": true},
	"avi":      {"mpeg4": true, "h264": true, "libx264": true, "mpeg2video": true, "mjpeg": true},
	"flv":      {"h264": true, "libx264": true, "mpeg4": true, "h264_nvenc": true, "h264_videotoolbox": true},
}

// containerAudioCodecs maps container format names to allowed audio codecs.
var containerAudioCodecs = map[string]map[string]bool{
	"mp4": {
		"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true,
		"ac3": true, "eac3": true, "alac": true,
		"opus": true, "libopus": true,
	},
	"mov": {
		"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true,
		"ac3": true, "eac3": true, "alac": true,
		"pcm_s16le": true, "pcm_s24le": true, "pcm_s32le": true,
	},
	"mkv":      nil,
	"matroska": nil,
	"webm":     {"libopus": true, "opus": true, "libvorbis": true, "vorbis": true},
	"mpegts":   {"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true, "ac3": true, "eac3": true, "opus": true, "libopus": true},
	"ts":       {"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true, "ac3": true, "eac3": true, "opus": true, "libopus": true},
	"hls":      {"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true, "ac3": true},
	"dash":     {"aac": true, "libfdk_aac": true, "libopus": true, "opus": true, "libvorbis": true},
	"avi":      {"mp3": true, "libmp3lame": true, "aac": true, "pcm_s16le": true, "ac3": true},
	"flv":      {"aac": true, "mp3": true, "libmp3lame": true, "libfdk_aac": true},
}

// pcmAudioCodecs is the set of uncompressed PCM audio codecs.
var pcmAudioCodecs = map[string]bool{
	"pcm_s16le": true, "pcm_s24le": true, "pcm_s32le": true,
	"pcm_u8": true, "pcm_s8": true,
	"pcm_s16be": true, "pcm_s24be": true, "pcm_s32be": true,
	"pcm_f32le": true, "pcm_f64le": true,
}

// bsfRequirement records when a BSF is expected for a codec+container pairing.
// BSF requirements only apply to stream-copy paths; for freshly encoded streams
// the muxer typically inserts the needed conversion automatically. These entries
// are emitted as WARNING rather than ERROR.
type bsfRequirement struct {
	container string
	codec     string
	bsf       string
}

var videoBSFRequirements = []bsfRequirement{
	{"mp4", "h264", "h264_mp4toannexb"},
	{"mov", "h264", "h264_mp4toannexb"},
	{"mkv", "h264", "h264_mp4toannexb"},
	{"matroska", "h264", "h264_mp4toannexb"},
	{"mp4", "hevc", "hevc_mp4toannexb"},
	{"mov", "hevc", "hevc_mp4toannexb"},
	{"mkv", "hevc", "hevc_mp4toannexb"},
	{"matroska", "hevc", "hevc_mp4toannexb"},
	{"mpegts", "hevc", "hevc_mp4toannexb"},
	{"ts", "hevc", "hevc_mp4toannexb"},
}

var audioBSFRequirements = []bsfRequirement{
	{"mp4", "aac", "aac_adtstoasc"},
	{"mov", "aac", "aac_adtstoasc"},
	{"mkv", "aac", "aac_adtstoasc"},
	{"matroska", "aac", "aac_adtstoasc"},
	{"mp4", "dts", "dca_core"},
}

// validateCodecContainer checks codec/container compatibility for every output.
func validateCodecContainer(cfg *Config, r *ValidationReport) {
	for _, out := range cfg.Outputs {
		container := inferContainer(out)
		if container == "" {
			continue
		}

		if out.CodecVideo != "" && out.CodecVideo != "copy" {
			checkVideoCodecCompat(out, container, r)
		}
		if out.CodecAudio != "" && out.CodecAudio != "copy" {
			checkAudioCodecCompat(out, container, r)
			checkPCMInMP4(out, container, r)
			checkOpusInMP4(out, container, r)
		}
		if out.CodecVideo == "copy" || out.CodecAudio == "copy" {
			checkStreamCopyBSF(out, container, r)
		}
		checkHLSCodecs(out, container, r)
		checkDASHMovflags(out, container, r)
		checkHEVCTagInMP4(out, container, r)
	}
}

func checkVideoCodecCompat(out Output, container string, r *ValidationReport) {
	allowed, known := containerVideoCodecs[container]
	if !known || allowed == nil {
		return // unknown or permissive container
	}
	if !allowed[out.CodecVideo] {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "CONTAINER_CODEC_UNSUPPORTED",
			Location:   "output:" + out.ID,
			Message:    fmt.Sprintf("video codec %q is not supported in %q container", out.CodecVideo, container),
			Suggestion: "choose a codec supported by the target container, or change the output container format",
		})
	}
}

func checkAudioCodecCompat(out Output, container string, r *ValidationReport) {
	allowed, known := containerAudioCodecs[container]
	if !known || allowed == nil {
		return
	}
	if !allowed[out.CodecAudio] {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "CONTAINER_CODEC_UNSUPPORTED",
			Location:   "output:" + out.ID,
			Message:    fmt.Sprintf("audio codec %q is not supported in %q container", out.CodecAudio, container),
			Suggestion: "choose a codec supported by the target container, or change the output container format",
		})
	}
}

func checkPCMInMP4(out Output, container string, r *ValidationReport) {
	if container != "mp4" {
		return
	}
	if pcmAudioCodecs[out.CodecAudio] {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "CONTAINER_PCM_IN_MP4",
			Location:   "output:" + out.ID,
			Message:    fmt.Sprintf("PCM audio codec %q is not supported in MP4 container", out.CodecAudio),
			Suggestion: "use AAC or another MP4-compatible audio codec; consider using MOV if uncompressed audio is required",
		})
	}
}

func checkOpusInMP4(out Output, container string, r *ValidationReport) {
	if container != "mp4" {
		return
	}
	if out.CodecAudio == "opus" || out.CodecAudio == "libopus" {
		r.add(ValidationIssue{
			Severity:   SeverityWarning,
			Code:       "CONTAINER_OPUS_IN_MP4",
			Location:   "output:" + out.ID,
			Message:    "Opus audio in MP4 has limited player support",
			Suggestion: "use MKV or WebM for maximum Opus compatibility",
		})
	}
}

// checkStreamCopyBSF warns when a stream-copy codec+container pairing typically
// requires a BSF that is not set.
func checkStreamCopyBSF(out Output, container string, r *ValidationReport) {
	if out.CodecVideo == "copy" {
		for _, req := range videoBSFRequirements {
			if req.container != container {
				continue
			}
			// Without probe data we cannot confirm the source codec; emit INFO.
			if !strings.Contains(out.BSFVideo, req.bsf) {
				r.add(ValidationIssue{
					Severity: SeverityWarning,
					Code:     "CONTAINER_BSF_REQUIRED",
					Location: "output:" + out.ID,
					Message: fmt.Sprintf(
						"stream-copying video into %q may require BSF %q if the source uses Annex B format",
						container, req.bsf),
					Suggestion: fmt.Sprintf(`add "bsf_video": "%s" to this output if the source codec is %q`, req.bsf, req.codec),
				})
				break // one warning per output is enough
			}
		}
	}
	if out.CodecAudio == "copy" {
		for _, req := range audioBSFRequirements {
			if req.container != container {
				continue
			}
			if !strings.Contains(out.BSFAudio, req.bsf) {
				r.add(ValidationIssue{
					Severity: SeverityWarning,
					Code:     "CONTAINER_BSF_REQUIRED",
					Location: "output:" + out.ID,
					Message: fmt.Sprintf(
						"stream-copying audio into %q may require BSF %q if the source uses ADTS format",
						container, req.bsf),
					Suggestion: fmt.Sprintf(`add "bsf_audio": "%s" to this output if the source codec is %q`, req.bsf, req.codec),
				})
				break
			}
		}
	}
}

func checkHLSCodecs(out Output, container string, r *ValidationReport) {
	if container != "hls" {
		return
	}
	hlsVideo := map[string]bool{
		"h264": true, "libx264": true, "hevc": true, "libx265": true,
		"h264_nvenc": true, "hevc_nvenc": true,
		"h264_videotoolbox": true, "hevc_videotoolbox": true,
		"h264_vaapi": true, "hevc_vaapi": true,
	}
	hlsAudio := map[string]bool{
		"aac": true, "libfdk_aac": true, "mp3": true, "libmp3lame": true, "ac3": true,
	}
	if out.CodecVideo != "" && out.CodecVideo != "copy" && !hlsVideo[out.CodecVideo] {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "CONTAINER_HLS_CODEC",
			Location:   "output:" + out.ID,
			Message:    fmt.Sprintf("HLS requires H.264 or H.265 video but %q is configured", out.CodecVideo),
			Suggestion: "use libx264 (H.264) or libx265 (H.265/HEVC) for HLS output",
		})
	}
	if out.CodecAudio != "" && out.CodecAudio != "copy" && !hlsAudio[out.CodecAudio] {
		r.add(ValidationIssue{
			Severity:   SeverityError,
			Code:       "CONTAINER_HLS_CODEC",
			Location:   "output:" + out.ID,
			Message:    fmt.Sprintf("HLS requires AAC or MP3 audio but %q is configured", out.CodecAudio),
			Suggestion: "use aac for maximum HLS compatibility",
		})
	}
}

func checkDASHMovflags(out Output, container string, r *ValidationReport) {
	if container != "dash" {
		return
	}
	movflags, _ := out.Options["movflags"].(string)
	hasFragKF := strings.Contains(movflags, "frag_keyframe")
	hasEmptyMoov := strings.Contains(movflags, "empty_moov")
	if !hasFragKF || !hasEmptyMoov {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "CONTAINER_DASH_NO_FRAGMENTED",
			Location: "output:" + out.ID,
			Message: fmt.Sprintf(
				"DASH output requires movflags=frag_keyframe+empty_moov (got %q)", movflags),
			Suggestion: `add "options": {"movflags": "frag_keyframe+empty_moov"} to this output`,
		})
	}
}

func checkHEVCTagInMP4(out Output, container string, r *ValidationReport) {
	if container != "mp4" {
		return
	}
	codec := out.CodecVideo
	if codec != "hevc" && codec != "libx265" && !strings.HasPrefix(codec, "hevc_") {
		return
	}
	tagV, _ := out.Options["tag:v"].(string)
	if tagV != "hvc1" {
		r.add(ValidationIssue{
			Severity:   SeverityWarning,
			Code:       "CONTAINER_HEVC_TAG_MISSING",
			Location:   "output:" + out.ID,
			Message:    "HEVC video in MP4 without tag:v=hvc1; some Apple devices will refuse to play it",
			Suggestion: `add "options": {"tag:v": "hvc1"} to this output`,
		})
	}
}

// inferContainer determines the output container format from Output.Format or
// the URL file extension.
func inferContainer(out Output) string {
	if out.Format != "" {
		return strings.ToLower(out.Format)
	}
	ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(out.URL), "."))
	switch ext {
	case "mp4", "m4v", "m4a":
		return "mp4"
	case "mov":
		return "mov"
	case "mkv":
		return "mkv"
	case "webm":
		return "webm"
	case "ts", "mts", "m2ts":
		return "mpegts"
	case "flv":
		return "flv"
	case "avi":
		return "avi"
	case "m3u8":
		return "hls"
	case "mpd":
		return "dash"
	default:
		return ext
	}
}

// ---------- Two-pass encoding consistency ----------

// validateTwoPass checks that every pass=2 encoder node has a corresponding
// pass=1 node using the same passlogfile and codec.
func validateTwoPass(cfg *Config, r *ValidationReport) {
	type passEntry struct {
		nodeID  string
		codec   string
		logfile string
	}
	pass1 := make(map[string]passEntry) // keyed by logfile
	pass2 := make(map[string]passEntry)

	for _, nd := range cfg.Graph.Nodes {
		pass := paramToInt(nd.Params["pass"])
		if pass != 1 && pass != 2 {
			continue
		}
		logfile := nodeParamString(nd, "passlogfile")
		if logfile == "" {
			logfile = "ffmpeg2pass" // FFmpeg default
		}
		codec := nodeParamString(nd, "codec")
		entry := passEntry{nodeID: nd.ID, codec: codec, logfile: logfile}
		switch pass {
		case 1:
			pass1[logfile] = entry
		case 2:
			pass2[logfile] = entry
		}
	}

	for logfile, p2 := range pass2 {
		p1, ok := pass1[logfile]
		if !ok {
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       "TWOPASS_MISSING_PASS1",
				Location:   "node:" + p2.nodeID,
				Message:    fmt.Sprintf("node %q specifies pass=2 but no pass=1 node with passlogfile=%q exists", p2.nodeID, logfile),
				Suggestion: "add a corresponding pass=1 encoder node targeting the same passlogfile",
			})
			continue
		}
		if p1.codec != p2.codec && p1.codec != "" && p2.codec != "" {
			r.add(ValidationIssue{
				Severity: SeverityError,
				Code:     "TWOPASS_CODEC_MISMATCH",
				Location: fmt.Sprintf("node:%s,node:%s", p1.nodeID, p2.nodeID),
				Message: fmt.Sprintf(
					"pass 1 uses codec %q but pass 2 uses codec %q (passlogfile=%q); both passes must use the same codec",
					p1.codec, p2.codec, logfile),
				Suggestion: "ensure pass 1 and pass 2 encoder nodes specify the same codec",
			})
		}
	}
}
