// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"syscall"
	"time"

	"github.com/MediaMolder/MediaMolder/internal/gui"
)

func cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	port := fs.Int("port", 8080, "HTTP listen port")
	host := fs.String("host", "127.0.0.1", "HTTP listen host")
	noOpen := fs.Bool("no-open", false, "do not auto-open the browser")
	dev := fs.Bool("dev", false, "dev mode: do not serve embedded frontend (use Vite on :5173)")
	examples := fs.String("examples", "testdata/examples",
		"directory of example job JSONs to expose at /examples/ and /api/examples (empty to disable)")
	metricsAddr := fs.String("metrics-addr", "", "start a metrics/perf server on this address (e.g. :9090); exposes /perf, /perf/stream, /metrics (Prometheus), /health and (for realtime jobs) /realtime/* endpoints")
	if err := fs.Parse(args); err != nil {
		return err
	}

	addr := fmt.Sprintf("%s:%d", *host, *port)

	// Resolve examples dir (silently disable if missing).
	examplesDir := ""
	if *examples != "" {
		if info, err := os.Stat(*examples); err == nil && info.IsDir() {
			examplesDir = *examples
		} else {
			fmt.Fprintf(os.Stderr, "mediamolder gui: examples dir %q not found, examples disabled\n", *examples)
		}
	}

	srv, err := gui.NewServer(gui.Options{
		Addr:        addr,
		Dev:         *dev,
		ExamplesDir: examplesDir,
		MetricsAddr: *metricsAddr,
	})
	if err != nil {
		return err
	}

	// Choose URL to display/open.
	url := fmt.Sprintf("http://%s/", addr)
	if *dev {
		url = "http://127.0.0.1:5173/"
	}

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	go func() {
		<-ctx.Done()
		shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer shutdownCancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	fmt.Fprintf(os.Stderr, "mediamolder gui listening on %s\n", addr)
	if *dev {
		fmt.Fprintln(os.Stderr, "dev mode: start the Vite dev server with `cd frontend && npm run dev`")
	}
	if !*noOpen {
		_ = openBrowser(url)
	}

	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func openBrowser(url string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", url)
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	default:
		cmd = exec.Command("xdg-open", url)
	}
	return cmd.Start()
}
