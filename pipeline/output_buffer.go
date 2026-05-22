// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
)

// PrerollState is the per-output state machine described in Phase 7
// of docs/architecture/node_perf_monitoring_design.md.
type PrerollState int32

const (
	PrerollFilling PrerollState = iota
	PrerollReady
	PrerollReadyPartial
	PrerollStreaming
	PrerollDraining
)

// String renders the state to its design-doc spelling.
func (s PrerollState) String() string {
	switch s {
	case PrerollFilling:
		return "FILLING"
	case PrerollReady:
		return "READY"
	case PrerollReadyPartial:
		return "READY_PARTIAL"
	case PrerollStreaming:
		return "STREAMING"
	case PrerollDraining:
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

// OutputPreroll is the per-sink pre-roll buffer wired in front of the
// muxer writer in real-time mode. Packets pile up in `buf` (PTS-ordered
// arrival order) until either the target duration is reached or the
// upstream EOSes. Duration accounting uses the per-channel time base so
// VBR / VFR streams account correctly.
//
// Once the buffer reaches `targetDur`, the state advances to READY and
// `readyCh` closes. The pipeline-level aggregator AND-combines every
// output's readiness; when the graph is ready the drainer transitions
// to STREAMING and the muxer starts to flow.
//
// Overflow (`buf` duration > `maxDur`) evicts oldest packets and
// increments the eviction counter; this is the documented failure mode
// for pathological producers.
type OutputPreroll struct {
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

	readyCh   chan struct{}
	readyOnce sync.Once
	readyAt   atomic.Int64 // unix nano when ready first fired
}

// NewOutputPreroll constructs a preroll for nodeID with the configured
// fill target and cap. timeBases is indexed by input-channel ordinal
// (matching `handleSink`'s `ins`); empty entries fall back to AV_TIME_BASE.
func NewOutputPreroll(nodeID string, targetDur, maxDur time.Duration, timeBases [][2]int) *OutputPreroll {
	if targetDur < 0 {
		targetDur = 0
	}
	if maxDur <= 0 || maxDur < targetDur {
		// Doc default: max = 2 × target.
		maxDur = 2 * targetDur
	}
	return &OutputPreroll{
		nodeID:    nodeID,
		targetDur: targetDur,
		maxDur:    maxDur,
		timeBases: timeBases,
		readyCh:   make(chan struct{}),
	}
}

// NodeID returns the output node ID this preroll serves.
func (p *OutputPreroll) NodeID() string { return p.nodeID }

// State returns the current state machine value.
func (p *OutputPreroll) State() PrerollState {
	return PrerollState(p.state.Load())
}

// TargetDur reports the configured fill target.
func (p *OutputPreroll) TargetDur() time.Duration { return p.targetDur }

// MaxDur reports the configured hard cap.
func (p *OutputPreroll) MaxDur() time.Duration { return p.maxDur }

// Evictions returns the running count of packets evicted past `maxDur`.
func (p *OutputPreroll) Evictions() int64 { return p.evictions.Load() }

// Ready returns a channel that is closed once this output is in
// READY / READY_PARTIAL / STREAMING.
func (p *OutputPreroll) Ready() <-chan struct{} { return p.readyCh }

// IsReady reports whether the output has at least reached READY.
func (p *OutputPreroll) IsReady() bool {
	return p.State() >= PrerollReady && p.State() != PrerollDraining
}

// ReadyAt returns the wall-clock time at which the output first became
// ready; zero when still filling.
func (p *OutputPreroll) ReadyAt() time.Time {
	n := p.readyAt.Load()
	if n == 0 {
		return time.Time{}
	}
	return time.Unix(0, n)
}

// BufferedDuration is the currently buffered PTS span (max-min PTS).
func (p *OutputPreroll) BufferedDuration() time.Duration {
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
func (p *OutputPreroll) AddOrPass(chanIdx int, pkt *av.Packet) (pass, full bool) {
	p.mu.Lock()
	defer p.mu.Unlock()

	if PrerollState(p.state.Load()) >= PrerollStreaming {
		return true, true
	}

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
		p.markReadyLocked(PrerollReady)
		full = true
	}
	return false, full
}

// MarkReadyPartial fast-paths the state to READY_PARTIAL when upstream
// EOSes before the buffer reaches `targetDur`. Idempotent.
func (p *OutputPreroll) MarkReadyPartial() {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.markReadyLocked(PrerollReadyPartial)
}

func (p *OutputPreroll) markReadyLocked(s PrerollState) {
	if PrerollState(p.state.Load()) < PrerollReady {
		p.state.Store(int32(s))
		p.readyAt.Store(time.Now().UnixNano())
		p.readyOnce.Do(func() { close(p.readyCh) })
	}
}

// Drain transfers ownership of every buffered packet to the caller and
// transitions the state to STREAMING under the same lock. Subsequent
// AddOrPass calls return `pass=true`.
func (p *OutputPreroll) Drain() []bufferedPacket {
	p.mu.Lock()
	defer p.mu.Unlock()
	out := p.buf
	p.buf = nil
	p.havePTS = false
	if PrerollState(p.state.Load()) < PrerollReady {
		// Drain called before fill target was met (e.g. graph-level
		// shutdown while still filling). Promote to READY_PARTIAL so
		// the readyCh closes and aggregators don't hang.
		p.markReadyLocked(PrerollReadyPartial)
	}
	p.state.Store(int32(PrerollStreaming))
	return out
}

// Close releases any still-buffered packets without forwarding them.
// Safe to call multiple times; should be invoked from the sink handler's
// cleanup path so a cancelled run does not leak refcounted AVPackets.
func (p *OutputPreroll) Close() {
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
	p.state.Store(int32(PrerollDraining))
}

func (p *OutputPreroll) tbFor(chanIdx int) [2]int {
	if chanIdx >= 0 && chanIdx < len(p.timeBases) {
		tb := p.timeBases[chanIdx]
		if tb[0] > 0 && tb[1] > 0 {
			return tb
		}
	}
	return [2]int{1, 1000000}
}

func (p *OutputPreroll) recomputeRangeLocked() {
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
	prerolls  []*OutputPreroll
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
func (g *graphReady) Add(p *OutputPreroll) {
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
	prerolls := append([]*OutputPreroll(nil), g.prerolls...)
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
