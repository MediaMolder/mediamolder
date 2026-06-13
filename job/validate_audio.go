// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"strings"

	"github.com/MediaMolder/MediaMolder/graph"
)

// audioEncoderSampleFmts maps audio encoder names to their required sample
// formats. When an encoder node explicitly sets sample_fmt to a value not in
// this list, AUDIO_SAMPLE_FMT_MISMATCH is reported.
var audioEncoderSampleFmts = map[string][]string{
	"aac":        {"fltp"},
	"libfdk_aac": {"s16"},
	"mp3":        {"s16p", "fltp"},
	"libmp3lame": {"s16p", "fltp"},
	"opus":       {"s16", "flt"},
	"libopus":    {"s16", "flt"},
	"flac":       {"s16", "s32"},
	"pcm_s16le":  {"s16"},
	"vorbis":     {"fltp"},
	"libvorbis":  {"fltp"},
	"ac3":        {"fltp"},
	"eac3":       {"fltp"},
}

// audioEncoderAllowedRates maps audio encoder names to allowed sample rates.
// An empty or absent entry means any rate is accepted by the encoder.
var audioEncoderAllowedRates = map[string][]int{
	"aac": {
		8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 96000,
	},
	"libfdk_aac": {
		8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000, 96000,
	},
	"mp3": {
		8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000,
	},
	"libmp3lame": {
		8000, 11025, 12000, 16000, 22050, 24000, 32000, 44100, 48000,
	},
}

// validateAudio performs static audio format checks that do not require probe
// data. Issues are only reported when both the encoder and the explicit param
// are set in the config (no probing required).
func validateAudio(cfg *Config, _ *graph.Graph, r *ValidationReport) {
	for _, nd := range cfg.Graph.Nodes {
		if nd.Type != "encoder" {
			continue
		}
		codec := nodeParamString(nd, "codec")
		if codec == "" {
			continue
		}
		checkAudioSampleFmt(nd, codec, r)
		checkAudioSampleRate(nd, codec, r)
	}
}

func checkAudioSampleFmt(nd NodeDef, codec string, r *ValidationReport) {
	required, known := audioEncoderSampleFmts[codec]
	if !known {
		return
	}
	fmtVal, ok := nd.Params["sample_fmt"]
	if !ok {
		return
	}
	sampleFmt := strings.TrimSpace(fmt.Sprintf("%v", fmtVal))
	if sampleFmt == "" {
		return
	}
	if !containsStr(required, sampleFmt) {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "AUDIO_SAMPLE_FMT_MISMATCH",
			Location: "node:" + nd.ID,
			Message: fmt.Sprintf(
				"encoder %q requires sample_fmt %v but node %q specifies sample_fmt=%q",
				codec, required, nd.ID, sampleFmt,
			),
			Suggestion: fmt.Sprintf(
				"remove the sample_fmt override or add an aformat=sample_fmts=%s filter before this encoder",
				required[0],
			),
		})
	}
}

func checkAudioSampleRate(nd NodeDef, codec string, r *ValidationReport) {
	allowed, known := audioEncoderAllowedRates[codec]
	if !known || len(allowed) == 0 {
		return
	}
	rateVal, ok := nd.Params["sample_rate"]
	if !ok {
		return
	}
	rate := paramToInt(rateVal)
	if rate <= 0 {
		return
	}
	if !containsInt(allowed, rate) {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "AUDIO_SAMPLE_RATE_MISMATCH",
			Location: "node:" + nd.ID,
			Message: fmt.Sprintf(
				"encoder %q does not support sample_rate=%d (allowed: %v)",
				codec, rate, allowed,
			),
			Suggestion: "add an aresample filter before this encoder to convert to a supported sample rate",
		})
	}
}
