// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// bitstream_trace scans an elementary video bitstream at the packet level —
// no decoding — and reports every NAL unit (H.264/H.265) or OBU (AV1) to a
// JSON, JSONL or trace_headers-format text file. It is an improved,
// machine-readable port of FFmpeg's `trace_headers` bitstream filter built
// on the cbs package (a Go port of libavcodec/cbs*).
//
// The node is a FrameSource that emits no frames: it opens the referenced
// input itself and walks packets with av.InputFormatContext.ReadPacket, so
// nothing in the graph decodes. See docs/bitstream-trace.md.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/cbs"
	"github.com/MediaMolder/MediaMolder/cbs/report"
)

// TraceConfig configures one bitstream trace run (shared by the
// bitstream_trace go_processor and `mediamolder trace-headers`).
type TraceConfig struct {
	// URL of the input container or elementary stream.
	URL string
	// Stream selects the stream: "" or "v:N" (Nth video stream) or a
	// plain stream index.
	Stream string
	// Options select the report format, detail level and filters.
	Options report.Options
	// OnPacket, when non-nil, is called after each packet is parsed
	// (events bus, progress).
	OnPacket func(pkt report.PacketInfo, frag *cbs.Fragment)
	// FileSize, when > 0, lets callers compute progress from
	// PacketInfo.Pos.
	FileSize int64
}

// resolveTraceStream picks the stream index for a Stream spec.
func resolveTraceStream(in *av.InputFormatContext, spec string) (int, av.StreamInfo, error) {
	streams, err := in.AllStreams()
	if err != nil {
		return 0, av.StreamInfo{}, err
	}
	spec = strings.TrimSpace(spec)
	if idx, err := strconv.Atoi(spec); err == nil && spec != "" {
		for _, si := range streams {
			if si.Index == idx {
				return idx, si, nil
			}
		}
		return 0, av.StreamInfo{}, fmt.Errorf("stream index %d not found", idx)
	}
	n := 0
	if strings.HasPrefix(spec, "v:") {
		if v, err := strconv.Atoi(spec[2:]); err == nil {
			n = v
		} else {
			return 0, av.StreamInfo{}, fmt.Errorf("invalid stream spec %q", spec)
		}
	} else if spec != "" && spec != "v" {
		return 0, av.StreamInfo{}, fmt.Errorf("invalid stream spec %q (want v:N or an index)", spec)
	}
	seen := 0
	for _, si := range streams {
		if si.Type == av.MediaTypeVideo {
			if seen == n {
				return si.Index, si, nil
			}
			seen++
		}
	}
	return 0, av.StreamInfo{}, fmt.Errorf("video stream v:%d not found", n)
}

// traceSourceFormat classifies the extradata shape for the report header.
func traceSourceFormat(codec cbs.CodecID, xd []byte) (format string, nalLengthSize int) {
	switch codec {
	case cbs.CodecH264:
		if len(xd) > 0 && xd[0] != 0 {
			if len(xd) >= 5 {
				return "avcc", int(xd[4]&3) + 1
			}
			return "avcc", 0
		}
		return "annexb", 0
	case cbs.CodecH265:
		if len(xd) > 0 && xd[0] != 0 {
			if len(xd) >= 22 {
				return "hvcc", int(xd[21]&3) + 1
			}
			return "hvcc", 0
		}
		return "annexb", 0
	case cbs.CodecAV1:
		if len(xd) > 0 && xd[0]&0x80 != 0 {
			return "av1c", 0
		}
		return "obu", 0
	}
	return "", 0
}

