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
	"kind", "packet", "pts", "dts", "duration", "packet_pos", "packet_size",
	"key_frame", "corrupt", "unit", "offset", "prefix", "size", "rbsp_size",
	"type", "name", "decomposed", "skip", "error", "summary",
}

// csvWriter renders one row per unit. The unit-type filter drops
// non-matching rows entirely (unlike JSON, which keeps identity-only
// entries), and the detail option is ignored: rows always carry the
// summary column.
type csvWriter struct {
	cw     *csv.Writer
	codec  string
	filter unitFilter
	gate   packetGate

	curPkt     PacketInfo
	includeCur bool
	err        error
}

func newCSVWriter(w io.Writer, opts Options) *csvWriter {
	return &csvWriter{
		cw:     csv.NewWriter(w),
		filter: newUnitFilter(opts.UnitTypes),
		gate:   newPacketGate(opts),
	}
}

// Tracer returns nil: the CSV format needs no element trace, so parsing
// runs without trace overhead.
func (c *csvWriter) Tracer() cbs.Tracer { return nil }

func (c *csvWriter) write(row []string) {
	if err := c.cw.Write(row); err != nil && c.err == nil {
		c.err = err
	}
}

func (c *csvWriter) BeginStream(src Source) error {
	c.codec = src.Codec
	c.write(csvHeader)
	return c.err
}

func (c *csvWriter) BeginExtradata() {}

func (c *csvWriter) EndExtradata(frag *cbs.Fragment, err error) error {
	c.writeUnits("extradata", nil, frag)
	return c.err
}

func (c *csvWriter) BeginPacket(pkt PacketInfo) {
	c.curPkt = pkt
	c.includeCur = c.gate.include(pkt.Index)
}

func (c *csvWriter) EndPacket(frag *cbs.Fragment, err error) error {
	c.gate.note(c.curPkt.Index, c.includeCur)
	if c.includeCur {
		c.writeUnits("packet", &c.curPkt, frag)
	}
	return c.err
}

func (c *csvWriter) Done() bool { return c.gate.done() }

func (c *csvWriter) Close() error {
	c.cw.Flush()
	if err := c.cw.Error(); err != nil && c.err == nil {
		c.err = err
	}
	return c.err
}

func (c *csvWriter) writeUnits(kind string, pkt *PacketInfo, frag *cbs.Fragment) {
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

	pktCols := []string{"", "", "", "", "", "", "", ""}
	if pkt != nil {
		pts, dts := "", ""
		if pkt.HasPTS {
			pts = i64(pkt.PTS)
		}
		if pkt.HasDTS {
			dts = i64(pkt.DTS)
		}
		pktCols = []string{i64(pkt.Index), pts, dts, i64(pkt.Duration),
			i64(pkt.Pos), num(pkt.Size), boolean(pkt.KeyFrame), boolean(pkt.Corrupt)}
	}

	for i := range frag.Units {
		u := &frag.Units[i]
		if !c.filter.match(u) {
			continue
		}
		summary := ""
		if s := summarize(u); len(s) > 0 {
			if b, err := json.Marshal(s); err == nil {
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
			u.TypeName, boolean(u.Decomposed), u.Skip, errText, summary)
		c.write(row)
	}
}
