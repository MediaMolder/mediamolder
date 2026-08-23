package av

// #include "libavcodec/defs.h"
import "C"

import (
	"fmt"
)

// Interlace detection: is this video stream two fields per picture, and which field is
// DISPLAYED first? The answer drives a deinterlacer (bwdif / yadif) that must be told the
// parity, because many sources do not signal it.

// FieldOrder is the detected field structure of a video stream.
type FieldOrder int

const (
	FieldUnknown     FieldOrder = iota // nothing could tell
	FieldProgressive                   // one picture per frame
	FieldTopFirst                      // interlaced, top field displayed first (tff)
	FieldBottomFirst                   // interlaced, bottom field displayed first (bff)
)

func (o FieldOrder) String() string {
	switch o {
	case FieldProgressive:
		return "progressive"
	case FieldTopFirst:
		return "tff"
	case FieldBottomFirst:
		return "bff"
	}
	return "unknown"
}

// Interlaced reports whether the order names two fields per picture.
func (o FieldOrder) Interlaced() bool { return o == FieldTopFirst || o == FieldBottomFirst }

// InterlaceReport is DetectInterlace's answer and how it was reached.
type InterlaceReport struct {
	Order  FieldOrder
	Source string // "codecpar" | "frames" | "idet" | "codec" | "" (unknown)

	Frames  int // frames decoded for the per-frame passes
	Flagged int // of those, frames the decoder flagged interlaced
	FlagTFF int // flagged frames with top field first
	FlagBFF int // flagged frames with bottom field first

	IdetTFF         int // idet per-frame verdicts (the "multiple-frame" classifier)
	IdetBFF         int
	IdetProgressive int
}

// DefaultInterlaceFrames is how many frames DetectInterlace decodes when maxFrames <= 0: two
// seconds of most material, enough for idet's history to settle.
const DefaultInterlaceFrames = 48

// DetectInterlace classifies video stream streamIndex of input, from the cheapest source
// that knows: the codecpar field order when the headers say anything; else the decoder's
// per-frame flags over the first frames; else idet's combing analysis of those same frames;
// else the codec's own rule (DV: NTSC 480 lines bff, PAL 576 tff). The report says which
// source decided and carries the tallies.
//
// Contract: packets are read from the input's CURRENT position (open a fresh input or seek
// first) with the software decoder, only when the headers are silent, and the input is left
// where the read stopped. Anything short of clear evidence is FieldUnknown — a deinterlacer
// is never applied on a guess.
func DetectInterlace(input *InputFormatContext, streamIndex, maxFrames int) (InterlaceReport, error) {
	var rep InterlaceReport
	if input == nil {
		return rep, fmt.Errorf("av: DetectInterlace: nil input")
	}
	si, err := input.StreamInfo(streamIndex)
	if err != nil {
		return rep, err
	}
	if si.Type != MediaTypeVideo {
		return rep, fmt.Errorf("av: DetectInterlace: stream %d is not video", streamIndex)
	}
	if o, ok := fieldOrderFromCodecpar(si.FieldOrder); ok {
		rep.Order, rep.Source = o, "codecpar"
		return rep, nil
	}
	if maxFrames <= 0 {
		maxFrames = DefaultInterlaceFrames
	}

	dec, err := OpenDecoder(input, streamIndex)
	if err != nil {
		return rep, err
	}
	defer dec.Close()
	pkt, err := AllocPacket()
	if err != nil {
		return rep, err
	}
	defer pkt.Close()
	d := newInterlaceDetector(si)
	defer d.close()

	for d.frames() < maxFrames {
		pkt.Unref()
		if e := input.ReadPacket(pkt); e != nil {
			if IsEOF(e) {
				_ = dec.Flush()
				d.drain(dec, maxFrames)
			}
			break
		}
		if pkt.StreamIndex() != streamIndex {
			continue
		}
		if e := dec.SendPacket(pkt); e != nil && !IsEAgain(e) {
			break
		}
		d.drain(dec, maxFrames)
	}
	rep = d.report()
	if rep.Order == FieldUnknown {
		if o, ok := fieldOrderFromCodec(CodecName(si.CodecID), si.Height); ok {
			rep.Order, rep.Source = o, "codec"
		}
	}
	return rep, nil
}

// The AVFieldOrder header values, importable by tests (cgo is unavailable in _test.go).
var (
	fieldOrderProgressive = int(C.AV_FIELD_PROGRESSIVE)
	fieldOrderTT          = int(C.AV_FIELD_TT)
	fieldOrderBB          = int(C.AV_FIELD_BB)
	fieldOrderTB          = int(C.AV_FIELD_TB)
	fieldOrderBT          = int(C.AV_FIELD_BT)
)

// fieldOrderFromCodecpar maps an AVFieldOrder header value to an answer, when it has one.
// AVFieldOrder names CODED order then DISPLAYED order: TT top/top, BB bottom/bottom, TB top
// coded but BOTTOM displayed first, BT bottom coded but TOP displayed first. The deinterlacer
// wants displayed order, so TB is bff and BT is tff — not the other way around.
func fieldOrderFromCodecpar(v int) (FieldOrder, bool) {
	switch v {
	case fieldOrderProgressive:
		return FieldProgressive, true
	case fieldOrderTT, fieldOrderBT:
		return FieldTopFirst, true
	case fieldOrderBB, fieldOrderTB:
		return FieldBottomFirst, true
	}
	return FieldUnknown, false
}

