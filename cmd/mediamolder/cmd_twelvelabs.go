// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/twelvelabs"
)

// twelvelabsConfigFile is the path to the on-disk fallback secrets store.
// Format: {"api_key": "..."}.
var twelvelabsConfigFile = filepath.Join(os.Getenv("HOME"), ".config", "mediamolder", "twelvelabs.json")

func cmdTwelveLabs(args []string) error {
	if len(args) == 0 {
		twelvelabsUsage()
		return nil
	}
	switch args[0] {
	case "index":
		return tlCmdIndex(args[1:])
	case "analyze":
		return tlCmdAnalyze(args[1:])
	case "search":
		return tlCmdSearch(args[1:])
	case "embed":
		return tlCmdEmbed(args[1:])
	case "indexes":
		return tlCmdIndexes(args[1:])
	case "help", "--help", "-h":
		twelvelabsUsage()
		return nil
	default:
		return fmt.Errorf("unknown twelvelabs subcommand %q\nRun 'mediamolder twelvelabs help' for usage", args[0])
	}
}

func twelvelabsUsage() {
	fmt.Print(`Usage: mediamolder twelvelabs <subcommand> [args]

Subcommands:
  index    --index <id> <file>           Upload a video file to an index.
                                         Flags: --wait (default true), --json.
  analyze  --video-id <id> --prompt "…"  Run Pegasus analyze on an indexed video.
                                         Flags: --segments, --temperature=F, --json.
  search   --index <id> --query "…"      Run Marengo search.
                                         Flags: --options=visual,audio (CSV),
                                                --threshold=low|medium|high,
                                                --page-limit=N, --json.
  embed    --video <file>                Generate a video embedding.
                                         Flags: --model=marengo3.0,
                                                --scopes=clip,video (CSV),
                                                --window=SECONDS,
                                                --out=PATH (default stdout),
                                                --format=json|jsonl.
  indexes list                           List all indexes (JSON).
  indexes create --name N --models M[,M] Create a new index.
  indexes delete <id>                    Delete an index by ID.

Global flags (any subcommand):
  --api-key=KEY     API key (overrides env and config file).
  --base-url=URL    Override https://api.twelvelabs.io/v1.3 (for tests).
  --format=json|text  Output format (default json).

Authentication precedence:
  --api-key flag → TWELVELABS_API_KEY env → ~/.config/mediamolder/twelvelabs.json
`)
}

// tlGlobalFlags holds the auth / transport flags shared by every subcommand.
type tlGlobalFlags struct {
	apiKey  string
	baseURL string
	format  string
}

func (g *tlGlobalFlags) register(fs *flag.FlagSet) {
	fs.StringVar(&g.apiKey, "api-key", "", "TwelveLabs API key (overrides env and config file)")
	fs.StringVar(&g.baseURL, "base-url", "", "API base URL override (for tests)")
	fs.StringVar(&g.format, "format", "json", "output format: json or text")
}

// resolveAPIKey applies the precedence: --api-key → env → config file.
func resolveAPIKey(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("TWELVELABS_API_KEY"); env != "" {
		return env, nil
	}
	data, err := os.ReadFile(twelvelabsConfigFile)
	if err != nil {
		return "", fmt.Errorf("no API key (--api-key not set, TWELVELABS_API_KEY env empty, %s missing)", twelvelabsConfigFile)
	}
	var cfg struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", twelvelabsConfigFile, err)
	}
	if cfg.APIKey == "" {
		return "", fmt.Errorf("api_key empty in %s", twelvelabsConfigFile)
	}
	return cfg.APIKey, nil
}

// buildClient constructs a Client from the global flags.
func buildClient(g *tlGlobalFlags) (*twelvelabs.Client, error) {
	key, err := resolveAPIKey(g.apiKey)
	if err != nil {
		return nil, err
	}
	c := twelvelabs.New(key)
	if g.baseURL != "" {
		c.BaseURL = g.baseURL
	}
	return c, nil
}

