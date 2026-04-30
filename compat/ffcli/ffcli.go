// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/pipeline"
)

// Parse converts an FFmpeg command-line string into a pipeline.Config.
func Parse(cmdline string) (*pipeline.Config, error) {
	return ParseArgs(tokenize(cmdline))
}

// ParseArgs converts FFmpeg-style arguments into a pipeline.Config.
func ParseArgs(args []string) (*pipeline.Config, error) {
	if len(args) > 0 && (args[0] == "ffmpeg" || strings.HasSuffix(args[0], "/ffmpeg")) {
		args = args[1:]
	}
	p := &parser{args: args}
	return p.parse()
}

type parser struct {
	args           []string
	pos            int
	inputs         []pipeline.Input
	outputs        []pipeline.Output
	nodes          []pipeline.NodeDef
	edges          []pipeline.EdgeDef
	codecV         string
	codecA         string
	codecS         string
	videoFilters   string
	audioFilters   string
	bsfVideo       string
	bsfAudio       string
	bsfSubtitle    string
	fpsMode        string
	audioSync      int
	shortest       bool
	maxFileSize    int64
	copyTS         bool
	startAtZero    bool
	muxDelay       float64
	muxPreload     float64
	avoidNegTS     string
	pass           int
	passLogFile    string
	forceKeyFrames string
	// pendingHLS / pendingDASH collect typed HLS/DASH muxer options
	// (`-hls_time`, `-hls_playlist_type`, `-seg_duration`, ...).
	// Allocated lazily on first matching flag and drained onto the
	// next output's HLS / DASH field. Mirrors libavformat/hlsenc.c
	// + libavformat/dashenc.c AVOption tables; the runtime renders
	// them back into the AVDictionary before avformat_write_header.
	pendingHLS  *pipeline.HLSOptions
	pendingDASH *pipeline.DASHOptions
	// pendingColor / pendingHDR collect typed color + HDR10
	// metadata (`-color_range`, `-color_primaries`, `-color_trc`,
	// `-colorspace`, `-chroma_sample_location`,
	// `-mastering_display_metadata`, `-content_light_level`).
	// Allocated lazily on first matching flag and drained onto the
	// next output's Color / HDR field.
	pendingColor *pipeline.ColorMetadata
	pendingHDR   *pipeline.HDRMetadata
	// pendingSAR / pendingDAR collect the `setsar` / `setdar`
	// shorthand (and the legacy `-aspect A:B`, which is rewritten
	// to DAR per §6.8 of docs/ffmpeg-coverage-roadmap.md).
	pendingSAR string
	pendingDAR string
	hwAccel    string
	hwDevice   string
	hwOutFmt   string
	globalOpts map[string]string
	// Container-level metadata collected from `-metadata key=value`
	// (no specifier). Latched onto the next output.
	containerMeta map[string]string
	// Per-stream attributes collected from `-metadata:s:<type>:<idx>
	// key=value` and `-disposition:s:<type>:<idx> flags`. Latched
	// onto the next output. Keyed by `<type>:<idx>` (e.g. "a:0",
	// "v:1"); each entry is a draft StreamSpec that gets finalised
	// when the output URL is seen.
	streamSpecs map[string]*pipeline.StreamSpec
	// Per-stream encoder options (preset, crf, b, g, ...). Populated
	// from CLI flags like -crf, -preset, -b:v, -tune, -profile:v,
	// -level, -g, -bf, -maxrate, -minrate, -bufsize. Attached to the
	// matching pipeline.Output.EncoderParams* field so the implicit
	// encoder pass picks them up.
	videoEncOpts map[string]any
	audioEncOpts map[string]any
	// Per-file timing/demuxer options (-t, -ss, -to) collected from the
	// CLI between file specifiers. FFmpeg attaches them to the *next*
	// file (input or output) named on the command line, so they are
	// queued here and drained when the next -i / output URL is seen.
	pendingFileOpts map[string]any
	// Pending demuxer-side typed flags collected between -i flags;
	// drained onto the next pipeline.Input. FFmpeg's options table
	// marks `-stream_loop`, `-itsoffset`, `-re`,
	// `-readrate{,_initial_burst,_catchup}` as `OPT_INPUT |
	// OPT_OFFSET`, which means they latch onto the *next* `-i`,
	// not the previous one. Mirror that here so command lines like
	// `-stream_loop -1 -i logo.png -i main.mp4` apply the loop to
	// `logo.png` only. The `*Set` companion booleans tell the input
	// emission code whether the user actually set the flag (so a
	// `-readrate 0` would still differ from "unset").
	pendingStreamLoop     int
	pendingStreamLoopSet  bool
	pendingITSOffset      float64
	pendingITSOffsetSet   bool
	pendingReadRate       float64
	pendingReadRateSet    bool
	pendingReadBurst      float64
	pendingReadBurstSet   bool
	pendingReadCatchup    float64
	pendingReadCatchupSet bool

	// `-map` selectors collected in CLI order; drained over inputs
	// just before buildGraph runs so the implicit per-input
	// "first-of-each-type" defaults are replaced when any map is
	// present (mirrors FFmpeg's `fftools/ffmpeg_opt.c::map_manual`).
	mapSpecs []parsedMap

	// Pending `-map_metadata IDX` / `-map_chapters IDX` (Wave 2 #11).
	// nil = unset; non-nil holds the input index. Latched onto the
	// next output and rendered as metadata_reader+metadata_writer
	// nodes connected by a metadata edge.
	pendingMapMetadata *int
	pendingMapChapters *int
}

func (p *parser) peek() string {
	if p.pos >= len(p.args) {
		return ""
	}
	return p.args[p.pos]
}

func (p *parser) next() string {
	s := p.peek()
	p.pos++
	return s
}

func (p *parser) hasMore() bool { return p.pos < len(p.args) }

