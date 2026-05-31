// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package ffcli

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/job"
)

// parseMasteringDisplay parses the canonical x265 / SVT-AV1
// `-master_display` and FFmpeg `-mastering_display_metadata` grammar:
//
//	G(gx,gy)B(bx,by)R(rx,ry)WP(wx,wy)L(maxL,minL)
//
// Coordinates are integers in 1/50000 chromaticity units; luminance is
// in 1/10000 cd/m^2. Order of the chunks is enforced (G, B, R, WP, L)
// to match x265's parser exactly. Returns a fully-populated
// MasteringDisplayMetadata struct on success.
func parseMasteringDisplay(s string) (*job.MasteringDisplayMetadata, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty mastering display spec")
	}
	md := &job.MasteringDisplayMetadata{}
	// Tokenise into "TAG(a,b)" chunks.
	var (
		i, n = 0, len(s)
		seen = map[string]bool{}
	)
	for i < n {
		// Skip whitespace.
		for i < n && (s[i] == ' ' || s[i] == '\t') {
			i++
		}
		if i >= n {
			break
		}
		tagStart := i
		for i < n && s[i] != '(' {
			i++
		}
		if i >= n {
			return nil, fmt.Errorf("missing '(' after tag in %q", s)
		}
		tag := s[tagStart:i]
		i++ // past '('
		valStart := i
		for i < n && s[i] != ')' {
			i++
		}
		if i >= n {
			return nil, fmt.Errorf("missing ')' for tag %q in %q", tag, s)
		}
		body := s[valStart:i]
		i++ // past ')'
		parts := strings.Split(body, ",")
		if len(parts) != 2 {
			return nil, fmt.Errorf("tag %q: want 2 comma-separated values, got %q", tag, body)
		}
		a, err := strconv.Atoi(strings.TrimSpace(parts[0]))
		if err != nil {
			return nil, fmt.Errorf("tag %q: %w", tag, err)
		}
		b, err := strconv.Atoi(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("tag %q: %w", tag, err)
		}
		if seen[tag] {
			return nil, fmt.Errorf("duplicate tag %q", tag)
		}
		seen[tag] = true
		switch tag {
		case "G":
			md.DisplayPrimariesGX, md.DisplayPrimariesGY = a, b
		case "B":
			md.DisplayPrimariesBX, md.DisplayPrimariesBY = a, b
		case "R":
			md.DisplayPrimariesRX, md.DisplayPrimariesRY = a, b
		case "WP":
			md.WhitePointX, md.WhitePointY = a, b
		case "L":
			// x265 emits L(max,min); FFmpeg's docs match. Store
			// max into MaxLuminance, min into MinLuminance.
			md.MaxLuminance, md.MinLuminance = a, b
		default:
			return nil, fmt.Errorf("unknown tag %q (want G|B|R|WP|L)", tag)
		}
	}
	if !seen["G"] || !seen["B"] || !seen["R"] || !seen["WP"] {
		return nil, fmt.Errorf("missing required tag(s) — need G, B, R, WP")
	}
	return md, nil
}

// parseContentLightLevel parses the canonical "MaxCLL,MaxFALL" grammar
// (also accepts ',' / '|' / whitespace separators). Both fields
// required; both must be non-negative integers in cd/m^2.
func parseContentLightLevel(s string) (*job.ContentLightLevelMetadata, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("empty content_light_level spec")
	}
	sep := func(r rune) bool { return r == ',' || r == '|' || r == ' ' || r == '\t' }
	parts := strings.FieldsFunc(s, sep)
	if len(parts) != 2 {
		return nil, fmt.Errorf("want \"MaxCLL,MaxFALL\", got %q", s)
	}
	maxCLL, err := strconv.ParseUint(parts[0], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("max_cll: %w", err)
	}
	maxFALL, err := strconv.ParseUint(parts[1], 10, 32)
	if err != nil {
		return nil, fmt.Errorf("max_fall: %w", err)
	}
	return &job.ContentLightLevelMetadata{
		MaxCLL:  uint32(maxCLL),
		MaxFALL: uint32(maxFALL),
	}, nil
}

// setDoViField applies a `-dovi_*` flag value to the pending Dolby
// Vision metadata. Wave 6 #35.
func setDoViField(dv *job.DoViMetadata, flag, val string) error {
	switch flag {
	case "-dovi_profile":
		n, err := strconv.ParseUint(val, 10, 8)
		if err != nil {
			return fmt.Errorf("profile: %w", err)
		}
		dv.Profile = uint8(n)
	case "-dovi_level":
		n, err := strconv.ParseUint(val, 10, 8)
		if err != nil {
			return fmt.Errorf("level: %w", err)
		}
		dv.Level = uint8(n)
	case "-dovi_bl_compatibility_id":
		n, err := strconv.ParseUint(val, 10, 8)
		if err != nil {
			return fmt.Errorf("bl_compatibility_id: %w", err)
		}
		dv.BLCompatibilityID = uint8(n)
	case "-dovi_rpu_present":
		b, err := parseBoolFlag(val)
		if err != nil {
			return fmt.Errorf("rpu_present: %w", err)
		}
		dv.RPUPresent = &b
	case "-dovi_el_present":
		b, err := parseBoolFlag(val)
		if err != nil {
			return fmt.Errorf("el_present: %w", err)
		}
		dv.ELPresent = b
	case "-dovi_bl_present":
		b, err := parseBoolFlag(val)
		if err != nil {
			return fmt.Errorf("bl_present: %w", err)
		}
		dv.BLPresent = &b
	default:
		return fmt.Errorf("unknown dovi flag")
	}
	return nil
}

// parseBoolFlag is defined in hls_dash.go.
