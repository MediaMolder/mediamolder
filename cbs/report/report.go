// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package report renders cbs parse results as a JSON document, JSON Lines
// stream, or FFmpeg trace_headers-format text. It is the sink shared by
// the bitstream_trace go_processor node and the `mediamolder trace-headers`
// CLI subcommand.
package report

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// Options selects the output shape.
type Options struct {
	// Format: "json" (single streamed document, default), "jsonl" (one
	// object per packet), "csv" (one row per unit — coded pictures fill
	// dedicated columns, other units carry a compact-JSON summary column;
	// element detail is json/jsonl-only), or "text" (trace_headers-format
	// lines).
	Format string
	// Detail: "elements" (full syntax-element trace), "headers"
	// (elements for parameter sets / SEI / metadata, summaries for
	// slices and frames), or "summary" (no element sections at all).
	Detail string
	// UnitTypes limits which units get full output: numeric NAL/OBU
	// types or names matched case-insensitively against the unit's
	// TypeName ("SPS", "IDR", "SEI", ...); "slice" matches all
	// slice/frame-carrying units. Empty = all units. Filtered-out units
	// are still counted in stats.
	UnitTypes []string
	// MaxPackets stops detailed output after this many packets
	// (0 = unlimited); the driver should stop feeding packets once
	// Done() reports true.
	MaxPackets int64
	// Range limits detailed output to packet indices [Range[0], Range[1]]
	// (0-based, inclusive at both ends: 0:0 is exactly the first packet).
	// Active only when RangeSet is true, so 0:0 is a valid window.
	Range    [2]int64
	RangeSet bool
}

// packetGate applies MaxPackets and the inclusive packet-index range
// uniformly across output formats. Every packet is still parsed (codec
// state must advance); the gate only controls what is written and when
// the driver may stop reading.
type packetGate struct {
	maxPackets int64
	rangeSet   bool
	lo, hi     int64
	written    int64 // packets actually output
	seen       int64 // packets delivered to the writer
}

func newPacketGate(opts Options) packetGate {
	return packetGate{
		maxPackets: opts.MaxPackets,
		rangeSet:   opts.RangeSet,
		lo:         opts.Range[0],
		hi:         opts.Range[1],
	}
}

// include reports whether the packet at idx belongs in the output.
func (g *packetGate) include(idx int64) bool {
	if g.maxPackets > 0 && g.written >= g.maxPackets {
		return false
	}
	if g.rangeSet && (idx < g.lo || idx > g.hi) {
		return false
	}
	return true
}

// note records the outcome for idx; call once per packet, after include.
func (g *packetGate) note(idx int64, included bool) {
	g.seen = idx + 1
	if included {
		g.written++
	}
}

// done reports that nothing further can be written, so the driver may
// stop feeding packets.
func (g *packetGate) done() bool {
	if g.maxPackets > 0 && g.written >= g.maxPackets {
		return true
	}
	if g.rangeSet && g.seen > g.hi {
		return true
	}
	return false
}

// Source describes the stream being traced (the report header).
type Source struct {
	URL           string `json:"url,omitempty"`
	StreamIndex   int    `json:"stream_index"`
	Codec         string `json:"codec"`
	Profile       string `json:"profile,omitempty"`
	Format        string `json:"format,omitempty"` // annexb | avcc | hvcc | av1c | obu
	NalLengthSize int    `json:"nal_length_size,omitempty"`
	TimeBase      [2]int `json:"time_base,omitempty"`
	FrameRate     [2]int `json:"frame_rate,omitempty"`
}

// PacketInfo carries the packet-level metadata the report records
// (mirrors the fields trace_headers prints, plus index and position).
type PacketInfo struct {
	Index    int64
	Size     int
	PTS      int64
	HasPTS   bool
	DTS      int64
	HasDTS   bool
	Duration int64
	Pos      int64 // byte position in the input file, -1 if unknown
	KeyFrame bool
	Corrupt  bool
}