// emit writes v as JSON (one line, pretty) to stdout.
func emit(v any) error {
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// --- index ---

func tlCmdIndex(args []string) error {
	fs := flag.NewFlagSet("twelvelabs index", flag.ContinueOnError)
	var (
		g       tlGlobalFlags
		indexID string
		wait    bool
	)
	g.register(fs)
	fs.StringVar(&indexID, "index", "", "index ID (required)")
	fs.BoolVar(&wait, "wait", true, "wait for the indexing task to reach ready")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if indexID == "" {
		return fmt.Errorf("--index is required")
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("exactly one input file required")
	}
	file := fs.Arg(0)
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	ctx := context.Background()
	task, err := c.CreateIndexTask(ctx, indexID, twelvelabs.TaskSource{File: file})
	if err != nil {
		return err
	}
	out := map[string]any{
		"task_id":  task.ID,
		"index_id": task.IndexID,
		"video_id": task.VideoID,
		"status":   task.Status,
	}
	if wait {
		done, werr := c.WaitForTask(ctx, task.ID, twelvelabs.WaitOpts{})
		if werr != nil {
			return werr
		}
		out["status"] = done.Status
		out["video_id"] = done.VideoID
	}
	return emit(out)
}

// --- analyze ---

func tlCmdAnalyze(args []string) error {
	fs := flag.NewFlagSet("twelvelabs analyze", flag.ContinueOnError)
	var (
		g           tlGlobalFlags
		videoID     string
		videoURL    string
		prompt      string
		segments    bool
		temperature float64
	)
	g.register(fs)
	fs.StringVar(&videoID, "video-id", "", "already-indexed video ID")
	fs.StringVar(&videoURL, "video-url", "", "remote video URL (one-shot)")
	fs.StringVar(&prompt, "prompt", "Describe what happens in this video.", "Pegasus prompt")
	fs.BoolVar(&segments, "segments", false, "request structured timestamped chapters")
	fs.Float64Var(&temperature, "temperature", 0.2, "sampling temperature")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if videoID == "" && videoURL == "" {
		return fmt.Errorf("--video-id or --video-url is required")
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	result, err := c.Analyze(context.Background(), twelvelabs.AnalyzeRequest{
		VideoID:     videoID,
		VideoURL:    videoURL,
		Prompt:      prompt,
		Temperature: float32(temperature),
		Segments:    segments,
	})
	if err != nil {
		return err
	}
	return emit(result)
}

// --- search ---

func tlCmdSearch(args []string) error {
	fs := flag.NewFlagSet("twelvelabs search", flag.ContinueOnError)
	var (
		g         tlGlobalFlags
		indexID   string
		query     string
		mediaURL  string
		options   string
		threshold string
		pageLimit int
	)
	g.register(fs)
	fs.StringVar(&indexID, "index", "", "index ID (required)")
	fs.StringVar(&query, "query", "", "natural-language query text")
	fs.StringVar(&mediaURL, "query-media-url", "", "image/audio query URL")
	fs.StringVar(&options, "options", "visual,audio", "comma-separated search options")
	fs.StringVar(&threshold, "threshold", "medium", "low|medium|high")
	fs.IntVar(&pageLimit, "page-limit", 0, "page size (0 = API default)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if indexID == "" {
		return fmt.Errorf("--index is required")
	}
	if query == "" && mediaURL == "" {
		return fmt.Errorf("--query or --query-media-url is required")
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	results, err := c.Search(context.Background(), twelvelabs.SearchRequest{
		IndexID:       indexID,
		Query:         query,
		QueryMediaURL: mediaURL,
		SearchOptions: splitCSV(options),
		Threshold:     threshold,
		PageLimit:     pageLimit,
	})
	if err != nil {
		return err
	}
	return emit(map[string]any{"matches": results, "count": len(results)})
}

// --- embed ---

func tlCmdEmbed(args []string) error {
	fs := flag.NewFlagSet("twelvelabs embed", flag.ContinueOnError)
	var (
		g         tlGlobalFlags
		video     string
		videoURL  string
		model     string
		scopes    string
		windowS   float64
		outPath   string
		outFormat string
	)
	g.register(fs)
	fs.StringVar(&video, "video", "", "local video file")
	fs.StringVar(&videoURL, "video-url", "", "remote video URL (alternative to --video)")
	fs.StringVar(&model, "model", "marengo3.0", "embedding model")
	fs.StringVar(&scopes, "scopes", "clip", "comma-separated scopes (clip,video)")
	fs.Float64Var(&windowS, "window", 6, "time_segment_duration in seconds")
	fs.StringVar(&outPath, "out", "", "write embeddings to PATH (default: stdout)")
	fs.StringVar(&outFormat, "format-out", "json", "output format: json or jsonl")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if video == "" && videoURL == "" {
		return fmt.Errorf("--video or --video-url is required")
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	src := twelvelabs.EmbedSource{File: video, URL: videoURL}
	opts := twelvelabs.EmbedOpts{Model: model, Scopes: splitCSV(scopes), WindowS: windowS}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	task, err := c.EmbedVideo(ctx, src, opts)
	if err != nil {
		return err
	}
	done, err := c.WaitForEmbedTask(ctx, task.ID, twelvelabs.WaitOpts{})
	if err != nil {
		return err
	}
	return writeEmbeddings(done.Embeddings, outPath, outFormat)
}

func writeEmbeddings(embs []twelvelabs.Embedding, outPath, format string) error {
	var w io.Writer = os.Stdout
	if outPath != "" {
		f, err := os.Create(outPath)
		if err != nil {
			return err
		}
		defer f.Close()
		w = f
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	switch strings.ToLower(format) {
	case "jsonl":
		flat := json.NewEncoder(w)
		for _, e := range embs {
			if err := flat.Encode(embeddingRecord(e)); err != nil {
				return err
			}
		}
		return nil
	case "json", "":
		recs := make([]map[string]any, len(embs))
		for i, e := range embs {
			recs[i] = embeddingRecord(e)
		}
		return enc.Encode(recs)
	default:
		return fmt.Errorf("unsupported --format-out %q (want json or jsonl)", format)
	}
}

func embeddingRecord(e twelvelabs.Embedding) map[string]any {
	return map[string]any{
		"scope":   e.Scope,
		"start_s": e.StartS,
		"end_s":   e.EndS,
		"vector":  e.Vector,
	}
}

// --- indexes list/create/delete ---

func tlCmdIndexes(args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: mediamolder twelvelabs indexes <list|create|delete> [...]")
	}
	switch args[0] {
	case "list":
		return tlCmdIndexesList(args[1:])
	case "create":
		return tlCmdIndexesCreate(args[1:])
	case "delete":
		return tlCmdIndexesDelete(args[1:])
	default:
		return fmt.Errorf("unknown indexes subcommand %q", args[0])
	}
}

func tlCmdIndexesList(args []string) error {
	fs := flag.NewFlagSet("twelvelabs indexes list", flag.ContinueOnError)
	var g tlGlobalFlags
	g.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	indexes, err := c.ListIndexes(context.Background())
	if err != nil {
		return err
	}
	return emit(indexes)
}

func tlCmdIndexesCreate(args []string) error {
	fs := flag.NewFlagSet("twelvelabs indexes create", flag.ContinueOnError)
	var (
		g      tlGlobalFlags
		name   string
		models string
	)
	g.register(fs)
	fs.StringVar(&name, "name", "", "index name (required)")
	fs.StringVar(&models, "models", "marengo3.0", "comma-separated model names")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if name == "" {
		return fmt.Errorf("--name is required")
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	specs := make([]twelvelabs.ModelSpec, 0)
	for _, m := range splitCSV(models) {
		specs = append(specs, twelvelabs.ModelSpec{Name: m})
	}
	idx, err := c.CreateIndex(context.Background(), name, specs)
	if err != nil {
		return err
	}
	return emit(idx)
}

func tlCmdIndexesDelete(args []string) error {
	fs := flag.NewFlagSet("twelvelabs indexes delete", flag.ContinueOnError)
	var g tlGlobalFlags
	g.register(fs)
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		return fmt.Errorf("exactly one index ID required")
	}
	c, err := buildClient(&g)
	if err != nil {
		return err
	}
	if err := c.DeleteIndex(context.Background(), fs.Arg(0)); err != nil {
		return err
	}
	return emit(map[string]string{"deleted": fs.Arg(0)})
}

// splitCSV splits a comma-separated list, trimming whitespace and dropping empties.
func splitCSV(s string) []string {
	parts := strings.Split(s, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
