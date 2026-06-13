// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"os"
	"path/filepath"
	"testing"
)

// minimalCfgForModelTest returns a Config with enough fields to pass
// the base validate() checks, with a single filter node that has the
// given params.
func minimalCfgForModelTest(nodeParams map[string]any) *Config {
	return &Config{
		SchemaVersion: "1.2",
		Inputs: []Input{
			{ID: "in0", URL: "input.mp4", Streams: []StreamSelect{{Type: "audio", Track: 0}}},
		},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "f0", Type: "filter", Filter: "arnndn", Params: nodeParams},
			},
			Edges: []EdgeDef{},
		},
		Outputs: []Output{
			{ID: "out0", URL: "output.mp4"},
		},
	}
}

// TestIsModelBearingKey covers the exact-match and suffix-match cases.
func TestIsModelBearingKey(t *testing.T) {
	hits := []string{"model", "sofa", "noise_model", "room_sofa", "a_model", "b_sofa"}
	for _, k := range hits {
		if !isModelBearingKey(k) {
			t.Errorf("isModelBearingKey(%q) = false, want true", k)
		}
	}
	misses := []string{"", "models", "sofabed", "font", "fontfile", "text"}
	for _, k := range misses {
		if isModelBearingKey(k) {
			t.Errorf("isModelBearingKey(%q) = true, want false", k)
		}
	}
}

// TestResolveModelPath_AbsoluteHit verifies that an existing absolute path is accepted.
func TestResolveModelPath_AbsoluteHit(t *testing.T) {
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "model*.rnnn")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	abs := f.Name()
	got, ok := resolveModelPath(abs, nil, "")
	if !ok {
		t.Fatalf("expected hit for absolute path %q", abs)
	}
	if got != filepath.Clean(abs) {
		t.Errorf("expected %q, got %q", filepath.Clean(abs), got)
	}
}

// TestResolveModelPath_AbsoluteMiss verifies that a non-existent absolute path fails.
func TestResolveModelPath_AbsoluteMiss(t *testing.T) {
	_, ok := resolveModelPath("/nonexistent/path/model_xxxxxxx.rnnn", nil, "")
	if ok {
		t.Fatal("expected miss for nonexistent absolute path")
	}
}

