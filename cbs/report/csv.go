// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"encoding/csv"
	"encoding/json"
	"io"
	"strconv"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// csvHeader is the fixed column set of the CSV format: one row per NAL
// unit / OBU, with its packet context and the same derived summary the
// JSON format carries (as one compact-JSON column). Element-level detail
// is not representable in CSV — use json or jsonl for that.
var csvHeader = []string{
	"kind", "class", "name", "packet",
	"pts", "dts", "time", "dts_time", "duration",
	"packet_pos", "packet_size", "key_frame", "corrupt",
	"unit", "offset", "prefix", "size", "rbsp_size",
	"type", "decomposed", "skip", "error",
	// Coded-picture columns, 1:1 with the JSON picture record, filled
	// for H.264/H.265 slice rows (their summary column stays empty —
	// the columns are the report).
	"pic_type", "pic_type_value", "poc", "frame_num", "pic_lsb",
	"first_mb", "segment_address", "first_slice", "dependent",
	"pps_id", "qp_delta", "ref_l0", "ref_l1", "idr_pic_id", "field",
	"summary",
}

// csvColumn maps header names to positions so rows are assembled by
// name; reordering csvHeader never silently misaligns a value.
var csvColumn = func() map[string]int {
	m := make(map[string]int, len(csvHeader))
	for i, h := range csvHeader {
		m[h] = i
	}
	return m
}()

// csvRow assembles one output row by column name.
type csvRow []string

func newCSVRow() csvRow { return make(csvRow, len(csvHeader)) }

func (r csvRow) set(col, val string) { r[csvColumn[col]] = val }

// csvWriter renders one row per unit. The unit-type filter drops
// non-matching rows entirely (unlike JSON, which keeps identity-only
// entries), and the detail option is ignored: rows always carry the
// summary column.
type csvWriter struct {
	cw     *csv.Writer
	codec  string
	tb     [2]int
	opts   Options
	filter unitFilter
	sum    *summarizer
	col    *collector
	vlog   violationLog
	checks *checker
	gate   packetGate

	curPkt     PacketInfo
	includeCur bool
	err        error
}

func newCSVWriter(w io.Writer, opts Options) *csvWriter {
	return &csvWriter{
		cw:     csv.NewWriter(w),
		opts:   opts,
		filter: newUnitFilter(opts.UnitTypes),
		sum:    newSummarizer(),
		col:    newCollector(), // elements disabled: captures diagnostics only
		gate:   newPacketGate(opts),
	}
}

// Tracer returns a diagnostics-only collector: the CSV format needs no
// element trace, but parse-error messages feed the violations list.
func (c *csvWriter) Tracer() cbs.Tracer { return c.col }

func (c *csvWriter) write(row []string) {
	if err := c.cw.Write(row); err != nil && c.err == nil {
		c.err = err
	}
}

func (c *csvWriter) BeginStream(src Source) error {
	c.codec = src.Codec
	c.tb = src.TimeBase
	c.checks = newChecker(c.opts.Checks, c.codec, c.sum, &c.vlog)
	c.write(csvHeader)
	return c.err
}

func (c *csvWriter) BeginExtradata() {
	c.col.reset()
	c.checks.beginPacket(-1)
}

func (c *csvWriter) EndExtradata(frag *cbs.Fragment, err error) error {
	c.writeUnits("extradata", nil, frag, true)
	if err != nil {
		c.addSplitViolation(-1, err)
	}
	c.checks.endPacket()
	return c.err
}

func (c *csvWriter) BeginPacket(pkt PacketInfo) {
	c.col.reset()
	c.curPkt = pkt
	c.includeCur = c.gate.include(pkt.Index)
	c.checks.beginPacket(pkt.Index)
}

func (c *csvWriter) EndPacket(frag *cbs.Fragment, err error) error {
	c.gate.note(c.curPkt.Index, c.includeCur)
	// Skipped packets still advance decode-order state via writeUnits.
	c.writeUnits("packet", &c.curPkt, frag, c.includeCur)
	if err != nil {
		c.addSplitViolation(c.curPkt.Index, err)
	}
	c.checks.endPacket()
	return c.err
}

// addSplitViolation mirrors the JSON writer: a fragment split failure is
// always an error-severity syntax violation.
func (c *csvWriter) addSplitViolation(packet int64, err error) {
	msg := firstErrorDiag(c.col.fragDiags)
	if msg == "" {
		msg = err.Error()
	}
	c.vlog.add(Violation{
		Severity: "error",
		Kind:     "syntax",
		Spec:     specName(c.codec),
		Packet:   packet,
		Unit:     -1,
		Message:  msg,
	})
}

func (c *csvWriter) Done() bool { return c.gate.done() }

func (c *csvWriter) Violations() []Violation { return c.vlog.list }

func (c *csvWriter) ErrorViolations() int { return c.vlog.errors }

