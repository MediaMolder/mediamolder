// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"net/http"
	"runtime"
	"sort"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/processors"
)

// NodeCatalogEntry describes a node template the palette can present.
type NodeCatalogEntry struct {
	Category    string   `json:"category"`              // top-level group ("Sources", "Filters", "Encoders", "Processors", "Sinks")
	Subcategory string   `json:"subcategory,omitempty"` // friendly subgroup ("Scaling & cropping", "Color & exposure", ...)
	Type        string   `json:"type"`                  // schema NodeDef.type ("filter", "encoder", "go_processor", ...)
	Name        string   `json:"name"`                  // canonical name passed to libav (e.g. "scale", "libx264")
	Label       string   `json:"label,omitempty"`       // friendly display name ("Scale (resize)")
	Description string   `json:"description,omitempty"`
	Streams     []string `json:"streams,omitempty"` // ["video"], ["audio"], etc.
	NumInputs   int      `json:"num_inputs,omitempty"`
	NumOutputs  int      `json:"num_outputs,omitempty"`

	// Curation metadata (Wave 8 #54a / ui_improvements §A–B). Populated
	// from internal/gui/curation.go::curatedNodes when this entry's
	// Name matches a curated key. Common surfaces in the default
	// palette view; FriendlyName drives the "Friendly names" toggle;
	// Aliases extend the search index with synonyms ("h264" → libx264,
	// "loudness" → loudnorm).
	Common       bool     `json:"common,omitempty"`
	FriendlyName string   `json:"friendly_name,omitempty"`
	Aliases      []string `json:"aliases,omitempty"`
}