// TestResolveModelPath_RelativeViaBasedir verifies resolution relative to basedir.
func TestResolveModelPath_RelativeViaBasedir(t *testing.T) {
	dir := t.TempDir()
	name := "noise.rnnn"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveModelPath(name, nil, dir)
	if !ok {
		t.Fatalf("expected hit via basedir %q", dir)
	}
	expected, _ := filepath.Abs(filepath.Join(dir, name))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestResolveModelPath_RelativeViaFilterAssetPaths verifies resolution via search dirs.
func TestResolveModelPath_RelativeViaFilterAssetPaths(t *testing.T) {
	dir := t.TempDir()
	name := "room.sofa"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, ok := resolveModelPath(name, []string{dir}, "")
	if !ok {
		t.Fatalf("expected hit via FilterAssetPaths %q", dir)
	}
	expected, _ := filepath.Abs(filepath.Join(dir, name))
	if got != expected {
		t.Errorf("expected %q, got %q", expected, got)
	}
}

// TestResolveModelPath_TraversalRejected verifies that directory-traversal
// sequences are rejected even when the underlying file exists.
func TestResolveModelPath_TraversalRejected(t *testing.T) {
	dir := t.TempDir()
	// Create a file that the traversal would reach if not blocked.
	if err := os.WriteFile(filepath.Join(dir, "secret"), []byte("secret"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Attempt to resolve "../<basename>/secret" from a sub-directory.
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	_, ok := resolveModelPath("../secret", []string{sub}, "")
	if ok {
		t.Fatal("expected traversal to be rejected")
	}
}

// TestValidateFilterModelPaths_NoBasedir verifies that the check is skipped
// when basedir is "" and FilterAssetPaths is empty.
func TestValidateFilterModelPaths_NoBasedir(t *testing.T) {
	cfg := minimalCfgForModelTest(map[string]any{"model": "nonexistent.rnnn"})
	if err := validateFilterModelPaths(cfg, ""); err != nil {
		t.Errorf("expected nil when no basedir, got %v", err)
	}
}

// TestValidateFilterModelPaths_MissingFile verifies rejection when a model
// file cannot be found relative to basedir.
func TestValidateFilterModelPaths_MissingFile(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalCfgForModelTest(map[string]any{"model": "ghost_model_xxxxxxx.rnnn"})
	if err := validateFilterModelPaths(cfg, dir); err == nil {
		t.Fatal("expected error for missing model file, got nil")
	}
}

// TestValidateFilterModelPaths_Hit verifies acceptance when the model
// file exists in basedir.
func TestValidateFilterModelPaths_Hit(t *testing.T) {
	dir := t.TempDir()
	name := "noise.rnnn"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := minimalCfgForModelTest(map[string]any{"model": name})
	if err := validateFilterModelPaths(cfg, dir); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateFilterModelPaths_FilterAssetPaths verifies that FilterAssetPaths
// directories are also searched.
func TestValidateFilterModelPaths_FilterAssetPaths(t *testing.T) {
	dir := t.TempDir()
	name := "room.sofa"
	if err := os.WriteFile(filepath.Join(dir, name), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := minimalCfgForModelTest(map[string]any{"sofa": name})
	cfg.FilterAssetPaths = []string{dir}
	if err := validateFilterModelPaths(cfg, t.TempDir() /* different basedir */); err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestValidateFilterModelPaths_AssetRefSkipped verifies that $asset: values
// are not subject to model-path validation.
func TestValidateFilterModelPaths_AssetRefSkipped(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalCfgForModelTest(map[string]any{"model": "$asset:myModel"})
	if err := validateFilterModelPaths(cfg, dir); err != nil {
		t.Errorf("$asset: reference must not be checked: %v", err)
	}
}

// TestValidateFilterModelPaths_EmptyValueSkipped verifies that empty param
// values are silently skipped.
func TestValidateFilterModelPaths_EmptyValueSkipped(t *testing.T) {
	dir := t.TempDir()
	cfg := minimalCfgForModelTest(map[string]any{"model": ""})
	if err := validateFilterModelPaths(cfg, dir); err != nil {
		t.Errorf("empty value must not be checked: %v", err)
	}
}

// TestValidateFilterModelPaths_NonFilterNodeSkipped verifies that nodes of
// types other than filter/filter_source/filter_sink are not checked.
func TestValidateFilterModelPaths_NonFilterNodeSkipped(t *testing.T) {
	dir := t.TempDir()
	cfg := &Config{
		SchemaVersion: "1.2",
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "enc0", Type: "encoder", Params: map[string]any{"model": "ghost_model_xxxxxxx.rnnn"}},
			},
		},
	}
	if err := validateFilterModelPaths(cfg, dir); err != nil {
		t.Errorf("non-filter node must not be checked: %v", err)
	}
}

// TestValidateFilterModelPaths_AbsolutePathCheckedDirectly verifies that
// absolute param values are checked directly without any search-dir join.
func TestValidateFilterModelPaths_AbsolutePathCheckedDirectly(t *testing.T) {
	dir := t.TempDir()
	name := "model.rnnn"
	absPath := filepath.Join(dir, name)
	if err := os.WriteFile(absPath, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := minimalCfgForModelTest(map[string]any{"model": absPath})
	// Use a completely different basedir to confirm the absolute path is used as-is.
	if err := validateFilterModelPaths(cfg, t.TempDir()); err != nil {
		t.Errorf("unexpected error for absolute path: %v", err)
	}
}

// TestParseConfigFile_ModelPathValidation is an end-to-end test: a config
// file that references a model that does not exist relative to the config
// file's directory must fail ParseConfigFile.
func TestParseConfigFile_ModelPathValidation(t *testing.T) {
	dir := t.TempDir()
	cfgJSON := `{
		"schema_version": "1.2",
		"inputs": [{"id":"in0","url":"input.mp4","streams":[{"type":"audio","track":0}]}],
		"graph": {
			"nodes": [{"id":"f0","type":"filter","filter":"arnndn","params":{"model":"ghost_model_xxxxxxx.rnnn"}}],
			"edges": []
		},
		"outputs": [{"id":"out0","url":"output.mp4"}]
	}`
	cfgPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseConfigFile(cfgPath)
	if err == nil {
		t.Fatal("expected error for missing model file, got nil")
	}
}

// TestParseConfigFile_ModelPathValidationHit is the passing case: model
// file exists next to the config file.
func TestParseConfigFile_ModelPathValidationHit(t *testing.T) {
	dir := t.TempDir()
	modelName := "noise.rnnn"
	if err := os.WriteFile(filepath.Join(dir, modelName), []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfgJSON := `{
		"schema_version": "1.2",
		"inputs": [{"id":"in0","url":"input.mp4","streams":[{"type":"audio","track":0}]}],
		"graph": {
			"nodes": [{"id":"f0","type":"filter","filter":"arnndn","params":{"model":"` + modelName + `"}}],
			"edges": []
		},
		"outputs": [{"id":"out0","url":"output.mp4"}]
	}`
	cfgPath := filepath.Join(dir, "job.json")
	if err := os.WriteFile(cfgPath, []byte(cfgJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := ParseConfigFile(cfgPath)
	if err != nil {
		t.Errorf("unexpected error: %v", err)
	}
}
