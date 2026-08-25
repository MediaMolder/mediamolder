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
	"kind", "packet", "pts", "dts", "time", "dts_time", "duration",
	"packet_pos", "packet_size", "key_frame", "corrupt",
	"unit", "offset", "prefix", "size", "rbsp_size",
	"type", "name", "class", "decomposed", "skip", "error",
	// Coded-picture columns, 1:1 with the JSON picture record, filled
	// for H.264/H.265 slice rows (their summary column stays empty —
	// the columns are the report).
	"pic_type", "pic_type_value", "poc", "frame_num", "pic_lsb",
	"first_mb", "segment_address", "first_slice", "dependent",
	"pps_id", "qp_delta", "ref_l0", "ref_l1", "idr_pic_id", "field",
	"summary",
}

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
	c.checks.endPacket()
	return c.err
}

func (c *csvWriter) Done() bool { return c.gate.done() }

func (c *csvWriter) Violations() []Violation { return c.vlog.list }

func (c *csvWriter) ErrorViolations() int { return c.vlog.errors }

// Close appends the violations as kind="violation" rows: packet/unit
// locate the finding, name carries the check id (or "syntax"), class the
// severity, error the message.
func (c *csvWriter) Close() error {
	for _, vi := range c.vlog.list {
		row := make([]string, len(csvHeader))
		row[0] = "violation"
		if vi.Packet >= 0 {
			row[1] = strconv.FormatInt(vi.Packet, 10)
		}
		name := vi.Check
		if name == "" {
			name = vi.Kind
		}
		for i, h := range csvHeader {
			switch h {
			case "unit":
				if vi.Unit >= 0 {
					row[i] = strconv.Itoa(vi.Unit)
				}
			case "name":
				row[i] = name
			case "class":
				row[i] = vi.Severity
			case "error":
				row[i] = vi.Message
			}
		}
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

	pktCols := []string{"", "", "", "", "", "", "", "", "", ""}
	if pkt != nil {
		pts, dts, tm, dtm := "", "", "", ""
		if pkt.HasPTS {
			pts = i64(pkt.PTS)
			if sec, ok := packetTime(c.tb, pkt.PTS); ok {
				tm = strconv.FormatFloat(sec, 'f', 6, 64)
			}
		}
		if pkt.HasDTS {
			dts = i64(pkt.DTS)
			if sec, ok := packetTime(c.tb, pkt.DTS); ok {
				dtm = strconv.FormatFloat(sec, 'f', 6, 64)
			}
		}
		pktCols = []string{i64(pkt.Index), pts, dts, tm, dtm, i64(pkt.Duration),
			i64(pkt.Pos), num(pkt.Size), boolean(pkt.KeyFrame), boolean(pkt.Corrupt)}
	}

	pktIndex := int64(-1)
	if pkt != nil {
		pktIndex = pkt.Index
	}
	for i := range frag.Units {
		u := &frag.Units[i]
		pic := c.sum.advance(u)
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
		picCols := []string{"", "", "", "", "", "", "", "", "", "", "", "", "", "", ""}
		summary := ""
		if pic != nil {
			opt := func(set bool, v string) string {
				if set {
					return v
				}
				return ""
			}
			picCols = []string{
				pic.Type,
				num(int(pic.TypeValue)),
				opt(pic.POC != nil, i64(int64(orZero32(pic.POC)))),
				opt(pic.FrameNum != nil, num(int(orZero16(pic.FrameNum)))),
				num(int(pic.Lsb)),
				opt(pic.FirstMB != nil, num(int(orZero32u(pic.FirstMB)))),
				opt(pic.SegAddr != nil, num(int(orZero16(pic.SegAddr)))),
				opt(pic.FirstSlice != nil, boolean(pic.FirstSlice != nil && *pic.FirstSlice)),
				boolean(pic.Dependent),
				num(int(pic.PPS)),
				num(int(pic.QPDelta)),
				opt(pic.RefL0 != 0, num(int(pic.RefL0))),
				opt(pic.RefL1 != 0, num(int(pic.RefL1))),
				opt(pic.IDRPicID != nil, num(int(orZero16(pic.IDRPicID)))),
				pic.Field,
			}
		} else if sm := summarize(u); len(sm) > 0 {
			if b, err := json.Marshal(sm); err == nil {
				summary = string(b)
			}
		}
		errText := ""
		if u.Err != nil {
			errText = u.Err.Error()
		}
		row := append(append([]string{kind}, pktCols...),
			num(i), num(u.Offset), num(u.PrefixSize), num(u.RawSize),
			num(len(u.RBSP)), strconv.FormatUint(uint64(u.Type), 10),
			u.TypeName, classify(c.codec, u), boolean(u.Decomposed),
			u.Skip, errText)
		row = append(row, picCols...)
		row = append(row, summary)
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