// handleListNodes returns the node palette catalogue assembled from the live
// av/* and processors/* registries plus a few synthetic built-ins (input/output).
func handleListNodes(w http.ResponseWriter, _ *http.Request) {
	out := make([]NodeCatalogEntry, 0, 512)

	// Built-in source/sink.
	out = append(out,
		NodeCatalogEntry{
			Category:    "Sources",
			Type:        "input",
			Name:        "Input",
			Label:       "Input file",
			Description: "Read media from a file or URL. Click to open a file picker.",
		},
		NodeCatalogEntry{
			Category:    "Sinks",
			Type:        "output",
			Name:        "Output",
			Label:       "Output file",
			Description: "Write the processed media to a file or URL.",
		},
	)

	// Stream-copy nodes — one per media type. A copy node forwards
	// demuxer packets straight to the muxer with no decode/encode, so
	// the source and destination must share a codec the output
	// container accepts.
	for _, st := range []struct{ name, label, desc, stream string }{
		{"copy_video", "Copy (video)", "Forward the input video stream to the output container without re-encoding.", "video"},
		{"copy_audio", "Copy (audio)", "Forward the input audio stream to the output container without re-encoding.", "audio"},
		{"copy_subtitle", "Copy (subtitle)", "Forward the input subtitle stream to the output container without re-encoding.", "subtitle"},
		{"copy_data", "Copy (data)", "Forward an input data stream (timecode, KLV, ...) to the output container without re-encoding.", "data"},
	} {
		out = append(out, NodeCatalogEntry{
			Category:    "Copy",
			Type:        "copy",
			Name:        st.name,
			Label:       st.label,
			Description: st.desc,
			Streams:     []string{st.stream},
			NumInputs:   1,
			NumOutputs:  1,
		})
	}

	// Filters from libavfilter — only 1→1 in the palette; multi-IO filters
	// (overlay, split, etc.) can be added by editing JSON directly.
	for _, f := range av.ListFilters() {
		if f.NumInputs != 1 || f.NumOutputs != 1 {
			continue
		}
		sub, label := classifyFilter(f.Name, f.Description)
		out = append(out, NodeCatalogEntry{
			Category:    "Filters",
			Subcategory: sub,
			Type:        "filter",
			Name:        f.Name,
			Label:       label,
			Description: f.Description,
			Streams:     filterStreams(f),
			NumInputs:   f.NumInputs,
			NumOutputs:  f.NumOutputs,
		})
	}

	// Routing filters — multi-IO filters (fan-out, fan-in, composition)
	// that are too common to omit from the palette. Exposed under
	// "Filters / Routing". The canvas represents N pads via a single
	// per-stream-type handle; multiple edges may originate from (fan-out)
	// or converge on (fan-in) the same handle, ordered by connection
	// sequence.
	for _, f := range av.ListFilters() {
		if _, ok := routingFilters[f.Name]; !ok {
			continue
		}
		_, label := classifyFilter(f.Name, f.Description)
		out = append(out, NodeCatalogEntry{
			Category:    "Filters",
			Subcategory: "Routing",
			Type:        "filter",
			Name:        f.Name,
			Label:       label,
			Description: f.Description,
			Streams:     filterStreams(f),
			NumInputs:   f.NumInputs,
			NumOutputs:  f.NumOutputs,
		})
	}

	// Virtual source / sink filters (Wave 8 #44). These libavfilter
	// nodes have zero inputs (source) or zero outputs (sink) and would
	// otherwise be filtered out by the 1→1 rule above. They land under
	// the "Sources" / "Sinks" top-level categories so users find them
	// alongside file inputs / outputs. Only the curated allow-lists
	// from pipeline/filter_availability.go (knownFilterSources /
	// knownFilterSinks) are exposed; the ones not built into the
	// running libavfilter are skipped automatically because
	// av.ListFilters() only reports linked filters.
	for _, f := range av.ListFilters() {
		if f.NumInputs == 0 && f.NumOutputs >= 1 {
			if _, ok := virtualSourceFilters[f.Name]; !ok {
				continue
			}
			out = append(out, NodeCatalogEntry{
				Category:    "Sources",
				Subcategory: "Virtual sources",
				Type:        "filter_source",
				Name:        f.Name,
				Label:       virtualFilterLabel(f.Name),
				Description: virtualSourceDescription(f.Name, f.Description),
				Streams:     filterStreams(f),
				NumInputs:   0,
				NumOutputs:  f.NumOutputs,
			})
			continue
		}
		if f.NumInputs >= 1 && f.NumOutputs == 0 {
			if _, ok := virtualSinkFilters[f.Name]; !ok {
				continue
			}
			out = append(out, NodeCatalogEntry{
				Category:    "Sinks",
				Subcategory: "Virtual sinks",
				Type:        "filter_sink",
				Name:        f.Name,
				Label:       virtualFilterLabel(f.Name),
				Description: virtualSinkDescription(f.Name, f.Description),
				Streams:     filterStreams(f),
				NumInputs:   f.NumInputs,
				NumOutputs:  0,
			})
		}
	}

	// `Input.Kind="lavfi"` shorthand: a single Input that opens a
	// libavfilter graph spec via the lavfi virtual demuxer. Distinct
	// from the per-node KindFilterSource entries above — this one
	// produces an Input + URL pair instead of a graph node, so the
	// existing "Input file" inspector form can edit the spec string.
	out = append(out, NodeCatalogEntry{
		Category:    "Sources",
		Subcategory: "Virtual sources",
		Type:        "lavfi_input",
		Name:        "Lavfi input",
		Label:       "Lavfi virtual input",
		Description: "Input.Kind=\"lavfi\" — opens a libavfilter graph spec (e.g. anullsrc=r=48000:cl=stereo, color=black:s=1920x1080:r=30) as a top-level input via FFmpeg's lavfi virtual demuxer.",
	})

	// Per-platform device capture inputs (Wave 11 #62).
	// Only the native capture formats for the running OS are exposed.
	// The device-name combobox (Wave 11 #63) populates from GET /api/devices?format=<fmt>.
	switch runtime.GOOS {
	case "windows":
		out = append(out,
			NodeCatalogEntry{
				Category:    "Sources",
				Subcategory: "Device capture",
				Type:        "device_input",
				Name:        "dshow",
				Label:       "DirectShow (camera / mic)",
				Description: "Capture from a DirectShow device (webcam, microphone, capture card). URL format: video=\"Device Name\" or audio=\"Device Name\".",
				Streams:     []string{"video", "audio"},
				Common:      true,
			},
			NodeCatalogEntry{
				Category:    "Sources",
				Subcategory: "Device capture",
				Type:        "device_input",
				Name:        "gdigrab",
				Label:       "GDI grab (screen capture)",
				Description: "Capture the Windows desktop or a specific window via GDI. URL: \"desktop\" or \"title=Window Title\".",
				Streams:     []string{"video"},
				Common:      true,
			},
		)
	case "darwin":
		out = append(out, NodeCatalogEntry{
			Category:    "Sources",
			Subcategory: "Device capture",
			Type:        "device_input",
			Name:        "avfoundation",
			Label:       "AVFoundation (camera / mic / screen)",
			Description: "Capture from an AVFoundation device on macOS. URL format: \"<video_index>:<audio_index>\", e.g. \"0:0\" for default camera + mic.",
			Streams:     []string{"video", "audio"},
			Common:      true,
		})
	default: // Linux and other POSIX
		out = append(out, NodeCatalogEntry{
			Category:    "Sources",
			Subcategory: "Device capture",
			Type:        "device_input",
			Name:        "v4l2",
			Label:       "V4L2 (Video for Linux)",
			Description: "Capture from a Video4Linux2 device. URL: /dev/video0 (or another V4L2 device node).",
			Streams:     []string{"video"},
			Common:      true,
		})
	}

	// Encoders from libavcodec.
	for _, c := range av.ListCodecs() {
		if !c.IsEncoder {
			continue
		}
		var sub string
		switch c.Type {
		case "video":
			sub = "Video encoders"
		case "audio":
			sub = "Audio encoders"
		case "subtitle":
			sub = "Subtitle encoders"
		default:
			continue
		}
		out = append(out, NodeCatalogEntry{
			Category:    "Encoders",
			Subcategory: sub,
			Type:        "encoder",
			Name:        c.Name,
			Label:       prettyEncoderName(c.Name, c.LongName),
			Description: c.LongName,
			Streams:     []string{c.Type},
		})
	}

	// Go processors.
	for _, name := range processors.Names() {
		out = append(out, NodeCatalogEntry{
			Category:    "Processors",
			Subcategory: "Built-in processors",
			Type:        "go_processor",
			Name:        name,
			Label:       prettyProcessorName(name),
			Description: processorDescription(name),
		})
	}

	// Attach curation metadata (Common / FriendlyName / Aliases) from
	// internal/gui/curation.go::curatedNodes. Built-in synthetic entries
	// (input / output / copy_*) are also flagged Common so they appear
	// in the default palette view; their friendly labels are already
	// set inline above.
	for i := range out {
		applyCuration(&out[i])
		switch out[i].Type {
		case "input", "output", "copy", "filter_source", "filter_sink", "lavfi_input", "device_input":
			// These are synthetic palette built-ins — always Common.
			out[i].Common = true
		}
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Category != out[j].Category {
			return categoryOrder(out[i].Category) < categoryOrder(out[j].Category)
		}
		if out[i].Subcategory != out[j].Subcategory {
			return out[i].Subcategory < out[j].Subcategory
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(out)
}

func categoryOrder(c string) int {
	switch c {
	case "Sources":
		return 0
	case "Filters":
		return 1
	case "Encoders":
		return 2
	case "Copy":
		return 3
	case "Processors":
		return 4
	case "Sinks":
		return 5
	default:
		return 6
	}
}

// filterStreams returns the unique set of media types appearing on a
// 1→1 filter's input + output pads. Used to populate the catalog entry's
// Streams field so the frontend renders only matching pins. Returns nil
// for dynamic-pad filters (libav reports an empty pad list), which the
// frontend treats as media-type-agnostic.
func filterStreams(f av.FilterInfo) []string {
	seen := map[string]struct{}{}
	var out []string
	for _, t := range append(append([]string(nil), f.InputTypes...), f.OutputTypes...) {
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	return out
}

// classifyFilter buckets a libavfilter into a friendly subcategory and
// generates a human-readable label. The classification uses both the filter's
// short name (rich source-of-truth in libavfilter) and its description text.
//
// Buckets are intentionally coarse — most users browse by intent ("I want to
// resize this") rather than by FFmpeg's internal categorisation.
func classifyFilter(name, desc string) (subcategory, label string) {
	n := strings.ToLower(name)
	d := strings.ToLower(desc)

	switch {
	// Audio — prefix `a` is the libavfilter convention for audio filters.
	case strings.HasPrefix(n, "a") && (strings.Contains(d, "audio") || strings.Contains(d, "sound")):
		return audioSubcategory(n, d), formatLabel(name, desc)
	case strings.Contains(d, "audio"), strings.Contains(d, "sound"), strings.Contains(d, "decibel"):
		return audioSubcategory(n, d), formatLabel(name, desc)

	// Subtitle.
	case strings.Contains(n, "subtitle"), strings.Contains(d, "subtitle"):
		return "Subtitles", formatLabel(name, desc)

	// Hardware acceleration plumbing.
	case strings.HasPrefix(n, "hwupload"), strings.HasPrefix(n, "hwdownload"), strings.HasPrefix(n, "hwmap"):
		return "Hardware acceleration", formatLabel(name, desc)
	case strings.Contains(n, "_vaapi"), strings.Contains(n, "_qsv"), strings.Contains(n, "_cuda"),
		strings.Contains(n, "_vulkan"), strings.Contains(n, "_opencl"):
		return "Hardware acceleration", formatLabel(name, desc)

	// Scaling, cropping, padding, rotation.
	case n == "scale", strings.HasPrefix(n, "scale_"), n == "scale2ref",
		strings.HasPrefix(n, "crop"), strings.HasPrefix(n, "pad"),
		n == "rotate", n == "transpose", n == "hflip", n == "vflip", n == "rotate":
		return "Scaling & geometry", formatLabel(name, desc)

	// Color, exposure, levels.
	case strings.Contains(n, "color"), strings.Contains(n, "eq"), strings.Contains(n, "curves"),
		strings.Contains(n, "lut"), strings.Contains(n, "hue"), strings.Contains(n, "saturation"),
		strings.Contains(n, "tonemap"), strings.Contains(n, "histogram"), strings.Contains(n, "vibrance"):
		return "Color & exposure", formatLabel(name, desc)

	// Denoise, deblock, deinterlace.
	case strings.Contains(n, "denoise"), strings.HasPrefix(n, "nlmeans"), strings.HasPrefix(n, "atadenoise"),
		strings.HasPrefix(n, "hqdn3d"), strings.HasPrefix(n, "deblock"),
		strings.HasPrefix(n, "yadif"), strings.HasPrefix(n, "bwdif"), strings.HasPrefix(n, "deinterlace"):
		return "Denoise & deinterlace", formatLabel(name, desc)

	// Sharpen / blur.
	case strings.Contains(n, "sharpen"), strings.Contains(n, "blur"), strings.Contains(n, "convolution"):
		return "Sharpen & blur", formatLabel(name, desc)

	// Text & overlays.
	case n == "drawtext", n == "drawbox", n == "drawgrid", strings.Contains(n, "subtitles"):
		return "Text & overlays", formatLabel(name, desc)

	// Time / framerate.
	case n == "fps", n == "framerate", strings.HasPrefix(n, "tmix"), n == "minterpolate",
		n == "setpts", n == "asetpts", n == "trim", n == "atrim",
		n == "setsar", n == "setdar", n == "settb", n == "asettb":
		return "Timing & framerate", formatLabel(name, desc)

	// Format conversions.
	case n == "format", n == "aformat", strings.Contains(n, "pixfmt"),
		n == "hue", n == "extractplanes":
		return "Format conversion", formatLabel(name, desc)

	// Metadata / passthrough utilities.
	case strings.Contains(n, "metadata"), n == "select", n == "aselect", n == "showinfo", n == "ashowinfo":
		return "Metadata & inspection", formatLabel(name, desc)
	}

	return "Other", formatLabel(name, desc)
}

func audioSubcategory(name, desc string) string {
	switch {
	case strings.Contains(name, "resample"), strings.Contains(name, "format"),
		strings.Contains(name, "channelmap"), strings.Contains(name, "channelsplit"):
		return "Audio: format & routing"
	case strings.Contains(name, "volume"), strings.Contains(name, "loudnorm"),
		strings.Contains(name, "compand"), strings.Contains(name, "compressor"),
		strings.Contains(name, "limiter"), strings.Contains(name, "agate"):
		return "Audio: dynamics & loudness"
	case strings.Contains(name, "eq"), strings.Contains(name, "filter"),
		strings.Contains(name, "echo"), strings.Contains(name, "reverb"),
		strings.Contains(name, "chorus"), strings.Contains(name, "phaser"):
		return "Audio: EQ & effects"
	case strings.Contains(desc, "show"), strings.Contains(name, "show"):
		return "Audio: visualisation"
	}
	return "Audio: other"
}

// formatLabel turns ("scale", "scale the input video size") into "Scale".
// We capitalise the filter name and append the first sentence of the
// description as a hint when it adds something beyond the name.
func formatLabel(name, desc string) string {
	pretty := strings.ReplaceAll(name, "_", " ")
	if pretty != "" {
		pretty = strings.ToUpper(pretty[:1]) + pretty[1:]
	}
	short := firstSentence(desc)
	if short == "" || strings.EqualFold(short, pretty) {
		return pretty
	}
	return pretty + " — " + short
}

func firstSentence(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexAny(s, ".\n"); i > 0 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func prettyEncoderName(name, longName string) string {
	if longName == "" {
		return name
	}
	return name + " — " + longName
}

func prettyProcessorName(name string) string {
	parts := strings.Split(name, "_")
	for i, p := range parts {
		if p == "" {
			continue
		}
		parts[i] = strings.ToUpper(p[:1]) + p[1:]
	}
	return strings.Join(parts, " ")
}

func processorDescription(name string) string {
	switch name {
	case "null":
		return "Pass frames through unchanged (used for testing)."
	case "frame_counter":
		return "Count frames and periodically emit metadata."
	case "frame_info":
		return "Emit per-frame diagnostics (size, format, PTS)."
	case "scene_change":
		return "Detect scene changes (MAFD-based, same algorithm as FFmpeg's scdet)."
	case "metadata_file_writer":
		return "Wrap another processor and write its metadata to a JSON Lines file."
	case "yolo_v8":
		return "YOLOv8 object detection (requires the with_onnx build tag)."
	}
	return ""
}

// virtualSourceFilters is the GUI-visible subset of
// pipeline.knownFilterSources (Wave 7 #36a). Kept in sync manually —
// `pipeline` is not imported here to avoid a cgo dependency cycle.
// Wave 8 #44.
var virtualSourceFilters = map[string]struct{}{
	"color":       {},
	"testsrc":     {},
	"testsrc2":    {},
	"smptebars":   {},
	"smptehdbars": {},
	"mandelbrot":  {},
	"life":        {},
	"yuvtestsrc":  {},
	"rgbtestsrc":  {},
	"sine":        {},
	"anullsrc":    {},
	"aevalsrc":    {},
	"movie":       {},
	"amovie":      {},
}

// virtualSinkFilters mirrors pipeline.knownFilterSinks. Wave 8 #44.
var virtualSinkFilters = map[string]struct{}{
	"nullsink":  {},
	"anullsink": {},
}

// routingFilters is the curated set of multi-IO libavfilter nodes exposed
// under "Filters / Routing" in the palette. Fan-out filters (split, asplit)
// have 1 static input and dynamic outputs; fan-in / composition filters
// (overlay, hstack, etc.) have multiple inputs and 1 static output. The
// canvas represents N pads via a single per-stream-type handle.
var routingFilters = map[string]struct{}{
	"split":   {},
	"asplit":  {},
	"overlay": {},
	"hstack":  {},
	"vstack":  {},
	"xstack":  {},
	"amerge":  {},
	"amix":    {},
	"concat":  {},
}

func virtualFilterLabel(name string) string {
	switch name {
	case "color":
		return "Color (solid colour video)"
	case "testsrc":
		return "Testsrc (test pattern)"
	case "testsrc2":
		return "Testsrc2 (test pattern v2)"
	case "smptebars":
		return "SMPTE bars (SD)"
	case "smptehdbars":
		return "SMPTE bars (HD)"
	case "mandelbrot":
		return "Mandelbrot (animated fractal)"
	case "life":
		return "Life (Conway's Game of Life)"
	case "yuvtestsrc":
		return "YUV test pattern"
	case "rgbtestsrc":
		return "RGB test pattern"
	case "sine":
		return "Sine (audio test tone)"
	case "anullsrc":
		return "Anullsrc (silent audio)"
	case "aevalsrc":
		return "Aevalsrc (audio expression)"
	case "movie":
		return "Movie (file as video source)"
	case "amovie":
		return "Amovie (file as audio source)"
	case "nullsink":
		return "Nullsink (discard video)"
	case "anullsink":
		return "Anullsink (discard audio)"
	}
	return name
}

func virtualSourceDescription(name, desc string) string {
	hint := "Append `duration=N` (seconds) or `nb_frames=N` to bound the source."
	switch name {
	case "movie", "amovie":
		hint = "Set `filename` to the asset path. Honour Config.ProtocolWhitelist for non-local paths."
	case "anullsrc":
		hint = "Silent audio. No duration cap required (consumed lazily by downstream)."
	}
	if desc == "" {
		return hint
	}
	return desc + " — " + hint
}

func virtualSinkDescription(name, desc string) string {
	hint := "Discards every frame. Lets an analyser branch (e.g. ebur128, signalstats) terminate without a muxer output."
	if desc == "" {
		return hint
	}
	return desc + " — " + hint
}
