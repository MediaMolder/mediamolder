package pipeline

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SecurityConfig holds input validation and resource limit settings.
type SecurityConfig struct {
	// AllowedSchemes is the set of permitted URL schemes.
	// Default: file, http, https, rtmp, rtsp, srt.
	AllowedSchemes []string

	// BaseDir constrains file:// paths to this directory subtree.
	// If empty, file path validation is skipped.
	BaseDir string

	// MaxWidth and MaxHeight cap decoded frame dimensions.
	MaxWidth  int
	MaxHeight int

	// MaxStreams caps the number of streams per input.
	MaxStreams int

	// ProbeTimeout is the maximum time (in seconds) for format probing.
	ProbeTimeout int

	// MaxConcurrentPipelines caps how many pipelines can run simultaneously.
	MaxConcurrentPipelines int

	// MemoryCapMB caps approximate memory usage in megabytes. 0 = unlimited.
	MemoryCapMB int

	// MaxThreads caps CPU threads used by the pipeline. 0 = unlimited.
	MaxThreads int
}

// DefaultSecurityConfig returns a SecurityConfig with sensible defaults.
func DefaultSecurityConfig() SecurityConfig {
	return SecurityConfig{
		AllowedSchemes:         []string{"file", "http", "https", "rtmp", "rtsp", "srt"},
		MaxWidth:               7680, // 8K
		MaxHeight:              4320, // 8K
		MaxStreams:             64,
		ProbeTimeout:           10,
		MaxConcurrentPipelines: 16,
	}
}

var defaultAllowedSchemes = map[string]bool{
	"file":  true,
	"http":  true,
	"https": true,
	"rtmp":  true,
	"rtsp":  true,
	"srt":   true,
}

// ValidateURL checks a URL string against the scheme allowlist and, for
// file:// URLs, performs path traversal and symlink validation.
func (sc *SecurityConfig) ValidateURL(rawURL string) error {
	allowed := sc.allowedSchemeSet()

	// Bare paths (no scheme) are treated as file paths.
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}

	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "file"
	}

	if !allowed[scheme] {
		return fmt.Errorf("URL scheme %q is not allowed (allowed: %v)", scheme, sc.allowedSchemeList())
	}

	if scheme == "file" {
		return sc.validateFilePath(rawURL, u)
	}
	return nil
}

func (sc *SecurityConfig) allowedSchemeSet() map[string]bool {
	if len(sc.AllowedSchemes) == 0 {
		return defaultAllowedSchemes
	}
	m := make(map[string]bool, len(sc.AllowedSchemes))
	for _, s := range sc.AllowedSchemes {
		m[strings.ToLower(s)] = true
	}
	return m
}

func (sc *SecurityConfig) allowedSchemeList() []string {
	if len(sc.AllowedSchemes) > 0 {
		return sc.AllowedSchemes
	}
	return []string{"file", "http", "https", "rtmp", "rtsp", "srt"}
}

// validateFilePath checks for path traversal and symlink escapes.
func (sc *SecurityConfig) validateFilePath(rawURL string, u *url.URL) error {
	// Extract the file path from the URL.
	p := u.Path
	if p == "" {
		p = rawURL // bare path, not a URL
	}

	// Reject obvious traversal components.
	cleaned := filepath.Clean(p)
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("path traversal detected in %q", rawURL)
	}

	if sc.BaseDir == "" {
		return nil
	}

	// Resolve the base dir (follow symlinks).
	base, err := filepath.EvalSymlinks(sc.BaseDir)
	if err != nil {
		return fmt.Errorf("resolve base dir %q: %w", sc.BaseDir, err)
	}

	// Resolve the target path. If the file doesn't exist yet, resolve its parent.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// File might not exist; resolve parent directory instead.
		dir := filepath.Dir(cleaned)
		resolved, err = filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("resolve path %q: %w", cleaned, err)
		}
		resolved = filepath.Join(resolved, filepath.Base(cleaned))
	}

	abs, err := filepath.Abs(resolved)
	if err != nil {
		return fmt.Errorf("absolute path %q: %w", resolved, err)
	}
	absBase, err := filepath.Abs(base)
	if err != nil {
		return fmt.Errorf("absolute base %q: %w", base, err)
	}

	// Ensure the resolved path is under the base directory.
	rel, err := filepath.Rel(absBase, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q escapes base directory %q", rawURL, sc.BaseDir)
	}

	return nil
}