func (p *parser) parse() (*pipeline.Config, error) {
	p.globalOpts = make(map[string]string)
	p.videoEncOpts = make(map[string]any)
	p.audioEncOpts = make(map[string]any)
	for p.hasMore() {
		arg := p.next()
		switch {
		case arg == "-i":
			if !p.hasMore() {
				return nil, fmt.Errorf("-i requires an argument")
			}
			url := p.next()
			id := fmt.Sprintf("input%d", len(p.inputs))
			streams := []pipeline.StreamSelect{
				{InputIndex: 0, Type: "video", Track: 0, Optional: true},
				{InputIndex: 0, Type: "audio", Track: 0, Optional: true},
			}
			// Add subtitle stream unless -sn was specified before -i.
			if p.codecS != "none" {
				streams = append(streams, pipeline.StreamSelect{
					InputIndex: 0, Type: "subtitle", Track: 0, Optional: true,
				})
			}
			in := pipeline.Input{ID: id, URL: url, Streams: streams}
			if len(p.pendingFileOpts) > 0 {
				in.Options = p.pendingFileOpts
				p.pendingFileOpts = nil
			}
			// Wave 5 #23-#28: pull the typed input-side demuxer keys
			// (latched from `-f`, `-framerate`, `-pix_fmt`,
			// `-video_size`/`-s`, `-ar`, `-ac`, `-sample_fmt`,
			// `-thread_queue_size`, `-pattern_type`,
			// `-protocol_whitelist`, `-accurate_seek`,
			// `-seek_timestamp`) out of the pendingFileOpts catch-all
			// and into the matching typed Input field. Unknown keys
			// stay in Options as before so legacy AVDict pass-through
			// still works.
			drainTypedInputDemuxer(&in, in.Options)
			if p.pendingStreamLoopSet {
				in.StreamLoop = p.pendingStreamLoop
				p.pendingStreamLoop, p.pendingStreamLoopSet = 0, false
			}
			if p.pendingITSOffsetSet {
				in.ITSOffset = p.pendingITSOffset
				p.pendingITSOffset, p.pendingITSOffsetSet = 0, false
			}
			if p.pendingReadRateSet {
				in.ReadRate = p.pendingReadRate
				p.pendingReadRate, p.pendingReadRateSet = 0, false
			}
			if p.pendingReadBurstSet {
				in.ReadRateInitialBurst = p.pendingReadBurst
				p.pendingReadBurst, p.pendingReadBurstSet = 0, false
			}
			if p.pendingReadCatchupSet {
				in.ReadRateCatchup = p.pendingReadCatchup
				p.pendingReadCatchup, p.pendingReadCatchupSet = 0, false
			}
			p.inputs = append(p.inputs, in)
		case arg == "-c:v" || arg == "-vcodec":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.codecV = p.next()
		case arg == "-c:a" || arg == "-acodec":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.codecA = p.next()
		case arg == "-c" || arg == "-codec":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			c := p.next()
			if p.codecV == "" {
				p.codecV = c
			}
			if p.codecA == "" {
				p.codecA = c
			}
		case arg == "-vf" || arg == "-filter:v":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.videoFilters = p.next()
		case arg == "-af" || arg == "-filter:a":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.audioFilters = p.next()
		case arg == "-f":
			// FFmpeg's `-f FMT` is per-file (OPT_INPUT|OPT_OUTPUT):
			// it sets the demuxer when followed by `-i`, the muxer
			// otherwise. Latch into pendingFileOpts under the
			// canonical `__format` key so the input/output drain
			// can route it to Input.Format or Output.Format.
			if !p.hasMore() {
				return nil, fmt.Errorf("-f requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["__format"] = p.next()
		case arg == "-b:v":
			if !p.hasMore() {
				return nil, fmt.Errorf("-b:v requires an argument")
			}
			p.videoEncOpts["b"] = p.next()
		case arg == "-b:a":
			if !p.hasMore() {
				return nil, fmt.Errorf("-b:a requires an argument")
			}
			p.audioEncOpts["b"] = p.next()
		case arg == "-r":
			// `-r` is per-file: input-side it's the demuxer
			// framerate (synonym of `-framerate`), output-side
			// it's the encoder framerate. Latch into
			// pendingFileOpts; drained by either input (typed
			// FrameRate) or output (videoEncOpts["r"]).
			if !p.hasMore() {
				return nil, fmt.Errorf("-r requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["__r"] = p.next()
		case arg == "-framerate":
			if !p.hasMore() {
				return nil, fmt.Errorf("-framerate requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["framerate"] = p.next()
		case arg == "-video_size" || arg == "-s":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["video_size"] = p.next()
		case arg == "-pixel_format":
			if !p.hasMore() {
				return nil, fmt.Errorf("-pixel_format requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["pixel_format"] = p.next()
		case arg == "-sample_fmt":
			if !p.hasMore() {
				return nil, fmt.Errorf("-sample_fmt requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["sample_fmt"] = p.next()
		case arg == "-thread_queue_size":
			if !p.hasMore() {
				return nil, fmt.Errorf("-thread_queue_size requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["thread_queue_size"] = p.next()
		case arg == "-protocol_whitelist":
			if !p.hasMore() {
				return nil, fmt.Errorf("-protocol_whitelist requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["protocol_whitelist"] = p.next()
		case arg == "-pattern_type":
			if !p.hasMore() {
				return nil, fmt.Errorf("-pattern_type requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["pattern_type"] = p.next()
		case arg == "-accurate_seek":
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["accurate_seek"] = "1"
		case arg == "-noaccurate_seek":
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["accurate_seek"] = "0"
		case arg == "-seek_timestamp":
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["seek_timestamp"] = "1"
		case arg == "-y" || arg == "-n":
			// overwrite flags - ignored
		case arg == "-an":
			p.codecA = "none"
		case arg == "-vn":
			p.codecV = "none"
		case arg == "-sn":
			p.codecS = "none"
		case arg == "-c:s" || arg == "-scodec":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.codecS = p.next()
		case arg == "-bsf:v":
			if !p.hasMore() {
				return nil, fmt.Errorf("-bsf:v requires an argument")
			}
			p.bsfVideo = p.next()
		case arg == "-bsf:a":
			if !p.hasMore() {
				return nil, fmt.Errorf("-bsf:a requires an argument")
			}
			p.bsfAudio = p.next()
		case arg == "-bsf:s":
			if !p.hasMore() {
				return nil, fmt.Errorf("-bsf:s requires an argument")
			}
			p.bsfSubtitle = p.next()
		case arg == "-color_range":
			if !p.hasMore() {
				return nil, fmt.Errorf("-color_range requires an argument")
			}
			if p.pendingColor == nil {
				p.pendingColor = &pipeline.ColorMetadata{}
			}
			p.pendingColor.Range = p.next()
		case arg == "-color_primaries":
			if !p.hasMore() {
				return nil, fmt.Errorf("-color_primaries requires an argument")
			}
			if p.pendingColor == nil {
				p.pendingColor = &pipeline.ColorMetadata{}
			}
			p.pendingColor.Primaries = p.next()
		case arg == "-color_trc":
			if !p.hasMore() {
				return nil, fmt.Errorf("-color_trc requires an argument")
			}
			if p.pendingColor == nil {
				p.pendingColor = &pipeline.ColorMetadata{}
			}
			p.pendingColor.Transfer = p.next()
		case arg == "-colorspace":
			if !p.hasMore() {
				return nil, fmt.Errorf("-colorspace requires an argument")
			}
			if p.pendingColor == nil {
				p.pendingColor = &pipeline.ColorMetadata{}
			}
			p.pendingColor.Space = p.next()
		case arg == "-chroma_sample_location":
			if !p.hasMore() {
				return nil, fmt.Errorf("-chroma_sample_location requires an argument")
			}
			if p.pendingColor == nil {
				p.pendingColor = &pipeline.ColorMetadata{}
			}
			p.pendingColor.ChromaLocation = p.next()
		case arg == "-mastering_display_metadata" || arg == "-master_display":
			// FFmpeg's x265/SVT-AV1 `-master_display` and the
			// generic `-mastering_display_metadata` flag both
			// accept the canonical x265 grammar:
			// "G(x,y)B(x,y)R(x,y)WP(x,y)L(min,max)" with chromaticity
			// in 1/50000 units and luminance in 1/10000 cd/m^2.
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			md, err := parseMasteringDisplay(p.next())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			if p.pendingHDR == nil {
				p.pendingHDR = &pipeline.HDRMetadata{}
			}
			p.pendingHDR.MasteringDisplay = md
		case arg == "-content_light_level" || arg == "-max_cll":
			// FFmpeg's `-max_cll` (x265) and the generic
			// `-content_light_level` accept "MaxCLL,MaxFALL" (or
			// "MaxCLL|MaxFALL" — both separators tolerated).
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			cll, err := parseContentLightLevel(p.next())
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			if p.pendingHDR == nil {
				p.pendingHDR = &pipeline.HDRMetadata{}
			}
			p.pendingHDR.ContentLightLevel = cll
		case arg == "-async":
			// Legacy FFmpeg audio-sync flag. The FFmpeg 8.0 CLI removed
			// it in favour of `-af aresample=async=N`; we accept it for
			// import compatibility and route the value through
			// pipeline.Output.AudioSync, which the runtime turns into
			// an aresample filter splice in front of the audio encoder.
			// Negative / non-numeric values are rejected.
			if !p.hasMore() {
				return nil, fmt.Errorf("-async requires an argument")
			}
			v := p.next()
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("-async: invalid value %q (want non-negative integer)", v)
			}
			p.audioSync = n
		case arg == "-shortest":
			// FFmpeg `-shortest` (per-output bool, OPT_OFFSET in
			// fftools/ffmpeg_opt.c). Latched onto the next output URL.
			p.shortest = true
		case arg == "-fs":
			// FFmpeg `-fs SIZE` (per-output int64 limit_filesize).
			// Accepts a plain byte count; the FFmpeg CLI also accepts
			// SI suffixes (K/M/G) via av_strtod, but the production
			// scripts in our corpus all use bare bytes.
			if !p.hasMore() {
				return nil, fmt.Errorf("-fs requires an argument")
			}
			v := p.next()
			n, err := strconv.ParseInt(v, 10, 64)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("-fs: invalid value %q (want non-negative integer bytes)", v)
			}
			p.maxFileSize = n
		case arg == "-copyts":
			// FFmpeg `-copyts` is a global bool. We carry it on the
			// pipeline.Config and use it both to suppress the
			// demuxer-side ts_offset shift and to interpret output-side
			// -ss/-to as absolute timeline values.
			p.copyTS = true
		case arg == "-start_at_zero":
			// FFmpeg `-start_at_zero` (global bool). Modulates `-copyts`:
			// re-enables the demuxer-side ts_offset shift even when
			// `-copyts` is set, so the first kept packet still anchors
			// at PTS 0. See fftools/ffmpeg_demux.c L486.
			p.startAtZero = true
		case arg == "-muxdelay":
			// FFmpeg `-muxdelay SECONDS` (per-output float OPT_OFFSET;
			// fftools/ffmpeg_opt.c L2134). Latched onto the next output's
			// MuxDelay; rendered into the muxer AVDict as
			// `max_delay = muxdelay * AV_TIME_BASE` (microseconds).
			if !p.hasMore() {
				return nil, fmt.Errorf("-muxdelay requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("-muxdelay: invalid value %q (want non-negative seconds)", v)
			}
			p.muxDelay = f
		case arg == "-muxpreload":
			// FFmpeg `-muxpreload SECONDS` (per-output float OPT_OFFSET;
			// fftools/ffmpeg_opt.c L2137). Latched onto Output.MuxPreload;
			// rendered as `preload = muxpreload * AV_TIME_BASE` (most
			// muxers ignore it; MPEG-PS is the historic consumer).
			if !p.hasMore() {
				return nil, fmt.Errorf("-muxpreload requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("-muxpreload: invalid value %q (want non-negative seconds)", v)
			}
			p.muxPreload = f
		case arg == "-avoid_negative_ts":
			// FFmpeg `-avoid_negative_ts MODE` (AVFormatContext AVOption;
			// libavformat/options_table.h L95-99). Validated here so
			// typos surface at parse time rather than only when the
			// muxer rejects the AVDict entry.
			if !p.hasMore() {
				return nil, fmt.Errorf("-avoid_negative_ts requires an argument")
			}
			v := p.next()
			switch v {
			case "auto", "disabled", "make_non_negative", "make_zero":
			default:
				return nil, fmt.Errorf("-avoid_negative_ts: invalid value %q (want auto|disabled|make_non_negative|make_zero)", v)
			}
			p.avoidNegTS = v
		case arg == "-pass":
			// FFmpeg `-pass N` (per-stream OPT_VIDEO int). Bit-field:
			// 1 = analysis pass (AV_CODEC_FLAG_PASS1), 2 = final pass
			// (AV_CODEC_FLAG_PASS2), 3 = both. Latched onto the next
			// output's Output.Pass; the runtime then propagates it to
			// the implicit video encoder via __pass.
			if !p.hasMore() {
				return nil, fmt.Errorf("-pass requires an argument")
			}
			v := p.next()
			n, err := strconv.Atoi(v)
			if err != nil || n < 1 || n > 3 {
				return nil, fmt.Errorf("-pass: invalid value %q (want 1|2|3)", v)
			}
			p.pass = n
		case arg == "-passlogfile":
			// FFmpeg `-passlogfile PREFIX` (per-stream OPT_VIDEO
			// string; default `ffmpeg2pass`). Latched onto the next
			// output's Output.PassLogFile; the runtime renders the
			// final filename as `<prefix>-<global-stream-idx>.log`.
			if !p.hasMore() {
				return nil, fmt.Errorf("-passlogfile requires an argument")
			}
			p.passLogFile = p.next()
		case arg == "-force_key_frames":
			// FFmpeg `-force_key_frames SPEC` (per-stream OPT_VIDEO
			// string). Three grammars: `expr:EXPR` (libavutil
			// expression evaluated per video frame), `source` (copy
			// keyframes from source), or comma-separated time list
			// (`3,7.5,10.25`). Latches onto the next output's
			// Output.ForceKeyFrames; pipeline parses + builds the
			// per-encoder matcher at run time.
			if !p.hasMore() {
				return nil, fmt.Errorf("-force_key_frames requires an argument")
			}
			p.forceKeyFrames = p.next()
		case arg == "-aspect":
			// Legacy `-aspect <ratio>` (DAR). Importer rewrites to
			// Output.DAR per §6.8 of docs/ffmpeg-coverage-roadmap.md.
			if !p.hasMore() {
				return nil, fmt.Errorf("-aspect requires an argument")
			}
			p.pendingDAR = p.next()
		case arg == "-setsar" || arg == "-vsar":
			// Convenience: capture an explicit SAR string for the
			// next output. Mirrors the `setsar=A:B` filter shorthand.
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			p.pendingSAR = p.next()
		case arg == "-setdar":
			if !p.hasMore() {
				return nil, fmt.Errorf("-setdar requires an argument")
			}
			p.pendingDAR = p.next()
		// ---- HLS muxer options (libavformat/hlsenc.c) ----
		case arg == "-hls_time" || arg == "-hls_init_time" ||
			arg == "-hls_list_size" || arg == "-hls_playlist_type" ||
			arg == "-hls_segment_type" || arg == "-hls_segment_filename" ||
			arg == "-hls_fmp4_init_filename" || arg == "-hls_flags" ||
			arg == "-master_pl_name" || arg == "-var_stream_map" ||
			arg == "-start_number":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			v := p.next()
			if p.pendingHLS == nil {
				p.pendingHLS = &pipeline.HLSOptions{}
			}
			if err := setHLSOption(p.pendingHLS, arg, v); err != nil {
				return nil, err
			}
		// ---- DASH muxer options (libavformat/dashenc.c) ----
		case arg == "-seg_duration" || arg == "-frag_duration" ||
			arg == "-window_size" || arg == "-extra_window_size" ||
			arg == "-init_seg_name" || arg == "-media_seg_name" ||
			arg == "-single_file" || arg == "-use_template" ||
			arg == "-use_timeline" || arg == "-streaming" ||
			arg == "-adaptation_sets" || arg == "-hls_playlist" ||
			arg == "-ldash" || arg == "-dash_flags":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			v := p.next()
			if p.pendingDASH == nil {
				p.pendingDASH = &pipeline.DASHOptions{}
			}
			if err := setDASHOption(p.pendingDASH, arg, v); err != nil {
				return nil, err
			}
		case arg == "-map":
			// FFmpeg `-map [-]INPUT[:SPEC][?]` (Wave 2 #9 + #10).
			// Collected here in CLI order; applied to inputs in
			// applyMapSelectors() once all flags are consumed so the
			// implicit per-input "first-of-each-type" defaults are
			// suppressed exactly as FFmpeg does in
			// fftools/ffmpeg_opt.c::map_manual.
			if !p.hasMore() {
				return nil, fmt.Errorf("-map requires an argument")
			}
			m, err := parseMapArg(p.next())
			if err != nil {
				return nil, err
			}
			p.mapSpecs = append(p.mapSpecs, m)
		case arg == "-map_metadata":
			// FFmpeg `-map_metadata IDX` (Wave 2 #11). IDX is the
			// 0-based index of an input file whose container metadata
			// is copied into the next output. Latches onto the next
			// output as a metadata_reader + metadata_writer node pair
			// linked by a metadata edge so multiple outputs can
			// independently route from different sources. The simple
			// `IDX = next-output-index` case (single input → single
			// output) is also covered by Input.MapMetadata; this
			// flag form lets multi-input jobs route per-output.
			if !p.hasMore() {
				return nil, fmt.Errorf("-map_metadata requires an argument")
			}
			v := p.next()
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("-map_metadata: invalid index %q (want non-negative integer; -1 / per-section selectors not yet supported)", v)
			}
			p.pendingMapMetadata = &n
		case arg == "-map_chapters":
			// FFmpeg `-map_chapters IDX` (Wave 2 #11). Single-source
			// per-output chapter routing.
			if !p.hasMore() {
				return nil, fmt.Errorf("-map_chapters requires an argument")
			}
			v := p.next()
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				return nil, fmt.Errorf("-map_chapters: invalid index %q (want non-negative integer; -1 not yet supported)", v)
			}
			p.pendingMapChapters = &n
		case arg == "-stream_loop":
			// FFmpeg per-input integer (OPT_OFFSET on InputFile.loop):
			// `-stream_loop N -i in.mp4` plays in.mp4 N+1 times total
			// (-1 = infinite). Latches onto the next -i; rejecting
			// values < -1 mirrors the demuxer's
			// "if (d->loop > 0) d->loop--" arithmetic, where -1 is
			// the magic infinite sentinel and any other negative
			// value is undefined.
			if !p.hasMore() {
				return nil, fmt.Errorf("-stream_loop requires an argument")
			}
			v := p.next()
			n, err := strconv.Atoi(v)
			if err != nil || n < -1 {
				return nil, fmt.Errorf("-stream_loop: invalid value %q (want integer >= -1)", v)
			}
			p.pendingStreamLoop, p.pendingStreamLoopSet = n, true
		case arg == "-itsoffset":
			// FFmpeg per-input OPT_TYPE_TIME (seconds, may be
			// negative). Stored on InputFile.input_ts_offset and
			// applied via `pkt->pts += av_rescale_q(ifile->ts_offset,
			// AV_TIME_BASE_Q, pkt->time_base)` in
			// fftools/ffmpeg_demux.c::ts_fixup. We accept the bare
			// seconds form (the FFmpeg CLI also accepts
			// HH:MM:SS via av_parse_time, deferred until we have
			// a corpus job that needs it).
			if !p.hasMore() {
				return nil, fmt.Errorf("-itsoffset requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil {
				return nil, fmt.Errorf("-itsoffset: invalid value %q (want seconds, e.g. -0.030)", v)
			}
			p.pendingITSOffset, p.pendingITSOffsetSet = f, true
		case arg == "-re":
			// FFmpeg shorthand for `-readrate 1`. Per-input bool.
			// FFmpeg warns when both -re and -readrate are set
			// (-readrate wins); we mirror that policy by letting
			// any subsequent -readrate overwrite the latched value.
			p.pendingReadRate, p.pendingReadRateSet = 1.0, true
		case arg == "-readrate":
			if !p.hasMore() {
				return nil, fmt.Errorf("-readrate requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("-readrate: invalid value %q (want non-negative float, e.g. 1.0)", v)
			}
			p.pendingReadRate, p.pendingReadRateSet = f, true
		case arg == "-readrate_initial_burst":
			if !p.hasMore() {
				return nil, fmt.Errorf("-readrate_initial_burst requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("-readrate_initial_burst: invalid value %q (want non-negative seconds)", v)
			}
			p.pendingReadBurst, p.pendingReadBurstSet = f, true
		case arg == "-readrate_catchup":
			if !p.hasMore() {
				return nil, fmt.Errorf("-readrate_catchup requires an argument")
			}
			v := p.next()
			f, err := strconv.ParseFloat(v, 64)
			if err != nil || f < 0 {
				return nil, fmt.Errorf("-readrate_catchup: invalid value %q (want non-negative float)", v)
			}
			p.pendingReadCatchup, p.pendingReadCatchupSet = f, true
		case arg == "-metadata" || strings.HasPrefix(arg, "-metadata:"):
			// FFmpeg `-metadata [SPEC] key=value`. Without a spec the
			// value lands on the container; with `:s:<type>:<idx>` it
			// targets a single output stream. We do not yet support
			// the `:g:`, `:c:`, `:p:`, or `:s:<type>` (no index)
			// variants — they require either chapter/program-level
			// dispatch (deferred) or "broadcast to every stream of
			// type" (which the explicit-stream form already covers in
			// practice).
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			kv := p.next()
			eq := strings.Index(kv, "=")
			if eq <= 0 {
				return nil, fmt.Errorf("%s: expected key=value, got %q", arg, kv)
			}
			key, val := kv[:eq], kv[eq+1:]
			spec := strings.TrimPrefix(arg, "-metadata")
			spec = strings.TrimPrefix(spec, ":")
			switch {
			case spec == "":
				if p.containerMeta == nil {
					p.containerMeta = make(map[string]string)
				}
				p.containerMeta[key] = val
			case strings.HasPrefix(spec, "s:"):
				typ, idx, err := parseStreamSpec(spec[2:])
				if err != nil {
					return nil, fmt.Errorf("%s: %w", arg, err)
				}
				ss := p.streamSpecFor(typ, idx)
				if ss.Metadata == nil {
					ss.Metadata = make(map[string]string)
				}
				ss.Metadata[key] = val
			default:
				return nil, fmt.Errorf("%s: unsupported specifier %q (only `s:<type>:<idx>` supported)", arg, spec)
			}
		case strings.HasPrefix(arg, "-disposition:s:"):
			// FFmpeg `-disposition:s:<type>:<idx> default+forced`. We
			// only accept the per-stream form; the bare `-disposition`
			// (which clears every stream) and the `:<type>` (broadcast)
			// forms are not yet supported.
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			val := p.next()
			typ, idx, err := parseStreamSpec(strings.TrimPrefix(arg, "-disposition:s:"))
			if err != nil {
				return nil, fmt.Errorf("%s: %w", arg, err)
			}
			ss := p.streamSpecFor(typ, idx)
			ss.Disposition = val
		case arg == "-fps_mode" || arg == "-vsync":
			// FFmpeg modern: -fps_mode {passthrough|cfr|vfr|drop|auto}.
			// FFmpeg legacy: -vsync {0|1|2|drop|passthrough|cfr|vfr|auto}.
			// We rewrite the numeric/auto aliases to the modern names.
			// `auto` falls back to passthrough (ffmpeg's actual default
			// depends on the muxer; passthrough is the safest no-op).
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			v := p.next()
			switch v {
			case "0", "passthrough", "auto", "-1":
				p.fpsMode = "passthrough"
			case "1", "cfr":
				p.fpsMode = "cfr"
			case "2", "vfr":
				p.fpsMode = "vfr"
			case "drop":
				p.fpsMode = "drop"
			default:
				return nil, fmt.Errorf("%s: unknown value %q (want passthrough|cfr|vfr|drop|auto|0|1|2)", arg, v)
			}
		case arg == "-hwaccel":
			if !p.hasMore() {
				return nil, fmt.Errorf("-hwaccel requires an argument")
			}
			p.hwAccel = p.next()
		case arg == "-hwaccel_device":
			if !p.hasMore() {
				return nil, fmt.Errorf("-hwaccel_device requires an argument")
			}
			p.hwDevice = p.next()
		case arg == "-hwaccel_output_format":
			if !p.hasMore() {
				return nil, fmt.Errorf("-hwaccel_output_format requires an argument")
			}
			p.hwOutFmt = p.next()
		// ---- Video encoder options ----
		case arg == "-crf" || arg == "-qp" || arg == "-preset" || arg == "-tune" ||
			arg == "-profile:v" || arg == "-level" || arg == "-g" || arg == "-bf" ||
			arg == "-maxrate" || arg == "-minrate" || arg == "-bufsize" ||
			arg == "-x264-params" || arg == "-x265-params":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			key := strings.TrimPrefix(arg, "-")
			// `-profile:v` -> AVOption `profile`.
			if key == "profile:v" {
				key = "profile"
			}
			p.videoEncOpts[key] = p.next()
		case arg == "-pix_fmt":
			// `-pix_fmt FMT` is per-file: input-side it's the
			// rawvideo demuxer's pixel format, output-side it's
			// the encoder's. Latch into pendingFileOpts; drained
			// by either input (typed PixelFormat) or output
			// (videoEncOpts["pix_fmt"]).
			if !p.hasMore() {
				return nil, fmt.Errorf("-pix_fmt requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["pix_fmt"] = p.next()
		// ---- Audio encoder options ----
		case arg == "-q:a" || arg == "-aq" ||
			arg == "-profile:a":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			key := strings.TrimPrefix(arg, "-")
			switch key {
			case "q:a", "aq":
				key = "q"
			case "profile:a":
				key = "profile"
			}
			p.audioEncOpts[key] = p.next()
		case arg == "-ar":
			// `-ar RATE` is per-file: input-side raw PCM sample
			// rate, output-side encoder sample rate. Latch.
			if !p.hasMore() {
				return nil, fmt.Errorf("-ar requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["ar"] = p.next()
		case arg == "-ac":
			// `-ac N` is per-file: input-side raw PCM channel
			// count, output-side encoder channel count. Latch.
			if !p.hasMore() {
				return nil, fmt.Errorf("-ac requires an argument")
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts["ac"] = p.next()
		case arg == "-t" || arg == "-ss" || arg == "-to":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			if p.pendingFileOpts == nil {
				p.pendingFileOpts = make(map[string]any)
			}
			p.pendingFileOpts[strings.TrimPrefix(arg, "-")] = p.next()
		case strings.HasPrefix(arg, "-"):
			if p.hasMore() && !strings.HasPrefix(p.peek(), "-") {
				p.globalOpts[strings.TrimPrefix(arg, "-")] = p.next()
			}
		default:
			id := fmt.Sprintf("output%d", len(p.outputs))
			out := pipeline.Output{ID: id, URL: arg}
			if len(p.pendingFileOpts) > 0 {
				out.Options = p.pendingFileOpts
				p.pendingFileOpts = nil
			}
			// Wave 5 #23-#28: re-route per-file flags that landed in
			// pendingFileOpts onto their canonical output-side
			// destinations: `-f` → Output.Format (existing field),
			// `-pix_fmt` / `-r` / `-ar` / `-ac` → encoder opts.
			drainTypedOutputDemuxer(&out, p.videoEncOpts, p.audioEncOpts, out.Options)
			if p.codecV != "" && p.codecV != "none" {
				out.CodecVideo = p.codecV
			}
			if p.codecA != "" && p.codecA != "none" {
				out.CodecAudio = p.codecA
			}
			if p.codecS != "" && p.codecS != "none" {
				out.CodecSubtitle = p.codecS
			}
			if p.bsfVideo != "" {
				out.BSFVideo = p.bsfVideo
			}
			if p.bsfAudio != "" {
				out.BSFAudio = p.bsfAudio
			}
			if p.bsfSubtitle != "" {
				out.BSFSubtitle = p.bsfSubtitle
			}
			if p.fpsMode != "" {
				out.FPSMode = p.fpsMode
			}
			if p.audioSync != 0 {
				out.AudioSync = p.audioSync
			}
			if p.shortest {
				out.Shortest = true
			}
			if p.maxFileSize > 0 {
				out.MaxFileSize = p.maxFileSize
			}
			if p.muxDelay > 0 {
				out.MuxDelay = p.muxDelay
				p.muxDelay = 0
			}
			if p.muxPreload > 0 {
				out.MuxPreload = p.muxPreload
				p.muxPreload = 0
			}
			if p.avoidNegTS != "" {
				out.AvoidNegativeTS = p.avoidNegTS
				p.avoidNegTS = ""
			}
			if p.pass != 0 {
				out.Pass = p.pass
				p.pass = 0
			}
			if p.passLogFile != "" {
				out.PassLogFile = p.passLogFile
				p.passLogFile = ""
			}
			if p.forceKeyFrames != "" {
				out.ForceKeyFrames = p.forceKeyFrames
				p.forceKeyFrames = ""
			}
			if p.pendingHLS != nil {
				out.HLS = p.pendingHLS
				p.pendingHLS = nil
			}
			if p.pendingDASH != nil {
				out.DASH = p.pendingDASH
				p.pendingDASH = nil
			}
			if p.pendingColor != nil {
				out.Color = p.pendingColor
				p.pendingColor = nil
			}
			if p.pendingHDR != nil {
				out.HDR = p.pendingHDR
				p.pendingHDR = nil
			}
			if p.pendingSAR != "" {
				out.SAR = p.pendingSAR
				p.pendingSAR = ""
			}
			if p.pendingDAR != "" {
				out.DAR = p.pendingDAR
				p.pendingDAR = ""
			}
			if len(p.containerMeta) > 0 {
				out.Metadata = p.containerMeta
				p.containerMeta = nil
			}
			if len(p.streamSpecs) > 0 {
				// Emit deterministically: sort by (type, index) so
				// repeated calls with the same flag set produce
				// byte-identical Output.Streams ordering. Mirrors the
				// stable order FFmpeg's `of_add_metadata` produces by
				// walking the OptionsContext list in declaration order.
				keys := make([]string, 0, len(p.streamSpecs))
				for k := range p.streamSpecs {
					keys = append(keys, k)
				}
				sort.Strings(keys)
				out.Streams = make([]pipeline.StreamSpec, 0, len(keys))
				for _, k := range keys {
					out.Streams = append(out.Streams, *p.streamSpecs[k])
				}
				p.streamSpecs = nil
			}
			if f, ok := p.globalOpts["format"]; ok {
				out.Format = f
				delete(p.globalOpts, "format")
			}
			// Attach encoder options collected from CLI flags so the
			// implicit-encoder pass (runtime + GUI) merges them into
			// the synthesised encoder node. Only attach for stream
			// types that aren't disabled or copied.
			if p.codecV != "none" && p.codecV != "copy" && len(p.videoEncOpts) > 0 {
				out.EncoderParamsVideo = copyAnyMap(p.videoEncOpts)
			}
			if p.codecA != "none" && p.codecA != "copy" && len(p.audioEncOpts) > 0 {
				out.EncoderParamsAudio = copyAnyMap(p.audioEncOpts)
			}
			// `-f tee URL` (where URL is `[opt=val]slave|[opt=val]slave`):
			// promote to typed Output.Kind="tee" + Output.Targets[]. The
			// pipeline runtime then reconstructs the slaves URL deterministically
			// via pipeline.buildTeeSlavesURL when it opens the tee muxer.
			if out.Format == "tee" {
				targets, terr := parseTeeSlaves(out.URL)
				if terr != nil {
					return nil, fmt.Errorf("output -f tee: %w", terr)
				}
				out.Kind = "tee"
				out.Targets = targets
				out.Format = ""
				out.URL = "tee"
			}
			p.outputs = append(p.outputs, out)
			// Wave 2 #11: drain pending -map_metadata / -map_chapters
			// onto this output. Emit metadata_reader + metadata_writer
			// nodes connected by a metadata edge so the runtime can
			// route per-output independently from any other output.
			if p.pendingMapMetadata != nil {
				if *p.pendingMapMetadata >= len(p.inputs) {
					return nil, fmt.Errorf("-map_metadata %d: only %d input(s) declared", *p.pendingMapMetadata, len(p.inputs))
				}
				p.emitMetadataRoute(out.ID, p.inputs[*p.pendingMapMetadata].ID, "global")
				p.pendingMapMetadata = nil
			}
			if p.pendingMapChapters != nil {
				if *p.pendingMapChapters >= len(p.inputs) {
					return nil, fmt.Errorf("-map_chapters %d: only %d input(s) declared", *p.pendingMapChapters, len(p.inputs))
				}
				p.emitMetadataRoute(out.ID, p.inputs[*p.pendingMapChapters].ID, "chapters")
				p.pendingMapChapters = nil
			}
		}
	}
	if len(p.inputs) == 0 {
		return nil, fmt.Errorf("no input specified (use -i)")
	}
	if len(p.outputs) == 0 {
		return nil, fmt.Errorf("no output specified")
	}
	if err := p.applyMapSelectors(); err != nil {
		return nil, err
	}
	p.buildGraph()
	nodes := p.nodes
	if nodes == nil {
		nodes = []pipeline.NodeDef{}
	}
	edges := p.edges
	if edges == nil {
		edges = []pipeline.EdgeDef{}
	}
	cfg := &pipeline.Config{
		SchemaVersion: "1.0",
		Inputs:        p.inputs,
		Graph:         pipeline.GraphDef{Nodes: nodes, Edges: edges},
		Outputs:       p.outputs,
	}
	if p.copyTS {
		cfg.CopyTS = true
	}
	if p.startAtZero {
		cfg.StartAtZero = true
	}
	if p.hwAccel != "" {
		cfg.GlobalOptions.HardwareAccel = p.hwAccel
	}
	if p.hwDevice != "" {
		cfg.GlobalOptions.HardwareDevice = p.hwDevice
	}
	return cfg, nil
}

// emitMetadataRoute appends a metadata_reader + metadata_writer node
// pair connected by a metadata edge so the runtime routes the named
// section ("global" or "chapters") from inputID into outputID. Wave 2
// #11 ffcli emission counterpart of pipeline's runtime resolver.
func (p *parser) emitMetadataRoute(outputID, inputID, section string) {
	suffix := "meta"
	if section == "chapters" {
		suffix = "chapters"
	}
	readerID := fmt.Sprintf("__%s_reader_%s", suffix, outputID)
	writerID := fmt.Sprintf("__%s_writer_%s", suffix, outputID)
	p.nodes = append(p.nodes,
		pipeline.NodeDef{
			ID:     readerID,
			Type:   "metadata_reader",
			Params: map[string]any{"source": inputID, "section": section},
		},
		pipeline.NodeDef{
			ID:     writerID,
			Type:   "metadata_writer",
			Params: map[string]any{"target": outputID, "section": section},
		},
	)
	p.edges = append(p.edges, pipeline.EdgeDef{
		From: readerID,
		To:   writerID,
		Type: "metadata",
	})
}

func (p *parser) buildGraph() {
	inID := p.inputs[0].ID
	outID := p.outputs[0].ID
	if p.codecV != "none" {
		vs, vd := inID+":v:0", outID+":v"
		if p.videoFilters != "" {
			fn := parseFilterChain(p.videoFilters, "vf")
			p.nodes = append(p.nodes, fn...)
			if len(fn) > 0 {
				p.edges = append(p.edges, pipeline.EdgeDef{From: vs, To: fn[0].ID + ":default", Type: "video"})
				for i := 0; i < len(fn)-1; i++ {
					p.edges = append(p.edges, pipeline.EdgeDef{From: fn[i].ID + ":default", To: fn[i+1].ID + ":default", Type: "video"})
				}
				p.edges = append(p.edges, pipeline.EdgeDef{From: fn[len(fn)-1].ID + ":default", To: vd, Type: "video"})
			}
		} else {
			p.edges = append(p.edges, pipeline.EdgeDef{From: vs, To: vd, Type: "video"})
		}
	}
	if p.codecA != "none" {
		as, ad := inID+":a:0", outID+":a"
		if p.audioFilters != "" {
			fn := parseFilterChain(p.audioFilters, "af")
			p.nodes = append(p.nodes, fn...)
			if len(fn) > 0 {
				p.edges = append(p.edges, pipeline.EdgeDef{From: as, To: fn[0].ID + ":default", Type: "audio"})
				for i := 0; i < len(fn)-1; i++ {
					p.edges = append(p.edges, pipeline.EdgeDef{From: fn[i].ID + ":default", To: fn[i+1].ID + ":default", Type: "audio"})
				}
				p.edges = append(p.edges, pipeline.EdgeDef{From: fn[len(fn)-1].ID + ":default", To: ad, Type: "audio"})
			}
		} else {
			p.edges = append(p.edges, pipeline.EdgeDef{From: as, To: ad, Type: "audio"})
		}
	}
	if p.codecS != "none" && p.codecS != "" {
		ss, sd := inID+":s:0", outID+":s"
		p.edges = append(p.edges, pipeline.EdgeDef{From: ss, To: sd, Type: "subtitle"})
	}
}

func parseFilterChain(chain, prefix string) []pipeline.NodeDef {
	var nodes []pipeline.NodeDef
	for i, f := range strings.Split(chain, ",") {
		f = strings.TrimSpace(f)
		if f == "" {
			continue
		}
		name, params := parseFilterExpr(f)
		nodes = append(nodes, pipeline.NodeDef{
			ID:     fmt.Sprintf("%s_%d_%s", prefix, i, name),
			Type:   "filter",
			Filter: name,
			Params: params,
		})
	}
	return nodes
}

func parseFilterExpr(expr string) (string, map[string]any) {
	parts := strings.SplitN(expr, "=", 2)
	if len(parts) == 1 {
		return parts[0], nil
	}
	params := make(map[string]any)
	for i, kv := range strings.Split(parts[1], ":") {
		if idx := strings.Index(kv, "="); idx > 0 {
			params[kv[:idx]] = kv[idx+1:]
		} else {
			params[fmt.Sprintf("_pos%d", i)] = kv
		}
	}
	return parts[0], params
}

func tokenize(s string) []string {
	var args []string
	var cur strings.Builder
	inSQ, inDQ := false, false
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c == '\'' && !inDQ:
			inSQ = !inSQ
		case c == '"' && !inSQ:
			inDQ = !inDQ
		case c == ' ' && !inSQ && !inDQ:
			if cur.Len() > 0 {
				args = append(args, cur.String())
				cur.Reset()
			}
		default:
			cur.WriteByte(c)
		}
	}
	if cur.Len() > 0 {
		args = append(args, cur.String())
	}
	return args
}

// copyAnyMap returns a shallow copy of m. Used so that each Output
// gets its own EncoderParams* map rather than aliasing the parser's
// accumulator (which would let a later output mutate an earlier one's
// params).
func copyAnyMap(m map[string]any) map[string]any {
	if len(m) == 0 {
		return nil
	}
	out := make(map[string]any, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}