// fieldOrderFromCodec is the format's own rule for codecs that are interlaced by definition.
// DV at standard-definition sizes: NTSC 480 lines is bottom field first, PAL 576 top first.
func fieldOrderFromCodec(codec string, height int) (FieldOrder, bool) {
	if codec != "dvvideo" {
		return FieldUnknown, false
	}
	switch height {
	case 480:
		return FieldBottomFirst, true
	case 576:
		return FieldTopFirst, true
	}
	return FieldUnknown, false
}

// interlaceDetector accumulates per-frame evidence: decoder flags, and idet's content verdict.
type interlaceDetector struct {
	si   StreamInfo
	rep  InterlaceReport
	idet *FilterGraph // built from the first frame's geometry; nil if idet is unavailable
	out  *Frame
}

func newInterlaceDetector(si StreamInfo) *interlaceDetector {
	return &interlaceDetector{si: si}
}

func (d *interlaceDetector) frames() int { return d.rep.Frames }

func (d *interlaceDetector) close() {
	if d.idet != nil {
		d.idet.Close()
		d.idet = nil
	}
	if d.out != nil {
		d.out.Close()
		d.out = nil
	}
}

// drain pulls every frame the decoder has ready and feeds it to the detector, up to max.
func (d *interlaceDetector) drain(dec *DecoderContext, max int) {
	for d.rep.Frames < max {
		f, err := AllocFrame()
		if err != nil {
			return
		}
		if e := dec.ReceiveFrame(f); e != nil {
			f.Close()
			return
		}
		d.feed(f)
		f.Close()
	}
}

// feed counts one decoded frame: its flags, and idet's verdict on its content.
func (d *interlaceDetector) feed(f *Frame) {
	d.rep.Frames++
	if f.Interlaced() {
		d.rep.Flagged++
		if f.TopFieldFirst() {
			d.rep.FlagTFF++
		} else {
			d.rep.FlagBFF++
		}
	}
	d.idetFeed(f)
}

// idetFeed runs the frame through libavfilter's idet and tallies its multiple-frame verdict
// ("lavfi.idet.multiple.current_frame": tff | bff | progressive | undetermined).
func (d *interlaceDetector) idetFeed(f *Frame) {
	if d.idet == nil {
		fg, err := NewVideoFilterGraph(VideoFilterGraphConfig{
			Width: f.Width(), Height: f.Height(), PixFmt: f.PixFmt(),
			TBNum: d.si.TimeBase[0], TBDen: d.si.TimeBase[1],
			SARNum: d.si.SampleAspectRatio[0], SARDen: d.si.SampleAspectRatio[1],
			FilterSpec: "idet",
		})
		if err != nil {
			return
		}
		d.idet = fg
		if d.out, err = AllocFrame(); err != nil {
			d.idet.Close()
			d.idet = nil
			return
		}
	}
	if err := d.idet.PushFrame(f); err != nil {
		return
	}
	for {
		d.out.Unref()
		if err := d.idet.PullFrame(d.out); err != nil {
			return
		}
		switch v, _ := d.out.MetadataValue("lavfi.idet.multiple.current_frame"); v {
		case "tff":
			d.rep.IdetTFF++
		case "bff":
			d.rep.IdetBFF++
		case "progressive":
			d.rep.IdetProgressive++
		}
	}
}

// report decides from the evidence. Decoder flags win when at least half the frames carry
// them (the bitstream knows); otherwise idet's content analysis decides when it has a
// verdict on at least half the frames it saw and one side clearly leads.
func (d *interlaceDetector) report() InterlaceReport {
	rep := d.rep
	rep.Order, rep.Source = classifyInterlace(rep)
	return rep
}

func classifyInterlace(r InterlaceReport) (FieldOrder, string) {
	if r.Frames == 0 {
		return FieldUnknown, ""
	}
	if r.Flagged*2 >= r.Frames {
		// The stream is interlaced by its own flags; a TFF-vs-BFF tie still
		// means the parity is not known, and a guessed parity deinterlaces on
		// the wrong foot — worse than not deinterlacing at all.
		switch {
		case r.FlagBFF > r.FlagTFF:
			return FieldBottomFirst, "frames"
		case r.FlagTFF > r.FlagBFF:
			return FieldTopFirst, "frames"
		}
		return FieldUnknown, ""
	}
	decided := r.IdetTFF + r.IdetBFF + r.IdetProgressive
	if decided*2 >= r.Frames && decided > 0 {
		interlaced := r.IdetTFF + r.IdetBFF
		switch {
		case interlaced > r.IdetProgressive:
			switch {
			case r.IdetBFF > r.IdetTFF:
				return FieldBottomFirst, "idet"
			case r.IdetTFF > r.IdetBFF:
				return FieldTopFirst, "idet"
			}
			return FieldUnknown, "" // interlaced, parity tied: not known
		case r.IdetProgressive > interlaced:
			return FieldProgressive, "idet"
		}
	}
	return FieldUnknown, ""
}
