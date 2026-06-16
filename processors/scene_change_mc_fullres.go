// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

// Windowed full-resolution re-decode provider for scene_change_mc's
// full-res edge measurement (lookahead.FrameProvider). Full-res retention
// is impossible (~2 MB/frame luma), so the provider re-opens the source and
// decodes small windows on demand, addressed by the exact per-frame PTS
// recorded during the first pass.
//
// Trust comes from two checks, not from index arithmetic:
//   - PTS matching: the engine's PTS may be rebased relative to the native
//     stream (ts_offset / container start_time), so the provider locks a
//     CONSTANT offset on the first fetched frame by scanning a small
//     candidate range for the lowres-fingerprint minimum;
//   - per-frame fingerprint: every returned frame is downsampled with the
//     same lowres filter and SAD-checked against the retained lowres plane
//     for that index. A re-decode of the same stream with the same decoder
//     is essentially bit-exact, so anything above a small threshold means
//     misalignment and the fetch fails (the measurement stage then skips
//     that dissolve — fail-safe).

import (
	"fmt"
	"math"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/lookahead"
)

const (
	// frAlignMaxSAD: max mean-abs lowres difference for a re-decoded frame
	// to count as the same frame. Same-decoder re-decode is near exact;
	// generous margin for decoder threading variations.
	frAlignMaxSAD = 2.0
	// frSeekPrerollSec: seek this far before the target so the decoder
	// reaches steady state from the preceding keyframe.
	frSeekPrerollSec = 1.0
	// frMaxScanSec: give up if no PTS match appears within this span after
	// the target (wrong offset lock, damaged stream, ...).
	frMaxScanSec = 4.0
)

type fullresProvider struct {
	url     string
	pts     []int64 // engine PTS per frame index (recorded in Process)
	scanner *lookahead.LookaheadScanner

	demux        *av.InputFormatContext
	dec          *av.DecoderContext
	pkt          *av.Packet
	vidIdx       int
	tbNum, tbDen int
	offset       int64 // native PTS − engine PTS, locked by fingerprint
	offsetLocked bool
	lastNative   int64 // PTS of the last decoded frame (native units)
	primed       bool  // a frame has been decoded since the last seek
}

func newFullresProvider(url string, pts []int64, sc *lookahead.LookaheadScanner) *fullresProvider {
	return &fullresProvider{url: url, pts: pts, scanner: sc, vidIdx: -1}
}

func (p *fullresProvider) ensureOpen() error {
	if p.demux != nil {
		return nil
	}
	demux, err := av.OpenInput(p.url, nil)
	if err != nil {
		return fmt.Errorf("fullres open %s: %w", p.url, err)
	}
	vidIdx := -1
	var si av.StreamInfo
	for i := 0; i < demux.NumStreams(); i++ {
		info, err := demux.StreamInfo(i)
		if err == nil && info.Type == av.MediaTypeVideo {
			vidIdx, si = i, info
			break
		}
	}
	if vidIdx < 0 {
		demux.Close()
		return fmt.Errorf("fullres: no video stream in %s", p.url)
	}
	dec, err := av.OpenDecoder(demux, vidIdx)
	if err != nil {
		demux.Close()
		return fmt.Errorf("fullres decoder: %w", err)
	}
	pkt, err := av.AllocPacket()
	if err != nil {
		dec.Close()
		demux.Close()
		return err
	}
	p.demux, p.dec, p.pkt, p.vidIdx = demux, dec, pkt, vidIdx
	p.tbNum, p.tbDen = si.TimeBase[0], si.TimeBase[1]
	p.lastNative = math.MinInt64
	return nil
}

func (p *fullresProvider) Close() {
	if p.pkt != nil {
		p.pkt.Close()
	}
	if p.dec != nil {
		p.dec.Close()
	}
	if p.demux != nil {
		p.demux.Close()
	}
	p.pkt, p.dec, p.demux = nil, nil, nil
}

// ptsToUS converts native stream PTS to AV_TIME_BASE microseconds.
func (p *fullresProvider) ptsToUS(pts int64) int64 {
	return int64(float64(pts) * float64(p.tbNum) / float64(p.tbDen) * 1e6)
}

func (p *fullresProvider) secToPTS(sec float64) int64 {
	return int64(sec * float64(p.tbDen) / float64(p.tbNum))
}

