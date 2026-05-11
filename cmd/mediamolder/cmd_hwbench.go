// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdHWBench implements the `mediamolder hwbench` subcommand.
//
// Usage:
//
//	mediamolder hwbench [flags]
//
// Flags:
//
//	--device <name>     hardware device to benchmark (e.g. "cuda", "videotoolbox")
//	--codecs <list>     comma-separated encoder names (default: all supported)
//	--resolutions <list> comma-separated WxH values (default: 640x360,1280x720,1920x1080,3840x2160)
//	--frames N          frames to time per codec×resolution (default: 200)
//	--warmup N          warmup frames before timing (default: 20)
//	--output <path>     write JSON report to path (default: hwbench_report_<ts>.json)
//	--stdout            print JSON to stdout instead of file
//
// The generated report is designed for community contribution.  See the
// MediaMolder documentation for how to submit your results.

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

func cmdHWBench(args []string) error {
	fs := flag.NewFlagSet("hwbench", flag.ContinueOnError)

	deviceFlag := fs.String("device", "",
		"hardware device type to benchmark (e.g. cuda, videotoolbox, vaapi, qsv)")
	codecsFlag := fs.String("codecs", "",
		"comma-separated encoder names; empty = all supported for the device")
	resolutionsFlag := fs.String("resolutions", "",
		"comma-separated WxH targets, e.g. 1920x1080,3840x2160; empty = standard set")
	framesFlag := fs.Int("frames", 200,
		"number of frames to time per codec×resolution")
	warmupFlag := fs.Int("warmup", 20,
		"number of warmup frames before timing")
	outputFlag := fs.String("output", "",
		"path for JSON report (default: hwbench_report_<timestamp>.json)")
	stdoutFlag := fs.Bool("stdout", false,
		"write JSON report to stdout instead of a file")
	capsOnlyFlag := fs.Bool("caps-only", false,
		"query and print hardware capabilities without running a benchmark")

	if err := fs.Parse(args); err != nil {
		return err
	}

	// ── Open HW device if requested ──────────────────────────────────────────
	var hwDev *av.HWDeviceContext
	if *deviceFlag != "" {
		devType := av.ParseHWDeviceType(*deviceFlag)
		if devType == av.HWDeviceNone {
			return fmt.Errorf("unknown device type %q\n\nAvailable: %s",
				*deviceFlag, availableDeviceTypes())
		}
		var err error
		hwDev, err = av.OpenHWDevice(devType, "")
		if err != nil {
			return fmt.Errorf("cannot open device %q: %v", *deviceFlag, err)
		}
		defer hwDev.Close()
	}

	// ── Caps-only mode ────────────────────────────────────────────────────────
	if *capsOnlyFlag {
		return printHWCaps(hwDev)
	}

	// ── Build BenchmarkConfig ─────────────────────────────────────────────────
	cfg := av.BenchmarkConfig{
		HWDevice:      hwDev,
		WarmupFrames:  *warmupFlag,
		MeasureFrames: *framesFlag,
	}

	if *codecsFlag != "" {
		cfg.Codecs = splitTrimmed(*codecsFlag, ",")
	}

	if *resolutionsFlag != "" {
		res, err := parseResolutions(*resolutionsFlag)
		if err != nil {
			return fmt.Errorf("invalid --resolutions: %v", err)
		}
		cfg.Resolutions = res
	}

	// ── Run benchmark ─────────────────────────────────────────────────────────
	fmt.Fprintln(os.Stderr, "Running benchmark… (this may take several minutes)")
	ctx := context.Background()
	report, err := av.RunBenchmark(ctx, cfg)
	if err != nil {
		return fmt.Errorf("benchmark failed: %v", err)
	}

	// ── Determine output path ─────────────────────────────────────────────────
	var outPath string
	if !*stdoutFlag {
		outPath = *outputFlag
		if outPath == "" {
			ts := time.Now().UTC().Format("20060102T150405Z")
			outPath = fmt.Sprintf("hwbench_report_%s.json", ts)
		}
	}

	// ── Serialise report ──────────────────────────────────────────────────────
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")

	if *stdoutFlag {
		return enc.Encode(report)
	}

	f, err := os.Create(outPath)
	if err != nil {
		return fmt.Errorf("cannot create output file: %v", err)
	}
	defer f.Close()

	enc2 := json.NewEncoder(f)
	enc2.SetIndent("", "  ")
	if err := enc2.Encode(report); err != nil {
		return err
	}

	// Print summary to stdout.
	printBenchSummary(report)
	fmt.Printf("\nReport written to: %s\n", outPath)
	fmt.Println("\nTo contribute your results, see: https://github.com/MediaMolder/MediaMolder/wiki/HW-Benchmark-Contributions")
	return nil
}

