// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// sanitizeProcessorOutputPath is the shared CWE-022 confinement helper for
// every processor "output_file" param: it requires an absolute,
// traversal-free path and re-derives it from the path's own filesystem
// root — the volume on Windows ("C:\\"), "/" on Unix — so filepath.Rel can
// express the path and a tainted value never reaches os.Create/WriteFile
// directly (CodeQL go/path-injection). processorName only shapes the error
// messages.
func sanitizeProcessorOutputPath(processorName, path string) (string, error) {
	path = filepath.Clean(path)
	if !filepath.IsAbs(path) {
		return "", fmt.Errorf("%s: output_file must be an absolute path, got %q", processorName, path)
	}
	root := filepath.VolumeName(path) + string(filepath.Separator)
	rel, relErr := filepath.Rel(root, path)
	if relErr != nil || strings.HasPrefix(rel, "..") {
		return "", fmt.Errorf("%s: output_file %q is not within an accessible filesystem root", processorName, path)
	}
	return filepath.Join(root, rel), nil
}

// openProcessorOutputFile opens an output_file for writing through os.Root,
// so path confinement is enforced by the operating system at open time —
// no "..", no absolute names, no symlink escape past the filesystem root,
// and no time-of-check/time-of-use window — rather than only by the string
// checks in sanitizeProcessorOutputPath (CWE-022). flag is the os.OpenFile
// flag set; perm applies when the file is created.
func openProcessorOutputFile(processorName, path string, flag int, perm os.FileMode) (*os.File, error) {
	safe, err := sanitizeProcessorOutputPath(processorName, path)
	if err != nil {
		return nil, err
	}
	rootDir := filepath.VolumeName(safe) + string(filepath.Separator)
	rel, err := filepath.Rel(rootDir, safe)
	if err != nil {
		return nil, fmt.Errorf("%s: output_file %q is not within an accessible filesystem root", processorName, path)
	}
	root, err := os.OpenRoot(rootDir)
	if err != nil {
		return nil, fmt.Errorf("%s: open filesystem root for output_file: %w", processorName, err)
	}
	defer root.Close()
	f, err := root.OpenFile(rel, flag, perm)
	if err != nil {
		return nil, fmt.Errorf("%s: open output_file %q: %w", processorName, safe, err)
	}
	return f, nil
}
