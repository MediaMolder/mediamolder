// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/compat/ffcli"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/pipeline"
	"github.com/MediaMolder/MediaMolder/processors"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "mediamolder: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	warnLicense()

	if len(args) == 0 {
		usage()
		return nil
	}
	switch args[0] {
	case "run":
		return cmdRun(args[1:])
	case "inspect":
		return cmdInspect(args[1:])
	case "convert-cmd":
		return cmdConvertCmd(args[1:])
	case "list-codecs":
		return cmdListCodecs(args[1:])
	case "list-filters":
		return cmdListFilters(args[1:])
	case "list-formats":
		return cmdListFormats(args[1:])
	case "list-processors":
		return cmdListProcessors(args[1:])
	case "gui":
		return cmdGUI(args[1:])
	case "version":
		lic := av.DetectLicense()
		fmt.Printf("mediamolder dev (%s)\n  license: %s\n  ffmpeg config: %s\n",
			av.LibVersions(), lic, av.FFmpegConfiguration())
		return nil
	case "migrate":
		return cmdMigrate(args[1:])
	case "help", "--help", "-h":
		usage()
		return nil
	default:
		return fmt.Errorf("unknown command %q\nRun 'mediamolder help' for usage.", args[0])
	}
}

// setVars is a repeatable --set KEY=VALUE flag used by cmdRun.
type setVars []string

func (s *setVars) String() string { return strings.Join(*s, ",") }
func (s *setVars) Set(v string) error {
	*s = append(*s, v)
	return nil
}

