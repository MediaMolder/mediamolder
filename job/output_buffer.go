// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"log"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

// OutputBufferState is the per-output state machine described in Phase 7
// of docs/architecture/node_perf_monitoring_design.md.
type OutputBufferState int32

const (
	BufferStateFilling OutputBufferState = iota
	BufferStateReady
	BufferStateReadyPartial
	BufferStateStreaming
	BufferStateDraining
)

// String renders the state to its design-doc spelling.
func (s OutputBufferState) String() string {
	switch s {
	case BufferStateFilling:
		return "FILLING"
	case BufferStateReady:
		return "READY"
	case BufferStateReadyPartial:
		return "READY_PARTIAL"
	case BufferStateStreaming:
		return "STREAMING"
	case BufferStateDraining:
		return "DRAINING"
	}
	return "UNKNOWN"
}

// notSetUS is the sentinel for "this packet has no usable PTS"; chosen
// well below any plausible AV_NOPTS_VALUE-translated timestamp.
const notSetUS = int64(-1 << 62)

// bufferedPacket pairs a packet with the input-channel index it arrived
// on so the drainer can replay it through the same per-channel rescale /
// BSF state that `sinkWriter.processOne` would have used on the
// non-buffered path.
type bufferedPacket struct {
	chanIdx int
	pkt     *av.Packet
}

// OutputBuffer is the per-sink output buffer wired in front of the muxer
// writer in real-time mode. It operates in two phases:
//
//  1. Fill phase (FILLING → READY): packets accumulate until targetDur is
//     reached or upstream EOSes. The pipeline-level AND-aggregator waits
//     for every output to be ready before releasing them all together.
//
//  2. Rolling phase (STREAMING): after the initial drain, producers call
//     Enqueue and the consumer goroutine calls TakePaced, which paces
//     delivery to the stream's PTS wall-clock rate. This keeps the buffer
//     depth (AheadNs) meaningful throughout the run — positive means the
//     encoder is ahead of real-time; draining to zero means it is behind.
//
// Overflow (buf duration > maxDur) evicts the oldest packets.
type OutputBuffer struct {
	nodeID    string
	targetDur time.Duration
	maxDur    time.Duration
	timeBases [][2]int

	mu       sync.Mutex
	buf      []bufferedPacket
	minPTSus int64
	maxPTSus int64
	havePTS  bool

	state     atomic.Int32
	evictions atomic.Int64

	readyCh    chan struct{}
	readyOnce  sync.Once
	readyAt    atomic.Int64 // unix nano when ready first fired
	debugCount atomic.Int64 // first N packets logged for diagnosis

	// Rolling-buffer fields used in STREAMING phase.
	// notifyCh is a buffered-1 channel that wakes TakePaced when a packet
	// is enqueued or a producer signals EOS.
	notifyCh chan struct{}
	// PTS/wall-clock origin for real-time pacing.
	wallOriginNs atomic.Int64 // unix ns; 0 = not yet established
	ptsOriginUs  atomic.Int64 // source PTS µs corresponding to wallOriginNs
	// EOS tracking: all producers must call EnqueueEOS before TakePaced exits.
	producerCount int32        // set by SetProducerCount before Phase D starts
	eosCount      atomic.Int32 // number of producers that called EnqueueEOS
}

// NewOutputBuffer constructs an OutputBuffer for nodeID with the configured
// fill target and cap. timeBases is indexed by input-channel ordinal
// (matching `handleSink`'s `ins`); empty entries fall back to AV_TIME_BASE.
func NewOutputBuffer(nodeID string, targetDur, maxDur time.Duration, timeBases [][2]int) *OutputBuffer {
	if targetDur < 0 {
		targetDur = 0
	}
	if maxDur <= 0 || maxDur < targetDur {
		// Doc default: max = 2 × target.
		maxDur = 2 * targetDur
	}
	return &OutputBuffer{
		nodeID:    nodeID,
		targetDur: targetDur,
		maxDur:    maxDur,
		timeBases: timeBases,
		readyCh:   make(chan struct{}),
		notifyCh:  make(chan struct{}, 1),
	}
}

