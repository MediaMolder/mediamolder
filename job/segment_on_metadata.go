// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"fmt"
	"regexp"
)

// printfIntVerb matches a printf integer verb such as %d, %05d, %3d, etc.
var printfIntVerb = regexp.MustCompile(`%[0-9]*d`)

// validateSegmentOnMetadata checks that SegmentOnMetadata usage is coherent:
//   - URL must contain a printf integer verb (e.g. %05d) for segment numbering.
//   - Incompatible with kind=="tee" (tee muxer manages its own file lifecycle).
//   - Incompatible with realtime pre-roll (the preroll buffer does not support
//     mid-stream muxer replacement).
//   - Incompatible with CoverArt (cover art is only written to the first segment
//     and the remaining segments would be missing the cover; use a separate
//     cover-art pass instead).
func validateSegmentOnMetadata(out Output) error {
	if out.SegmentOnMetadata == "" {
		return nil
	}
	if !printfIntVerb.MatchString(out.URL) {
		return fmt.Errorf("output %q: segment_on_metadata requires URL to contain a printf integer verb (e.g. %%05d); got %q", out.ID, out.URL)
	}
	if out.Kind == "tee" {
		return fmt.Errorf("output %q: segment_on_metadata is incompatible with kind=tee", out.ID)
	}
	if out.Realtime != nil {
		return fmt.Errorf("output %q: segment_on_metadata is incompatible with realtime pre-roll", out.ID)
	}
	if out.CoverArt != "" {
		return fmt.Errorf("output %q: segment_on_metadata is incompatible with cover_art (cover art is written only to the first segment)", out.ID)
	}
	return nil
}
