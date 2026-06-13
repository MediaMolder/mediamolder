// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"net/url"
	"strings"
)

// validateSecurity runs SEC_* checks using the provided SecurityConfig.
// Called only when sec != nil.
func validateSecurity(cfg *Config, sec *SecurityConfig, r *ValidationReport) {
	checkURLSecurity(cfg, sec, r)
	checkMaxStreams(cfg, sec, r)
	checkMaxThreads(cfg, sec, r)
	checkMaxDimensions(cfg, sec, r)
}

func checkURLSecurity(cfg *Config, sec *SecurityConfig, r *ValidationReport) {
	checkURL := func(rawURL, location string) {
		if rawURL == "" {
			return
		}
		// Reuse the existing ValidateURL for scheme and path-traversal checks.
		if err := sec.ValidateURL(rawURL); err != nil {
			msg := err.Error()
			code := "SEC_PATH_TRAVERSAL"
			if strings.Contains(msg, "scheme") {
				code = "SEC_DISALLOWED_SCHEME"
			}
			r.add(ValidationIssue{
				Severity:   SeverityError,
				Code:       code,
				Location:   location,
				Message:    fmt.Sprintf("URL %q failed security check: %s", rawURL, msg),
				Suggestion: buildURLSecuritySuggestion(rawURL, sec),
			})
		}
	}

	for _, inp := range cfg.Inputs {
		checkURL(inp.URL, "input:"+inp.ID)
	}
	for _, out := range cfg.Outputs {
		checkURL(out.URL, "output:"+out.ID)
	}
}

func buildURLSecuritySuggestion(rawURL string, sec *SecurityConfig) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	scheme := strings.ToLower(u.Scheme)
	if scheme == "" {
		scheme = "file"
	}
	allowed := sec.allowedSchemeList()
	if !sec.allowedSchemeSet()[scheme] {
		return fmt.Sprintf("use one of the allowed URL schemes: %v", allowed)
	}
	if sec.BaseDir != "" {
		return fmt.Sprintf("ensure the path is inside the configured base directory %q", sec.BaseDir)
	}
	return ""
}

func checkMaxStreams(cfg *Config, sec *SecurityConfig, r *ValidationReport) {
	if sec.MaxStreams <= 0 {
		return
	}
	total := len(cfg.Outputs)
	if total > sec.MaxStreams {
		r.add(ValidationIssue{
			Severity: SeverityError,
			Code:     "SEC_MAX_STREAMS_EXCEEDED",
			Message: fmt.Sprintf(
				"pipeline has %d output streams; security policy allows at most %d",
				total, sec.MaxStreams),
			Suggestion: fmt.Sprintf("reduce the number of outputs to %d or fewer, or increase MaxStreams in the security config", sec.MaxStreams),
		})
	}
}

func checkMaxThreads(cfg *Config, sec *SecurityConfig, r *ValidationReport) {
	if sec.MaxThreads <= 0 {
		return
	}

	// Check the global option first.
	if cfg.GlobalOptions.Threads > sec.MaxThreads {
		r.add(ValidationIssue{
			Severity: SeverityWarning,
			Code:     "SEC_MAX_THREADS_EXCEEDED",
			Message: fmt.Sprintf(
				"global threads=%d exceeds the security limit of %d",
				cfg.GlobalOptions.Threads, sec.MaxThreads),
			Suggestion: fmt.Sprintf("set global threads to %d or below", sec.MaxThreads),
		})
	}

	// Check per-encoder-node thread params.
	for _, nd := range cfg.Graph.Nodes {
		if nd.Type != "encoder" {
			continue
		}
		v, ok := nd.Params["threads"]
		if !ok {
			continue
		}
		t := paramToInt(v)
		if t > 0 && t > sec.MaxThreads {
			r.add(ValidationIssue{
				Severity: SeverityWarning,
				Code:     "SEC_MAX_THREADS_EXCEEDED",
				Location: "node:" + nd.ID,
				Message: fmt.Sprintf(
					"encoder node %q has threads=%d which exceeds the security limit of %d",
					nd.ID, t, sec.MaxThreads),
				Suggestion: fmt.Sprintf("reduce threads to %d or below", sec.MaxThreads),
			})
		}
	}
}

func checkMaxDimensions(cfg *Config, sec *SecurityConfig, r *ValidationReport) {
	if sec.MaxWidth <= 0 && sec.MaxHeight <= 0 {
		return
	}

	for _, nd := range cfg.Graph.Nodes {
		if nd.Type != "encoder" {
			continue
		}
		for _, wKey := range []string{"width", "w"} {
			v, ok := nd.Params[wKey]
			if !ok {
				continue
			}
			w := paramToInt(v)
			if w > 0 && sec.MaxWidth > 0 && w > sec.MaxWidth {
				r.add(ValidationIssue{
					Severity: SeverityError,
					Code:     "SEC_MAX_DIMENSIONS_EXCEEDED",
					Location: "node:" + nd.ID,
					Message: fmt.Sprintf(
						"encoder node %q width=%d exceeds security limit of %d",
						nd.ID, w, sec.MaxWidth),
					Suggestion: fmt.Sprintf("reduce width to %d or below", sec.MaxWidth),
				})
			}
		}
		for _, hKey := range []string{"height", "h"} {
			v, ok := nd.Params[hKey]
			if !ok {
				continue
			}
			h := paramToInt(v)
			if h > 0 && sec.MaxHeight > 0 && h > sec.MaxHeight {
				r.add(ValidationIssue{
					Severity: SeverityError,
					Code:     "SEC_MAX_DIMENSIONS_EXCEEDED",
					Location: "node:" + nd.ID,
					Message: fmt.Sprintf(
						"encoder node %q height=%d exceeds security limit of %d",
						nd.ID, h, sec.MaxHeight),
					Suggestion: fmt.Sprintf("reduce height to %d or below", sec.MaxHeight),
				})
			}
		}
	}
}
