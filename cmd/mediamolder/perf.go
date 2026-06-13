// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// perfSnapshot mirrors the JSON encoding of job.MetricsSnapshot.
// Defined locally so this binary has no CGO or pipeline dependencies.
type perfSnapshot struct {
	State   string     `json:"State"`
	Elapsed int64      `json:"Elapsed"` // nanoseconds
	Perf    []nodePerf `json:"Perf"`
}

// nodePerf mirrors the JSON encoding of job.NodePerfSnapshot.
type nodePerf struct {
	NodeID            string  `json:"NodeID"`
	FPS               float64 `json:"FPS"`
	FPSTarget         float64 `json:"FPSTarget"`
	FPSDeficit        float64 `json:"FPSDeficit"`
	ActiveFrac        float64 `json:"ActiveFrac"`
	IdleFrac          float64 `json:"IdleFrac"`
	StalledFrac       float64 `json:"StalledFrac"`
	StallCount        int64   `json:"StallCount"`
	MaxStallDuration  int64   `json:"MaxStallDuration"` // nanoseconds
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
	ansiCyan   = "\033[36m"
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

// ── Phase 8: watch mode (SSE-based RT controller inspector) ────────────────

// watchNode mirrors snap.ControllerNodeSnapshot for JSON decoding.
type watchNode struct {
	NodeID               string  `json:"NodeID"`
	FPS                  float64 `json:"FPS"`
	FPSTarget            float64 `json:"FPSTarget"`
	FPSDeficit           float64 `json:"FPSDeficit"`
	ActiveFrac           float64 `json:"ActiveFrac"`
	StalledFrac          float64 `json:"StalledFrac"`
	IdleFrac             float64 `json:"IdleFrac"`
	ThreadsConfigured    int     `json:"ThreadsConfigured"`
	ThreadsBusy          int     `json:"ThreadsBusy"`
	InputBufferFillFrac  float64 `json:"InputBufferFillFrac"`
	OutputBufferFillFrac float64 `json:"OutputBufferFillFrac"`
	FrameLatencyMean     int64   `json:"FrameLatencyMean"` // nanoseconds
	CurrentPreset        string  `json:"CurrentPreset"`
	PresetLocked         bool    `json:"PresetLocked"`
	CooldownRemaining    int     `json:"CooldownRemaining"`
	OvershootWindows     int     `json:"OvershootWindows"`
}

// watchSink mirrors snap.SinkNodeSnapshot for JSON decoding.
type watchSink struct {
	NodeID               string  `json:"NodeID"`
	OutputBufferFillFrac float64 `json:"OutputBufferFillFrac"`
}

// watchDecision mirrors snap.DecisionRecord for JSON decoding.
type watchDecision struct {
	Time    string  `json:"time"`
	NodeID  string  `json:"node"`
	Action  string  `json:"action"`
	From    string  `json:"from"`
	To      string  `json:"to"`
	Deficit float64 `json:"deficit"`
	Reason  string  `json:"reason"`
}

// watchSnapshot mirrors snap.RTControllerSnapshot for JSON decoding.
type watchSnapshot struct {
	Enabled              bool            `json:"Enabled"`
	Status               string          `json:"Status"`
	Tick                 int64           `json:"Tick"`
	Elapsed              int64           `json:"Elapsed"` // nanoseconds
	FPSTarget            float64         `json:"FPSTarget"`
	FPSActual            float64         `json:"FPSActual"`
	Satisfied            bool            `json:"Satisfied"`
	HighestQualityPreset string          `json:"HighestQualityPreset"`
	GroupStep            bool            `json:"GroupStep"`
	CooldownWindows      int             `json:"CooldownWindows"`
	TickIntervalMs       int64           `json:"TickIntervalMs"`
	Nodes                []watchNode     `json:"Nodes"`
	Sinks                []watchSink     `json:"Sinks"`
	RecentDecisions      []watchDecision `json:"RecentDecisions"`
}

// blockBar returns a 4-character fill bar using block elements.
// frac is clamped to [0,1]; 0.0 → "░░░░", 1.0 → "████".
func blockBar(frac float64) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	filled := int(frac*4 + 0.5)
	return strings.Repeat("█", filled) + strings.Repeat("░", 4-filled)
}

// statusBadge returns a short coloured badge string for the controller status.
func statusBadge(status string, cooldown int) string {
	switch status {
	case "satisfied":
		return ansiGreen + "OK" + ansiReset
	case "cooldown":
		return ansiYellow + fmt.Sprintf("CD:%d", cooldown) + ansiReset
	case "dropping":
		return ansiRed + "DROP" + ansiReset
	case "disabled":
		return ansiRed + "OFF" + ansiReset
	default:
		return ansiCyan + "OBS" + ansiReset
	}
}

