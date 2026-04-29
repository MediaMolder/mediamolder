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
	args         []string
	pos          int
	inputs       []pipeline.Input
	outputs      []pipeline.Output
	nodes        []pipeline.NodeDef
	edges        []pipeline.EdgeDef
	codecV       string
	codecA       string
	codecS       string
	videoFilters string
	audioFilters string
	bsfVideo     string
	bsfAudio     string
	fpsMode      string
	audioSync    int
	shortest     bool
	maxFileSize  int64
	copyTS       bool
	pass         int
	passLogFile  string
	hwAccel      string
	hwDevice     string
	hwOutFmt     string
	globalOpts   map[string]string
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
				{InputIndex: 0, Type: "video", Track: 0},
				{InputIndex: 0, Type: "audio", Track: 0},
			}
			// Add subtitle stream unless -sn was specified before -i.
			if p.codecS != "none" {
				streams = append(streams, pipeline.StreamSelect{
					InputIndex: 0, Type: "subtitle", Track: 0,
				})
			}
			in := pipeline.Input{ID: id, URL: url, Streams: streams}
			if len(p.pendingFileOpts) > 0 {
				in.Options = p.pendingFileOpts
				p.pendingFileOpts = nil
			}
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
			if !p.hasMore() {
				return nil, fmt.Errorf("-f requires an argument")
			}
			p.globalOpts["format"] = p.next()
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
			if !p.hasMore() {
				return nil, fmt.Errorf("-r requires an argument")
			}
			p.globalOpts["framerate"] = p.next()
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
			arg == "-pix_fmt" || arg == "-x264-params" || arg == "-x265-params":
			if !p.hasMore() {
				return nil, fmt.Errorf("%s requires an argument", arg)
			}
			key := strings.TrimPrefix(arg, "-")
			// `-profile:v` -> AVOption `profile`.
			if key == "profile:v" {
				key = "profile"
			}
			p.videoEncOpts[key] = p.next()
		// ---- Audio encoder options ----
		case arg == "-q:a" || arg == "-aq" || arg == "-ar" || arg == "-ac" ||
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
			if p.pass != 0 {
				out.Pass = p.pass
				p.pass = 0
			}
			if p.passLogFile != "" {
				out.PassLogFile = p.passLogFile
				p.passLogFile = ""
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
		}
	}
	if len(p.inputs) == 0 {
		return nil, fmt.Errorf("no input specified (use -i)")
	}
	if len(p.outputs) == 0 {
		return nil, fmt.Errorf("no output specified")
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
	if p.hwAccel != "" {
		cfg.GlobalOptions.HardwareAccel = p.hwAccel
	}
	if p.hwDevice != "" {
		cfg.GlobalOptions.HardwareDevice = p.hwDevice
	}
	return cfg, nil
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
