package frameindex

import (
	"context"
	"errors"
	"fmt"
	"sort"

	"github.com/MediaMolder/MediaMolder/av"
)

// MaxIndexFrames is the most rows a table may hold. A walk that would record
// one more row returns ErrTooManyFrames and no table.
const MaxIndexFrames = 1_000_000

// ErrTooManyFrames is returned when a walk passes MaxIndexFrames.
var ErrTooManyFrames = errors.New("frameindex: too many frames")

// maxFrames is the cap WalkPackets and WalkDecode enforce. Tests lower it.
// It is not safe to change during a walk.
var maxFrames = MaxIndexFrames

// Table is one video stream's presentation timestamps.
// TimeBase is {num, den} in the stream timebase. PTS is in that timebase.
type Table struct {
	Stream   int
	TimeBase [2]int
	PTS      []int64
}

// Estimate guesses how many frames stream has, from its duration and frame
// rate. stream < 0 selects the first video stream. A return of 0 means the
// container did not say; it is not an empty file. The guess is not the table.
func Estimate(path string, stream int) (frames int64, streamIndex int, timeBase [2]int, err error) {
	in, si, err := openVideo(path, stream)
	if err != nil {
		return 0, 0, [2]int{}, err
	}
	defer in.Close()
	return framesEst(si), si.Index, si.TimeBase, nil
}

// WalkPackets reads one video stream's packet timestamps and returns them in
// presentation order. It reads every packet, including ones flagged corrupt,
// because the decode walk does too. It does not decode. stream < 0 selects the first video
// stream. progress, when set, is called with the number of timestamps kept so
// far. A cancelled context returns that error and no table.
func WalkPackets(ctx context.Context, path string, stream int, progress func(n int)) (*Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in, si, err := openVideo(path, stream)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	pkt, err := av.AllocPacket()
	if err != nil {
		return nil, err
	}
	defer pkt.Close()
	pts := make([]int64, 0, 256)
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		pkt.Unref()
		e := in.ReadPacket(pkt)
		if e != nil {
			if av.IsEOF(e) {
				break
			}
			return nil, e
		}
		if pkt.StreamIndex() != si.Index {
			continue
		}
		ts := pkt.PTS()
		if ts == av.NoPTSValue {
			ts = pkt.DTS()
		}
		if err := appendPTS(&pts, ts, progress); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(pts, func(i, j int) bool { return pts[i] < pts[j] })
	return &Table{Stream: si.Index, TimeBase: si.TimeBase, PTS: pts}, nil
}

// WalkDecode decodes one video stream and returns each output frame's PTS in
// presentation order, in the stream timebase. stream < 0 selects the first
// video stream. An interlaced stream is deinterlaced first (one row per frame,
// not per field). progress, when set, is called with the number of rows kept
// so far. A cancelled context returns that error and no table.
func WalkDecode(ctx context.Context, path string, stream int, progress func(n int)) (*Table, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	in, si, err := openVideo(path, stream)
	if err != nil {
		return nil, err
	}
	defer in.Close()

	parity, err := interlaceParity(path, si.Index)
	if err != nil {
		return nil, err
	}
	dec, err := av.OpenDecoder(in, si.Index)
	if err != nil {
		return nil, err
	}
	defer dec.Close()

	var di *deinterlacer
	if parity != "" {
		di = newDeinterlacer(parity, si.TimeBase, si.SampleAspectRatio)
		defer di.close()
	}

	pkt, err := av.AllocPacket()
	if err != nil {
		return nil, err
	}
	defer pkt.Close()
	fr, err := av.AllocFrame()
	if err != nil {
		return nil, err
	}
	defer fr.Close()

	pts := make([]int64, 0, 256)
	drained := false
	holding := false
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		recvErr := dec.ReceiveFrame(fr)
		if recvErr == nil {
			err := takeFrame(di, fr, &pts, progress)
			fr.Unref()
			if err != nil {
				return nil, err
			}
			continue
		}
		if !av.IsEAgain(recvErr) && !av.IsEOF(recvErr) {
			return nil, recvErr
		}
		if av.IsEOF(recvErr) || (drained && !holding) {
			break
		}
		if !holding {
			pkt.Unref()
			e := in.ReadPacket(pkt)
			if e != nil {
				if av.IsEOF(e) {
					if e := dec.Flush(); e != nil && !av.IsEOF(e) {
						return nil, e
					}
					drained = true
					continue
				}
				return nil, e
			}
			if pkt.StreamIndex() != si.Index {
				continue
			}
			holding = true
		}
		if e := dec.SendPacket(pkt); e != nil {
			if av.IsEAgain(e) {
				continue
			}
			return nil, e
		}
		holding = false
	}
	if di != nil {
		if err := di.drain(&pts, progress); err != nil {
			return nil, err
		}
		if len(di.q) != 0 {
			return nil, fmt.Errorf("frameindex: deinterlace held %d frame(s) back", len(di.q))
		}
	}
	return &Table{Stream: si.Index, TimeBase: si.TimeBase, PTS: pts}, nil
}

func takeFrame(di *deinterlacer, fr *av.Frame, pts *[]int64, progress func(n int)) error {
	ts := framePTS(fr)
	if di == nil {
		return appendPTS(pts, ts, progress)
	}
	return di.push(fr, ts, pts, progress)
}

