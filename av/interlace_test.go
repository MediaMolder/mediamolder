package av

import "testing"

func TestClassifyInterlace(t *testing.T) {
	cases := []struct {
		name string
		r    InterlaceReport
		want FieldOrder
		src  string
	}{
		{"no frames", InterlaceReport{}, FieldUnknown, ""},
		{"decoder flags bff (DV NTSC)", InterlaceReport{Frames: 48, Flagged: 48, FlagBFF: 48}, FieldBottomFirst, "frames"},
		{"decoder flags tff", InterlaceReport{Frames: 48, Flagged: 40, FlagTFF: 39, FlagBFF: 1}, FieldTopFirst, "frames"},
		{"flags on fewer than half: ignored, idet decides", InterlaceReport{Frames: 48, Flagged: 10, FlagTFF: 10, IdetProgressive: 40}, FieldProgressive, "idet"},
		{"idet combing tff", InterlaceReport{Frames: 48, IdetTFF: 30, IdetBFF: 2, IdetProgressive: 6}, FieldTopFirst, "idet"},
		{"idet combing bff", InterlaceReport{Frames: 48, IdetTFF: 1, IdetBFF: 33, IdetProgressive: 4}, FieldBottomFirst, "idet"},
		{"idet mostly undetermined: unknown", InterlaceReport{Frames: 48, IdetTFF: 3, IdetProgressive: 2}, FieldUnknown, ""},
		{"idet tie: unknown", InterlaceReport{Frames: 10, IdetTFF: 5, IdetProgressive: 5}, FieldUnknown, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, src := classifyInterlace(tc.r)
			if got != tc.want || src != tc.src {
				t.Fatalf("classifyInterlace(%+v) = %v/%q, want %v/%q", tc.r, got, src, tc.want, tc.src)
			}
		})
	}
}

func TestFieldOrderFromCodecAndCodecpar(t *testing.T) {
	if o, ok := fieldOrderFromCodec("dvvideo", 480); !ok || o != FieldBottomFirst {
		t.Fatalf("NTSC DV = %v/%v, want bff", o, ok)
	}
	if o, ok := fieldOrderFromCodec("dvvideo", 576); !ok || o != FieldTopFirst {
		t.Fatalf("PAL DV = %v/%v, want tff", o, ok)
	}
	if _, ok := fieldOrderFromCodec("dvvideo", 1080); ok {
		t.Fatal("DVCPRO HD has no fixed rule")
	}
	if _, ok := fieldOrderFromCodec("h264", 480); ok {
		t.Fatal("only DV is interlaced by definition")
	}
	if _, ok := fieldOrderFromCodecpar(0); ok {
		t.Fatal("AV_FIELD_UNKNOWN must not answer")
	}
	if o := FieldBottomFirst; !o.Interlaced() || o.String() != "bff" {
		t.Fatalf("bff: interlaced=%v string=%q", o.Interlaced(), o)
	}
	if o := FieldProgressive; o.Interlaced() || o.String() != "progressive" {
		t.Fatalf("progressive: interlaced=%v string=%q", o.Interlaced(), o)
	}
}

func TestFrameFieldFlagsRoundTrip(t *testing.T) {
	f, err := AllocFrame()
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if f.Interlaced() || f.TopFieldFirst() {
		t.Fatal("a fresh frame must be progressive")
	}
	f.SetFieldFlags(true, false)
	if !f.Interlaced() || f.TopFieldFirst() {
		t.Fatalf("after SetFieldFlags(true,false): interlaced=%v tff=%v", f.Interlaced(), f.TopFieldFirst())
	}
	f.SetFieldFlags(true, true)
	if !f.Interlaced() || !f.TopFieldFirst() {
		t.Fatalf("after SetFieldFlags(true,true): interlaced=%v tff=%v", f.Interlaced(), f.TopFieldFirst())
	}
	f.SetFieldFlags(false, false)
	if f.Interlaced() {
		t.Fatal("flags must clear")
	}
	if _, ok := f.MetadataValue("lavfi.idet.multiple.current_frame"); ok {
		t.Fatal("a fresh frame has no idet metadata")
	}
}

