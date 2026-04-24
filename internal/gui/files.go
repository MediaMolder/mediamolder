// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// fileEntry represents one item in a directory listing.
type fileEntry struct {
	Name  string `json:"name"`
	Path  string `json:"path"` // absolute path
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size,omitempty"` // bytes (files only)
}

type fileListResponse struct {
	Path    string      `json:"path"`             // absolute, normalized path that was listed
	Parent  string      `json:"parent,omitempty"` // absolute path of parent (empty at filesystem root)
	Entries []fileEntry `json:"entries"`
	Roots   []string    `json:"roots,omitempty"` // shortcut roots ($HOME, /, cwd)
}

// handleListDir returns a directory listing for the GUI's file picker.
//
// Query params:
//   - path:        absolute or ~-prefixed directory to list (default: $HOME)
//   - filter:      optional comma-separated extensions ("mp4,mkv,mov") to keep
//   - dirs_only:   "1" to omit files (used for output-folder browsing)
//
// Security note: this endpoint is intended for localhost developer use. It
// does not constrain the listed path beyond the OS's own permissions; do not
// expose the GUI server on untrusted networks.
func handleListDir(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	target := strings.TrimSpace(q.Get("path"))
	if target == "" {
		if home, err := os.UserHomeDir(); err == nil {
			target = home
		} else {
			target = "/"
		}
	}
	target = expandHome(target)
	abs, err := filepath.Abs(target)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}

	info, err := os.Stat(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			writeJSONError(w, http.StatusNotFound, err)
		} else {
			writeJSONError(w, http.StatusForbidden, err)
		}
		return
	}
	if !info.IsDir() {
		// User passed a file path; fall back to its parent directory.
		abs = filepath.Dir(abs)
	}

	rawEntries, err := os.ReadDir(abs)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, err)
		return
	}

	dirsOnly := q.Get("dirs_only") == "1"
	exts := parseExtensions(q.Get("filter"))

	entries := make([]fileEntry, 0, len(rawEntries))
	for _, e := range rawEntries {
		name := e.Name()
		if strings.HasPrefix(name, ".") {
			// Hide dotfiles by default; nothing in the GUI needs them today.
			continue
		}
		isDir := e.IsDir()
		if !isDir && dirsOnly {
			continue
		}
		if !isDir && len(exts) > 0 {
			ext := strings.ToLower(strings.TrimPrefix(filepath.Ext(name), "."))
			if _, ok := exts[ext]; !ok {
				continue
			}
		}
		var size int64
		if !isDir {
			if fi, err := e.Info(); err == nil {
				size = fi.Size()
			}
		}
		entries = append(entries, fileEntry{
			Name:  name,
			Path:  filepath.Join(abs, name),
			IsDir: isDir,
			Size:  size,
		})
	}
	sort.Slice(entries, func(i, j int) bool {
		// Directories first, then case-insensitive name.
		if entries[i].IsDir != entries[j].IsDir {
			return entries[i].IsDir
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})

	parent := ""
	if p := filepath.Dir(abs); p != abs {
		parent = p
	}

	resp := fileListResponse{
		Path:    abs,
		Parent:  parent,
		Entries: entries,
		Roots:   shortcutRoots(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			if p == "~" {
				return home
			}
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

func parseExtensions(spec string) map[string]struct{} {
	out := map[string]struct{}{}
	for _, raw := range strings.Split(spec, ",") {
		ext := strings.ToLower(strings.TrimSpace(strings.TrimPrefix(raw, ".")))
		if ext != "" {
			out[ext] = struct{}{}
		}
	}
	return out
}

func shortcutRoots() []string {
	out := []string{}
	if home, err := os.UserHomeDir(); err == nil {
		out = append(out, home)
	}
	if cwd, err := os.Getwd(); err == nil {
		out = append(out, cwd)
	}
	out = append(out, "/")
	out = append(out, mountedVolumes()...)
	return uniqueStrings(out)
}

// mountedVolumes returns paths to mounted drives/volumes that are likely
// to contain user-visible data. Best-effort and platform-aware:
//   - macOS: entries under /Volumes (excludes the system volume which
//     simply re-mounts /).
//   - Linux: entries under /media/<user> and /mnt that are directories.
//   - Other: nothing (Windows would need a different enumeration).
func mountedVolumes() []string {
	candidates := []string{"/Volumes"}
	if u, err := os.UserHomeDir(); err == nil {
		// /media/<user> is the udisks2 default on most desktops.
		base := filepath.Base(u)
		if base != "" && base != "/" {
			candidates = append(candidates, "/media/"+base)
		}
	}
	candidates = append(candidates, "/media", "/mnt", "/run/media")

	var out []string
	for _, dir := range candidates {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if !e.IsDir() || strings.HasPrefix(e.Name(), ".") {
				continue
			}
			out = append(out, filepath.Join(dir, e.Name()))
		}
	}
	return out
}

// mkdirRequest is the body of POST /api/files/mkdir.
type mkdirRequest struct {
	Path string `json:"path"` // parent directory
	Name string `json:"name"` // new directory name
}

type mkdirResponse struct {
	Path string `json:"path"` // absolute path of the created directory
}

// handleMkdir creates a new directory inside an existing one. Used by the
// file-save dialog's "New folder" button. Same security caveat as
// handleListDir: localhost-only.
func handleMkdir(w http.ResponseWriter, r *http.Request) {
	var req mkdirRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	parent := strings.TrimSpace(req.Path)
	name := strings.TrimSpace(req.Name)
	if parent == "" || name == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("path and name are required"))
		return
	}
	// Disallow path separators in the new folder name to prevent the
	// caller from escaping the parent directory.
	if strings.ContainsAny(name, `/\`) || name == "." || name == ".." {
		writeJSONError(w, http.StatusBadRequest, errors.New("invalid folder name"))
		return
	}
	parent = expandHome(parent)
	abs, err := filepath.Abs(parent)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		writeJSONError(w, http.StatusNotFound, errors.New("parent directory does not exist"))
		return
	}
	target := filepath.Join(abs, name)
	if err := os.Mkdir(target, 0o755); err != nil {
		if errors.Is(err, fs.ErrExist) {
			writeJSONError(w, http.StatusConflict, err)
		} else {
			writeJSONError(w, http.StatusForbidden, err)
		}
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(mkdirResponse{Path: target})
}

func uniqueStrings(in []string) []string {
	seen := map[string]struct{}{}
	out := in[:0]
	for _, s := range in {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	return out
}