func cmdRun(args []string) error {
	fs := flag.NewFlagSet("run", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output progress as JSON")
	metadataOut := fs.String("metadata-out", "", "write processor metadata events as JSON Lines to this file (- for stdout)")
	var sets setVars
	fs.Var(&sets, "set", "set a template variable in the job JSON: KEY=VALUE replaces every {{KEY}} occurrence (may be repeated)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: mediamolder run [--json] [--metadata-out=path] [--set KEY=VALUE ...] <config.json>")
	}
	rawBytes, err := os.ReadFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("read config %q: %w", fs.Arg(0), err)
	}
	raw := string(rawBytes)
	for _, kv := range sets {
		k, v, ok := strings.Cut(kv, "=")
		if !ok {
			return fmt.Errorf("--set: expected KEY=VALUE, got %q", kv)
		}
		raw = strings.ReplaceAll(raw, "{{"+k+"}}", v)
	}
	cfg, err := pipeline.ParseConfig([]byte(raw))
	if err != nil {
		return err
	}
	eng, err := pipeline.NewPipeline(cfg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Metadata file writer (JSON Lines).
	var metaFile *os.File
	var metaEnc *json.Encoder
	if *metadataOut != "" {
		if *metadataOut == "-" {
			metaFile = os.Stdout
		} else {
			metaFile, err = os.Create(*metadataOut)
			if err != nil {
				return fmt.Errorf("metadata-out: %w", err)
			}
			defer metaFile.Close()
		}
		metaEnc = json.NewEncoder(metaFile)
	}

	// Event consumer goroutine — writes metadata and drives progress.
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(500 * time.Millisecond)
		defer ticker.Stop()
		start := time.Now()
		for {
			select {
			case ev, ok := <-eng.Events():
				if !ok {
					return
				}
				if md, isMetadata := ev.(pipeline.ProcessorMetadata); isMetadata && metaEnc != nil {
					metaEnc.Encode(md) //nolint:errcheck // best-effort
				}
			case <-ticker.C:
				snap := eng.GetMetrics()
				elapsed := time.Since(start).Truncate(time.Millisecond)
				var totalFrames int64
				for _, n := range snap.Nodes {
					totalFrames += n.Frames
				}
				if *jsonOut {
					m := map[string]any{
						"state":   snap.State,
						"elapsed": elapsed.String(),
						"frames":  totalFrames,
						"nodes":   snap.Nodes,
					}
					b, _ := json.Marshal(m)
					fmt.Fprintf(os.Stderr, "%s\n", b)
				} else {
					fmt.Fprintf(os.Stderr, "\r[%s] state=%s frames=%d",
						elapsed, snap.State, totalFrames)
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	err = eng.Run(ctx)
	cancel()
	<-done

	if !*jsonOut {
		fmt.Fprintln(os.Stderr) // newline after progress
	}
	return err
}

func cmdInspect(args []string) error {
	fs := flag.NewFlagSet("inspect", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: mediamolder inspect <config.json>")
	}
	cfg, err := pipeline.ParseConfigFile(fs.Arg(0))
	if err != nil {
		return err
	}

	if cfg.Description != "" {
		fmt.Fprintf(os.Stderr, "description: %s\n", cfg.Description)
	}

	// Build and validate graph
	gDef := configToGraphDef(cfg)
	g, err := graph.Build(gDef)
	if err != nil {
		fmt.Fprintf(os.Stderr, "graph validation error: %v\n", err)
	} else {
		fmt.Fprintf(os.Stderr, "graph: %d nodes, %d edges, %d sources, %d sinks\n",
			len(g.Nodes), len(g.Edges), len(g.Sources), len(g.Sinks))
		order := make([]string, len(g.Order))
		for i, n := range g.Order {
			order[i] = n.ID
		}
		fmt.Fprintf(os.Stderr, "topological order: %s\n", strings.Join(order, " → "))
	}

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func cmdConvertCmd(args []string) error {
	fs := flag.NewFlagSet("convert-cmd", flag.ContinueOnError)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: mediamolder convert-cmd \"ffmpeg -i in.mp4 -c:v libx264 out.mp4\"")
	}
	cmdline := strings.Join(fs.Args(), " ")
	cfg, err := ffcli.Parse(cmdline)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func cmdListCodecs(args []string) error {
	fs := flag.NewFlagSet("list-codecs", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	codecs := av.ListCodecs()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(codecs)
	}
	for _, c := range codecs {
		flags := ""
		if c.IsDecoder {
			flags += "D"
		} else {
			flags += "."
		}
		if c.IsEncoder {
			flags += "E"
		} else {
			flags += "."
		}
		fmt.Printf(" %s %-5s %-20s %s\n", flags, c.Type, c.Name, c.LongName)
	}
	return nil
}

func cmdListFilters(args []string) error {
	fs := flag.NewFlagSet("list-filters", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	filters := av.ListFilters()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(filters)
	}
	for _, f := range filters {
		fmt.Printf(" %d→%d %-20s %s\n", f.NumInputs, f.NumOutputs, f.Name, f.Description)
	}
	return nil
}

func cmdListFormats(args []string) error {
	fs := flag.NewFlagSet("list-formats", flag.ContinueOnError)
	jsonOut := fs.Bool("json", false, "output as JSON")
	if err := fs.Parse(args); err != nil {
		return err
	}
	formats := av.ListFormats()
	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(formats)
	}
	for _, f := range formats {
		flags := ""
		if f.IsDemuxer {
			flags += "D"
		} else {
			flags += "."
		}
		if f.IsMuxer {
			flags += "E"
		} else {
			flags += "."
		}
		fmt.Printf(" %s %-20s %s\n", flags, f.Name, f.LongName)
	}
	return nil
}

// configToGraphDef converts a pipeline.Config to a graph.Def for validation.
func configToGraphDef(cfg *pipeline.Config) *graph.Def {
	def := &graph.Def{}
	for _, in := range cfg.Inputs {
		def.Inputs = append(def.Inputs, graph.InputDef{ID: in.ID})
	}
	for _, n := range cfg.Graph.Nodes {
		def.Nodes = append(def.Nodes, graph.NodeDef{
			ID: n.ID, Type: n.Type, Filter: n.Filter, Processor: n.Processor, Params: n.Params,
		})
	}
	for _, e := range cfg.Graph.Edges {
		def.Edges = append(def.Edges, graph.EdgeDef{
			From: e.From, To: e.To, Type: e.Type,
		})
	}
	for _, out := range cfg.Outputs {
		def.Outputs = append(def.Outputs, graph.OutputDef{ID: out.ID})
	}
	return def
}

func cmdMigrate(args []string) error {
	fs := flag.NewFlagSet("migrate", flag.ContinueOnError)
	from := fs.String("from", "", "source schema version (currently unused)")
	to := fs.String("to", "", "target schema version (currently unused)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		return fmt.Errorf("usage: mediamolder migrate [--from=N --to=N] <config.json>")
	}
	_ = from // reserved for future multi-version migration
	_ = to

	cfg, err := pipeline.ParseConfigFile(fs.Arg(0))
	if err != nil {
		return fmt.Errorf("validation failed: %w", err)
	}

	fmt.Fprintf(os.Stderr, "config OK (schema_version=%q, %d inputs, %d outputs, %d nodes, %d edges)\n",
		cfg.SchemaVersion,
		len(cfg.Inputs),
		len(cfg.Outputs),
		len(cfg.Graph.Nodes),
		len(cfg.Graph.Edges),
	)

	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(cfg)
}

func warnLicense() {
	lic := av.DetectLicense()
	switch lic {
	case av.LicenseNonfree:
		fmt.Fprintln(os.Stderr,
			"WARNING: Linked FFmpeg was built with --enable-nonfree. "+
				"The resulting binary is NOT redistributable.")
	case av.LicenseGPL2, av.LicenseGPL3:
		fmt.Fprintf(os.Stderr,
			"NOTICE: Linked FFmpeg was built with --enable-gpl. "+
				"The combined binary is licensed under %s, not LGPL-2.1-or-later.\n", lic)
	}
}

func usage() {
	fmt.Print(`Usage: mediamolder <command> [args]

Commands:
  run <config.json>      Execute a pipeline from a JSON config file.
                         Flags: --json (JSON progress), --metadata-out=PATH (write metadata as JSONL),
                                --set KEY=VALUE (substitute {{KEY}} in the job JSON; may be repeated).
  inspect <config.json>  Validate and pretty-print the resolved pipeline config.
  convert-cmd "..."      Convert an FFmpeg command line to JSON config.
  migrate <config.json>  Validate config and pretty-print (v1.0 migration scaffolding).
  list-codecs            List available codecs.
  list-filters           List available filters.
  list-formats           List available formats.
  list-processors        List registered go_processor processors.
  gui                    Launch the browser-based visual job editor.
                         Flags: --port=N, --host=ADDR, --no-open, --dev, --examples=DIR.
  version                Print library versions.
  help                   Show this help.
`)
}

func cmdListProcessors(_ []string) error {
	names := processors.Names()
	sort.Strings(names)
	if len(names) == 0 {
		fmt.Println("(no processors registered)")
		return nil
	}
	for _, name := range names {
		fmt.Println(name)
	}
	return nil
}