// NodeID returns the output node ID this preroll serves.
func (p *OutputBuffer) NodeID() string { return p.nodeID }

// State returns the current state machine value.
func (p *OutputBuffer) State() OutputBufferState {
	return OutputBufferState(p.state.Load())
}

// TargetDur reports the configured fill target.
func (p *OutputBuffer) TargetDur() time.Duration { return p.targetDur }

// MaxDur reports the configured hard cap.
func (p *OutputBuffer) MaxDur() time.Duration { return p.maxDur }

// Evictions returns the running count of packets evicted past `maxDur`.
func (p *OutputBuffer) Evictions() int64 { return p.evictions.Load() }

// Ready returns a channel that is closed once this output is in
// READY / READY_PARTIAL / STREAMING.
func (p *OutputBuffer) Ready() <-chan struct{} { return p.readyCh }

// IsReady reports whether the output has at least reached READY.
func (p *OutputBuffer) IsReady() bool {
	return p.State() >= BufferStateReady && p.State() != BufferStateDraining
}

// ReadyAt returns the wall-clock time at which the output first became
// ready; zero when still filling.
func (p *OutputBuffer) ReadyAt() time.Time {
	n := p.readyAt.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// BufferedDuration is the currently buffered PTS span (max-min PTS).
func (p *OutputBuffer) BufferedDuration() time.Duration {
	p.mu.Lock()
	defer p.mu.Unlock()
	if !p.havePTS {
		return 0
	}
	return time.Duration(p.maxPTSus-p.minPTSus) * time.Microsecond
}

// AddOrPass enqueues pkt into the buffer while still pre-rolling, or
// returns pass=true once the output has transitioned to STREAMING (so
// the caller forwards pkt directly to the muxer).
//
// Returned `full` is true once the target duration is met; the caller
// can stop pulling new packets and wait for the graph-level Ready
// signal, but it is safe to keep pushing — additional packets simply
// grow the buffer up to `maxDur`.
//
// When the caller stops owning pkt, it must Close() it; AddOrPass
// transfers ownership only on `pass=false`.
func (p *OutputBuffer) AddOrPass(chanIdx int, pkt *av.Packet) (pass, full bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if OutputBufferState(p.state.Load()) >= BufferStateStreaming {
		return true, true
	}

	p.buf = append(p.buf, bufferedPacket{chanIdx: chanIdx, pkt: pkt})

	ptsUS := ptsToUS(pkt, p.tbFor(chanIdx))
	if cnt := p.debugCount.Add(1); cnt <= 8 {
		span := p.maxPTSus - p.minPTSus
		log.Printf("preroll %q: chan=%d tb=%v pts=%d ptsUS=%d span_us=%d target_us=%d havePTS=%v",
			p.nodeID, chanIdx, p.tbFor(chanIdx), pkt.PTS(), ptsUS,
			span, int64(p.targetDur/time.Microsecond), p.havePTS)
	}
	if ptsUS != notSetUS {
		if !p.havePTS || ptsUS < p.minPTSus {
			p.minPTSus = ptsUS
		}
		if !p.havePTS || ptsUS > p.maxPTSus {
			p.maxPTSus = ptsUS
		}
		p.havePTS = true
	}

	// Evict from the front while we exceed the hard cap.
	for p.havePTS && len(p.buf) > 1 &&
		time.Duration(p.maxPTSus-p.minPTSus)*time.Microsecond > p.maxDur {
		evicted := p.buf[0]
		p.buf = p.buf[1:]
		_ = evicted.pkt.Close()
		p.evictions.Add(1)
		p.recomputeRangeLocked()
	}

	dur := time.Duration(p.maxPTSus-p.minPTSus) * time.Microsecond
	if dur >= p.targetDur && p.targetDur > 0 {
		p.markReadyLocked(BufferStateReady)
		full = true
	}
	return false, full
}

// MarkReadyPartial fast-paths the state to READY_PARTIAL when upstream
// EOSes before the buffer reaches `targetDur`. Idempotent.
func (p *OutputBuffer) MarkReadyPartial() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markReadyLocked(BufferStateReadyPartial)
}