// Writer receives the parse stream. Begin* must be called before the
// corresponding cbs.Read* call so that text output interleaves exactly
// like trace_headers; End* delivers the parsed fragment.
type Writer interface {
	// Tracer returns the tracer to pass to cbs.New.
	Tracer() cbs.Tracer
	BeginStream(src Source) error
	BeginExtradata()
	EndExtradata(frag *cbs.Fragment, err error) error
	BeginPacket(pkt PacketInfo)
	EndPacket(frag *cbs.Fragment, err error) error
	// Done reports that MaxPackets / Range have been exhausted and the
	// driver may stop early.
	Done() bool
	// Close flushes and writes the stats trailer.
	Close() error
}

// NewWriter returns a Writer for the given options.
func NewWriter(w io.Writer, opts Options) (Writer, error) {
	switch opts.Format {
	case "", "json", "jsonl":
		return newJSONWriter(w, opts), nil
	case "csv":
		return newCSVWriter(w, opts), nil
	case "text":
		return newTextWriter(w, opts), nil
	}
	return nil, fmt.Errorf("report: unknown format %q (json, jsonl, csv, text)", opts.Format)
}

// --- unit-type filter ---

type unitFilter struct {
	names   map[string]bool
	numbers map[uint32]bool
	slices  bool
	all     bool
}

// unitNameAliases expands the family names of the proposal ("sps", "sei",
// "idr", ...) to every per-codec TypeName they cover, so the same filter
// spec works across H.264, H.265 and AV1.
var unitNameAliases = map[string][]string{
	"sps":      {"sps", "sequence_header"},
	"seq":      {"sequence_header"},
	"sei":      {"sei", "sei_prefix", "sei_suffix"},
	"idr":      {"idr", "idr_w_radl", "idr_n_lp"},
	"td":       {"temporal_delimiter"},
	"metadata": {"metadata"},
}

func newUnitFilter(types []string) unitFilter {
	f := unitFilter{names: map[string]bool{}, numbers: map[uint32]bool{}}
	if len(types) == 0 {
		f.all = true
		return f
	}
	for _, t := range types {
		t = strings.TrimSpace(strings.ToLower(t))
		if t == "" {
			continue
		}
		if n, err := strconv.ParseUint(t, 10, 32); err == nil {
			f.numbers[uint32(n)] = true
			continue
		}
		if t == "slice" || t == "slices" {
			f.slices = true
			continue
		}
		if expanded, ok := unitNameAliases[t]; ok {
			for _, name := range expanded {
				f.names[name] = true
			}
			continue
		}
		f.names[t] = true
	}
	return f
}

func (f unitFilter) match(u *cbs.Unit) bool {
	if f.all {
		return true
	}
	if f.numbers[u.Type] {
		return true
	}
	name := strings.ToLower(u.TypeName)
	if f.names[name] {
		return true
	}
	if f.slices && isSliceLike(u) {
		return true
	}
	return false
}

// isSliceLike reports units that carry picture data (slice / frame /
// tile-group payloads) — the ones "headers" detail summarises rather than
// dumping element by element.
func isSliceLike(u *cbs.Unit) bool {
	switch u.Content.(type) {
	case *cbs.H264RawSlice:
		return true
	}
	n := strings.ToLower(u.TypeName)
	switch n {
	case "idr", "coded slice of a non-idr picture",
		"auxiliary coded picture without partitioning",
		"frame", "tile_group", "frame_header", "redundant_frame_header":
		return true
	}
	// H.265 VCL types 0..21.
	if strings.HasPrefix(n, "trail_") || strings.HasPrefix(n, "tsa_") ||
		strings.HasPrefix(n, "stsa_") || strings.HasPrefix(n, "radl_") ||
		strings.HasPrefix(n, "rasl_") || strings.HasPrefix(n, "bla_") ||
		strings.HasPrefix(n, "idr_") || n == "cra_nut" {
		return true
	}
	return false
}

// --- collecting tracer (JSON path) ---

