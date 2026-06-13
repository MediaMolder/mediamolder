// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

// filter_model_paths.go — Validation of model-file paths embedded
// directly in filter params (Wave 11 #66).
//
// Filters such as arnndn (model=) and sofalizer (sofa=) accept file-path
// option values. validateFilterModelPaths walks the graph and, for every
// filter node param whose key name matches the model-bearing suffix
// heuristic, verifies that the referenced file can be found.
//
// Path resolution order (first match wins):
//  1. Absolute paths: checked directly via os.Stat.
//  2. Relative paths tried against each Config.FilterAssetPaths directory.
//  3. Relative paths tried against basedir (directory of the pipeline file).
//  4. Relative paths tried against the current working directory.
//
// "$asset:<name>" values are owned by the Assets registry and are
// skipped here. Empty values are also skipped.
//
// The check is skipped entirely when basedir is "" (i.e. ParseConfig was
// called without a file path).

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// isModelBearingKey returns true when an AVOption name conventionally
// holds a file path to a model/data file. Exact matches: "model",
// "sofa". Suffix matches: "_model", "_sofa".
func isModelBearingKey(k string) bool {
	if k == "model" || k == "sofa" {
		return true
	}
	return strings.HasSuffix(k, "_model") || strings.HasSuffix(k, "_sofa")
}

// resolveModelPath attempts to resolve path against searchDirs (tried in
// order) and basedir. Returns (resolved absolute path, true) on success
// and ("", false) when not found.
//
// Absolute paths are checked directly without any directory join.
// For relative paths each candidate is validated against path traversal
// using the same filepath.Rel sentinel used by resolveAssetPath.
func resolveModelPath(path string, searchDirs []string, basedir string) (string, bool) {
	if filepath.IsAbs(path) {
		if _, err := os.Stat(filepath.Clean(path)); err == nil {
			return filepath.Clean(path), true
		}
		return "", false
	}
	// Build ordered search list: FilterAssetPaths, basedir, "." (cwd).
	dirs := make([]string, 0, len(searchDirs)+2)
	dirs = append(dirs, searchDirs...)
	if basedir != "" {
		dirs = append(dirs, basedir)
	}
	dirs = append(dirs, ".")

	for _, dir := range dirs {
		if dir == "" {
			continue
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		candidate := filepath.Join(absDir, path)
		// Guard against path traversal: reject candidates that escape absDir.
		rel, relErr := filepath.Rel(absDir, candidate)
		if relErr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			continue
		}
		if _, statErr := os.Stat(candidate); statErr == nil {
			return candidate, true
		}
	}
	return "", false
}

// validateFilterModelPaths validates that every model-bearing filter param
// in cfg refers to a file that can be resolved. When basedir is "" the
// function returns nil immediately (no file context to resolve against).
func validateFilterModelPaths(cfg *Config, basedir string) error {
	if basedir == "" && len(cfg.FilterAssetPaths) == 0 {
		// No resolution context; skip existence checks.
		return nil
	}
	for i, node := range cfg.Graph.Nodes {
		if node.Type != "filter" && node.Type != "filter_source" && node.Type != "filter_sink" {
			continue
		}
		for k, v := range node.Params {
			if !isModelBearingKey(k) {
				continue
			}
			val, ok := v.(string)
			if !ok || val == "" || strings.HasPrefix(val, AssetRefPrefix) {
				continue
			}
			if _, found := resolveModelPath(val, cfg.FilterAssetPaths, basedir); !found {
				dirs := append([]string(nil), cfg.FilterAssetPaths...)
				if basedir != "" {
					dirs = append(dirs, basedir)
				}
				dirs = append(dirs, ".")
				return fmt.Errorf(
					"node[%d] %q: filter param %q: model file %q not found (searched: %s)",
					i, node.ID, k, val, strings.Join(dirs, ", "),
				)
			}
		}
	}
	return nil
}
