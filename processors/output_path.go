// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"
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
