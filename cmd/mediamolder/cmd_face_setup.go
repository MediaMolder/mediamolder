// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdFaceSetup implements `mediamolder face-setup`: a doctor that diagnoses face-detection
// readiness (build support, ONNX Runtime, models) and prints the exact command to fix each
// gap — or, with --fetch, downloads the SHA-pinned models. Exits 0 only when ready.

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/MediaMolder/MediaMolder/face"
)

func cmdFaceSetup(args []string) error {
	fs := flag.NewFlagSet("face-setup", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder face-setup [flags]

Diagnose face-detection readiness — build support, the ONNX Runtime, and the
bundled models — and print exactly how to fix anything missing. Exits 0 only
when face detection is ready to run.

Flags:
  --models-dir <p>   Where the face models live / should be fetched
                     (default: $MEDIAMOLDER_FACE_MODELS, else testdata/face_models)
  --ort-lib <p>      Path to the ONNX Runtime shared library (else auto-discovered)
  --fetch            Download the models if missing (runs scripts/fetch-face-models.sh)

`)
	}
	modelsDirFlag := fs.String("models-dir", "", "where the face models live / should be fetched")
	ortLibFlag := fs.String("ort-lib", "", "path to the ONNX Runtime shared library")
	fetchFlag := fs.Bool("fetch", false, "download the models if missing")
	if err := fs.Parse(args); err != nil {
		return err
	}

	modelsDir := *modelsDirFlag
	if modelsDir == "" {
		modelsDir = os.Getenv(face.EnvModelsDir)
	}
	if modelsDir == "" {
		modelsDir = "testdata/face_models"
	}
	if abs, err := filepath.Abs(modelsDir); err == nil {
		modelsDir = abs
	}
	face.SetModelsDir(modelsDir)
	if *ortLibFlag != "" {
		face.SetONNXLib(*ortLibFlag)
	}

	out := os.Stdout
	fmt.Fprintf(out, "MediaMolder — face detection setup\n\n")

	// ── Models ──────────────────────────────────────────────────────────────
	fmt.Fprintf(out, "Models directory: %s\n", modelsDir)
	if err := face.CheckModels(modelsDir); err != nil {
		fmt.Fprintf(out, "  ✗ %v\n", err)
		if *fetchFlag {
			fmt.Fprintln(out, "  → fetching models…")
			if ferr := runFetchModels(modelsDir); ferr != nil {
				fmt.Fprintf(out, "  ✗ fetch failed: %v\n", ferr)
			}
		}
		if err := face.CheckModels(modelsDir); err != nil {
			fmt.Fprintf(out, "  → fetch them: scripts/fetch-face-models.sh %q\n", modelsDir)
			fmt.Fprintf(out, "    (or re-run with --fetch), then: export MEDIAMOLDER_FACE_MODELS=%q\n", modelsDir)
		} else {
			fmt.Fprintln(out, "  ✓ models present and SHA-256 verified")
		}
	} else {
		fmt.Fprintln(out, "  ✓ models present and SHA-256 verified")
	}

	// ── Overall readiness (build + ONNX Runtime + models, end to end) ───────
	fmt.Fprintln(out)
	err := face.Available()
	switch {
	case err == nil:
		fmt.Fprintln(out, "✅ Face detection is ready.")
		return nil
	case errors.Is(err, face.ErrUnsupported):
		fmt.Fprintln(out, "✗ This binary was built WITHOUT face support (no with_onnx tag).")
		fmt.Fprintln(out, "  → GUI:  make build-gui-whisper EXTRA_TAGS=with_onnx")
		fmt.Fprintln(out, "  → CLI:  go build -tags with_onnx ./cmd/mediamolder")
	default:
		fmt.Fprintf(out, "✗ Not ready: %v\n", err)
		if strings.Contains(err.Error(), "onnxruntime") {
			fmt.Fprintln(out, "  → install the ONNX Runtime (then it is auto-discovered):")
			fmt.Fprintln(out, "      macOS:  brew install onnxruntime")
			fmt.Fprintln(out, "      Linux:  your distro's onnxruntime package")
			fmt.Fprintln(out, "    Non-standard install? pass --ort-lib /path/to/libonnxruntime.{dylib,so}")
		}
	}
	return fmt.Errorf("face detection is not ready (see above)")
}

// runFetchModels shells out to the repo's SHA-pinned fetch script (network access; only when
// the user passes --fetch). Returns an error if the script isn't reachable from the cwd.
func runFetchModels(dir string) error {
	const script = "scripts/fetch-face-models.sh"
	if _, err := os.Stat(script); err != nil {
		return fmt.Errorf("%s not found — run from the MediaMolder repo root, or fetch manually", script)
	}
	cmd := exec.Command("bash", script, dir)
	cmd.Stdout = os.Stderr
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
