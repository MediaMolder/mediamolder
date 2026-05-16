// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/MediaMolder/MediaMolder/internal/gui"
)

func cmdGUI(args []string) error {
	fs := flag.NewFlagSet("gui", flag.ContinueOnError)
	port := fs.Int("port", 7042, "HTTP port to listen on")
	staticDir := fs.String("static", "", "path to frontend/dist; defaults to <binary-dir>/frontend/dist")
	if err := fs.Parse(args); err != nil {
		return err
	}

	// Resolve static directory.
	dir := *staticDir
	if dir == "" {
		exe, err := os.Executable()
		if err != nil {
			return fmt.Errorf("resolve executable: %w", err)
		}
		// Development: if running from the repo root (via `go run`), look for
		// frontend/dist in the working directory.
		cwd, _ := os.Getwd()
		candidates := []string{
			filepath.Join(cwd, "frontend", "dist"),
			filepath.Join(filepath.Dir(exe), "frontend", "dist"),
			filepath.Join(filepath.Dir(filepath.Dir(exe)), "frontend", "dist"),
		}
		for _, c := range candidates {
			if fi, err := os.Stat(c); err == nil && fi.IsDir() {
				dir = c
				break
			}
		}
		if dir == "" {
			return fmt.Errorf(
				"cannot find frontend/dist — build the frontend first with `npm run build` in the frontend/ directory, "+
					"or specify --static=/path/to/frontend/dist\n"+
					"(searched: %v)", candidates,
			)
		}
	}

	addr := fmt.Sprintf("http://localhost:%d", *port)
	fmt.Printf("MediaMolder GUI\n  Listening on %s\n  Press Ctrl-C to stop.\n", addr)

	// Try to open browser.
	go openBrowser(addr)

	srv := gui.New(*port, dir)
	return srv.ListenAndServe()
}

// openBrowser opens url in the default browser. Failures are silently ignored.
func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd, args = "open", []string{url}
	case "windows":
		cmd, args = "cmd", []string{"/c", "start", url}
	default: // linux, etc.
		cmd, args = "xdg-open", []string{url}
	}
	c := exec.Command(cmd, args...)
	c.Stdout = nil
	c.Stderr = nil
	_ = c.Run()
}
