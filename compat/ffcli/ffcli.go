// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
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
	hwAccel      string
	hwDevice     string
	hwOutFmt     string
	globalOpts   map[string]string
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
