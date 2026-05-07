package pipeline

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// AssetRefPrefix is the sentinel prefix used in filter params to refer
// to a named asset. A value of the form "$asset:<key>" is substituted
// at runtime with the resolved filesystem path of Config.Assets[<key>].
const AssetRefPrefix = "$asset:"

// resolveAssetPath resolves the filesystem path of a single AssetRef.
//
// Absolute paths are checked for existence and returned unchanged.
// Relative paths are tried in order:
//  1. The current working directory.
//  2. Each directory listed in the MEDIAMOLDER_ASSET_PATH environment
//     variable (colon-separated on POSIX, semicolon-separated on Windows).
//
// The first match is returned as an absolute path. An error is returned
// when the path cannot be found in any search location.
func resolveAssetPath(ref AssetRef) (string, error) {
	p := ref.Path
	if p == "" {
		return "", fmt.Errorf("asset path is empty")
	}
	// Normalise the path first; filepath.Clean removes redundant separators and
	// dot-segments, and is recognised by static-analysis tools as a path sanitiser.
	p = filepath.Clean(p)

	if filepath.IsAbs(p) {
		if _, err := os.Stat(p); err != nil {
			return "", fmt.Errorf("asset not found at %q: %w", ref.Path, err)
		}
		return p, nil
	}

	// After cleaning, any relative path that would escape above the search root
	// begins with "..".  Reject it to prevent directory-traversal attacks.
	if p == ".." || strings.HasPrefix(p, ".."+string(filepath.Separator)) {
		return "", fmt.Errorf("asset path %q: directory traversal is not allowed", ref.Path)
	}

	// Build search list: cwd first, then MEDIAMOLDER_ASSET_PATH entries.
	searchDirs := []string{"."}
	if env := os.Getenv("MEDIAMOLDER_ASSET_PATH"); env != "" {
		sep := ":"
		if os.PathSeparator == '\\' {
			sep = ";"
		}
		searchDirs = append(searchDirs, strings.Split(env, sep)...)
	}
	for _, dir := range searchDirs {
		absDir, err := filepath.Abs(dir)
		if err != nil {
			continue
		}
		candidate := filepath.Join(absDir, p)
		// Defence-in-depth containment check: the resolved candidate must stay
		// within the search directory even after Join (guards against symlinks or
		// edge cases not caught by the traversal check above).
		if !strings.HasPrefix(candidate, absDir+string(filepath.Separator)) && candidate != absDir {
			continue
		}
		if _, err := os.Stat(candidate); err == nil {
			return candidate, nil
		}
	}
	return "", fmt.Errorf("asset %q not found in search path (tried cwd + MEDIAMOLDER_ASSET_PATH)", ref.Path)
}

// resolveParamAssets walks params and replaces any string value that
// starts with AssetRefPrefix with the resolved filesystem path of the
// named asset. Returns a shallow copy of params when at least one
// substitution was made; returns the original map when no substitution
// was needed.
//
// Returns an error when:
//   - A referenced asset name is absent from assets.
//   - The asset's path cannot be resolved on the filesystem.
func resolveParamAssets(params map[string]any, assets map[string]AssetRef) (map[string]any, error) {
	if len(assets) == 0 || len(params) == 0 {
		return params, nil
	}
	var out map[string]any // lazy copy — only allocated on first substitution
	for k, v := range params {
		s, ok := v.(string)
		if !ok || !strings.HasPrefix(s, AssetRefPrefix) {
			if out != nil {
				out[k] = v
			}
			continue
		}
		name := strings.TrimPrefix(s, AssetRefPrefix)
		ref, exists := assets[name]
		if !exists {
			return nil, fmt.Errorf("param %q: unknown asset %q (not in Config.Assets)", k, name)
		}
		resolved, err := resolveAssetPath(ref)
		if err != nil {
			return nil, fmt.Errorf("param %q (asset %q): %w", k, name, err)
		}
		if out == nil {
			// First substitution: copy all existing entries into the new map.
			out = make(map[string]any, len(params))
			for kk, vv := range params {
				out[kk] = vv
			}
		}
		out[k] = resolved
	}
	if out == nil {
		return params, nil
	}
	return out, nil
}

// resolveConfigAssets returns a shallow copy of cfg with every filter
// node param that contains an "$asset:<name>" reference substituted
// with the resolved absolute filesystem path. When cfg.Assets is empty,
// cfg is returned unchanged. Returns an error if any referenced name is
// absent from cfg.Assets or its file cannot be found.
func resolveConfigAssets(cfg *Config) (*Config, error) {
	if len(cfg.Assets) == 0 {
		return cfg, nil
	}
	// Shallow-copy the config, then replace the nodes slice so we can
	// safely mutate individual NodeDef.Params without affecting the
	// original.
	out := *cfg
	out.Graph.Nodes = make([]NodeDef, len(cfg.Graph.Nodes))
	copy(out.Graph.Nodes, cfg.Graph.Nodes)
	for i, node := range out.Graph.Nodes {
		if len(node.Params) == 0 {
			continue
		}
		resolved, err := resolveParamAssets(node.Params, cfg.Assets)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.ID, err)
		}
		out.Graph.Nodes[i].Params = resolved
	}
	return &out, nil
}
