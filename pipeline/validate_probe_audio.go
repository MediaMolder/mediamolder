// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
)

// downmixFilters is the set of filter names that can reduce channel count.
var downmixFilters = map[string]bool{
	"pan":      true,
	"amerge":   true,
	"adownmix": true,
}

// validateProbeAudio runs all probe-assisted audio checks for every encoder node
// in the graph that has an audio stream feeding it.
func validateProbeAudio(cfg *Config, g *graph.Graph, probed map[string][]av.StreamInfo, r *ValidationReport) {
	if g == nil {
		return
	}
	for _, node := range g.Nodes {
		if node.Kind != graph.KindEncoder {
			continue
		}
		if encoderMediaType(node) != graph.PortAudio {
			continue
		}
		codec := encoderCodec(node)
		inputID, ss := sourceStreamForEncoder(g, node, graph.PortAudio, cfg)
		if inputID == "" || ss == nil {
			continue
		}
		streams, ok := probed[inputID]
		if !ok {
			continue
		}
		if ss.InputIndex < 0 || ss.InputIndex >= len(streams) {
			continue
		}
		stream := streams[ss.InputIndex]

		checkProbeSampleFmt(node, codec, stream, r)
		checkProbeSampleRate(node, codec, stream, r)
		checkMultichannelNoDownmix(node, g, codec, stream, r)
	}
}

// checkProbeSampleFmt reports AUDIO_SAMPLE_FMT_MISMATCH when the probed sample
// format is not accepted by the encoder and no aformat filter is in the path.
func checkProbeSampleFmt(node *graph.Node, codec string, stream av.StreamInfo, r *ValidationReport) {
	required, known := audioEncoderSampleFmts[codec]
	if !known || len(required) == 0 {
		return
	}
	// Use the LiveQuery approach: check via av.EncoderSampleFmts if available,
	// falling back to the static table.
	liveFmts := av.EncoderSampleFmts(codec)
	var acceptedNames []string
	if len(liveFmts) > 0 {
		for _, sf := range liveFmts {
			acceptedNames = append(acceptedNames, av.SampleFmtName(sf))
		}
	} else {
		acceptedNames = required
	}

	probedName := av.SampleFmtName(stream.SampleFmt)
	if probedName == "" {
		return // can't determine the sample format
	}
	if containsStr(acceptedNames, probedName) {
		return
	}
	r.add(ValidationIssue{
		Severity: SeverityError,
		Code:     "AUDIO_SAMPLE_FMT_MISMATCH",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source audio stream has sample format %q but encoder %q requires %v",
			probedName, codec, acceptedNames,
		),
		Suggestion: fmt.Sprintf(
			"add an aformat=sample_fmts=%s filter before %q",
			acceptedNames[0], node.ID,
		),
	})
}

// checkProbeSampleRate reports AUDIO_SAMPLE_RATE_MISMATCH when the probed sample
// rate is not accepted by the encoder.
func checkProbeSampleRate(node *graph.Node, codec string, stream av.StreamInfo, r *ValidationReport) {
	if stream.SampleRate <= 0 {
		return
	}
	// Prefer live query over static table.
	liveRates := av.EncoderSampleRates(codec)
	var allowed []int
	if len(liveRates) > 0 {
		allowed = liveRates
	} else {
		allowed = audioEncoderAllowedRates[codec]
	}
	if len(allowed) == 0 {
		return // encoder accepts any rate
	}
	if containsInt(allowed, stream.SampleRate) {
		return
	}
	r.add(ValidationIssue{
		Severity: SeverityError,
		Code:     "AUDIO_SAMPLE_RATE_MISMATCH",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source audio stream has sample rate %d Hz but encoder %q does not support it (allowed: %v)",
			stream.SampleRate, codec, allowed,
		),
		Suggestion: fmt.Sprintf(
			"add an aresample=%d filter before %q to convert to a supported rate",
			nearestAllowedRate(stream.SampleRate, allowed), node.ID,
		),
	})
}

// checkMultichannelNoDownmix warns when the source has more than 2 channels but
// the output stream is stereo and no downmix filter is present.
func checkMultichannelNoDownmix(node *graph.Node, g *graph.Graph, codec string, stream av.StreamInfo, r *ValidationReport) {
	if stream.Channels <= 2 {
		return
	}
	// Check if the encoder's channel layout targets stereo by looking for a
	// channels param, or by inferring from codec defaults.
	outputChannels := nodeOutputChannels(node, codec)
	if outputChannels != 1 && outputChannels != 2 {
		return // output is not mono/stereo — no mismatch
	}
	sourceNode := findSourceAncestor(g, node)
	if pathContainsFilter(g, sourceNode, node, downmixFilters) {
		return
	}
	r.add(ValidationIssue{
		Severity: SeverityWarning,
		Code:     "AUDIO_MULTICHANNEL_NO_DOWNMIX",
		Location: "node:" + node.ID,
		Message: fmt.Sprintf(
			"source has %d audio channels but encoder %q targets %s output and no downmix filter is present",
			stream.Channels, codec, channelLayoutName(outputChannels),
		),
		Suggestion: "add a pan=stereo|c0=0.5*c0+0.5*c2|c1=0.5*c1+0.5*c3 filter, or use aformat=channel_layouts=stereo",
	})
}

// ---------- helpers ----------

// nodeOutputChannels returns the number of output channels the encoder targets,
// inferred from Params or codec defaults. Returns 0 if unknown.
func nodeOutputChannels(node *graph.Node, codec string) int {
	if node.Params != nil {
		if v, ok := node.Params["channels"]; ok {
			n := paramToInt(v)
			if n > 0 {
				return n
			}
		}
		if v, ok := node.Params["channel_layout"]; ok {
			layout := fmt.Sprintf("%v", v)
			switch layout {
			case "stereo":
				return 2
			case "mono":
				return 1
			case "5.1", "5.1(side)":
				return 6
			case "7.1":
				return 8
			}
		}
	}
	// Default output channel counts for well-known audio codecs.
	switch codec {
	case "aac", "libfdk_aac", "mp3", "libmp3lame", "opus", "libopus",
		"vorbis", "libvorbis":
		return 2 // default stereo for these codecs
	}
	return 0
}

// channelLayoutName returns a human-readable name for a channel count.
func channelLayoutName(n int) string {
	switch n {
	case 1:
		return "mono"
	case 2:
		return "stereo"
	default:
		return fmt.Sprintf("%d-channel", n)
	}
}

// nearestAllowedRate returns the allowed sample rate closest to the given rate.
func nearestAllowedRate(rate int, allowed []int) int {
	if len(allowed) == 0 {
		return rate
	}
	best := allowed[0]
	bestDiff := abs(rate - best)
	for _, r := range allowed[1:] {
		if d := abs(rate - r); d < bestDiff {
			best = r
			bestDiff = d
		}
	}
	return best
}

func abs(n int) int {
	if n < 0 {
		return -n
	}
	return n
}