// ValidateDimensions checks that width and height are within configured limits.
func (sc *SecurityConfig) ValidateDimensions(width, height int) error {
	if sc.MaxWidth > 0 && width > sc.MaxWidth {
		return fmt.Errorf("width %d exceeds maximum %d", width, sc.MaxWidth)
	}
	if sc.MaxHeight > 0 && height > sc.MaxHeight {
		return fmt.Errorf("height %d exceeds maximum %d", height, sc.MaxHeight)
	}
	return nil
}

// ValidateStreamCount checks that the number of streams is within limits.
func (sc *SecurityConfig) ValidateStreamCount(count int) error {
	if sc.MaxStreams > 0 && count > sc.MaxStreams {
		return fmt.Errorf("stream count %d exceeds maximum %d", count, sc.MaxStreams)
	}
	return nil
}

// ValidateConfig performs security validation on the pipeline config.
func (sc *SecurityConfig) ValidateConfig(cfg *Config) error {
	for _, inp := range cfg.Inputs {
		if err := sc.ValidateURL(inp.URL); err != nil {
			return fmt.Errorf("input %q: %w", inp.ID, err)
		}
	}
	for _, out := range cfg.Outputs {
		if err := sc.ValidateURL(out.URL); err != nil {
			return fmt.Errorf("output %q: %w", out.ID, err)
		}
	}
	return nil
}

// ConcurrencyLimiter tracks the number of active pipelines and enforces
// the MaxConcurrentPipelines limit. Thread-safe.
type ConcurrencyLimiter struct {
	sem chan struct{}
}

// NewConcurrencyLimiter creates a limiter. If max <= 0, no limit is enforced.
func NewConcurrencyLimiter(max int) *ConcurrencyLimiter {
	if max <= 0 {
		return &ConcurrencyLimiter{}
	}
	return &ConcurrencyLimiter{sem: make(chan struct{}, max)}
}

// Acquire blocks until a slot is available or returns false immediately
// if non-blocking mode is desired (the current implementation blocks).
func (l *ConcurrencyLimiter) Acquire() bool {
	if l.sem == nil {
		return true
	}
	l.sem <- struct{}{}
	return true
}

// TryAcquire returns true if a slot was acquired, false if none available.
func (l *ConcurrencyLimiter) TryAcquire() bool {
	if l.sem == nil {
		return true
	}
	select {
	case l.sem <- struct{}{}:
		return true
	default:
		return false
	}
}

// Release frees a slot.
func (l *ConcurrencyLimiter) Release() {
	if l.sem == nil {
		return
	}
	<-l.sem
}

// IsPathSafe is a convenience function that checks a file path for traversal
// attacks without requiring a SecurityConfig. It uses os.Getwd as the base.
func IsPathSafe(path string) error {
	cleaned := filepath.Clean(path)
	if strings.Contains(cleaned, "..") {
		return fmt.Errorf("path traversal detected in %q", path)
	}
	// Resolve symlinks.
	resolved, err := filepath.EvalSymlinks(cleaned)
	if err != nil {
		// If the file doesn't exist, resolve parent.
		dir := filepath.Dir(cleaned)
		resolved, err = filepath.EvalSymlinks(dir)
		if err != nil {
			return fmt.Errorf("resolve path %q: %w", cleaned, err)
		}
		resolved = filepath.Join(resolved, filepath.Base(cleaned))
	}

	wd, err := os.Getwd()
	if err != nil {
		return nil // cannot check; allow
	}
	abs, _ := filepath.Abs(resolved)
	absWd, _ := filepath.Abs(wd)
	rel, err := filepath.Rel(absWd, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return fmt.Errorf("path %q escapes working directory", path)
	}
	return nil
}
