// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdRawDecode implements `mediamolder raw-decode`: develop a single camera-RAW file to a
// full-resolution 8-bit sRGB image via the bundled LibRaw. Always compiled; if the binary lacks
// LibRaw (no with_libraw tag) it fails with the exact command to enable it. libav renders camera
// RAW black, so this is the standalone way to get a real develop from a RAW.

import (
	"flag"
	"fmt"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"

	"github.com/MediaMolder/MediaMolder/raw"
)

func cmdRawDecode(args []string) error {
	fs := flag.NewFlagSet("raw-decode", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder raw-decode [flags] <input.raw>

Develop a camera-RAW file (NEF/CR2/CR3/ARW/RAF/ORF/RW2/PEF/SRW/DNG) to a full-resolution,
8-bit sRGB image using the bundled, version-pinned LibRaw (camera white balance, AHD demosaic,
no creative adjustments). Requires a build with -tags with_libraw.

Flags:
  -o, --output <path>    Output image path (default: input path with the format's extension)
  --format <png|jpeg>    Output format (default: inferred from --output, else png)
  --quality <1-100>      JPEG quality (default: 92)
`)
	}
	var output, format string
	var quality int
	fs.StringVar(&output, "output", "", "output image path")
	fs.StringVar(&output, "o", "", "output image path (shorthand)")
	fs.StringVar(&format, "format", "", "output format: png|jpeg")
	fs.IntVar(&quality, "quality", 92, "JPEG quality 1-100")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return fmt.Errorf("raw-decode: exactly one input file is required")
	}
	input := fs.Arg(0)

	if !raw.Capable() {
		return fmt.Errorf("raw-decode: this binary has no LibRaw — camera-RAW develop is unavailable\n"+
			"  → CLI: go build -tags with_libraw ./cmd/mediamolder\n"+
			"  → GUI: make build-gui-libraw\n"+
			"  (run scripts/bundle-libraw.sh first to build the bundled LibRaw %s)", raw.LibRawVersion)
	}
	if !raw.IsRAW(input) {
		return fmt.Errorf("raw-decode: %q is not a recognised camera-RAW file", input)
	}

	img, err := raw.Decode(input)
	if err != nil {
		return fmt.Errorf("raw-decode: %w", err)
	}

	if format == "" {
		switch strings.ToLower(filepath.Ext(output)) {
		case ".jpg", ".jpeg":
			format = "jpeg"
		default:
			format = "png"
		}
	}
	if output == "" {
		ext := ".png"
		if format == "jpeg" {
			ext = ".jpg"
		}
		output = strings.TrimSuffix(input, filepath.Ext(input)) + ext
	}

	f, err := os.Create(output)
	if err != nil {
		return fmt.Errorf("raw-decode: create output: %w", err)
	}
	switch format {
	case "jpeg":
		err = jpeg.Encode(f, img, &jpeg.Options{Quality: quality})
	case "png":
		err = png.Encode(f, img)
	default:
		f.Close()
		return fmt.Errorf("raw-decode: unknown format %q (want png or jpeg)", format)
	}
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		return fmt.Errorf("raw-decode: encode: %w", err)
	}

	b := img.Bounds()
	fmt.Printf("raw-decode: wrote %s (%d×%d, %s)\n", output, b.Dx(), b.Dy(), format)
	return nil
}
