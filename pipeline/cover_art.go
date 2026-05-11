// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: GPL-3.0-or-later

package pipeline

import (
	"fmt"
	"strings"
)

// coverArtCapableContainers is the closed set of libavformat muxers that
// support AV_DISPOSITION_ATTACHED_PIC on a video stream for cover art.
//
//   - mp4 / m4a / mov / ipod: libavformat/movenc.c writes the cover image
//     into the `covr` box when a video stream has AV_DISPOSITION_ATTACHED_PIC.
//   - mp3: libavformat/mp3enc.c writes the image as an ID3 APIC frame.
//   - mkv / matroska: libavformat/matroskaenc.c writes the stream as a
//     video track with the ATTACHED_PIC flag; some players surface it as
//     cover art. (Note: mkv also supports AV_DISPOSITION_ATTACHED_PIC via
//     the AVMEDIA_TYPE_VIDEO path, distinct from AVMEDIA_TYPE_ATTACHMENT.)
//
// Wave 11 #64.
var coverArtCapableContainers = map[string]bool{
	"mp4":      true,
	"m4a":      true,
	"mov":      true,
	"ipod":     true, // libavformat name used when muxing M4A/iPod
	"mp3":      true,
	"mkv":      true,
	"matroska": true,
}

func validateCoverArt(out Output) error {
	if out.CoverArt == "" {
		return nil
	}
	if out.Format != "" && !coverArtCapableContainers[strings.ToLower(out.Format)] {
		return fmt.Errorf("output %q: cover_art requires mp4 / m4a / mov / mp3 / mkv container (have format=%q)", out.ID, out.Format)
	}
	return nil
}