type sectionJSON struct {
	Name     string        `json:"name"`
	Elements []elementJSON `json:"elements"`
}

type elementJSON struct {
	Pos   int    `json:"pos"`
	Bits  int    `json:"bits"`
	Name  string `json:"name"`
	Subs  []int  `json:"subs,omitempty"`
	Raw   string `json:"raw,omitempty"`
	Value int64  `json:"value"`
}

type unitEvents struct {
	sections []sectionJSON
	diags    []string
}

// collector implements cbs.UnitBoundaryTracer, bucketing trace events per
// unit; events outside a unit belong to the fragment (split phase).
type collector struct {
	units     map[int]*unitEvents
	fragDiags []string
	cur       *unitEvents
	elements  bool // record element sections at all
}

func newCollector() *collector {
	return &collector{units: map[int]*unitEvents{}}
}

func (c *collector) reset() {
	c.units = map[int]*unitEvents{}
	c.fragDiags = nil
	c.cur = nil
}

func (c *collector) BeginUnit(index int) {
	ue := &unitEvents{}
	c.units[index] = ue
	c.cur = ue
}

func (c *collector) EndUnit(index int) { c.cur = nil }

func (c *collector) Header(name string) {
	if c.cur == nil || !c.elements {
		return
	}
	c.cur.sections = append(c.cur.sections, sectionJSON{Name: name})
}

func (c *collector) Element(e cbs.Element) {
	if c.cur == nil || !c.elements {
		return
	}
	if len(c.cur.sections) == 0 {
		c.cur.sections = append(c.cur.sections, sectionJSON{})
	}
	s := &c.cur.sections[len(c.cur.sections)-1]
	s.Elements = append(s.Elements, elementJSON{
		Pos:   e.Position,
		Bits:  e.Length,
		Name:  e.DisplayName(),
		Subs:  e.Subscripts,
		Raw:   e.Bits,
		Value: e.Value,
	})
}

func (c *collector) Diag(level cbs.Level, msg string) {
	tagged := levelTag(level) + msg
	if c.cur != nil {
		c.cur.diags = append(c.cur.diags, tagged)
	} else {
		c.fragDiags = append(c.fragDiags, tagged)
	}
}

func levelTag(level cbs.Level) string {
	switch level {
	case cbs.LevelError:
		return "error: "
	case cbs.LevelWarning:
		return "warning: "
	case cbs.LevelVerbose, cbs.LevelDebug:
		return "verbose: "
	}
	return ""
}

// --- stats ---

type stats struct {
	Packets int64            `json:"packets"`
	Units   int64            `json:"units"`
	ByType  map[string]int64 `json:"by_type"`
	Skipped int64            `json:"skipped"`
	Errors  int64            `json:"errors"`
}

func (st *stats) addFragment(frag *cbs.Fragment) {
	if frag == nil {
		return
	}
	for i := range frag.Units {
		u := &frag.Units[i]
		st.Units++
		st.ByType[strconv.FormatUint(uint64(u.Type), 10)]++
		if u.Skip != "" {
			st.Skipped++
		}
		if u.Err != nil {
			st.Errors++
		}
	}
}

// --- JSON / JSONL writer ---

type unitJSON struct {
	Index      int            `json:"index"`
	Offset     int            `json:"offset"`
	Prefix     int            `json:"prefix,omitempty"`
	Size       int            `json:"size"`
	RbspSize   int            `json:"rbsp_size"`
	EPB        []int          `json:"epb,omitempty"`
	Type       uint32         `json:"type"`
	Name       string         `json:"name"`
	Class      string         `json:"class"`
	Header     map[string]any `json:"header,omitempty"`
	Picture    *pictureJSON   `json:"picture,omitempty"`
	Summary    map[string]any `json:"summary,omitempty"`
	Sections   []sectionJSON  `json:"sections,omitempty"`
	Decomposed bool           `json:"decomposed"`
	Skip       string         `json:"skip,omitempty"`
	Error      string         `json:"error,omitempty"`
	Diags      []string       `json:"diagnostics,omitempty"`
}

