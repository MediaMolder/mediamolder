// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"encoding/json"
	"strings"
	"testing"
)

// initWithTransition builds a minimal two-clip sequence_editor params object
// whose first clip carries the given transition JSON, and runs Init. The
// transition snippet is the value of the clip's "transition" key (or "" for
// no transition).
func initWithTransition(t *testing.T, transition string) error {
	t.Helper()
	trans := ""
	if transition != "" {
		trans = `, "transition": ` + transition
	}
	raw := `{
      "format": { "width": 320, "height": 240, "pix_fmt": "yuv420p", "frame_rate": 30, "time_base": [1, 90000] },
      "tracks": [
        { "id": "V1", "type": "video", "clips": [
          { "url": "a.mp4", "source_in": 0, "source_out": 3, "timeline_in": 0` + trans + ` },
          { "url": "b.mp4", "source_in": 0, "source_out": 3, "timeline_in": 3 }
        ]}
      ]
    }`
	var params map[string]any
	if err := json.Unmarshal([]byte(raw), &params); err != nil {
		t.Fatalf("test JSON invalid: %v", err)
	}
	return (&SequenceEditor{}).Init(params)
}

// An unsupported transition type must be rejected at Init (not silently
// rendered as a hard cut), pointing the user at xfade_sequence.
func TestSequenceEditor_RejectsUnsupportedTransition(t *testing.T) {
	err := initWithTransition(t, `{ "type": "notarealtransition", "duration": 0.5 }`)
	if err == nil {
		t.Fatal("expected error for unsupported transition type")
	}
	if !strings.Contains(err.Error(), "notarealtransition") {
		t.Errorf("error %q should name the bad type", err.Error())
	}
}

// A transition block without a positive duration is meaningless and must error.
func TestSequenceEditor_RejectsTransitionWithoutDuration(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve" }`); err == nil {
		t.Fatal("expected error for transition with no duration")
	}
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0 }`); err == nil {
		t.Fatal("expected error for transition with zero duration")
	}
}

// dissolve is accepted; a bare {duration} defaults to dissolve.
func TestSequenceEditor_AcceptsDissolve(t *testing.T) {
	if err := initWithTransition(t, `{ "type": "dissolve", "duration": 0.5 }`); err != nil {
		t.Fatalf("dissolve rejected: %v", err)
	}
	if err := initWithTransition(t, `{ "duration": 0.5 }`); err != nil {
		t.Fatalf("bare-duration transition rejected: %v", err)
	}
	// No transition at all is fine (hard cut between clips).
	if err := initWithTransition(t, ""); err != nil {
		t.Fatalf("no-transition sequence rejected: %v", err)
	}
}

// xfade transition names are now accepted at Init (rendered via the xfade
// graph path).
func TestSequenceEditor_AcceptsXfadeTransitions(t *testing.T) {
	for _, typ := range []string{"fade", "wipeleft", "slideright", "circleopen", "fadeblack", "hblur"} {
		if err := initWithTransition(t, `{ "type": "`+typ+`", "duration": 0.5 }`); err != nil {
			t.Errorf("xfade transition %q rejected: %v", typ, err)
		}
	}
}