// RunBitstreamTrace opens cfg.URL, parses the selected stream's packets
// with the cbs codec and writes the report to out.
func RunBitstreamTrace(ctx context.Context, cfg TraceConfig, out io.Writer) error {
	in, err := av.OpenInput(cfg.URL, nil)
	if err != nil {
		return fmt.Errorf("bitstream_trace: open %q: %w", cfg.URL, err)
	}
	defer in.Close()

	idx, si, err := resolveTraceStream(in, cfg.Stream)
	if err != nil {
		return fmt.Errorf("bitstream_trace: %s: %w", cfg.URL, err)
	}

	codecID, ok := cbs.CodecFromAV(si.CodecID)
	if !ok {
		return fmt.Errorf("bitstream_trace: codec %s is not supported (H.264, H.265 and AV1 are)",
			av.CodecName(si.CodecID))
	}

	w, err := report.NewWriter(out, cfg.Options)
	if err != nil {
		return err
	}

	c, err := cbs.New(codecID, w.Tracer())
	if err != nil {
		return err
	}

	xd := in.StreamExtraData(idx)
	format, nls := traceSourceFormat(codecID, xd)
	src := report.Source{
		URL:           cfg.URL,
		StreamIndex:   idx,
		Codec:         codecID.String(),
		Profile:       av.ProfileName(si.CodecID, si.Profile),
		Format:        format,
		NalLengthSize: nls,
		TimeBase:      si.TimeBase,
		FrameRate:     si.FrameRate,
	}
	if err := w.BeginStream(src); err != nil {
		return err
	}

	if len(xd) > 0 {
		w.BeginExtradata()
		frag, xerr := c.ReadExtradata(xd)
		if err := w.EndExtradata(frag, xerr); err != nil {
			return err
		}
	}

	pkt, err := av.AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()

	var index int64
	for {
		if ctx != nil {
			select {
			case <-ctx.Done():
				_ = w.Close()
				return ctx.Err()
			default:
			}
		}
		rerr := in.ReadPacket(pkt)
		if rerr != nil {
			if errors.Is(rerr, av.ErrEOF) {
				break
			}
			_ = w.Close()
			return fmt.Errorf("bitstream_trace: read packet: %w", rerr)
		}
		if pkt.StreamIndex() != idx {
			pkt.Unref()
			continue
		}
		pi := report.PacketInfo{
			Index:    index,
			Size:     pkt.Size(),
			PTS:      pkt.PTS(),
			HasPTS:   pkt.PTS() != av.NoPTSValue,
			DTS:      pkt.DTS(),
			HasDTS:   pkt.DTS() != av.NoPTSValue,
			Duration: pkt.Duration(),
			Pos:      pkt.Pos(),
			KeyFrame: pkt.IsKeyFrame(),
			Corrupt:  pkt.IsCorrupt(),
		}
		index++

		w.BeginPacket(pi)
		frag, perr := c.ReadPacket(pkt.Data())
		if err := w.EndPacket(frag, perr); err != nil {
			pkt.Unref()
			_ = w.Close()
			return err
		}
		if cfg.OnPacket != nil {
			cfg.OnPacket(pi, frag)
		}
		pkt.Unref()
		if w.Done() {
			break
		}
	}

	return w.Close()
}

// BitstreamTrace is the bitstream_trace go_processor.
type BitstreamTrace struct {
	cfg        TraceConfig
	outputPath string
	emitEvents bool
	emit       MetadataEmitter
	fileSize   int64
}