type packetJSON struct {
	Index    int64      `json:"index"`
	PTS      *int64     `json:"pts"`
	DTS      *int64     `json:"dts"`
	Time     *float64   `json:"time,omitempty"` // pts in seconds
	Duration int64      `json:"duration,omitempty"`
	Pos      int64      `json:"pos,omitempty"`
	Size     int        `json:"size"`
	KeyFrame bool       `json:"key_frame"`
	Corrupt  bool       `json:"corrupt,omitempty"`
	Units    []unitJSON `json:"units"`
	Error    string     `json:"error,omitempty"`
	Diags    []string   `json:"diagnostics,omitempty"`
}

type jsonWriter struct {
	w           *bufio.Writer
	codec       string
	opts        Options
	jsonl       bool
	col         *collector
	colElements bool // detail wants element sections at all
	filter      unitFilter
	sum         *summarizer
	tb          [2]int
	st          stats
	gate        packetGate
	started     bool
	inPkts      bool
	first       bool
	curPkt      PacketInfo
	includeCur  bool
	err         error
}

func newJSONWriter(w io.Writer, opts Options) *jsonWriter {
	jw := &jsonWriter{
		w:      bufio.NewWriter(w),
		opts:   opts,
		jsonl:  opts.Format == "jsonl",
		col:    newCollector(),
		filter: newUnitFilter(opts.UnitTypes),
		sum:    newSummarizer(),
		st:     stats{ByType: map[string]int64{}},
		gate:   newPacketGate(opts),
		first:  true,
	}
	jw.colElements = opts.Detail != "summary"
	jw.col.elements = jw.colElements
	return jw
}

func (jw *jsonWriter) Tracer() cbs.Tracer { return jw.col }

// writeBytes / writeString / writeByte capture the first write error into
// jw.err (bufio errors are sticky; Close reports them).
func (jw *jsonWriter) writeBytes(b []byte) {
	if _, err := jw.w.Write(b); err != nil && jw.err == nil {
		jw.err = err
	}
}

func (jw *jsonWriter) writeString(str string) {
	if _, err := jw.w.WriteString(str); err != nil && jw.err == nil {
		jw.err = err
	}
}

func (jw *jsonWriter) writeByte(c byte) {
	if err := jw.w.WriteByte(c); err != nil && jw.err == nil {
		jw.err = err
	}
}

func (jw *jsonWriter) marshal(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil && jw.err == nil {
		jw.err = err
	}
	return b
}

func (jw *jsonWriter) BeginStream(src Source) error {
	hdr := struct {
		Schema string `json:"schema"`
		Source Source `json:"source"`
	}{"mediamolder.bitstream_trace/2", src}
	jw.codec = src.Codec
	jw.tb = src.TimeBase
	if jw.jsonl {
		jw.writeBytes(jw.marshal(hdr))
		jw.writeByte('\n')
	} else {
		jw.writeString(`{"schema":"mediamolder.bitstream_trace/2","source":`)
		jw.writeBytes(jw.marshal(src))
	}
	jw.started = true
	return jw.err
}

func (jw *jsonWriter) BeginExtradata() {
	jw.col.reset()
	jw.col.elements = jw.colElements
}

func (jw *jsonWriter) EndExtradata(frag *cbs.Fragment, err error) error {
	jw.st.addFragment(frag)
	xd := struct {
		Size  int        `json:"size"`
		Units []unitJSON `json:"units"`
		Error string     `json:"error,omitempty"`
		Diags []string   `json:"diagnostics,omitempty"`
	}{Units: []unitJSON{}}
	if frag != nil {
		xd.Size = len(frag.Data)
		xd.Units = jw.unitsJSON(frag)
	}
	if err != nil {
		xd.Error = err.Error()
	}
	xd.Diags = jw.col.fragDiags
	if jw.jsonl {
		jw.writeBytes(jw.marshal(struct {
			Extradata any `json:"extradata"`
		}{xd}))
		jw.writeByte('\n')
	} else {
		jw.writeString(`,"extradata":`)
		jw.writeBytes(jw.marshal(xd))
	}
	return jw.err
}

