package pipeline

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveParamAssets_NoAssets verifies no-op when assets map is empty.
func TestResolveParamAssets_NoAssets(t *testing.T) {
	params := map[string]any{"fontfile": "$asset:myFont"}
	got, err := resolveParamAssets(params, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["fontfile"] != "$asset:myFont" {
		t.Errorf("expected unchanged value, got %v", got["fontfile"])
	}
}

// TestResolveParamAssets_NoSubstitution verifies no-op when no param
// starts with the AssetRefPrefix.
func TestResolveParamAssets_NoSubstitution(t *testing.T) {
	assets := map[string]AssetRef{
		"myFont": {Path: "/tmp/font.ttf", Kind: "font"},
	}
	params := map[string]any{"text": "hello", "x": 10}
	got, err := resolveParamAssets(params, assets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should return original map (same pointer — not a copy).
	if len(got) != len(params) {
		t.Errorf("expected %d entries, got %d", len(params), len(got))
	}
}

// TestResolveParamAssets_UnknownAsset verifies an error is returned when
// a param references an asset name that is absent from the assets map.
func TestResolveParamAssets_UnknownAsset(t *testing.T) {
	assets := map[string]AssetRef{
		"myFont": {Path: "/tmp/font.ttf", Kind: "font"},
	}
	params := map[string]any{"model": "$asset:missingModel"}
	_, err := resolveParamAssets(params, assets)
	if err == nil {
		t.Fatal("expected error for unknown asset, got nil")
	}
}

// TestResolveParamAssets_AbsolutePath verifies that an existing absolute
// path asset is resolved correctly.
func TestResolveParamAssets_AbsolutePath(t *testing.T) {
	// Create a temporary file so os.Stat succeeds.
	f, err := os.CreateTemp(t.TempDir(), "asset*.ttf")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	absPath := f.Name()

	assets := map[string]AssetRef{
		"myFont": {Path: absPath, Kind: "font"},
	}
	params := map[string]any{"fontfile": "$asset:myFont", "size": 24}
	got, err := resolveParamAssets(params, assets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got["fontfile"] != absPath {
		t.Errorf("expected %q, got %q", absPath, got["fontfile"])
	}
	// Unrelated param must be preserved.
	if got["size"] != 24 {
		t.Errorf("expected size=24, got %v", got["size"])
	}
}

// TestResolveParamAssets_RelativePath verifies that a relative path is
// resolved via MEDIAMOLDER_ASSET_PATH when it is not in the cwd.
func TestResolveParamAssets_RelativePath(t *testing.T) {
	dir := t.TempDir()
	// Place the asset in the temp dir.
	assetName := "cb.rnnn"
	assetPath := filepath.Join(dir, assetName)
	if err := os.WriteFile(assetPath, []byte("dummy"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("MEDIAMOLDER_ASSET_PATH", dir)

	assets := map[string]AssetRef{
		"denoiseModel": {Path: assetName, Kind: "model"},
	}
	params := map[string]any{"model": "$asset:denoiseModel"}
	got, err := resolveParamAssets(params, assets)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	resolved, ok := got["model"].(string)
	if !ok {
		t.Fatalf("expected string, got %T", got["model"])
	}
	absExpected, _ := filepath.Abs(assetPath)
	if resolved != absExpected {
		t.Errorf("expected %q, got %q", absExpected, resolved)
	}
}

// TestResolveParamAssets_MissingFile verifies an error when the asset
// path does not exist anywhere in the search path.
func TestResolveParamAssets_MissingFile(t *testing.T) {
	t.Setenv("MEDIAMOLDER_ASSET_PATH", "")
	assets := map[string]AssetRef{
		"ghost": {Path: "nonexistent_asset_file_xxxxxxx.ttf", Kind: "font"},
	}
	params := map[string]any{"fontfile": "$asset:ghost"}
	_, err := resolveParamAssets(params, assets)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}
}

// TestResolveConfigAssets_NoAssets verifies that cfg is returned
// unchanged when Config.Assets is nil.
func TestResolveConfigAssets_NoAssets(t *testing.T) {
	cfg := &Config{
		SchemaVersion: "1.2",
		Graph: GraphDef{
			Nodes: []NodeDef{{ID: "f0", Type: "filter", Params: map[string]any{"fontfile": "$asset:x"}}},
		},
	}
	got, err := resolveConfigAssets(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got != cfg {
		t.Error("expected same pointer when Assets is nil")
	}
}

// TestResolveConfigAssets_Substitution verifies params are resolved in
// a returned copy of cfg, leaving the original untouched.
func TestResolveConfigAssets_Substitution(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "font*.ttf")
	if err != nil {
		t.Fatal(err)
	}
	f.Close()
	absFont := f.Name()

	cfg := &Config{
		SchemaVersion: "1.2",
		Assets: map[string]AssetRef{
			"heading": {Path: absFont, Kind: "font"},
		},
		Graph: GraphDef{
			Nodes: []NodeDef{
				{ID: "dt", Type: "filter", Params: map[string]any{"fontfile": "$asset:heading", "text": "hello"}},
			},
		},
	}
	resolved, err := resolveConfigAssets(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resolved == cfg {
		t.Error("expected a copy, got same pointer")
	}
	// Original must be untouched.
	if cfg.Graph.Nodes[0].Params["fontfile"] != "$asset:heading" {
		t.Error("original cfg was mutated")
	}
	// Resolved copy must have the absolute path.
	if resolved.Graph.Nodes[0].Params["fontfile"] != absFont {
		t.Errorf("expected %q, got %v", absFont, resolved.Graph.Nodes[0].Params["fontfile"])
	}
}

// TestValidateAssets verifies that validate() catches bad asset entries.
func TestValidateAssets(t *testing.T) {
	base := func() *Config {
		return &Config{
			SchemaVersion: "1.2",
			Inputs:        []Input{{ID: "in", URL: "in.mp4", Streams: []StreamSelect{{Type: "video", Track: 0}}}},
			Graph: GraphDef{
				Nodes: []NodeDef{{ID: "sink", Type: "filter_sink", Params: map[string]any{"output": "out"}}},
				Edges: []EdgeDef{{From: "in:v:0", To: "sink", Type: "video"}},
			},
			Outputs: []Output{{ID: "out", URL: "out.mp4"}},
		}
	}

	t.Run("empty path rejected", func(t *testing.T) {
		cfg := base()
		cfg.Assets = map[string]AssetRef{"x": {Path: "", Kind: "font"}}
		if err := validate(cfg); err == nil {
			t.Error("expected error for empty path")
		}
	})

	t.Run("invalid kind rejected", func(t *testing.T) {
		cfg := base()
		cfg.Assets = map[string]AssetRef{"x": {Path: "/tmp/f.ttf", Kind: "badkind"}}
		if err := validate(cfg); err == nil {
			t.Error("expected error for invalid kind")
		}
	})

	t.Run("valid asset accepted", func(t *testing.T) {
		cfg := base()
		cfg.Assets = map[string]AssetRef{
			"myFont": {Path: "/tmp/f.ttf", Kind: "font"},
			"myLUT":  {Path: "film.cube", Kind: "lut", Desc: "Filmic LUT"},
		}
		if err := validate(cfg); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