func (p *BitstreamTrace) Init(params map[string]any) error {
	getString := func(key string) string {
		if v, ok := params[key].(string); ok {
			return v
		}
		return ""
	}

	p.cfg.URL = getString("url")
	if p.cfg.URL == "" {
		return fmt.Errorf("bitstream_trace: requires \"url\" (or \"input_id\" resolved by the engine)")
	}
	p.cfg.Stream = getString("stream")

	out := getString("output_file")
	if out == "" {
		return fmt.Errorf("bitstream_trace: requires \"output_file\" (absolute path)")
	}
	safe, err := sanitizeOutputPath(out)
	if err != nil {
		return fmt.Errorf("bitstream_trace: %w", err)
	}
	p.outputPath = safe

	p.cfg.Options.Format = getString("output_format")
	if p.cfg.Options.Format == "" {
		p.cfg.Options.Format = "json"
	}
	p.cfg.Options.Detail = getString("detail")
	if p.cfg.Options.Detail == "" {
		p.cfg.Options.Detail = "headers"
	}
	switch p.cfg.Options.Detail {
	case "summary", "headers", "elements":
	default:
		return fmt.Errorf("bitstream_trace: detail %q is not valid (want summary, headers, elements)", p.cfg.Options.Detail)
	}

	if raw, ok := params["unit_types"].([]any); ok {
		for _, v := range raw {
			switch t := v.(type) {
			case string:
				p.cfg.Options.UnitTypes = append(p.cfg.Options.UnitTypes, t)
			case float64:
				p.cfg.Options.UnitTypes = append(p.cfg.Options.UnitTypes, strconv.Itoa(int(t)))
			}
		}
	}
	if v, ok := params["max_packets"].(float64); ok {
		p.cfg.Options.MaxPackets = int64(v)
	}
	if raw, ok := params["packet_range"].([]any); ok && len(raw) == 2 {
		if a, ok := raw[0].(float64); ok {
			p.cfg.Options.Range[0] = int64(a)
		}
		if b, ok := raw[1].(float64); ok {
			p.cfg.Options.Range[1] = int64(b)
		}
	}
	if v, ok := params["emit_events"].(bool); ok {
		p.emitEvents = v
	}

	// Fail on an unwritable output before doing any work.
	f, err := os.OpenFile(p.outputPath, os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return fmt.Errorf("bitstream_trace: output_file: %w", err)
	}
	f.Close()

	return nil
}

// SetMetadataEmitter implements AsyncMetadataProcessor for emit_events.
func (p *BitstreamTrace) SetMetadataEmitter(emit MetadataEmitter) { p.emit = emit }

// Run implements FrameSource: the node produces no frames; all work
// happens against its own demuxer here.
func (p *BitstreamTrace) Run(ctx context.Context, _ func(*av.Frame) error) error {
	if st, err := os.Stat(p.cfg.URL); err == nil {
		p.fileSize = st.Size()
	}
	if p.emitEvents && p.emit != nil {
		p.cfg.OnPacket = p.onPacket
	}

	f, err := os.Create(p.outputPath)
	if err != nil {
		return fmt.Errorf("bitstream_trace: %w", err)
	}
	defer f.Close()

	if err := RunBitstreamTrace(ctx, p.cfg, f); err != nil {
		return err
	}
	if p.emit != nil {
		p.emit(&Metadata{FilePath: p.outputPath,
			LogMessage: fmt.Sprintf("bitstream_trace: report written to %s", p.outputPath)})
	}
	return nil
}

func (p *BitstreamTrace) onPacket(pkt report.PacketInfo, frag *cbs.Fragment) {
	md := &Metadata{Custom: map[string]any{
		"packet":   pkt.Index,
		"size":     pkt.Size,
		"keyframe": pkt.KeyFrame,
	}}
	if frag != nil {
		units := make([]map[string]any, 0, len(frag.Units))
		for i := range frag.Units {
			u := &frag.Units[i]
			units = append(units, map[string]any{
				"type": u.Type, "name": u.TypeName, "size": u.RawSize,
			})
		}
		md.Custom["units"] = units
	}
	if p.fileSize > 0 && pkt.Pos >= 0 {
		md.Progress = true
		md.Custom["progress"] = float64(pkt.Pos) / float64(p.fileSize)
	}
	p.emit(md)
}

// Process is unused: the node is a FrameSource with no inbound edges.
func (p *BitstreamTrace) Process(frame *av.Frame, _ ProcessorContext) (*av.Frame, *Metadata, error) {
	return frame, nil, nil
}

func (p *BitstreamTrace) Close() error { return nil }

func init() {
	Register("bitstream_trace", func() Processor { return &BitstreamTrace{} })
}
