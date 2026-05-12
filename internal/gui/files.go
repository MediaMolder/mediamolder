// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// mkdirBodyLimit caps the request body for POST /api/files/mkdir.
// The endpoint only carries a parent path and a folder name, so 4 KiB is
// generous. If you need to pass unusually long paths, raise this constant.
const mkdirBodyLimit = 4 * 1024

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
	Drives  []string    `json:"drives,omitempty"` // local drive/volume roots (e.g. "C:\\", "E:\\" on Windows)
	Roots   []string    `json:"roots,omitempty"`  // shortcut roots ($HOME, cwd, …)
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
	roots := defaultRoots()
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
	abs = filepath.Clean(abs)
	if !isWithinAnyRoot(abs, roots) {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is outside allowed roots"))
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
		Drives:  localDrives(),
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
	if runtime.GOOS != "windows" {
		// On Unix the filesystem root is a meaningful shortcut. On Windows
		// each drive letter is its own root and is exposed via localDrives()
		// instead, so adding "/" here would just resolve to the cwd's drive.
		out = append(out, "/")
		out = append(out, mountedVolumes()...)
	}
	return uniqueStrings(out)
}

// localDrives returns the set of mounted drive roots that the file picker
// should expose as a top-level navigation group above the user's shortcuts.
//
// On Windows this is every drive letter A: through Z: that os.Stat reports
// as existing, in alphabetical order. The values are returned with a
// trailing separator ("C:\\", "E:\\") so they round-trip through
// filepath.Abs without being interpreted as the cwd's drive.
//
// On non-Windows platforms this returns nil; the existing shortcutRoots()
// machinery already covers "/" and removable media under /Volumes, /media,
// /mnt, and /run/media.
func localDrives() []string {
	if runtime.GOOS != "windows" {
		return nil
	}
	var out []string
	for letter := 'A'; letter <= 'Z'; letter++ {
		root := string(letter) + `:\`
		if _, err := os.Stat(root); err == nil {
			out = append(out, root)
		}
	}
	return out
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
	if err := json.NewDecoder(io.LimitReader(r.Body, mkdirBodyLimit)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	parent := strings.TrimSpace(req.Path)
	name := strings.TrimSpace(req.Name)
	if parent == "" || name == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("path and name are required"))
		return
	}
	// Disallow path separators and any non-canonical/special components
	// so the folder name is always a single safe path component.
	if strings.ContainsAny(name, `/\`) ||
		name == "." || name == ".." ||
		filepath.Base(name) != name ||
		filepath.Clean(name) != name {
		writeJSONError(w, http.StatusBadRequest, errors.New("invalid folder name"))
		return
	}
	parent = expandHome(parent)
	abs, err := filepath.Abs(parent)
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}
	abs = filepath.Clean(abs)
	if !isWithinAnyRoot(abs, defaultRoots()) {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is outside allowed roots"))
		return
	}
	info, err := os.Stat(abs)
	if err != nil || !info.IsDir() {
		writeJSONError(w, http.StatusNotFound, errors.New("parent directory does not exist"))
		return
	}
	target := filepath.Join(abs, name)
	target = filepath.Clean(target)
	if !isWithinAnyRoot(target, defaultRoots()) {
		writeJSONError(w, http.StatusBadRequest, errors.New("target is outside allowed roots"))
		return
	}
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

// fileReadWriteBodyLimit caps the request body for PUT /api/file.
// 10 MiB should comfortably cover even the largest job JSON files.
const fileReadWriteBodyLimit = 10 * 1024 * 1024

// handleReadFile returns the raw text content of a file.
//
// GET /api/file?path=<absolute-path>
//
// The path must resolve to a regular file within the allowed roots.
// The raw file bytes are returned with Content-Type: text/plain so
// the frontend can consume them with response.text().
func handleReadFile(w http.ResponseWriter, r *http.Request) {
	roots := defaultRoots()
	rawPath := strings.TrimSpace(r.URL.Query().Get("path"))
	if rawPath == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	abs, err := filepath.Abs(expandHome(rawPath))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}
	abs = filepath.Clean(abs)
	if !isWithinAnyRoot(abs, roots) {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is outside allowed roots"))
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
	if info.IsDir() {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is a directory"))
		return
	}
	data, err := os.ReadFile(abs)
	if err != nil {
		writeJSONError(w, http.StatusForbidden, fmt.Errorf("read %q: %w", abs, err))
		return
	}
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write(data)
}

type fileWriteRequest struct {
	Path    string `json:"path"`
	Content string `json:"content"`
}

// handleWriteFile atomically writes text content to a file on disk.
//
// PUT /api/file  body: { "path": "...", "content": "..." }
//
// The path must resolve to a location within the allowed roots. The
// parent directory must already exist. Content is written atomically
// via a temp-file rename so a crash mid-write never truncates the
// original file.
func handleWriteFile(w http.ResponseWriter, r *http.Request) {
	roots := defaultRoots()
	var req fileWriteRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, fileReadWriteBodyLimit)).Decode(&req); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid JSON body: %w", err))
		return
	}
	rawPath := strings.TrimSpace(req.Path)
	if rawPath == "" {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is required"))
		return
	}
	abs, err := filepath.Abs(expandHome(rawPath))
	if err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("invalid path: %w", err))
		return
	}
	abs = filepath.Clean(abs)
	if !isWithinAnyRoot(abs, roots) {
		writeJSONError(w, http.StatusBadRequest, errors.New("path is outside allowed roots"))
		return
	}
	// Parent directory must exist.
	if _, err := os.Stat(filepath.Dir(abs)); err != nil {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("parent directory does not exist: %w", err))
		return
	}
	// Write atomically: temp file in the same directory (guarantees
	// same filesystem for the rename), then rename over the target.
	dir := filepath.Dir(abs)
	tmp, err := os.CreateTemp(dir, ".mm-save-*")
	if err != nil {
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("create temp file: %w", err))
		return
	}
	tmpName := tmp.Name()
	if _, err := io.WriteString(tmp, req.Content); err != nil {
		_ = tmp.Close()
		_ = os.Remove(tmpName)
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("write: %w", err))
		return
	}
	if err := tmp.Close(); err != nil {
		_ = os.Remove(tmpName)
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("close: %w", err))
		return
	}
	if err := os.Rename(tmpName, abs); err != nil {
		_ = os.Remove(tmpName)
		writeJSONError(w, http.StatusInternalServerError, fmt.Errorf("rename: %w", err))
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
// browse: the user's shortcut roots (home, cwd, filesystem root on Unix and
// mounted volumes) plus any local drives. Returned paths are absolute and
// cleaned so they can be compared with filepath.Rel.
func defaultRoots() []string {
	var out []string
	for _, r := range append(shortcutRoots(), localDrives()...) {
		if abs, err := filepath.Abs(r); err == nil {
			out = append(out, filepath.Clean(abs))
		}
	}
	return uniqueStrings(out)
}

// isWithinAnyRoot reports whether path resolves inside one of the supplied
// allow-listed root directories.
func isWithinAnyRoot(path string, roots []string) bool {
	for _, root := range roots {
		if isWithinRoot(path, root) {
			return true
		}
	}
	return false
}

// isWithinRoot reports whether path is equal to root or nested below it.
func isWithinRoot(path, root string) bool {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	absPath = filepath.Clean(absPath)
	absRoot = filepath.Clean(absRoot)
	rel, err := filepath.Rel(absRoot, absPath)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)))
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