// lavfiFrames pulls up to n frames from a source filter spec (testsrc2, tinterlace, …).
func lavfiFrames(t *testing.T, spec string, n int) []*Frame {
	t.Helper()
	fg, err := NewSourceFilterGraph(SourceFilterGraphConfig{
		Outputs:    []FilterOutputConfig{{Label: "out0", MediaType: MediaTypeVideo}},
		FilterSpec: spec + "[out0]",
	})
	if err != nil {
		t.Fatalf("NewSourceFilterGraph(%q): %v", spec, err)
	}
	defer fg.Close()
	var frames []*Frame
	for len(frames) < n {
		f, err := AllocFrame()
		if err != nil {
			t.Fatal(err)
		}
		if err := fg.PullFrame(f); err != nil {
			f.Close()
			if IsEAgain(err) {
				continue
			}
			break
		}
		frames = append(frames, f)
	}
	if len(frames) == 0 {
		t.Fatalf("source %q produced no frames", spec)
	}
	return frames
}

func closeFrames(fs []*Frame) {
	for _, f := range fs {
		f.Close()
	}
}

func detectFrames(t *testing.T, frames []*Frame, rate int) InterlaceReport {
	t.Helper()
	d := newInterlaceDetector(StreamInfo{
		Type: MediaTypeVideo, TimeBase: [2]int{1, rate}, SampleAspectRatio: [2]int{1, 1},
		Width: frames[0].Width(), Height: frames[0].Height(),
	})
	defer d.close()
	for _, f := range frames {
		d.feed(f)
	}
	return d.report()
}

// Progressive pictures: no decoder flags, and idet sees no combing. The fixture needs motion
// and texture without chart detail: a fast mandelbrot zoom. testsrc2 is NOT usable here — its
// alternating-line detail reads as combing to any field-difference detector (idet: 47/48
// "tff") — and a slow zoom gives idet nothing to judge (all "undetermined" → unknown).
func TestDetectInterlaceProgressiveContent(t *testing.T) {
	frames := lavfiFrames(t, "mandelbrot=size=320x240:rate=30:end_scale=0.01", 48)
	defer closeFrames(frames)
	rep := detectFrames(t, frames, 30)
	if rep.Order != FieldProgressive || rep.Source != "idet" {
		t.Fatalf("mandelbrot = %v via %q (%+v), want progressive via idet", rep.Order, rep.Source, rep)
	}
}

// tinterlace weaves pairs of frames into one interlaced picture and flags it; the decoder-flag
// path must answer from the flags alone, top field first.
func TestDetectInterlaceFromDecoderFlags(t *testing.T) {
	frames := lavfiFrames(t, "mandelbrot=size=320x240:rate=60,tinterlace=mode=interleave_top", 48)
	defer closeFrames(frames)
	if !frames[0].Interlaced() {
		t.Skip("this libavfilter's tinterlace does not flag its output interlaced")
	}
	rep := detectFrames(t, frames, 30)
	if rep.Order != FieldTopFirst || rep.Source != "frames" {
		t.Fatalf("tinterlace = %v via %q (%+v), want tff via frames", rep.Order, rep.Source, rep)
	}
}

// The same woven pictures with their flags stripped — a source re-encoded without field
// signalling — must still be found interlaced by idet's combing analysis, with the right parity.
func TestDetectInterlaceFromContentWhenUnflagged(t *testing.T) {
	frames := lavfiFrames(t, "mandelbrot=size=320x240:rate=60,tinterlace=mode=interleave_top", 48)
	defer closeFrames(frames)
	for _, f := range frames {
		f.SetFieldFlags(false, false)
	}
	rep := detectFrames(t, frames, 30)
	if rep.Flagged != 0 {
		t.Fatalf("flags were stripped, yet %d flagged", rep.Flagged)
	}
	if !rep.Order.Interlaced() || rep.Source != "idet" {
		t.Fatalf("unflagged woven frames = %v via %q (%+v), want interlaced via idet", rep.Order, rep.Source, rep)
	}
	if rep.Order != FieldTopFirst {
		t.Fatalf("tinterlace interleave_top is top field first; idet said %v (%+v)", rep.Order, rep)
	}
}
