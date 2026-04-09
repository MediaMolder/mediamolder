package ffcli

import (
	"fmt"
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
	videoFilters string
	audioFilters string
	globalOpts   map[string]string
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
	for p.hasMore() {
		arg := p.next()
		switch {
		case arg == "-i":
			if !p.hasMore() {
				return nil, fmt.Errorf("-i requires an argument")
			}
			url := p.next()
			id := fmt.Sprintf("input%d", len(p.inputs))
			p.inputs = append(p.inputs, pipeline.Input{
				ID: id, URL: url,
				Streams: []pipeline.StreamSelect{
					{InputIndex: 0, Type: "video", Track: 0},
					{InputIndex: 0, Type: "audio", Track: 0},
				},
			})
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
			p.globalOpts["video_bitrate"] = p.next()
		case arg == "-b:a":
			if !p.hasMore() {
				return nil, fmt.Errorf("-b:a requires an argument")
			}
			p.globalOpts["audio_bitrate"] = p.next()
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
		case strings.HasPrefix(arg, "-"):
			if p.hasMore() && !strings.HasPrefix(p.peek(), "-") {
				p.globalOpts[strings.TrimPrefix(arg, "-")] = p.next()
			}
		default:
			id := fmt.Sprintf("output%d", len(p.outputs))
			out := pipeline.Output{ID: id, URL: arg}
			if p.codecV != "" && p.codecV != "none" {
				out.CodecVideo = p.codecV
			}
			if p.codecA != "" && p.codecA != "none" {
				out.CodecAudio = p.codecA
			}
			if f, ok := p.globalOpts["format"]; ok {
				out.Format = f
				delete(p.globalOpts, "format")
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
	return &pipeline.Config{
		SchemaVersion: "1.0",
		Inputs:        p.inputs,
		Graph:         pipeline.GraphDef{Nodes: p.nodes, Edges: p.edges},
		Outputs:       p.outputs,
	}, nil
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