func (p *fullresProvider) seekBefore(nativePTS int64) error {
	target := p.ptsToUS(nativePTS) - int64(frSeekPrerollSec*1e6)
	if target < 0 {
		target = 0
	}
	if err := p.demux.SeekFile(target); err != nil {
		return fmt.Errorf("fullres seek: %w", err)
	}
	// avcodec_flush_buffers, NOT Flush()/SendPacket(nil): the latter is the
	// drain/EOF signal and leaves the decoder refusing post-seek packets.
	p.dec.FlushBuffers()
	p.lastNative = math.MinInt64
	p.primed = false
	return nil
}

// nextFrame decodes the next video frame (the xfade_sequence pattern).
func (p *fullresProvider) nextFrame() (*av.Frame, error) {
	f, err := av.AllocFrame()
	if err != nil {
		return nil, err
	}
	for {
		recvErr := p.dec.ReceiveFrame(f)
		if recvErr == nil {
			p.lastNative = f.PTS()
			p.primed = true
			return f, nil
		}
		if !av.IsEAgain(recvErr) {
			f.Close()
			return nil, recvErr
		}
		for {
			p.pkt.Unref()
			if err := p.demux.ReadPacket(p.pkt); err != nil {
				if av.IsEOF(err) {
					if err := p.dec.Flush(); err != nil {
						f.Close()
						return nil, err
					}
					break // drain buffered frames
				}
				f.Close()
				return nil, err
			}
			if p.pkt.StreamIndex() != p.vidIdx {
				continue
			}
			if err := p.dec.SendPacket(p.pkt); err != nil {
				f.Close()
				return nil, err
			}
			break
		}
	}
}

// FullresLuma implements lookahead.FrameProvider.
func (p *fullresProvider) FullresLuma(frameIdx int) ([]byte, int, int, int, error) {
	if frameIdx < 0 || frameIdx >= len(p.pts) {
		return nil, 0, 0, 0, fmt.Errorf("fullres: frame %d out of range", frameIdx)
	}
	if err := p.ensureOpen(); err != nil {
		return nil, 0, 0, 0, err
	}
	target := p.pts[frameIdx] + p.offset // best guess until locked

	// Reposition when behind us or far ahead (> ~5 s of decoding).
	if !p.primed || target <= p.lastNative || target > p.lastNative+p.secToPTS(5) {
		if err := p.seekBefore(target); err != nil {
			return nil, 0, 0, 0, err
		}
	}

	scanLimit := target + p.secToPTS(frMaxScanSec)
	for {
		f, err := p.nextFrame()
		if err != nil {
			return nil, 0, 0, 0, fmt.Errorf("fullres decode (frame %d): %w", frameIdx, err)
		}
		native := f.PTS()
		switch {
		case p.offsetLocked && native < target:
			f.Close()
			continue
		case p.offsetLocked && native > target:
			f.Close()
			return nil, 0, 0, 0, fmt.Errorf("fullres: frame %d (pts %d) missing in re-decode", frameIdx, target)
		case !p.offsetLocked && native < p.pts[frameIdx]-p.secToPTS(frSeekPrerollSec):
			f.Close()
			continue
		}

		luma, w, h, err := frameLuma(f)
		f.Close()
		if err != nil {
			return nil, 0, 0, 0, err
		}
		sad, err := p.scanner.AlignmentSAD(frameIdx, luma, w, h, w)
		if err != nil {
			return nil, 0, 0, 0, err
		}
		if sad <= frAlignMaxSAD {
			if !p.offsetLocked {
				p.offset = native - p.pts[frameIdx]
				p.offsetLocked = true
			}
			return luma, w, h, w, nil
		}
		if p.offsetLocked {
			return nil, 0, 0, 0, fmt.Errorf("fullres: frame %d fingerprint mismatch (SAD %.2f)", frameIdx, sad)
		}
		if native > scanLimit {
			return nil, 0, 0, 0, fmt.Errorf("fullres: no fingerprint match for frame %d within scan window", frameIdx)
		}
	}
}

func frameLuma(f *av.Frame) ([]byte, int, int, error) {
	bgr, err := f.ToBGR24()
	if err != nil {
		return nil, 0, 0, err
	}
	w, h := f.Width(), f.Height()
	return lookahead.BGRToLuma(bgr, w, h), w, h, nil
}
