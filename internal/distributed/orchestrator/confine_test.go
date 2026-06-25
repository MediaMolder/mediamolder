// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package orchestrator

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfineToRoots(t *testing.T) {
	root := t.TempDir()
	inside := filepath.Join(root, "sub", "f.json")

	if got, ok := confineToRoots(inside, []string{root}); !ok || got != filepath.Clean(inside) {
		t.Errorf("inside root: got %q ok=%v, want %q true", got, ok, filepath.Clean(inside))
	}
	if _, ok := confineToRoots("/etc/passwd", []string{root}); ok {
		t.Error("path outside every root must be refused")
	}
	if _, ok := confineToRoots(filepath.Join(root, "..", "evil"), []string{root}); ok {
		t.Error("escape via .. must be refused")
	}
	if _, ok := confineToRoots(inside, nil); ok {
		t.Error("no roots configured must refuse")
	}
}

// readManifestFile must read manifests only from within the allowed roots, so a
// submitted job cannot read arbitrary files (e.g. /etc/passwd) via manifest_uri.
func TestReadManifestFileConfinement(t *testing.T) {
	root := t.TempDir()
	manifest := filepath.Join(root, "m.json")
	if err := os.WriteFile(manifest, []byte(`{"ok":true}`), 0o600); err != nil {
		t.Fatal(err)
	}

	data, err := readManifestFile("file://"+manifest, []string{root})
	if err != nil || string(data) != `{"ok":true}` {
		t.Fatalf("within-root read: data=%q err=%v", data, err)
	}
	if _, err := readManifestFile("file:///etc/passwd", []string{root}); err == nil {
		t.Error("reading outside the allowed roots must be refused")
	}
	if _, err := readManifestFile("file://"+manifest, nil); err == nil {
		t.Error("no roots configured must refuse the read")
	}
	if _, err := readManifestFile("relative/m.json", []string{root}); err == nil {
		t.Error("a relative manifest_uri must be refused")
	}
}

// manifestRoots falls back to a safe default (temp dir + cwd) when no AllowedRoots
// are configured, so confinement is always in effect.
func TestManifestRootsDefault(t *testing.T) {
	o := &Orchestrator{}
	if roots := o.manifestRoots(); len(roots) == 0 {
		t.Error("default manifest roots must be non-empty")
	}
	o.AllowedRoots = []string{"/srv/jobs"}
	if got := o.manifestRoots(); len(got) != 1 || got[0] != "/srv/jobs" {
		t.Errorf("configured roots: got %v, want [/srv/jobs]", got)
	}
}