// renderWatch renders one RTControllerSnapshot to stdout using ANSI in-place
// rewrite (cursor-home, no full clear to avoid flicker).
func renderWatch(cs watchSnapshot) {
	fmt.Print(ansiHome)

	elapsed := time.Duration(cs.Elapsed).Truncate(time.Second)
	badge := statusBadge(cs.Status, cs.CooldownWindows)

	fmt.Printf("%smediamolder watch%s  tick=%-6d elapsed=%-8s fps=%.1f/%.1f  %s\n",
		ansiBold, ansiReset,
		cs.Tick, elapsed,
		cs.FPSActual, cs.FPSTarget,
		badge,
	)
	if cs.HighestQualityPreset != "" {
		groupStr := ""
		if cs.GroupStep {
			groupStr = " group-step=on"
		}
		fmt.Printf("  hq-preset=%s%s\n", cs.HighestQualityPreset, groupStr)
	}
	fmt.Println()

	if len(cs.Nodes) > 0 {
		// Column widths: NODE(20) FPS(6) TGT(6) DEF(6) ACT%(6) STL%(6) THRD(5) IN(6) OUT(6) PRESET(12) CD(4)
		const hdr = "%-20s %6s %6s %6s %6s %6s %5s  %-6s %-6s  %-12s %4s"
		const row = "%-20s %6.1f %6.1f %6.2f %5.1f%% %5.1f%% %5d  %-6s %-6s  %-12s %4s"
		fmt.Printf(ansiBold + "  ──── PERFORMANCE ────────────────────────────────  ──── APPLIED ────────────────" + ansiReset + "\n")
		fmt.Printf(ansiBold+"  "+hdr+ansiReset+"\n",
			"NODE", "FPS", "TGT", "DEF", "ACT%", "STL%", "THRD", "IN BUF", "OUT BUF", "PRESET", "CD")
		fmt.Println("  " + strings.Repeat("─", 88))

		for _, n := range cs.Nodes {
			color := ansiGreen
			if n.FPSTarget > 0 && n.FPSDeficit > 1.0 {
				color = ansiRed
			} else if n.FPSTarget > 0 && n.FPSDeficit > 0.2 {
				color = ansiYellow
			}
			preset := n.CurrentPreset
			if n.PresetLocked {
				preset += "🔒"
			}
			cdStr := ""
			if n.CooldownRemaining > 0 {
				cdStr = fmt.Sprintf("%d", n.CooldownRemaining)
			}
			fmt.Printf("  "+color+row+ansiReset+"\n",
				truncate(n.NodeID, 20),
				n.FPS, n.FPSTarget, n.FPSDeficit,
				n.ActiveFrac*100, n.StalledFrac*100,
				n.ThreadsConfigured,
				blockBar(n.InputBufferFillFrac),
				blockBar(n.OutputBufferFillFrac),
				truncate(preset, 12),
				cdStr,
			)
		}
		fmt.Println()
	}

	if len(cs.Sinks) > 0 {
		fmt.Printf(ansiBold + "  ──── SINKS ────" + ansiReset + "\n")
		fmt.Printf(ansiBold+"  %-20s  %s"+ansiReset+"\n", "NODE", "OUT BUF")
		fmt.Println("  " + strings.Repeat("─", 30))
		for _, s := range cs.Sinks {
			fmt.Printf("  %-20s  %s\n", truncate(s.NodeID, 20), blockBar(s.OutputBufferFillFrac))
		}
		fmt.Println()
	}

	if len(cs.RecentDecisions) > 0 {
		fmt.Printf(ansiBold+"  ──── RECENT DECISIONS (last %d) ────"+ansiReset+"\n", len(cs.RecentDecisions))
		// Show at most 5 most recent.
		decisions := cs.RecentDecisions
		if len(decisions) > 5 {
			decisions = decisions[len(decisions)-5:]
		}
		for _, d := range decisions {
			ts := d.Time
			if len(ts) > 19 {
				ts = ts[11:19] // extract HH:MM:SS from RFC3339
			}
			actionColor := ansiCyan
			if strings.HasPrefix(d.Action, "step_slower") || strings.HasPrefix(d.Action, "drop") {
				actionColor = ansiYellow
			}
			change := ""
			if d.From != "" && d.To != "" {
				change = fmt.Sprintf(" %s→%s", d.From, d.To)
			}
			fmt.Printf("  %s  %s%-14s%s %-16s %s%s\n",
				ts, actionColor, d.Action, ansiReset,
				truncate(d.NodeID, 16), d.Reason, change)
		}
		fmt.Println()
	}
}

// runWatch connects to the /realtime/snapshot/stream SSE endpoint and renders
// each incoming snapshot in-place on the terminal.
func runWatch(args []string) {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	baseURL := fs.String("url", "http://localhost:9090", "metrics server base URL (without path)")
	_ = fs.Parse(args)

	streamURL := strings.TrimRight(*baseURL, "/") + "/realtime/snapshot/stream"

	client := &http.Client{} // no timeout — SSE is long-lived
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, streamURL, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mediamolder watch: %v\n", err)
		os.Exit(1)
	}
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")

	resp, err := client.Do(req)
	if err != nil {
		fmt.Fprintf(os.Stderr, "mediamolder watch: connect %s: %v\n", streamURL, err)
		os.Exit(1)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		fmt.Fprintf(os.Stderr, "mediamolder watch: realtime mode is not active on %s\n", *baseURL)
		os.Exit(1)
	}
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintf(os.Stderr, "mediamolder watch: HTTP %d from %s\n", resp.StatusCode, streamURL)
		os.Exit(1)
	}

	// Clear screen once, then use cursor-home for in-place updates.
	fmt.Print(ansiClear + ansiHome)
	fmt.Printf("%smediamolder watch%s  connecting to %s …\n", ansiBold, ansiReset, streamURL)

	scanner := bufio.NewScanner(resp.Body)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "data: ") {
			// Handle SSE error events.
			if strings.HasPrefix(line, "event: error") {
				fmt.Fprintf(os.Stderr, "\nmediamolder watch: server sent error event — realtime mode ended\n")
				return
			}
			continue
		}
		data := line[6:] // strip "data: "
		var cs watchSnapshot
		if err := json.Unmarshal([]byte(data), &cs); err != nil {
			continue // skip malformed events
		}
		renderWatch(cs)
	}
	if err := scanner.Err(); err != nil {
		fmt.Fprintf(os.Stderr, "\nmediamolder watch: stream error: %v\n", err)
	}
}
