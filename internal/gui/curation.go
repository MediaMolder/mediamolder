// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

// curatedNodes is the single declarative registry that tells the palette
// which nodes (encoders / filters / processors) belong in the default
// "Common" view, plus a friendly display label and search aliases for
// non-technical users (e.g. "x264" for libx264, "h264"/"avc"/"mp4" for
// search).
//
// The map is keyed by the canonical libavcodec / libavfilter name as
// reported by av.ListCodecs() / av.ListFilters(). handleListNodes
// looks each emitted entry up here; if present it sets Common=true and
// copies FriendlyName / Aliases onto the NodeCatalogEntry.
//
// The list is hand-maintained on FFmpeg version bumps. A unit test
// (curation_test.go) asserts every key resolves to a real entry so the
// table cannot silently drift from the linked FFmpeg.
var curatedNodes = map[string]NodeMeta{
	// ── Video encoders (software) ────────────────────────────────────
	"libx264":    {Friendly: "x264", Aliases: []string{"h264", "avc", "mp4"}},
	"libx265":    {Friendly: "x265", Aliases: []string{"h265", "hevc"}},
	"libsvtav1":  {Friendly: "SVT-AV1", Aliases: []string{"av1"}},
	"libaom-av1": {Friendly: "AV1 (libaom)", Aliases: []string{"av1"}},
	"libvpx-vp9": {Friendly: "VP9", Aliases: []string{"webm"}},
	"libvpx":     {Friendly: "VP8", Aliases: []string{"webm"}},
	"mpeg2video": {Friendly: "MPEG-2", Aliases: []string{"dvd", "broadcast"}},
	"mjpeg":      {Friendly: "Motion JPEG", Aliases: []string{"mjpg"}},
	"gif":        {Friendly: "GIF"},
	"prores_ks":  {Friendly: "ProRes", Aliases: []string{"apple", "intermediate"}},
	"dnxhd":      {Friendly: "DNxHD / DNxHR", Aliases: []string{"avid", "intermediate"}},
	"ffv1":       {Friendly: "FFV1 (lossless)", Aliases: []string{"lossless", "archive"}},

	// ── Video encoders (hardware) ────────────────────────────────────
	"h264_videotoolbox":   {Friendly: "H.264 (Apple VideoToolbox)", Aliases: []string{"hwaccel", "mac", "apple"}},
	"hevc_videotoolbox":   {Friendly: "H.265 (Apple VideoToolbox)", Aliases: []string{"hwaccel", "mac", "apple", "hevc"}},
	"prores_videotoolbox": {Friendly: "ProRes (Apple VideoToolbox)", Aliases: []string{"hwaccel", "mac", "apple"}},
	"h264_nvenc":          {Friendly: "H.264 (NVIDIA NVENC)", Aliases: []string{"hwaccel", "nvidia", "gpu"}},
	"hevc_nvenc":          {Friendly: "H.265 (NVIDIA NVENC)", Aliases: []string{"hwaccel", "nvidia", "gpu", "hevc"}},
	"av1_nvenc":           {Friendly: "AV1 (NVIDIA NVENC)", Aliases: []string{"hwaccel", "nvidia", "gpu"}},
	"h264_qsv":            {Friendly: "H.264 (Intel QSV)", Aliases: []string{"hwaccel", "intel", "gpu"}},
	"hevc_qsv":            {Friendly: "H.265 (Intel QSV)", Aliases: []string{"hwaccel", "intel", "gpu", "hevc"}},
	"av1_qsv":             {Friendly: "AV1 (Intel QSV)", Aliases: []string{"hwaccel", "intel", "gpu"}},
	"h264_amf":            {Friendly: "H.264 (AMD AMF)", Aliases: []string{"hwaccel", "amd", "gpu"}},
	"hevc_amf":            {Friendly: "H.265 (AMD AMF)", Aliases: []string{"hwaccel", "amd", "gpu", "hevc"}},
	"h264_vaapi":          {Friendly: "H.264 (VAAPI)", Aliases: []string{"hwaccel", "linux", "gpu"}},
	"hevc_vaapi":          {Friendly: "H.265 (VAAPI)", Aliases: []string{"hwaccel", "linux", "gpu", "hevc"}},

	// ── Audio encoders ───────────────────────────────────────────────
	"aac":        {Friendly: "AAC", Aliases: []string{"m4a", "mp4"}},
	"libfdk_aac": {Friendly: "AAC (Fraunhofer FDK)", Aliases: []string{"m4a", "mp4"}},
	"libopus":    {Friendly: "Opus", Aliases: []string{"webm", "ogg"}},
	"libmp3lame": {Friendly: "MP3", Aliases: []string{"mp3"}},
	"libvorbis":  {Friendly: "Vorbis", Aliases: []string{"ogg"}},
	"flac":       {Friendly: "FLAC (lossless)", Aliases: []string{"lossless"}},
	"alac":       {Friendly: "ALAC (Apple lossless)", Aliases: []string{"lossless", "apple"}},
	"ac3":        {Friendly: "AC-3 (Dolby Digital)", Aliases: []string{"dolby", "dvd"}},
	"eac3":       {Friendly: "E-AC-3 (Dolby Digital Plus)", Aliases: []string{"dolby"}},
	"pcm_s16le":  {Friendly: "PCM 16-bit (uncompressed)", Aliases: []string{"wav", "lossless"}},
	"pcm_s24le":  {Friendly: "PCM 24-bit (uncompressed)", Aliases: []string{"wav", "lossless"}},

	// ── Subtitle encoders ────────────────────────────────────────────
	"mov_text": {Friendly: "MP4 subtitles", Aliases: []string{"mp4", "tx3g"}},
	"srt":      {Friendly: "SRT subtitles"},
	"webvtt":   {Friendly: "WebVTT subtitles"},
	"ass":      {Friendly: "ASS / SSA subtitles"},

	// ── Filters: scaling & geometry ──────────────────────────────────
	"scale":     {Friendly: "Resize", Aliases: []string{"resolution", "size"}},
	"crop":      {Friendly: "Crop"},
	"pad":       {Friendly: "Pad / letterbox", Aliases: []string{"letterbox", "pillarbox"}},
	"rotate":    {Friendly: "Rotate"},
	"transpose": {Friendly: "Transpose (90° / flip)", Aliases: []string{"flip", "rotate"}},
	"hflip":     {Friendly: "Horizontal flip", Aliases: []string{"mirror"}},
	"vflip":     {Friendly: "Vertical flip", Aliases: []string{"mirror"}},

	// ── Filters: timing & frame rate ─────────────────────────────────
	"fps":    {Friendly: "Frame rate", Aliases: []string{"framerate", "fps"}},
	"setpts": {Friendly: "Speed (PTS)", Aliases: []string{"speed", "tempo"}},
	"trim":   {Friendly: "Trim video", Aliases: []string{"cut", "clip"}},
	"atrim":  {Friendly: "Trim audio", Aliases: []string{"cut", "clip"}},
	"fade":   {Friendly: "Fade in / out (video)"},
	"afade":  {Friendly: "Fade in / out (audio)"},
	"yadif":  {Friendly: "Deinterlace (yadif)", Aliases: []string{"interlace"}},
	"bwdif":  {Friendly: "Deinterlace (bwdif)", Aliases: []string{"interlace"}},

	// ── Filters: colour & exposure ───────────────────────────────────
	"eq":           {Friendly: "Brightness / contrast / saturation"},
	"curves":       {Friendly: "Curves"},
	"colorbalance": {Friendly: "Colour balance"},
	"hue":          {Friendly: "Hue / saturation"},
	"lut3d":        {Friendly: "3D LUT"},
	"tonemap":      {Friendly: "HDR → SDR tonemap"},

	// ── Filters: text & overlay ──────────────────────────────────────
	"drawtext":  {Friendly: "Text overlay", Aliases: []string{"caption", "title"}},
	"drawbox":   {Friendly: "Box overlay"},
	"subtitles": {Friendly: "Burn-in subtitles", Aliases: []string{"srt", "ass"}},

	// ── Filters: denoise & sharpen ───────────────────────────────────
	"hqdn3d":  {Friendly: "Denoise (hqdn3d)", Aliases: []string{"clean"}},
	"nlmeans": {Friendly: "Denoise (nlmeans, slow/high-quality)"},
	"unsharp": {Friendly: "Sharpen / blur"},

	// ── Filters: audio ───────────────────────────────────────────────
	"volume":      {Friendly: "Volume", Aliases: []string{"gain", "level"}},
	"loudnorm":    {Friendly: "Loudness normalise (EBU R128)", Aliases: []string{"normalize", "loudness", "lufs"}},
	"dynaudnorm":  {Friendly: "Dynamic loudness", Aliases: []string{"normalize"}},
	"acompressor": {Friendly: "Audio compressor"},
	"highpass":    {Friendly: "High-pass filter"},
	"lowpass":     {Friendly: "Low-pass filter"},
	"aresample":   {Friendly: "Resample audio", Aliases: []string{"sample rate"}},
	"pan":         {Friendly: "Channel mix (pan)", Aliases: []string{"downmix", "channel"}},

	// ── Filters: format conversion ───────────────────────────────────
	"format":  {Friendly: "Pixel format"},
	"aformat": {Friendly: "Audio format"},

	// ── Virtual sources (already curated by Wave 8 #44) ──────────────
	"color":     {Friendly: "Solid colour"},
	"testsrc2":  {Friendly: "Test pattern", Aliases: []string{"testsrc", "bars"}},
	"smptebars": {Friendly: "SMPTE bars (SD)", Aliases: []string{"colourbars"}},
	"sine":      {Friendly: "Sine tone", Aliases: []string{"tone", "test"}},
	"anullsrc":  {Friendly: "Silent audio", Aliases: []string{"silence"}},

	// ── Built-in processors ──────────────────────────────────────────
	"frame_counter": {Friendly: "Frame counter", Aliases: []string{"count"}},
	"frame_info":    {Friendly: "Frame info logger", Aliases: []string{"debug", "stats"}},
	"scene_change":  {Friendly: "Scene change detector", Aliases: []string{"scdet", "cuts"}},
}

// NodeMeta is the per-node curation entry.
type NodeMeta struct {
	Friendly string   // Friendly display label, e.g. "x264 (H.264)".
	Aliases  []string // Search synonyms, e.g. {"h264", "avc", "mp4"}.
}

// applyCuration mutates e in place to attach curation metadata when
// e.Name matches a curated entry.
func applyCuration(e *NodeCatalogEntry) {
	meta, ok := curatedNodes[e.Name]
	if !ok {
		return
	}
	e.Common = true
	e.FriendlyName = meta.Friendly
	if len(meta.Aliases) > 0 {
		e.Aliases = append([]string(nil), meta.Aliases...)
	}
}