func (jw *jsonWriter) Done() bool { return jw.gate.done() }

// BeginPacket decides inclusion (same point as the text writer, so the
// two formats can never drift on range semantics) and skips element
// collection for excluded packets; parsing still runs for codec state.
func (jw *jsonWriter) BeginPacket(pkt PacketInfo) {
	jw.col.reset()
	jw.curPkt = pkt
	jw.includeCur = jw.gate.include(pkt.Index)
	jw.col.elements = jw.colElements && jw.includeCur
}

func (jw *jsonWriter) EndPacket(frag *cbs.Fragment, err error) error {
	jw.st.Packets++
	jw.st.addFragment(frag)
	jw.gate.note(jw.curPkt.Index, jw.includeCur)
	if !jw.includeCur {
		// Skipped packets still advance decode-order state (parameter
		// sets, POC previous-picture tracking).
		if frag != nil {
			for i := range frag.Units {
				jw.sum.advance(&frag.Units[i])
			}
		}
		return jw.err
	}

	pkt := jw.curPkt
	pj := packetJSON{
		Index:    pkt.Index,
		Duration: pkt.Duration,
		Pos:      pkt.Pos,
		Size:     pkt.Size,
		KeyFrame: pkt.KeyFrame,
		Corrupt:  pkt.Corrupt,
		Units:    []unitJSON{},
		Diags:    jw.col.fragDiags,
	}
	if pkt.HasPTS {
		v := pkt.PTS
		pj.PTS = &v
		if sec, ok := packetTime(jw.tb, pkt.PTS); ok {
			pj.Time = &sec
		}
	}
	if pkt.HasDTS {
		v := pkt.DTS
		pj.DTS = &v
	}
	if frag != nil {
		pj.Units = jw.unitsJSON(frag)
	}
	if err != nil {
		pj.Error = err.Error()
	}

	if jw.jsonl {
		jw.writeBytes(jw.marshal(struct {
			Packet packetJSON `json:"packet"`
		}{pj}))
		jw.writeByte('\n')
	} else {
		if !jw.inPkts {
			jw.writeString(`,"packets":[`)
			jw.inPkts = true
		}
		if !jw.first {
			jw.writeByte(',')
		}
		jw.first = false
		jw.writeByte('\n')
		jw.writeBytes(jw.marshal(pj))
	}
	return jw.err
}

func (jw *jsonWriter) unitsJSON(frag *cbs.Fragment) []unitJSON {
	out := make([]unitJSON, 0, len(frag.Units))
	for i := range frag.Units {
		u := &frag.Units[i]
		poc, hasPOC := jw.sum.advance(u)
		uj := unitJSON{
			Index:      i,
			Offset:     u.Offset,
			Prefix:     u.PrefixSize,
			Size:       u.RawSize,
			RbspSize:   len(u.RBSP),
			Type:       u.Type,
			Name:       u.TypeName,
			Class:      classify(jw.codec, u),
			Header:     unitHeaderJSON(jw.codec, u),
			Decomposed: u.Decomposed,
			Skip:       u.Skip,
		}
		if u.Err != nil {
			uj.Error = u.Err.Error()
		}
		if !jw.filter.match(u) {
			// Filtered: identity only, no content.
			out = append(out, uj)
			continue
		}
		if jw.opts.Detail == "elements" || jw.opts.Detail == "" {
			uj.EPB = u.EPBPositions
		}
		if pic := pictureOf(u, poc, hasPOC); pic != nil {
			uj.Picture = pic
		} else {
			uj.Summary = summarize(u)
		}
		if ue := jw.col.units[i]; ue != nil {
			uj.Diags = ue.diags
			includeSections := false
			switch jw.opts.Detail {
			case "", "elements":
				includeSections = true
			case "headers":
				includeSections = !isSliceLike(u)
			}
			if includeSections {
				uj.Sections = ue.sections
			}
		}
		out = append(out, uj)
	}
	return out
}

