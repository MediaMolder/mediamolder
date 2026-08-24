// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdTraceHeaders implements the `mediamolder trace-headers` subcommand:
// an improved, machine-readable version of FFmpeg's trace_headers
// bitstream filter. It scans an elementary video bitstream (H.264, H.265
// or AV1, in any container libavformat opens) without decoding and reports
// NAL unit / OBU details as JSON, JSON Lines, or trace_headers-format
// text.
//
// Usage:
//
//	mediamolder trace-headers [flags] <input>
//
// Flags:
//
//	--stream <spec>       stream to trace: v:N or an absolute index (default v:0)
//	--output <path>       output file (- = stdout, default)
//	--format <fmt>        json (default), jsonl, text
//	--detail <level>      summary, headers, elements (default elements)
//	--units <list>        comma-separated unit-type filter (names or numbers)
//	--max-packets <n>     stop after n packets
//	--range <a:b>         only packets with index in [a, b]

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"

	"github.com/MediaMolder/MediaMolder/cbs/report"
	"github.com/MediaMolder/MediaMolder/processors"
)

func cmdTraceHeaders(args []string) error {
	fs := flag.NewFlagSet("trace-headers", flag.ContinueOnError)
	var (
		stream     = fs.String("stream", "v:0", "stream to trace: v:N or an absolute index")
		output     = fs.String("output", "-", "output file (- = stdout)")
		format     = fs.String("format", "json", "output format: json, jsonl, text")
		detail     = fs.String("detail", "elements", "detail level: summary, headers, elements")
		units      = fs.String("units", "", "comma-separated unit-type filter (names or numbers)")
		maxPackets = fs.Int64("max-packets", 0, "stop after n packets (0 = all)")
		rangeSpec  = fs.String("range", "", "packet index range a:b (inclusive)")
	)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder trace-headers [flags] <input>

Scan an elementary video bitstream (H.264, H.265, AV1) at the packet level
and report NAL unit / OBU details. --format text reproduces FFmpeg's
"-bsf:v trace_headers" output; json and jsonl are structured reports with
per-unit summaries. No decoding is performed.

Flags:
  --stream <spec>       Stream to trace: v:N or an absolute index (default v:0)
  --output <path>       Output file (- = stdout, default)
  --format <fmt>        Output format: json (default), jsonl, text
  --detail <level>      summary, headers, elements (default elements)
  --units <list>        Comma-separated unit-type filter (e.g. sps,pps,sei or 7,8,6)
  --max-packets <n>     Stop after n packets
  --range <a:b>         Only packets with index in [a, b]

`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("trace-headers: exactly one input expected")
	}

	cfg := processors.TraceConfig{
		URL:    fs.Arg(0),
		Stream: *stream,
		Options: report.Options{
			Format:     *format,
			Detail:     *detail,
			MaxPackets: *maxPackets,
		},
	}
	if *units != "" {
		cfg.Options.UnitTypes = strings.Split(*units, ",")
	}
	if *rangeSpec != "" {
		parts := strings.SplitN(*rangeSpec, ":", 2)
		if len(parts) != 2 {
			return fmt.Errorf("trace-headers: --range wants a:b, got %q", *rangeSpec)
		}
		a, errA := strconv.ParseInt(parts[0], 10, 64)
		b, errB := strconv.ParseInt(parts[1], 10, 64)
		if errA != nil || errB != nil || a < 0 || b < a {
			return fmt.Errorf("trace-headers: invalid --range %q", *rangeSpec)
		}
		cfg.Options.Range = [2]int64{a, b}
	}

	out := os.Stdout
	if *output != "-" && *output != "" {
		f, err := os.Create(*output)
		if err != nil {
			return fmt.Errorf("trace-headers: %w", err)
		}
		defer f.Close()
		out = f
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	return processors.RunBitstreamTrace(ctx, cfg, out)
}