// Close appends the violations as kind="violation" rows: packet/unit
// locate the finding, name carries the check id (or "syntax"), class the
// severity, error the message.
func (c *csvWriter) Close() error {
	for _, vi := range c.vlog.list {
		row := newCSVRow()
		row.set("kind", "violation")
		row.set("class", vi.Severity)
		name := vi.Check
		if name == "" {
			name = vi.Kind
		}
		row.set("name", name)
		if vi.Packet >= 0 {
			row.set("packet", strconv.FormatInt(vi.Packet, 10))
		}
		if vi.Unit >= 0 {
			row.set("unit", strconv.Itoa(vi.Unit))
		}
		row.set("error", vi.Message)
		c.write(row)
	}
	c.cw.Flush()
	if err := c.cw.Error(); err != nil && c.err == nil {
		c.err = err
	}
	return c.err
}

func (c *csvWriter) writeUnits(kind string, pkt *PacketInfo, frag *cbs.Fragment, emit bool) {
	if frag == nil {
		return
	}
	i64 := func(v int64) string { return strconv.FormatInt(v, 10) }
	num := func(v int) string { return strconv.Itoa(v) }
	boolean := func(v bool) string {
		if v {
			return "true"
		}
		return "false"
	}
	opt := func(set bool, v string) string {
		if set {
			return v
		}
		return ""
	}

	pktIndex := int64(-1)
	for i := range frag.Units {
		u := &frag.Units[i]
		pic := c.sum.advance(u)
		if pkt != nil {
			pktIndex = pkt.Index
		}
		if u.Err != nil {
			msg := ""
			if ue := c.col.units[i]; ue != nil {
				msg = firstErrorDiag(ue.diags)
			}
			c.vlog.addUnitError(c.codec, pktIndex, i, u, msg)
		}
		c.checks.observe(pktIndex, i, u, pic)
		if !emit || !c.filter.match(u) {
			continue
		}

		row := newCSVRow()
		row.set("kind", kind)
		row.set("class", classify(c.codec, u))
		row.set("name", u.TypeName)
		if pkt != nil {
			row.set("packet", i64(pkt.Index))
			if pkt.HasPTS {
				row.set("pts", i64(pkt.PTS))
				if sec, ok := packetTime(c.tb, pkt.PTS); ok {
					row.set("time", strconv.FormatFloat(sec, 'f', 6, 64))
				}
			}
			if pkt.HasDTS {
				row.set("dts", i64(pkt.DTS))
				if sec, ok := packetTime(c.tb, pkt.DTS); ok {
					row.set("dts_time", strconv.FormatFloat(sec, 'f', 6, 64))
				}
			}
			row.set("duration", i64(pkt.Duration))
			row.set("packet_pos", i64(pkt.Pos))
			row.set("packet_size", num(pkt.Size))
			row.set("key_frame", boolean(pkt.KeyFrame))
			row.set("corrupt", boolean(pkt.Corrupt))
		}
		row.set("unit", num(i))
		row.set("offset", num(u.Offset))
		row.set("prefix", num(u.PrefixSize))
		row.set("size", num(u.RawSize))
		row.set("rbsp_size", num(len(u.RBSP)))
		row.set("type", strconv.FormatUint(uint64(u.Type), 10))
		row.set("decomposed", boolean(u.Decomposed))
		row.set("skip", u.Skip)
		if u.Err != nil {
			row.set("error", u.Err.Error())
		}

		if pic != nil {
			row.set("pic_type", pic.Type)
			row.set("pic_type_value", num(int(pic.TypeValue)))
			row.set("poc", opt(pic.POC != nil, i64(int64(orZero32(pic.POC)))))
			row.set("frame_num", opt(pic.FrameNum != nil, num(int(orZero16(pic.FrameNum)))))
			row.set("pic_lsb", num(int(pic.Lsb)))
			row.set("first_mb", opt(pic.FirstMB != nil, num(int(orZero32u(pic.FirstMB)))))
			row.set("segment_address", opt(pic.SegAddr != nil, num(int(orZero16(pic.SegAddr)))))
			row.set("first_slice", opt(pic.FirstSlice != nil, boolean(pic.FirstSlice != nil && *pic.FirstSlice)))
			row.set("dependent", boolean(pic.Dependent))
			row.set("pps_id", num(int(pic.PPS)))
			row.set("qp_delta", num(int(pic.QPDelta)))
			row.set("ref_l0", opt(pic.RefL0 != 0, num(int(pic.RefL0))))
			row.set("ref_l1", opt(pic.RefL1 != 0, num(int(pic.RefL1))))
			row.set("idr_pic_id", opt(pic.IDRPicID != nil, num(int(orZero16(pic.IDRPicID)))))
			row.set("field", pic.Field)
		} else if sm := summarize(u); len(sm) > 0 {
			if b, err := json.Marshal(sm); err == nil {
				row.set("summary", string(b))
			}
		}
		c.write(row)
	}
}

func orZero32(v *int32) int32 {
	if v == nil {
		return 0
	}
	return *v
}

func orZero16(v *uint16) uint16 {
	if v == nil {
		return 0
	}
	return *v
}

func orZero32u(v *uint32) uint32 {
	if v == nil {
		return 0
	}
	return *v
}
