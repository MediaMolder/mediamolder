// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// ProcessorMetadataEvent carries metadata emitted by a go_processor node.
// It is published on the pipeline event bus after each Process call that
// returns non-nil Metadata.
type ProcessorMetadataEvent struct {
	NodeID     string
	FrameIndex uint64
	PTS        int64
	Metadata   *Metadata
}
