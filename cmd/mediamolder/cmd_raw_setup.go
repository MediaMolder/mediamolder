// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdRawSetup implements `mediamolder raw-setup`: a doctor that reports whether this binary can
// develop camera RAW (the bundled LibRaw is built in) and, if not, prints exactly how to enable
// it. Exits 0 only when RAW develop is ready. Mirrors the face-setup doctor.

import (
	"flag"
	"fmt"
	"os"

	"github.com/MediaMolder/MediaMolder/raw"
)

func cmdRawSetup(args []string) error {
	fs := flag.NewFlagSet("raw-setup", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder raw-setup

Diagnose camera-RAW (LibRaw) readiness — whether the bundled, version-pinned LibRaw is built
into this binary — and print exactly how to enable it if not. Exits 0 only when RAW develop is
ready to run.
`)
	}
	if err := fs.Parse(args); err != nil {
		return err
	}

	out := os.Stdout
	fmt.Fprintf(out, "MediaMolder — camera-RAW (LibRaw) setup\n\n")
	fmt.Fprintf(out, "Pinned LibRaw version: %s\n", raw.LibRawVersion)

	if raw.Capable() {
		fmt.Fprintln(out, "✅ Camera-RAW develop is ready (LibRaw is built in).")
		return nil
	}

	fmt.Fprintln(out, "✗ This binary was built WITHOUT LibRaw (no with_libraw tag).")
	fmt.Fprintln(out, "  → build the bundled LibRaw:  scripts/bundle-libraw.sh")
	fmt.Fprintln(out, "  → CLI:  go build -tags with_libraw ./cmd/mediamolder")
	fmt.Fprintln(out, "  → GUI:  make build-gui-libraw")
	return fmt.Errorf("camera-RAW develop is not ready (see above)")
}
