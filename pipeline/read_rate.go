// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sync"
	"time"
)

// readRatePacer enforces FFmpeg's `-readrate` / `-re` /
// `-readrate_initial_burst` / `-readrate_catchup` semantics on a
// demux loop. The algorithm is a faithful port of
// fftools/ffmpeg_demux.c::readrate_sleep:
//
//	max_pts = stream_ts_offset + initial_burst
//	          + (wallclock_elapsed × readrate)
//	if pts > max_pts: sleep(pts - max_pts)
//
// Lag detection switches the multiplier to readrate_catchup until
// the lag is recovered (matches the same 0.3 s threshold FFmpeg
// uses in readrate_sleep). All times are AV_TIME_BASE microseconds.
//
// FFmpeg's implementation is per-stream; ours is per-input
// (effectively per-source goroutine). The single-stream view is
// faithful for the common live-restream case where every selected
// stream advances together; if a future job needs independent
// per-stream pacing the struct can be promoted to a map keyed by
// stream index without changing the maybeSleep contract.
type readRatePacer struct {
	rate    float64 // ReadRate: 1.0 = realtime
	burst   float64 // ReadRateInitialBurst: seconds of media unpaced at start
	catchup float64 // ReadRateCatchup: rate during lag recovery

	mu             sync.Mutex
	initialised    bool
	wallStart      time.Time
	streamTSOffset int64 // first packet's PTS in AV_TIME_BASE us; pacing baseline
	burstUS        int64 // burst window in AV_TIME_BASE us, precomputed
	resumeWC       time.Time
	resumePTSus    int64
	lagUS          int64
}

func newReadRatePacer(rate, burst, catchup float64) *readRatePacer {
	return &readRatePacer{
		rate:    rate,
		burst:   burst,
		catchup: catchup,
		burstUS: int64(burst * 1_000_000),
	}
}

// maybeSleep blocks the caller for as long as the configured
// ReadRate requires before this packet should be released
// downstream. ptsUS is the packet's PTS in AV_TIME_BASE
// microseconds, in post-shift coordinates (matches the
// `ds->dts` value FFmpeg uses inside readrate_sleep, which is
// also already shifted by ts_offset). Returns immediately when
// the context is cancelled.
func (p *readRatePacer) maybeSleep(ctx context.Context, ptsUS int64) {
	if p == nil {
		return
	}
	p.mu.Lock()
	if !p.initialised {
		p.initialised = true
		p.wallStart = time.Now()
		p.streamTSOffset = ptsUS
		p.mu.Unlock()
		return
	}
	now := time.Now()
	wcElapsedUS := now.Sub(p.wallStart).Microseconds()
	maxPTSus := p.streamTSOffset + p.burstUS + int64(float64(wcElapsedUS)*p.rate)

	// Burst window: no pacing for the first burstUS of media time.
	if ptsUS <= p.streamTSOffset+p.burstUS {
		p.mu.Unlock()
		return
	}

	// Lag detection mirrors readrate_sleep's 0.3 s threshold.
	const lagThresholdUS = int64(0.3 * 1_000_000)
	lag := maxPTSus - ptsUS
	if lag < 0 {
		lag = 0
	}
	if (p.lagUS == 0 && lag > lagThresholdUS) || (lag > p.lagUS+lagThresholdUS) {
		p.lagUS = lag
		p.resumeWC = now
		p.resumePTSus = ptsUS
	}
	if p.lagUS != 0 && lag == 0 {
		p.lagUS = 0
		p.resumeWC = time.Time{}
		p.resumePTSus = 0
	}

	var limitPTSus int64
	if !p.resumeWC.IsZero() {
		elapsedUS := now.Sub(p.resumeWC).Microseconds()
		limitPTSus = p.resumePTSus + int64(float64(elapsedUS)*p.catchup)
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