func (p *OutputBuffer) markReadyLocked(s OutputBufferState) {
	if OutputBufferState(p.state.Load()) < BufferStateReady {
		p.state.Store(int32(s))
		p.readyAt.Store(time.Now().UnixNano())
		p.readyOnce.Do(func() { close(p.readyCh) })
	}
}

// Drain transfers ownership of every buffered packet to the caller and
// transitions the state to STREAMING under the same lock. Subsequent
// AddOrPass calls return `pass=true`.
func (p *OutputBuffer) Drain() []bufferedPacket {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.buf
	p.buf = nil
	p.havePTS = false
	if OutputBufferState(p.state.Load()) < BufferStateReady {
		// Drain called before fill target was met (e.g. graph-level
		// shutdown while still filling). Promote to READY_PARTIAL so
		// the readyCh closes and aggregators don't hang.
		p.markReadyLocked(BufferStateReadyPartial)
	}
	p.state.Store(int32(BufferStateStreaming))
	return out
}

// Close releases any still-buffered packets without forwarding them.
// Safe to call multiple times; should be invoked from the sink handler's
// cleanup path so a cancelled run does not leak refcounted AVPackets.
func (p *OutputBuffer) Close() {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, b := range p.buf {
		_ = b.pkt.Close()
	}
	p.buf = nil
	p.havePTS = false
	p.state.Store(int32(BufferStateDraining))
}

// ---------------------------------------------------------------------------
// Rolling-buffer API (STREAMING phase).
// ---------------------------------------------------------------------------

// SetProducerCount must be called before any producer goroutine calls
// Enqueue or EnqueueEOS. It records how many EnqueueEOS calls are needed
// before TakePaced treats the buffer as exhausted.
func (p *OutputBuffer) SetProducerCount(n int) {
	p.producerCount = int32(n)
}

// Enqueue adds pkt to the rolling buffer, updates the PTS range, enforces
// the maxDur eviction cap, and signals TakePaced. Ownership of pkt is
// transferred to the buffer.
func (p *OutputBuffer) Enqueue(chanIdx int, pkt *av.Packet) {
	p.mu.Lock()
	p.buf = append(p.buf, bufferedPacket{chanIdx: chanIdx, pkt: pkt})
	ptsUS := ptsToUS(pkt, p.tbFor(chanIdx))
	if ptsUS != notSetUS {
		if !p.havePTS || ptsUS < p.minPTSus {
			p.minPTSus = ptsUS
		}
		if !p.havePTS || ptsUS > p.maxPTSus {
			p.maxPTSus = ptsUS
		}
		p.havePTS = true
	}
	// Enforce maxDur cap.
	for p.havePTS && len(p.buf) > 1 &&
		time.Duration(p.maxPTSus-p.minPTSus)*time.Microsecond > p.maxDur {
		evicted := p.buf[0]
		p.buf = p.buf[1:]
		_ = evicted.pkt.Close()
		p.evictions.Add(1)
		p.recomputeRangeLocked()
	}
	p.mu.Unlock()
	select {
	case p.notifyCh <- struct{}{}:
	default:
	}
}

// EnqueueEOS signals that one producer has finished. When all producers
// have called EnqueueEOS and the buffer is empty, TakePaced returns false.
func (p *OutputBuffer) EnqueueEOS() {
	p.eosCount.Add(1)
	select {
	case p.notifyCh <- struct{}{}:
	default:
	}
}

