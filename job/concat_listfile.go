// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

// concat_listfile.go — Wave 5 #28: serialise job.ConcatEntry
// into a temp file in the format libavformat/concatdec.c parses, so
// the editor can describe a concat playlist inline rather than
// shipping a sidecar `.txt` under version control.

import (
	"fmt"
	"os"
	"strconv"
	"strings"
)

// materialiseConcatList writes the given ConcatList to a temp file
// in the libavformat concat-demuxer grammar and returns its path
// plus a cleanup func the caller must invoke when the demuxer is
// done with the file. The grammar mirrors libavformat/concatdec.c:
//
//	ffconcat version 1.0
//	file 'segment1.mp4'
//	duration 5.0
//	inpoint 1.0
//	outpoint 6.0
//	file_packet_metadata key1=value1
//	file 'segment2.mp4'
//	…
//
// File paths are wrapped in single quotes; concatdec.c rejects
// embedded apostrophes (validated upstream by
// validateInputDemuxerFields), so no escaping is needed.
func materialiseConcatList(list []ConcatEntry) (string, func(), error) {
	f, err := os.CreateTemp("", "mediamolder-concat-*.txt")
	if err != nil {
		return "", nil, fmt.Errorf("create concat listfile: %w", err)
	}
	cleanup := func() {
		_ = os.Remove(f.Name())
	}
	var b strings.Builder
	b.WriteString("ffconcat version 1.0\n")
	for _, ent := range list {
		b.WriteString("file '")
		b.WriteString(ent.File)
		b.WriteString("'\n")
		if ent.Duration > 0 {
			b.WriteString("duration ")
			b.WriteString(strconv.FormatFloat(ent.Duration, 'f', -1, 64))
			b.WriteByte('\n')
		}
		if ent.InPoint > 0 {
			b.WriteString("inpoint ")
			b.WriteString(strconv.FormatFloat(ent.InPoint, 'f', -1, 64))
			b.WriteByte('\n')
		}
		if ent.OutPoint > 0 {
			b.WriteString("outpoint ")
			b.WriteString(strconv.FormatFloat(ent.OutPoint, 'f', -1, 64))
			b.WriteByte('\n')
		}
		for k, v := range ent.Metadata {
			b.WriteString("file_packet_metadata ")
			b.WriteString(k)
			b.WriteByte('=')
			b.WriteString(v)
			b.WriteByte('\n')
		}
	}
	if _, err := f.WriteString(b.String()); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, fmt.Errorf("write concat listfile: %w", err)
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, fmt.Errorf("close concat listfile: %w", err)
	}
	return f.Name(), cleanup, nil
}
