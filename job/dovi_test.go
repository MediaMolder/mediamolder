// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"strings"
	"testing"
)

func validDoViOutput() Output {
	o := validHDROutput()
	o.HDR.DoVi = &DoViMetadata{Profile: 8, Level: 9, BLCompatibilityID: 1}
	return o
}

func TestValidateDoVi_Valid(t *testing.T) {
	if err := validate(baseHDRConfig(validDoViOutput())); err != nil {
		t.Fatalf("expected valid dovi config, got %v", err)
	}
}

func TestValidateDoVi_RejectsBadProfile(t *testing.T) {
	o := validDoViOutput()
	o.HDR.DoVi.Profile = 6
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "profile=6") {
		t.Fatalf("expected profile=6 rejection, got %v", err)
	}
}

func TestValidateDoVi_RequiresProfile(t *testing.T) {
	o := validDoViOutput()
	o.HDR.DoVi.Profile = 0
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "profile is required") {
		t.Fatalf("expected profile-required error, got %v", err)
	}
}

func TestValidateDoVi_RejectsBadLevel(t *testing.T) {
	o := validDoViOutput()
	o.HDR.DoVi.Level = 14
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "level=14") {
		t.Fatalf("expected level error, got %v", err)
	}
}

func TestValidateDoVi_RejectsBadCompat(t *testing.T) {
	o := validDoViOutput()
	o.HDR.DoVi.BLCompatibilityID = 3
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "bl_compatibility_id=3") {
		t.Fatalf("expected compat error, got %v", err)
	}
}

func TestValidateDoVi_RejectsUnsupportedCodec(t *testing.T) {
	o := validDoViOutput()
	o.CodecVideo = "vp9"
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "HEVC / AV1 / H.264") {
		t.Fatalf("expected codec rejection, got %v", err)
	}
}

func TestValidateDoVi_RejectsUnsupportedContainer(t *testing.T) {
	o := validDoViOutput()
	o.Format = "webm"
	o.URL = "out.webm"
	err := validate(baseHDRConfig(o))
	if err == nil || !strings.Contains(err.Error(), "mp4 / mov / matroska") {
		t.Fatalf("expected container rejection, got %v", err)
	}
}
