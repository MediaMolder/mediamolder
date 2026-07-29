// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package twelvelabs

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

// DefaultConfigPath is the on-disk fallback secrets store for the
// TwelveLabs API key. Format: {"api_key": "..."}.
//
// It is a package-level variable so tests can override it.
var DefaultConfigPath = filepath.Join(os.Getenv("HOME"), ".config", "mediamolder", "twelvelabs.json")

// ResolveAPIKey returns the TwelveLabs API key following the documented
// precedence: explicit flag value → TWELVELABS_API_KEY env → JSON config
// file at DefaultConfigPath.
//
// Pass an empty flagVal to skip the first source.
func ResolveAPIKey(flagVal string) (string, error) {
	if flagVal != "" {
		return flagVal, nil
	}
	if env := os.Getenv("TWELVELABS_API_KEY"); env != "" {
		return env, nil
	}
	data, err := os.ReadFile(DefaultConfigPath)
	if err != nil {
		return "", fmt.Errorf("no API key (--api-key not set, TWELVELABS_API_KEY env empty, %s missing)", DefaultConfigPath)
	}
	var cfg struct {
		APIKey string `json:"api_key"`
	}
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", fmt.Errorf("parse %s: %w", DefaultConfigPath, err)
	}
	if cfg.APIKey == "" {
		return "", fmt.Errorf("api_key empty in %s", DefaultConfigPath)
	}
	return cfg.APIKey, nil
}
