// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"sync"
	"time"
)

// pacerStreamState mirrors the per-stream lag-recovery fields in
// fftools/ffmpeg_demux.c's DemuxStream (ds->lag, ds->resume_wc,
// ds->resume_pts, ds->first_dts). One instance is allocated lazily
// the first time a given stream index calls maybeSleep.
type pacerStreamState struct {
	streamTSOffset int64     // first ptsUS for this stream (mirrors ds->first_dts)
	lagUS          int64     // last recorded lag in us; 0 when not in catchup
	resumeWC       time.Time // wall-clock time when catchup mode last started
	resumePTSus    int64     // ptsUS when catchup mode last started
}

// readRatePacer enforces FFmpeg's `-readrate` / `-re` /
// `-readrate_initial_burst` / `-readrate_catchup` semantics on a
// demux loop. The algorithm is a faithful port of
// fftools/ffmpeg_demux.c::readrate_sleep.
//
// Per-stream state (streamTSOffset, lagUS, resumeWC, resumePTSus)
// mirrors DemuxStream in ffmpeg_demux.c; a single pacer serves all
// streams of one input, with the wall-clock start shared across
// streams. This is required for correctness with container formats
// (e.g. AVI) where audio is prefetched significantly ahead of video:
// without per-stream lag state a late-arriving video packet resets
// the shared resumePTSus, forcing the next (already-ahead) audio
// packet into a multi-hundred-millisecond sleep.
//
// All times are AV_TIME_BASE microseconds.
type readRatePacer struct {
	rate    float64 // ReadRate: 1.0 = realtime
	burst   float64 // ReadRateInitialBurst: seconds of media unpaced at start
	catchup float64 // ReadRateCatchup: rate during lag recovery
	burstUS int64   // burst window in us, precomputed from burst

	mu        sync.Mutex
	wallStart time.Time // shared across all streams
	streams   map[int]*pacerStreamState
}

func newReadRatePacer(rate, burst, catchup float64) *readRatePacer {
	return &readRatePacer{
		rate:    rate,
		burst:   burst,
		catchup: catchup,
		burstUS: int64(burst * 1_000_000),
		streams: make(map[int]*pacerStreamState),
	}
}

// maybeSleep blocks the caller for as long as the configured ReadRate
// requires before this packet should be released downstream. ptsUS is
// the packet PTS in AV_TIME_BASE microseconds (post ts_offset shift,
// matching ffmpeg_demux.c::readrate_sleep's use of ds->dts after
// ts_fixup). streamIdx is the AVStream index for the packet. Returns
// immediately when the context is cancelled.
func (p *readRatePacer) maybeSleep(ctx context.Context, ptsUS int64, streamIdx int) {
	if p == nil {
		return
	}
	p.mu.Lock()

	// Shared wall-clock start: set on the very first call across all streams.
	if p.wallStart.IsZero() {
		p.wallStart = time.Now()
	}

	// Per-stream state: allocate on first use.
	ss := p.streams[streamIdx]
	if ss == nil {
		ss = &pacerStreamState{streamTSOffset: ptsUS}
		p.streams[streamIdx] = ss
		p.mu.Unlock()
		return // first packet for this stream initialises the offset; no sleep
	}

	now := time.Now()
	wcElapsedUS := now.Sub(p.wallStart).Microseconds()
	maxPTSus := ss.streamTSOffset + p.burstUS + int64(float64(wcElapsedUS)*p.rate)

	// Burst window: no pacing for the first burstUS of this stream's media time.
	if ptsUS <= ss.streamTSOffset+p.burstUS {
		p.mu.Unlock()
		return
	}

	// Lag detection mirrors readrate_sleep's 0.3 s threshold.
	// lag > 0 means the source is behind the wallclock budget.
	const lagThresholdUS = int64(0.3 * 1_000_000)
	lag := maxPTSus - ptsUS
	if lag < 0 {
		lag = 0
	}
	if (ss.lagUS == 0 && lag > lagThresholdUS) || (lag > ss.lagUS+lagThresholdUS) {
		ss.lagUS = lag
		ss.resumeWC = now
		ss.resumePTSus = ptsUS
	}
	if ss.lagUS != 0 && lag == 0 {
		ss.lagUS = 0
		ss.resumeWC = time.Time{}
		ss.resumePTSus = 0
	}

	var limitPTSus int64
	if !ss.resumeWC.IsZero() {
		elapsedUS := now.Sub(ss.resumeWC).Microseconds()
		limitPTSus = ss.resumePTSus + int64(float64(elapsedUS)*p.catchup)
	} else {
		limitPTSus = maxPTSus
	}

	var sleepUS int64
	if ptsUS > limitPTSus {
		sleepUS = ptsUS - limitPTSus
	}
	p.mu.Unlock()

	if sleepUS <= 0 {
		return
	}
	timer := time.NewTimer(time.Duration(sleepUS) * time.Microsecond)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
	}
}