// printHWCaps prints a structured hardware capability summary for the device.
func printHWCaps(dev *av.HWDeviceContext) error {
	if dev == nil {
		fmt.Println("No hardware device specified (use --device <type>).")
		fmt.Println("Available device types:")
		for _, p := range av.ProbeHWDevices() {
			if p.Available {
				fmt.Printf("  %s\n", p.Type.String())
			}
		}
		return nil
	}

	caps := dev.QueryCapabilities()

	fmt.Printf("Device:      %s\n", displayOrFallback(caps.DisplayName, dev.Type().String()))
	if caps.CUDAArch != "" {
		fmt.Printf("CUDA arch:   %s (SM %d.%d)\n", caps.CUDAArch, caps.CUDASMMajor, caps.CUDASMMinor)
	}
	if caps.MaxWidth > 0 {
		fmt.Printf("Max res:     %dx%d\n", caps.MaxWidth, caps.MaxHeight)
	}

	fmt.Printf("\nCodecs (%d):\n", len(caps.Codecs))
	for _, c := range caps.Codecs {
		note := ""
		if c.Note != "" {
			note = "  [" + c.Note + "]"
		}
		fmt.Printf("  %-30s %s%s\n", c.Name, c.Role, note)
	}

	if len(caps.Filters) > 0 {
		fmt.Printf("\nHW filters: %s\n", strings.Join(caps.Filters, ", "))
	}

	if len(caps.NVENCCaps) > 0 {
		fmt.Printf("\nNVENC capabilities:\n")
		for _, nc := range caps.NVENCCaps {
			fmt.Printf("  %-20s max %dx%d  engines=%d  MB/s=%d  10bit=%v  yuv444=%v\n",
				nc.CodecName, nc.MaxWidth, nc.MaxHeight,
				nc.NumEncoderEngines, nc.MBPerSecMax,
				nc.Support10Bit, nc.SupportYUV444)
		}
	}

	if len(caps.NVDECCaps) > 0 {
		// Summarise by codec (show max resolution across chroma/bit-depth combos).
		type summary struct{ maxW, maxH, nvdecs int }
		byCodec := map[string]*summary{}
		for _, nd := range caps.NVDECCaps {
			s, ok := byCodec[nd.CodecName]
			if !ok {
				s = &summary{}
				byCodec[nd.CodecName] = s
			}
			if nd.MaxWidth > s.maxW {
				s.maxW = nd.MaxWidth
			}
			if nd.MaxHeight > s.maxH {
				s.maxH = nd.MaxHeight
			}
			if nd.NumNVDECs > s.nvdecs {
				s.nvdecs = nd.NumNVDECs
			}
		}
		fmt.Printf("\nNVDEC capabilities (summary):\n")
		for name, s := range byCodec {
			fmt.Printf("  %-26s max %dx%d  engines=%d\n",
				name, s.maxW, s.maxH, s.nvdecs)
		}
	}

	// JSON output of the full caps structure.
	fmt.Printf("\nFull capabilities as JSON:\n")
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(caps)
}

// printBenchSummary prints a human-readable table of benchmark results.
func printBenchSummary(report *av.BenchmarkReport) {
	fmt.Printf("\n%-25s %-12s %10s %10s %12s\n",
		"Codec", "Resolution", "Enc fps", "Dec fps", "Bitrate Mbps")
	fmt.Println(strings.Repeat("-", 75))
	for _, r := range report.Results {
		if r.Err != "" {
			fmt.Printf("  %-23s %-12s  SKIPPED: %s\n", r.Codec, r.Resolution.String(), r.Err)
			continue
		}
		fmt.Printf("  %-23s %-12s %10.1f %10.1f %12.2f\n",
			r.Codec, r.Resolution.String(),
			r.EncodeFPS, r.DecodeFPS, r.EncodeBitrateMbps)
	}
}

// ── Helpers ───────────────────────────────────────────────────────────────────

func availableDeviceTypes() string {
	var names []string
	for _, p := range av.ProbeHWDevices() {
		if p.Available {
			names = append(names, p.Type.String())
		}
	}
	if len(names) == 0 {
		return "(none detected)"
	}
	return strings.Join(names, ", ")
}

func splitTrimmed(s, sep string) []string {
	parts := strings.Split(s, sep)
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if t := strings.TrimSpace(p); t != "" {
			out = append(out, t)
		}
	}
	return out
}

func parseResolutions(s string) ([]av.Resolution, error) {
	parts := splitTrimmed(s, ",")
	out := make([]av.Resolution, 0, len(parts))
	for _, p := range parts {
		// Accept "WxH" or "W:H".
		p = strings.ReplaceAll(p, ":", "x")
		wh := strings.SplitN(p, "x", 2)
		if len(wh) != 2 {
			return nil, fmt.Errorf("expected WxH, got %q", p)
		}
		w, err := strconv.Atoi(strings.TrimSpace(wh[0]))
		if err != nil || w <= 0 {
			return nil, fmt.Errorf("invalid width in %q", p)
		}
		h, err := strconv.Atoi(strings.TrimSpace(wh[1]))
		if err != nil || h <= 0 {
			return nil, fmt.Errorf("invalid height in %q", p)
		}
		// Align to 16 (macroblock boundary) to avoid encoder rejections.
		w = (w + 15) &^ 15
		h = (h + 15) &^ 15
		out = append(out, av.Resolution{Width: w, Height: h})
	}
	return out, nil
}

func displayOrFallback(display, fallback string) string {
	if display != "" {
		return display
	}
	return fallback
}
