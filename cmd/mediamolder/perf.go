// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// perfSnapshot mirrors the JSON encoding of pipeline.MetricsSnapshot.
// Defined locally so this binary has no CGO or pipeline dependencies.
type perfSnapshot struct {
	State   string     `json:"State"`
	Elapsed int64      `json:"Elapsed"` // nanoseconds
	Perf    []nodePerf `json:"Perf"`
}

// nodePerf mirrors the JSON encoding of pipeline.NodePerfSnapshot.
type nodePerf struct {
	NodeID            string  `json:"NodeID"`
	FPS               float64 `json:"FPS"`
	FPSTarget         float64 `json:"FPSTarget"`
	FPSDeficit        float64 `json:"FPSDeficit"`
	ActiveFrac        float64 `json:"ActiveFrac"`
	IdleFrac          float64 `json:"IdleFrac"`
	StalledFrac       float64 `json:"StalledFrac"`
	StallCount        int64   `json:"StallCount"`
	MaxStallDuration  int64   `json:"MaxStallDuration"`  // nanoseconds
	QueueFillFrac     float64 `json:"QueueFillFrac"`
	ThreadsConfigured int     `json:"ThreadsConfigured"`
	ThreadMode        string  `json:"ThreadMode"`
	ThreadsBusy       int     `json:"ThreadsBusy"`
	EstimatedCPUCores float64 `json:"EstimatedCPUCores"`
	FrameLatencyMean  int64   `json:"FrameLatencyMean"` // nanoseconds
}

const (
	ansiHome   = "\033[H"
	ansiClear  = "\033[2J"
	ansiBold   = "\033[1m"
	ansiReset  = "\033[0m"
	ansiGreen  = "\033[32m"
	ansiYellow = "\033[33m"
	ansiRed    = "\033[31m"
)

// tableWidth is the total character width of the printed table.
const tableWidth = 104

func runPerf(args []string) {
	fs := flag.NewFlagSet("perf", flag.ExitOnError)
	url := fs.String("url", "http://localhost:9090/perf", "URL of the pipeline /perf JSON endpoint")
	interval := fs.Duration("interval", time.Second, "refresh interval")
	_ = fs.Parse(args)

	client := &http.Client{Timeout: 5 * time.Second}

	// Clear the screen once, then use cursor-home for in-place updates.
	fmt.Print(ansiClear + ansiHome)

	tick := time.NewTicker(*interval)
	defer tick.Stop()

	// Render immediately before the first tick.
	renderPerf(client, *url)

	for range tick.C {
		renderPerf(client, *url)
	}
}

func renderPerf(client *http.Client, url string) {
	snap, err := fetchPerf(client, url)

	// Move to top-left for in-place table redraw.
	fmt.Print(ansiHome)

	if err != nil {
		fmt.Printf("%smediamolder perf%s  %serror: %v%s\n",
			ansiBold, ansiReset, ansiRed, err, ansiReset)
		fmt.Printf("  url: %s\n", url)
		return
	}

	elapsed := time.Duration(snap.Elapsed).Truncate(time.Second)
	fmt.Printf("%smediamolder perf%s  state=%-10s elapsed=%s\n\n",
		ansiBold, ansiReset, snap.State, elapsed)

	if len(snap.Perf) == 0 {
		fmt.Println("  (no performance data — pipeline not running?)")
		return
	}

	// Header row.
	fmt.Printf("%s%-20s %8s %8s %8s %8s %8s %8s %8s %6s %10s%s\n",
		ansiBold,
		"NODE", "FPS", "TARGET", "DEFICIT", "ACTIVE%", "IDLE%", "STALL%", "THREADS", "BUSY", "LATENCY",
		ansiReset)
	fmt.Println(strings.Repeat("─", tableWidth))

	for _, p := range snap.Perf {
		color := ansiGreen
		if p.FPSTarget > 0 && p.FPSDeficit > 1.0 {
			color = ansiRed
		} else if p.FPSTarget > 0 && p.FPSDeficit > 0.2 {
			color = ansiYellow
		}

		busy := fmt.Sprintf("%d", p.ThreadsBusy)
		if p.ThreadsBusy < 0 {
			busy = "n/a"
		}

		latency := "n/a"
		if p.FrameLatencyMean > 0 {
			latency = time.Duration(p.FrameLatencyMean).Truncate(time.Millisecond).String()
		}

		fmt.Printf("%s%-20s%s %8.1f %8.1f %8.2f %7.1f%% %7.1f%% %7.1f%% %8d %6s %10s\n",
			color, truncate(p.NodeID, 20), ansiReset,
			p.FPS,
			p.FPSTarget,
			p.FPSDeficit,
			p.ActiveFrac*100,
			p.IdleFrac*100,
			p.StalledFrac*100,
			p.ThreadsConfigured,
			busy,
			latency,
		)
	}

	fmt.Println()
	fmt.Printf("  Press Ctrl-C to exit. Refreshes every %s.\n",
		time.Duration(snap.Elapsed/int64(len(snap.Perf)+1)).Truncate(time.Millisecond))
}

func fetchPerf(client *http.Client, url string) (*perfSnapshot, error) {
	resp, err := client.Get(url) //nolint:noctx
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d from %s", resp.StatusCode, url)
	}
	var snap perfSnapshot
	if err := json.NewDecoder(resp.Body).Decode(&snap); err != nil {
		return nil, fmt.Errorf("decode: %w", err)
	}
	return &snap, nil
}

// truncate returns s truncated to at most n runes, appending "…" if truncated.
func truncate(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n-1]) + "…"
}

func init() {
	// Ensure stderr is used for any flag parse errors in runPerf.
	flag.CommandLine.SetOutput(os.Stderr)
}