func framePTS(fr *av.Frame) int64 {
	// best_effort_timestamp is the corrected value. pts is the fallback when
	// the decoder did not fill it.
	ts := fr.BestEffortTimestamp()
	if ts == av.NoPTSValue {
		ts = fr.PTS()
	}
	return ts
}

func appendPTS(pts *[]int64, ts int64, progress func(n int)) error {
	if ts == av.NoPTSValue {
		return nil
	}
	*pts = append(*pts, ts)
	if progress != nil {
		progress(len(*pts))
	}
	if len(*pts) > maxFrames {
		return fmt.Errorf("%w (cap %d)", ErrTooManyFrames, maxFrames)
	}
	return nil
}

func openVideo(path string, stream int) (*av.InputFormatContext, av.StreamInfo, error) {
	in, err := av.OpenInputResilient(path, nil)
	if err != nil {
		return nil, av.StreamInfo{}, err
	}
	streams, err := in.AllStreams()
	if err != nil {
		in.Close()
		return nil, av.StreamInfo{}, err
	}
	if stream >= 0 {
		if stream >= len(streams) || streams[stream].Type != av.MediaTypeVideo {
			in.Close()
			return nil, av.StreamInfo{}, fmt.Errorf("frameindex: stream %d is not video", stream)
		}
		return in, streams[stream], nil
	}
	for i := range streams {
		if streams[i].Type == av.MediaTypeVideo {
			return in, streams[i], nil
		}
	}
	in.Close()
	return nil, av.StreamInfo{}, fmt.Errorf("frameindex: no video stream in %s", path)
}

func framesEst(si av.StreamInfo) int64 {
	fps := si.RFrameRate
	if fps[0] <= 0 || fps[1] <= 0 {
		fps = si.FrameRate
	}
	tbN, tbD := si.TimeBase[0], si.TimeBase[1]
	if si.Duration <= 0 || tbN <= 0 || tbD <= 0 || fps[0] <= 0 || fps[1] <= 0 {
		return 0
	}
	return si.Duration * int64(tbN) * int64(fps[0]) / (int64(tbD) * int64(fps[1]))
}

// interlaceParity returns "tff" or "bff" when the stream is interlaced, or ""
// when it is progressive or the headers and the frames do not say. It opens
// the file itself so the caller's demuxer stays at the start.
func interlaceParity(path string, stream int) (string, error) {
	in, err := av.OpenInputResilient(path, nil)
	if err != nil {
		return "", err
	}
	defer in.Close()
	rep, err := av.DetectInterlace(in, stream, 0)
	if err != nil {
		return "", err
	}
	if !rep.Order.Interlaced() {
		return "", nil
	}
	return rep.Order.String(), nil
}

// deinterlacer runs bwdif in send_frame mode and tags each output with the
// source frame's stream-timebase PTS. The filter's own PTS is ignored.
type deinterlacer struct {
	parity string
	tb     [2]int
	sar    [2]int
	fg     *av.FilterGraph
	q      []int64
}

func newDeinterlacer(parity string, tb, sar [2]int) *deinterlacer {
	if sar[0] <= 0 || sar[1] <= 0 {
		sar = [2]int{1, 1}
	}
	return &deinterlacer{parity: parity, tb: tb, sar: sar}
}

func (d *deinterlacer) close() {
	if d != nil && d.fg != nil {
		d.fg.Close()
		d.fg = nil
	}
}

func (d *deinterlacer) ensure(fr *av.Frame) error {
	if d.fg != nil {
		return nil
	}
	if d.tb[0] <= 0 || d.tb[1] <= 0 {
		return fmt.Errorf("frameindex: stream has no timebase")
	}
	fg, err := av.NewVideoFilterGraph(av.VideoFilterGraphConfig{
		Width: fr.Width(), Height: fr.Height(), PixFmt: fr.PixFmt(),
		TBNum: d.tb[0], TBDen: d.tb[1],
		SARNum: d.sar[0], SARDen: d.sar[1],
		FilterSpec: "bwdif=mode=send_frame:parity=" + d.parity + ":deint=all",
	})
	if err != nil {
		return fmt.Errorf("frameindex: deinterlace: %w", err)
	}
	d.fg = fg
	return nil
}

func (d *deinterlacer) push(fr *av.Frame, ts int64, pts *[]int64, progress func(n int)) error {
	if err := d.ensure(fr); err != nil {
		return err
	}
	if err := d.fg.PushFrame(fr); err != nil {
		return err
	}
	d.q = append(d.q, ts)
	return d.pull(pts, progress)
}

func (d *deinterlacer) drain(pts *[]int64, progress func(n int)) error {
	if d.fg == nil {
		return nil
	}
	if err := d.fg.Flush(); err != nil && !av.IsEOF(err) {
		return err
	}
	return d.pull(pts, progress)
}

func (d *deinterlacer) pull(pts *[]int64, progress func(n int)) error {
	for {
		out, err := av.AllocFrame()
		if err != nil {
			return err
		}
		err = d.fg.PullFrame(out)
		out.Close()
		if err != nil {
			if av.IsEAgain(err) || av.IsEOF(err) {
				return nil
			}
			return err
		}
		if len(d.q) == 0 {
			return fmt.Errorf("frameindex: deinterlaced frame has no source timestamp")
		}
		ts := d.q[0]
		d.q = d.q[1:]
		if err := appendPTS(pts, ts, progress); err != nil {
			return err
		}
	}
}