// TakePaced blocks until the next packet is ready to be consumed at its
// PTS-scheduled wall-clock time, then returns it. Returns ({}, false)
// when all producers have finished and the buffer is empty, or when ctx
// is cancelled.
//
// The PTS/wall-clock origin is established on the first call: the first
// packet's PTS is anchored to the current wall clock, and all subsequent
// packets are paced relative to that anchor.
func (p *OutputBuffer) TakePaced(ctx context.Context) (bufferedPacket, bool) {
	for {
		p.mu.Lock()
		if len(p.buf) > 0 {
			item := p.popMinPTSLocked()
			p.mu.Unlock()

			// Establish PTS/wall origin on first consumption.
			ptsUS := ptsToUS(item.pkt, p.tbFor(item.chanIdx))
			if ptsUS != notSetUS {
				if p.wallOriginNs.CompareAndSwap(0, time.Now().UnixNano()) {
					p.ptsOriginUs.Store(ptsUS)
				}
				wallOriginNs := p.wallOriginNs.Load()
				ptsOriginUs := p.ptsOriginUs.Load()
				targetNs := wallOriginNs + (ptsUS-ptsOriginUs)*1000
				if sleepDur := time.Duration(targetNs - time.Now().UnixNano()); sleepDur > 0 {
					select {
					case <-time.After(sleepDur):
					case <-ctx.Done():
						// Return packet anyway; caller will write it.
					}
				}
			}
			return item, true
		}
		allDone := p.producerCount > 0 && p.eosCount.Load() >= p.producerCount
		p.mu.Unlock()

		if allDone {
			return bufferedPacket{}, false
		}
		select {
		case <-p.notifyCh:
		case <-ctx.Done():
			return bufferedPacket{}, false
		}
	}
}

// AheadNs returns how far the buffer's leading PTS edge is ahead of the
// current real-time playback position, in nanoseconds. Positive means
// the encoder is ahead; zero or negative means it is at or behind real-time.
// Returns 0 before rolling-phase pacing has started.
func (p *OutputBuffer) AheadNs() int64 {
	wallOriginNs := p.wallOriginNs.Load()
	if wallOriginNs == 0 {
		return 0
	}
	p.mu.Lock()
	if !p.havePTS {
		p.mu.Unlock()
		return 0
	}
	maxPTS := p.maxPTSus
	p.mu.Unlock()
	ptsOriginUs := p.ptsOriginUs.Load()
	ahead := wallOriginNs + (maxPTS-ptsOriginUs)*1000 - time.Now().UnixNano()
	return ahead
}

// popMinPTSLocked removes and returns the packet with the smallest PTS from
// buf. Must be called with p.mu held.
func (p *OutputBuffer) popMinPTSLocked() bufferedPacket {
	minIdx := 0
	minPTS := ptsToUS(p.buf[0].pkt, p.tbFor(p.buf[0].chanIdx))
	for i := 1; i < len(p.buf); i++ {
		pts := ptsToUS(p.buf[i].pkt, p.tbFor(p.buf[i].chanIdx))
		if pts != notSetUS && (minPTS == notSetUS || pts < minPTS) {
			minIdx = i
			minPTS = pts
		}
	}
	item := p.buf[minIdx]
	p.buf = append(p.buf[:minIdx], p.buf[minIdx+1:]...)
	p.recomputeRangeLocked()
	return item
}

func (p *OutputBuffer) tbFor(chanIdx int) [2]int {
	if chanIdx >= 0 && chanIdx < len(p.timeBases) {
		tb := p.timeBases[chanIdx]
		if tb[0] > 0 && tb[1] > 0 {
			return tb
		}
	}
	return [2]int{1, 1000000}
}

