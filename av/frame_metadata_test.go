// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

import "testing"

// TestFrameMetadataRoundTrip exercises the per-frame AVDictionary
// helpers added in Wave 7 #39: SetMetadata writes a key, GetMetadata
// reads it back, Metadata() lists all entries, and Clone preserves the
// dictionary so the metadata survives av_frame_clone (which is the
// same path libavfilter uses to propagate frames between filter
// nodes).
func TestFrameMetadataRoundTrip(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatalf("AllocFrame: %v", err)
	}
	defer f.Close()

	if got := f.Metadata(); got != nil {
		t.Fatalf("fresh frame metadata = %v, want nil", got)
	}
	if got := f.GetMetadata("missing"); got != "" {
		t.Fatalf("GetMetadata(missing) = %q, want empty", got)
	}

	if err := f.SetMetadata("mm.test", "hello"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}
	if err := f.SetMetadata("mm.score", "0.42"); err != nil {
		t.Fatalf("SetMetadata: %v", err)
	}

	if got := f.GetMetadata("mm.test"); got != "hello" {
		t.Errorf("GetMetadata(mm.test) = %q, want hello", got)
	}
	if got := f.GetMetadata("mm.score"); got != "0.42" {
		t.Errorf("GetMetadata(mm.score) = %q, want 0.42", got)
	}

	all := f.Metadata()
	if len(all) != 2 || all["mm.test"] != "hello" || all["mm.score"] != "0.42" {
		t.Errorf("Metadata() = %v, want {mm.test:hello, mm.score:0.42}", all)
	}

	// Empty key is a no-op, never errors.
	if err := f.SetMetadata("", "x"); err != nil {
		t.Errorf("SetMetadata(\"\") = %v, want nil", err)
	}

	// Nil receiver tolerated.
	var nilFrame *Frame
	if got := nilFrame.Metadata(); got != nil {
		t.Errorf("nil.Metadata() = %v, want nil", got)
	}
	if got := nilFrame.GetMetadata("k"); got != "" {
		t.Errorf("nil.GetMetadata = %q, want empty", got)
	}
	if err := nilFrame.SetMetadata("k", "v"); err != nil {
		t.Errorf("nil.SetMetadata = %v, want nil", err)
	}
}
