// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package job

import (
	"fmt"
	"strings"
)

// attachmentCapableContainers is the closed set of muxers that
// accept `AVMEDIA_TYPE_ATTACHMENT` streams. matroska / mkv / webm
// share the same muxer (libavformat/matroskaenc.c) which writes the
// `Attachments` master element. mp4 attachments via the `mvex` /
// `udta` tree are not supported by libavformat. Wave 6 #31.
var attachmentCapableContainers = map[string]bool{
	"matroska": true,
	"mkv":      true,
	"webm":     true,
}

func validateAttachments(out Output) error {
	if len(out.Attachments) == 0 {
		return nil
	}
	if out.Format != "" && !attachmentCapableContainers[strings.ToLower(out.Format)] {
		return fmt.Errorf("output %q: attachments require a matroska / mkv / webm container (have format=%q)", out.ID, out.Format)
	}
	for i, a := range out.Attachments {
		if strings.TrimSpace(a.Path) == "" {
			return fmt.Errorf("output %q: attachments[%d].path is required", out.ID, i)
		}
	}
	return nil
}