func (p *OutputBuffer) recomputeRangeLocked() {
	p.havePTS = false
	var lo, hi int64
	for _, b := range p.buf {
		ptsUS := ptsToUS(b.pkt, p.tbFor(b.chanIdx))
		if ptsUS == notSetUS {
			continue
		}
		if !p.havePTS || ptsUS < lo {
			lo = ptsUS
		}
		if !p.havePTS || ptsUS > hi {
			hi = ptsUS
		}
		p.havePTS = true
	}
	p.minPTSus = lo
	p.maxPTSus = hi
}

func ptsToUS(pkt *av.Packet, tb [2]int) int64 {
	pts := pkt.PTS()
	if pts == int64(av.NoPTSValue) {
		return notSetUS
	}
	if tb[0] <= 0 || tb[1] <= 0 {
		return notSetUS
	}
	return pts * int64(tb[0]) * 1_000_000 / int64(tb[1])
}

// graphReady tracks the conjunction of every output's preroll readiness
// and exposes the single channel returned by Pipeline.Ready().
type graphReady struct {
	mu        sync.Mutex
	prerolls  []*OutputBuffer
	ch        chan struct{}
	closed    bool
	since     time.Time
	closeOnce sync.Once
}

func newGraphReady() *graphReady {
	return &graphReady{ch: make(chan struct{})}
}

// Add registers a per-output preroll with the aggregator. Safe to call
// from multiple goroutines before the runner is started.
func (g *graphReady) Add(p *OutputBuffer) {
	g.mu.Lock()
	g.prerolls = append(g.prerolls, p)
	g.mu.Unlock()
}

// Ready returns the AND-aggregated readiness channel.
func (g *graphReady) Ready() <-chan struct{} { return g.ch }

// Since returns the time at which the graph became ready, or zero.
func (g *graphReady) Since() time.Time {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.since
}

// State returns a snapshot of per-output state for /realtime/ready
// responses and the GUI toolbar pill.
func (g *graphReady) State() (ready bool, since time.Time, outs []OutputReadyView) {
	g.mu.Lock()
	defer g.mu.Unlock()
	outs = make([]OutputReadyView, 0, len(g.prerolls))
	allReady := true
	for _, p := range g.prerolls {
		state := p.State()
		if !p.IsReady() {
			allReady = false
		}
		outs = append(outs, OutputReadyView{
			NodeID:      p.NodeID(),
			State:       state.String(),
			BufferedDur: p.BufferedDuration(),
			TargetDur:   p.TargetDur(),
			Evictions:   p.Evictions(),
		})
	}
	if len(g.prerolls) == 0 {
		allReady = false
	}
	return allReady, g.since, outs
}

// run blocks until every registered preroll has signalled readiness
// (or ctx cancels) and closes the aggregated channel exactly once.
// Posts a RealTimeReady event on the bus on success.
func (g *graphReady) run(ctx context.Context, events *EventBus) {
	g.mu.Lock()
	prerolls := append([]*OutputBuffer(nil), g.prerolls...)
	g.mu.Unlock()

	if len(prerolls) == 0 {
		return
	}

	for _, p := range prerolls {
		select {
		case <-p.Ready():
		case <-ctx.Done():
			return
		}
	}

	g.mu.Lock()
	if g.closed {
		g.mu.Unlock()
		return
	}
	g.closed = true
	g.since = time.Now()
	perOut := make(map[string]string, len(prerolls))
	for _, p := range prerolls {
		perOut[p.NodeID()] = p.State().String()
	}
	g.mu.Unlock()

	g.closeOnce.Do(func() { close(g.ch) })
	if events != nil {
		events.Post(RealTimeReady{When: time.Now(), PerOutput: perOut})
	}
}

// OutputReadyView is the GUI/HTTP-friendly serialisation of one output's
// readiness; exported so observability/jobTypes can hold it without
// importing internal types.
type OutputReadyView struct {
	NodeID      string        `json:"node_id"`
	State       string        `json:"state"`
	BufferedDur time.Duration `json:"buffered_ns"`
	TargetDur   time.Duration `json:"target_ns"`
	Evictions   int64         `json:"evictions"`
}