func (jw *jsonWriter) Close() error {
	if jw.jsonl {
		jw.writeBytes(jw.marshal(struct {
			Stats stats `json:"stats"`
		}{jw.st}))
		jw.writeByte('\n')
	} else {
		if !jw.started {
			jw.writeString(`{"schema":"mediamolder.bitstream_trace/2"`)
		}
		if jw.inPkts {
			jw.writeString("\n]")
		} else {
			jw.writeString(`,"packets":[]`)
		}
		jw.writeString(`,"stats":`)
		jw.writeBytes(jw.marshal(jw.st))
		jw.writeString("}\n")
	}
	if err := jw.w.Flush(); err != nil && jw.err == nil {
		jw.err = err
	}
	return jw.err
}

// --- text writer (trace_headers parity) ---

type textWriter struct {
	tt       *cbs.TextTracer
	opts     Options
	st       stats
	gate     packetGate
	curIdx   int64
	suppress bool
}

func newTextWriter(w io.Writer, opts Options) *textWriter {
	return &textWriter{tt: cbs.NewTextTracer(w), opts: opts,
		gate: newPacketGate(opts),
		st:   stats{ByType: map[string]int64{}}}
}

// gatedTextTracer drops trace events while the writer is inside an
// out-of-range packet; parsing still runs so codec state stays correct.
type gatedTextTracer struct{ tw *textWriter }

func (g gatedTextTracer) Header(name string) {
	if !g.tw.suppress {
		g.tw.tt.Header(name)
	}
}

func (g gatedTextTracer) Element(e cbs.Element) {
	if !g.tw.suppress {
		g.tw.tt.Element(e)
	}
}

func (g gatedTextTracer) Diag(level cbs.Level, msg string) {
	if !g.tw.suppress {
		g.tw.tt.Diag(level, msg)
	}
}

func (tw *textWriter) Tracer() cbs.Tracer { return gatedTextTracer{tw} }

func (tw *textWriter) BeginStream(Source) error { return tw.tt.Err() }

func (tw *textWriter) BeginExtradata() { tw.tt.Line("Extradata") }

func (tw *textWriter) EndExtradata(frag *cbs.Fragment, err error) error {
	tw.st.addFragment(frag)
	return tw.tt.Err()
}

// BeginPacket prints the packet line exactly as trace_headers.c does,
// unless the packet falls outside the MaxPackets / Range window.
func (tw *textWriter) BeginPacket(pkt PacketInfo) {
	tw.curIdx = pkt.Index
	included := tw.gate.include(pkt.Index)
	tw.suppress = !included
	if !included {
		return
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Packet: %d bytes", pkt.Size)
	if pkt.KeyFrame {
		b.WriteString(", key frame")
	}
	if pkt.Corrupt {
		b.WriteString(", corrupt")
	}
	if pkt.HasPTS {
		fmt.Fprintf(&b, ", pts %d", pkt.PTS)
	} else {
		b.WriteString(", no pts")
	}
	if pkt.HasDTS {
		fmt.Fprintf(&b, ", dts %d", pkt.DTS)
	} else {
		b.WriteString(", no dts")
	}
	if pkt.Duration > 0 {
		fmt.Fprintf(&b, ", duration %d", pkt.Duration)
	}
	b.WriteString(".")
	tw.tt.Line(b.String())
}

func (tw *textWriter) EndPacket(frag *cbs.Fragment, err error) error {
	tw.gate.note(tw.curIdx, !tw.suppress)
	tw.suppress = false
	tw.st.Packets++
	tw.st.addFragment(frag)
	return tw.tt.Err()
}

func (tw *textWriter) Done() bool { return tw.gate.done() }

func (tw *textWriter) Close() error { return tw.tt.Err() }
