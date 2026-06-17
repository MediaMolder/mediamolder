// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

import (
	"context"
	"fmt"
	"log"
	"math"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/graph"
	"github.com/MediaMolder/MediaMolder/processors"
	"golang.org/x/sync/errgroup"
)

// ---------- Sink handler ----------

// chanState tracks per-channel muxing bookkeeping inside handleSink.
type chanState struct {
	written   int
	lastPTSus int64
	lastPTSok bool
}

// ptsToMicros converts a PTS value (in tb units) to AV_TIME_BASE microseconds,
// returning (us, true) on success or (0, false) when the PTS is unset or the
// time_base is invalid. Mirrors the av_rescale_q(pts, tb, AV_TIME_BASE_Q) calls
// scattered throughout fftools for cross-stream PTS comparison.
func ptsToMicros(pts int64, tb [2]int) (int64, bool) {
	if pts == math.MinInt64 || tb[0] <= 0 || tb[1] <= 0 {
		return 0, false
	}
	return pts * 1_000_000 * int64(tb[0]) / int64(tb[1]), true
}

// shiftPTSus converts deltaUS microseconds into tb units and subtracts it from
// pkt's PTS/DTS. Mirrors of_streamcopy's `pkt->pts -= ts_offset` after
// rebasing the output to start at PTS 0.
func shiftPTSus(pkt *av.Packet, deltaUS int64, tb [2]int) {
	if deltaUS == 0 || tb[0] <= 0 || tb[1] <= 0 {
		return
	}
	off := deltaUS * int64(tb[1]) / (1_000_000 * int64(tb[0]))
	if off != 0 {
		pkt.ShiftTS(-off)
	}
}

// sinkWriter carries the per-output muxing state and implements the per-packet
// and per-channel write operations for handleSink. Separating this type from
// graphRunner makes processOne and writeOne independently testable.
type sinkWriter struct {
	sink    *sinkResources
	node    *graph.Node
	startUS int64       // output-side -ss in AV_TIME_BASE units; av.NoPTSValue = no trim
	stopUS  int64       // output-side -t/-to; noLimitUS = no trim
	shiftUS int64       // subtracted from kept packets so output anchors at PTS 0
	mu      *sync.Mutex // nil for single-stream path; shared across goroutines otherwise
	pipe    *Pipeline
	// pendingCut is non-nil when the output has SegmentOnMetadata set.
	// The go_processor handler signals it with the cut frame's source-PTS
	// (microseconds); the video goroutine rotates the muxer at the next
	// keyframe whose PTS is at or past that value, and non-video goroutines
	// wait on the gate so their packets land in the correct segment.
	pendingCut *cutGate
	// segCounter is the zero-based index of the current segment.
	// 0 on the first segment; incremented each time the muxer is rotated.
	segCounter int
}

// limitForChan returns the max-frames cap for input channel i (0 = unlimited).
func (w *sinkWriter) limitForChan(i int) int {
	if i >= len(w.node.Inbound) {
		return 0
	}
	switch w.node.Inbound[i].Type {
	case graph.PortVideo:
		return w.sink.cfg.MaxFramesVideo
	case graph.PortAudio:
		return w.sink.cfg.MaxFramesAudio
	}
	return 0
}

// recordShortest updates the shared shortestPTSus to min(current, lastPTSus)
// when the -shortest flag is active. Called on natural channel close.
func (w *sinkWriter) recordShortest(lastPTSus int64, ok bool) {
	if !w.sink.shortest || !ok {
		return
	}
	w.sink.shortestMu.Lock()
	if lastPTSus < w.sink.shortestPTSus {
		w.sink.shortestPTSus = lastPTSus
	}
	w.sink.shortestMu.Unlock()
}

// shortestReached returns true when another channel has already closed and the
// -shortest PTS cap has been reached for ptsUS.
func (w *sinkWriter) shortestReached(ptsUS int64, ok bool) bool {
	if !w.sink.shortest || !ok {
		return false
	}
	w.sink.shortestMu.Lock()
	bound := w.sink.shortestPTSus
	w.sink.shortestMu.Unlock()
	return bound != noLimitUS && ptsUS >= bound
}

// writeOne handles a single muxer-bound packet: max_file_size check,
// WritePacket under w.mu (when non-nil), and per-channel bookkeeping.
// Returns (wrote, stopAll, err).
func (w *sinkWriter) writeOne(pkt *av.Packet, i int, dstTB [2]int, st *chanState) (bool, bool, error) {
	frameStart := time.Now()
	if w.mu != nil {
		w.mu.Lock()
	}
	stopAllNow := false
	if w.sink.maxFileSize > 0 {
		if cur := w.sink.muxer.BytesWritten(); cur >= 0 && cur >= w.sink.maxFileSize {
			stopAllNow = true
		}
	}
	var wErr error
	if !stopAllNow {
		wErr = w.sink.muxer.WritePacket(pkt)
	}
	if w.mu != nil {
		w.mu.Unlock()
	}
	if stopAllNow {
		return false, true, nil
	}
	if wErr != nil {
		return false, false, wErr
	}
	st.written++
	if pPTS, hasP := ptsToMicros(pkt.PTS(), dstTB); hasP {
		st.lastPTSus = pPTS
		st.lastPTSok = true
		ptsNs := time.Duration(pkt.PTS()) * time.Second *
			time.Duration(dstTB[0]) / time.Duration(dstTB[1])
		// Track per-stream so a fast AAC encoder doesn't push the
		// node's reported OutputPTS to 100% while libx265 still has
		// minutes left.
		w.pipe.Metrics().Node(w.node.ID).AdvanceOutputPTSStream(i, ptsNs)
	}
	w.pipe.Metrics().Node(w.node.ID).RecordLatency(time.Since(frameStart))
	return true, false, nil
}

// processOne runs the full per-packet pipeline for input channel i:
// max-frames cap, output-side trim drop/stop, ts shift, rescale,
// shortest cap, BSF chain, then writeOne.
// Returns (wrote, stopAll, err); stopAll signals every channel to drain-and-drop.
func (w *sinkWriter) processOne(i int, pkt *av.Packet, dstTB [2]int, rs *sinkRescale, st *chanState) (bool, bool, error) {
	// Per-stream max frames (mirrors FFmpeg's `-frames:v` / `-frames:a`).
	if lim := w.limitForChan(i); lim > 0 && st.written >= lim {
		return false, false, nil
	}
	// Attachment data is carried in codecpar->extradata and written by
	// WriteHeader; the muxer ignores per-packet writes for attachment
	// streams. Drop the packet here so WritePacket is never called.
	if i < len(w.node.Inbound) && w.node.Inbound[i].Type == graph.PortAttachment {
		return false, false, nil
	}
	pkt.SetStreamIndex(i)
	if rs != nil {
		pkt.Rescale(rs.srcTB, rs.dstTB)
	}
	ptsUS, hasPTS := ptsToMicros(pkt.PTS(), dstTB)

	// When no output-side -ss is configured and copyTS is false, drop any
	// packet arriving with a negative PTS. After an input-side -ss seek the
	// source shifts every packet by ts_offset so the keyframe that landed
	// just before the seek point gets a small negative PTS; writing it to
	// the muxer shortens the video track relative to audio, causing A/V
	// duration mismatches. Mirrors of_streamcopy's ts_copy_start=0 guard
	// in fftools/ffmpeg_mux.c: when !copy_ts the ts_copy_start defaults to
	// 0 and packets with pts < 0 are silently dropped.
	if !w.sink.copyTS && w.startUS == int64(av.NoPTSValue) && hasPTS && ptsUS < 0 {
		return false, false, nil
	}

	// Output-side `-ss`: drop packets below the configured start.
	// Mirrors of_streamcopy's `if (dts < of->start_time) return EAGAIN`.
	if w.startUS != int64(av.NoPTSValue) && hasPTS && ptsUS < w.startUS {
		return false, false, nil
	}
	// Output-side `-t`/`-to`: stop when any kept packet reaches the end.
	// Mirrors `check_recording_time`'s `av_compare_ts >= 0` => stop.
	if w.stopUS != noLimitUS && hasPTS && ptsUS >= w.stopUS {
		return false, true, nil
	}
	// `-shortest`: stop once another channel has closed at a lower PTS.
	if w.shortestReached(ptsUS, hasPTS) {
		return false, false, nil
	}

	// Shift kept packets so the output file anchors at PTS 0
	// (suppressed under -copyts).
	shiftPTSus(pkt, w.shiftUS, dstTB)

	// BSF chain: drive through av_bsf_send_packet / av_bsf_receive_packet
	// and call writeOne for each output packet. Mirrors
	// fftools/ffmpeg_mux.c::write_packet's BSF loop.
	var bsf *av.BitstreamFilter
	if i < len(w.sink.streamBSF) {
		bsf = w.sink.streamBSF[i]
	}
	if bsf != nil {
		outs, err := bsf.FilterPacket(pkt)
		if err != nil {
			return false, false, fmt.Errorf("bsf filter: %w", err)
		}
		var wroteAny bool
		for _, op := range outs {
			op.SetStreamIndex(i)
			wrote, stopAll, werr := w.writeOne(op, i, dstTB, st)
			op.Close()
			wroteAny = wroteAny || wrote
			if stopAll || werr != nil {
				return wroteAny, stopAll, werr
			}
		}
		return wroteAny, false, nil
	}
	return w.writeOne(pkt, i, dstTB, st)
}

// flushBSF drains residual packets buffered inside the BSF chain at
// end-of-stream by sending a null packet (EOF signal), then writing the
// drained output through WritePacket. Mirrors fftools/ffmpeg_mux.c::mux_thread
// flushing the BSF before WriteTrailer.
func (w *sinkWriter) flushBSF(i int, _ [2]int, st *chanState) error {
	var bsf *av.BitstreamFilter
	if i < len(w.sink.streamBSF) {
		bsf = w.sink.streamBSF[i]
	}
	if bsf == nil {
		return nil
	}
	outs, err := bsf.Flush()
	if err != nil {
		return fmt.Errorf("bsf flush: %w", err)
	}
	for _, op := range outs {
		op.SetStreamIndex(i)
		if w.mu != nil {
			w.mu.Lock()
		}
		werr := w.sink.muxer.WritePacket(op)
		if w.mu != nil {
			w.mu.Unlock()
		}
		op.Close()
		if werr != nil {
			return werr
		}
		st.written++
	}
	return nil
}

// chanCtx tracks per-channel muxing bookkeeping inside handleSink.
type chanCtx struct {
	idx   int
	dstTB [2]int
	// srcTB is the time-base of pkt.PTS() as it arrives on this channel,
	// before processOne calls pkt.Rescale. Used for the pre-rescale
	// microsecond conversion when comparing against the cut-PTS gate.
	srcTB [2]int
	rs    *sinkRescale
	st    *chanState
}

// dispatchSegmentCompleted notifies any go_processors registered as
// segment_sink event consumers for sinkNodeID. Each consumer's
// OnSegmentCompleted is non-blocking: it registers wg.Add(1) then launches
// its own goroutine internally, so calling it synchronously here is safe and
// ensures the WaitGroup counter is incremented before Close()→wg.Wait() runs.
func (r *graphRunner) dispatchSegmentCompleted(ctx context.Context, sinkNodeID, outputID, filePath string, segmentIndex int) {
	consumers := r.segmentConsumers[sinkNodeID]
	if len(consumers) == 0 {
		return
	}
	ev := processors.SegmentEvent{
		OutputID:     outputID,
		FilePath:     filePath,
		SegmentIndex: segmentIndex,
	}
	// OnSegmentCompleted is non-blocking (it registers wg.Add(1) then launches
	// its own goroutine). Call synchronously so the wg counter is incremented
	// before dispatchSegmentCompleted returns — eliminating the race where
	// Close()→wg.Wait() could return before the goroutine calls wg.Add.
	for _, c := range consumers {
		c.OnSegmentCompleted(ctx, ev)
	}
}

func (r *graphRunner) handleSink(ctx context.Context, node *graph.Node, ins []<-chan any) error {
	sink := r.sinks[node.ID]
	if sink == nil {
		return fmt.Errorf("sink handler: no resources for node %q", node.ID)
	}

	startUS := sink.timing.startTimestampUS()
	stopUS := sink.timing.stopTimestampUS(sink.copyTS)
	var shiftDownUS int64
	if !sink.copyTS && startUS != int64(av.NoPTSValue) {
		shiftDownUS = startUS
	}

	w := &sinkWriter{
		sink:    sink,
		node:    node,
		startUS: startUS,
		stopUS:  stopUS,
		shiftUS: shiftDownUS,
		pipe:    r.pipe,
	}

	// The pending-cut flag was pre-registered in r.segmentCuts during openSink
	// (before any goroutines started). Just wire it into the sinkWriter.
	w.pendingCut = sink.pendingCut

	// Pre-build per-channel rescale + chanState bookkeeping.
	ctxs := make([]*chanCtx, len(ins))
	for i := range ins {
		var rs *sinkRescale
		if i < len(sink.streamRescale) {
			rs = sink.streamRescale[i]
		}
		dstTB := sink.muxer.StreamTimeBase(i)
		srcTB := dstTB
		if rs != nil {
			dstTB = rs.dstTB
			srcTB = rs.srcTB
		}
		ctxs[i] = &chanCtx{idx: i, dstTB: dstTB, srcTB: srcTB, rs: rs, st: &chanState{}}
	}

	// Multi-channel paths share the muxer; serialise writes via mu.
	if len(ins) > 1 {
		w.mu = &sync.Mutex{}
	}

	processOne := func(c *chanCtx, pkt *av.Packet) error {
		if sink.stopAll.Load() {
			_ = pkt.Close()
			return nil
		}
		_, stopAll, err := w.processOne(c.idx, pkt, c.dstTB, c.rs, c.st)
		if stopAll {
			sink.stopAll.Store(true)
		}
		_ = pkt.Close()
		return err
	}

	// Phase A — real-time preroll fill (Phase 7). When sink.preroll is
	// non-nil the writer must not push to the muxer until both this
	// output's buffer is full AND every other output is also ready.
	// We spawn one pull-goroutine per input channel that enqueues
	// packets via AddOrPass until the preroll transitions to STREAMING
	// (Drain has been called) or the input closes.
	if sink.preroll != nil {
		eosAll := make(chan struct{})
		var eosWG sync.WaitGroup
		eosWG.Add(len(ins))
		go func() { eosWG.Wait(); close(eosAll) }()

		// Per-channel "leftover": a packet that AddOrPass returned
		// pass=true for (preroll already in STREAMING). Phase D
		// must process it before resuming normal channel pulls.
		leftover := make([]*av.Packet, len(ins))

		fillCtx, fillCancel := context.WithCancel(ctx)
		defer fillCancel()

		for i, in := range ins {
			i, in := i, in
			go func() {
				defer eosWG.Done()
				for {
					select {
					case <-fillCtx.Done():
						return
					case v, ok := <-in:
						if !ok {
							return
						}
						pkt := v.(*av.Packet)
						pass, _ := sink.preroll.AddOrPass(i, pkt)
						if pass {
							leftover[i] = pkt
							return
						}
					}
				}
			}()
		}

		// Wait for: this output is ready OR every channel EOS'd first.
		select {
		case <-sink.preroll.Ready():
		case <-eosAll:
			sink.preroll.MarkReadyPartial()
		case <-ctx.Done():
			return ctx.Err()
		}

		// Wait for the graph-level AND across all outputs.
		select {
		case <-r.pipe.Ready():
		case <-ctx.Done():
			return ctx.Err()
		}

		// Drain preroll in PTS-arrival order and feed the muxer.
		buffered := sink.preroll.Drain()
		for _, b := range buffered {
			if err := processOne(ctxs[b.chanIdx], b.pkt); err != nil {
				fillCancel()
				return err
			}
		}

		// Stop phase-A goroutines and wait for them to settle.
		fillCancel()
		eosWG.Wait()

		// Process any packets parked in leftover[].
		for i, pkt := range leftover {
			if pkt == nil {
				continue
			}
			if err := processOne(ctxs[i], pkt); err != nil {
				return err
			}
		}
	}

	// Phase D — normal processing. When sink.preroll is non-nil (realtime
	// mode) the OutputBuffer continues as a rolling jitter buffer: N
	// producer goroutines push packets via Enqueue and the single consumer
	// below paces delivery to the stream's PTS wall-clock rate.
	if sink.preroll != nil {
		sink.preroll.SetProducerCount(len(ins))
		for i, in := range ins {
			i, in := i, in
			go func() {
				defer sink.preroll.EnqueueEOS()
				for v := range in {
					sink.preroll.Enqueue(i, v.(*av.Packet))
				}
			}()
		}
		for {
			item, ok := sink.preroll.TakePaced(ctx)
			if !ok {
				break
			}
			if err := processOne(ctxs[item.chanIdx], item.pkt); err != nil {
				return err
			}
		}
		for i, c := range ctxs {
			w.recordShortest(c.st.lastPTSus, c.st.lastPTSok)
			if err := w.flushBSF(i, c.dstTB, c.st); err != nil {
				return err
			}
		}
		return sink.muxer.WriteTrailer()
	}

	// Non-realtime Phase D — no output buffer; write at encode rate.
	if len(ins) == 1 {
		c := ctxs[0]
		t := perfTrackerFrom(ctx)
		for {
			v, cancelled := perfReceive(ctx, ins[0], t)
			if cancelled {
				break
			}
			pkt := v.(*av.Packet)
			// Rotate at the first video keyframe whose source-PTS is at or past
			// the pending cut PTS (stored in microseconds by the go_processor).
			// Using a PTS threshold (rather than a plain bool) prevents the sink
			// from rotating early when the go_processor runs ahead of the encoder.
			if w.pendingCut != nil && pkt.IsKeyFrame() {
				if cutUS := w.pendingCut.frontCut(); cutUS >= 0 {
					if pktUS, ok := ptsToMicros(pkt.PTS(), c.srcTB); ok && pktUS >= cutUS {
						nextURL := fmt.Sprintf(sink.cfg.URL, w.segCounter+1)
						if err := r.rotateSegment(w, &sink.cfg, nextURL); err != nil {
							_ = pkt.Close()
							return err
						}
						w.pendingCut.popFront()
						// Anchor the new segment at PTS 0 so each output
						// MP4 plays back from time zero rather than carrying
						// the original source timeline. Suppressed under
						// -copyts.
						if !sink.copyTS {
							w.shiftUS = pktUS
						}
						c.dstTB = sink.muxer.StreamTimeBase(0)
						if c.rs != nil {
							c.dstTB = c.rs.dstTB
						}
					}
				}
			}
			if err := processOne(c, pkt); err != nil {
				return err
			}
		}
		w.recordShortest(c.st.lastPTSus, c.st.lastPTSok)
		if err := w.flushBSF(0, c.dstTB, c.st); err != nil {
			return err
		}
		if err := sink.muxer.WriteTrailer(); err != nil {
			return err
		}
		// Emit SegmentCompleted for the last segment.
		if w.pendingCut != nil {
			filePath := fmt.Sprintf(sink.cfg.URL, w.segCounter)
			// Finalize (close IO + atomic rename .tmp → final) before
			// notifying so the file exists when OnSegmentCompleted fires.
			if err := sink.muxer.Close(); err != nil {
				return fmt.Errorf("finalize segment %q: %w", filePath, err)
			}
			r.pipe.events.Post(SegmentCompleted{
				OutputID:     sink.cfg.ID,
				FilePath:     filePath,
				SegmentIndex: w.segCounter,
			})
			// Use context.Background() so the dispatch goroutine is not cancelled
			// when the scheduler errgroup context is cancelled after all node
			// handlers return — the same reason rotateSegment uses Background().
			r.dispatchSegmentCompleted(context.Background(), w.node.ID, sink.cfg.ID, filePath, w.segCounter)
		}
		return nil
	}

	// Multiple input streams: interleave with per-stream goroutines.
	// Keep the derived context: if one stream's consumer fails (e.g. the
	// muxer rejects a non-monotonic DTS), egctx is cancelled so the sibling
	// consumers — which would otherwise block forever on a never-closed
	// input channel — unwind promptly. Without this the whole pipeline
	// deadlocks: a single failed stream hangs the muxer's eg.Wait and
	// back-pressures every upstream node feeding the abandoned channel.
	eg, egctx := errgroup.WithContext(ctx)

	for _, c := range ctxs {
		c := c
		in := ins[c.idx]
		isVideo := c.idx < len(w.node.Inbound) && w.node.Inbound[c.idx].Type == graph.PortVideo
		eg.Go(func() error {
			// Use a closure so lastPTSus/lastPTSok are read at deferred-call
			// time, not at defer-statement time. Plain `defer f(a, b)` evaluates
			// a and b immediately — capturing (0, false) here at goroutine start
			// — causing recordShortest to always no-op (ok=false).
			defer func() { w.recordShortest(c.st.lastPTSus, c.st.lastPTSok) }()
			// When the video goroutine exits (either normally or via error),
			// drop all queued cuts so any still-held audio packets fall
			// through to the current (final) muxer in the loop below.
			if isVideo {
				defer func() {
					if w.pendingCut != nil {
						w.pendingCut.clearAll()
					}
				}()
			}
			// Non-video channels (audio stream-copy etc.) typically run
			// ahead of the video encoder's lookahead delay. Rather than
			// blocking — which back-pressures the source goroutine that
			// feeds video and deadlocks — we drain and locally buffer
			// packets whose PTS is at/past the front pending cut. After
			// each rotation we flush every held packet whose PTS is below
			// the *next* queued cut (or all of them once the queue drains).
			type heldPkt struct {
				pkt *av.Packet
				us  int64
			}
			var held []heldPkt
			refreshDst := func() {
				c.dstTB = sink.muxer.StreamTimeBase(c.idx)
				if c.rs != nil {
					c.dstTB = c.rs.dstTB
				}
			}
			// flushHeldUpTo writes every held packet whose PTS is < boundary
			// (boundary < 0 means "no upper bound"). Refreshes c.dstTB
			// before the first write since the muxer may have rotated.
			flushHeldUpTo := func(boundary int64) error {
				if len(held) == 0 {
					return nil
				}
				refreshed := false
				keep := held[:0]
				for _, hp := range held {
					if boundary >= 0 && hp.us >= boundary {
						keep = append(keep, hp)
						continue
					}
					if !refreshed {
						refreshDst()
						refreshed = true
					}
					if err := processOne(c, hp.pkt); err != nil {
						return err
					}
				}
				held = keep
				return nil
			}
			// audioBoundary returns the highest PTS at which a held audio
			// packet may safely be written to the current segment. It is
			// min(frontCut, progress+1): the next cut hasn't happened yet,
			// AND go_processor has confirmed no earlier cut at that PTS.
			audioBoundary := func() int64 {
				front := w.pendingCut.frontCut()
				prog := w.pendingCut.progress()
				// progress+1 because a packet exactly AT progress has been
				// processed by go_processor (so we know its cut status).
				progBoundary := int64(math.MaxInt64)
				if prog < math.MaxInt64 {
					progBoundary = prog + 1
				}
				if front < 0 {
					return progBoundary
				}
				if front < progBoundary {
					return front
				}
				return progBoundary
			}
			for {
				var v any
				var ok bool
				select {
				case <-egctx.Done():
					return egctx.Err()
				case v, ok = <-in:
				}
				if !ok {
					break
				}
				pkt := v.(*av.Packet)
				// If audio is holding packets and we can now safely flush
				// some (because either the muxer rotated or go_processor
				// advanced its progress cursor past them), do so.
				if !isVideo && len(held) > 0 && w.pendingCut != nil {
					if err := flushHeldUpTo(audioBoundary()); err != nil {
						_ = pkt.Close()
						return err
					}
				}
				if w.pendingCut != nil {
					pktUS, ok := ptsToMicros(pkt.PTS(), c.srcTB)
					if isVideo {
						if pkt.IsKeyFrame() {
							if cutUS := w.pendingCut.frontCut(); cutUS >= 0 && ok && pktUS >= cutUS {
								nextURL := fmt.Sprintf(sink.cfg.URL, w.segCounter+1)
								// Hold w.mu for the full WriteTrailer + reopen
								// to prevent audio goroutines from writing to
								// a closed/new muxer.
								w.mu.Lock()
								rotErr := r.rotateSegment(w, &sink.cfg, nextURL)
								if rotErr == nil && !sink.copyTS {
									// Anchor each new segment at PTS 0.
									w.shiftUS = pktUS
								}
								w.mu.Unlock()
								if rotErr != nil {
									_ = pkt.Close()
									return rotErr
								}
								// Advance the queue after the muxer is reopened
								// so audio goroutines that wake see the new
								// front cut.
								w.pendingCut.popFront()
								refreshDst()
							}
						}
					} else if ok {
						// Non-video: hold packets that may belong to a
						// future segment rather than blocking (which would
						// deadlock the single-goroutine source).
						if pktUS >= audioBoundary() {
							held = append(held, heldPkt{pkt: pkt, us: pktUS})
							continue
						}
					}
				}
				if err := processOne(c, pkt); err != nil {
					return err
				}
			}
			// Drain any remaining held packets into the final segment(s).
			// The video goroutine's clearAll() defer empties the queue, so
			// flushing with boundary < 0 writes everything to whatever muxer
			// is current at that point.
			if !isVideo && len(held) > 0 {
				if err := flushHeldUpTo(-1); err != nil {
					return err
				}
			}
			return w.flushBSF(c.idx, c.dstTB, c.st)
		})
	}

	if err := eg.Wait(); err != nil {
		return err
	}
	if err := sink.muxer.WriteTrailer(); err != nil {
		return err
	}
	// Emit SegmentCompleted for the last segment.
	if w.pendingCut != nil {
		filePath := fmt.Sprintf(sink.cfg.URL, w.segCounter)
		// Finalize (close IO + atomic rename .tmp → final) before
		// notifying so the file exists when OnSegmentCompleted fires.
		if err := sink.muxer.Close(); err != nil {
			return fmt.Errorf("finalize segment %q: %w", filePath, err)
		}
		r.pipe.events.Post(SegmentCompleted{
			OutputID:     sink.cfg.ID,
			FilePath:     filePath,
			SegmentIndex: w.segCounter,
		})
		// Use context.Background() so the dispatch goroutine is not cancelled
		// when the scheduler errgroup context is cancelled after all node
		// handlers return — the same reason rotateSegment uses Background().
		r.dispatchSegmentCompleted(context.Background(), w.node.ID, sink.cfg.ID, filePath, w.segCounter)
	}
	return nil
}

// rotateSegment closes the current segment file and opens the next one.
// Must be called with w.mu held (multi-stream) or from a single goroutine
// (single-stream). BSF chains are closed without flushing; any BSF-buffered
// packets at the rotation boundary are discarded (acceptable for metadata-
// driven segmentation). On success it updates w.sink.muxer,
// w.sink.streamRescale, w.sink.streamBSF, and w.segCounter.
// The caller is responsible for refreshing its own c.dstTB after the call.
func (r *graphRunner) rotateSegment(w *sinkWriter, out *Output, newURL string) error {
	sink := w.sink

	// Close the current segment (write container trailer + flush avio).
	if err := sink.muxer.WriteTrailer(); err != nil {
		return fmt.Errorf("segment rotate write trailer: %w", err)
	}
	prevURL := fmt.Sprintf(out.URL, w.segCounter)

	// Close old BSF chains (no flush — discarding any buffered packets
	// at the segment boundary is acceptable and avoids a deadlock when
	// w.mu is already held by the video goroutine).
	for i := range sink.streamBSF {
		if sink.streamBSF[i] != nil {
			_ = sink.streamBSF[i].Close()
			sink.streamBSF[i] = nil
		}
	}

	// Finalize the completed segment: close IO and atomically rename
	// .tmp → final path so the file exists before OnSegmentCompleted fires.
	if err := sink.muxer.Close(); err != nil {
		return fmt.Errorf("segment rotate finalize %q: %w", prevURL, err)
	}

	// Notify downstream processors that the segment is complete.
	r.pipe.events.Post(SegmentCompleted{
		OutputID:     out.ID,
		FilePath:     prevURL,
		SegmentIndex: w.segCounter,
	})
	r.dispatchSegmentCompleted(context.Background(), w.node.ID, out.ID, prevURL, w.segCounter)

	// Open the new segment muxer.
	format := out.Format
	if out.SegmentFormat != "" {
		format = out.SegmentFormat
	}
	newMuxer, err := av.OpenOutputWithFormat(newURL, format)
	if err != nil {
		return fmt.Errorf("segment rotate open %q: %w", newURL, err)
	}

	// Add one output stream per inbound edge (same logic as openSink, minus
	// cover art and attachments which are only written to the first segment).
	newRescales := make([]*sinkRescale, len(w.node.Inbound))
	for i, e := range w.node.Inbound {
		from := e.From
		var outIdx int
		switch from.Kind {
		case graph.KindEncoder:
			enc := r.encoders[from.ID]
			if enc == nil {
				newMuxer.Abort()
				return fmt.Errorf("segment rotate sink %q: no encoder for %q", w.node.ID, from.ID)
			}
			idx, addErr := newMuxer.AddStream(enc)
			if addErr != nil {
				newMuxer.Abort()
				return fmt.Errorf("segment rotate add stream: %w", addErr)
			}
			outIdx = idx
			newRescales[i] = &sinkRescale{srcTB: enc.TimeBase(), dstTB: newMuxer.StreamTimeBase(outIdx)}
		case graph.KindCopy:
			srcInput, srcIdx, srcTB, cpErr := r.copySourceFor(from)
			if cpErr != nil {
				newMuxer.Abort()
				return fmt.Errorf("segment rotate copy source: %w", cpErr)
			}
			idx, addErr := newMuxer.AddStreamFromInput(srcInput, srcIdx)
			if addErr != nil {
				newMuxer.Abort()
				return fmt.Errorf("segment rotate add stream from input: %w", addErr)
			}
			outIdx = idx
			newRescales[i] = &sinkRescale{srcTB: srcTB, dstTB: newMuxer.StreamTimeBase(outIdx)}
		default:
			newMuxer.Abort()
			return fmt.Errorf("segment rotate sink %q: unknown edge kind %q", w.node.ID, from.Kind)
		}
		// Apply optional codec_tag override.
		var tag string
		switch e.Type {
		case graph.PortVideo:
			tag = out.CodecTagVideo
		case graph.PortAudio:
			tag = out.CodecTagAudio
		case graph.PortSubtitle:
			tag = out.CodecTagSubtitle
		}
		if tag != "" {
			if tagErr := newMuxer.SetStreamCodecTag(outIdx, tag); tagErr != nil {
				newMuxer.Abort()
				return fmt.Errorf("segment rotate set codec_tag: %w", tagErr)
			}
		}
	}

	// Attach new BSF chains.
	newBSF := make([]*av.BitstreamFilter, len(w.node.Inbound))
	for i, e := range w.node.Inbound {
		var spec string
		switch e.Type {
		case graph.PortVideo:
			spec = out.BSFVideo
		case graph.PortAudio:
			spec = out.BSFAudio
		case graph.PortSubtitle:
			spec = out.BSFSubtitle
		}
		if spec == "" {
			continue
		}
		bsf, bsfErr := newMuxer.AttachStreamBSF(i, spec)
		if bsfErr != nil {
			for _, b := range newBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			newMuxer.Abort()
			return fmt.Errorf("segment rotate attach bsf: %w", bsfErr)
		}
		newBSF[i] = bsf
	}

	// Apply container metadata.
	if metaErr := r.applyOutputMetadata(newMuxer, out); metaErr != nil {
		for _, b := range newBSF {
			if b != nil {
				_ = b.Close()
			}
		}
		newMuxer.Abort()
		return fmt.Errorf("segment rotate metadata: %w", metaErr)
	}

	// Apply per-stream color metadata and HDR10 side data.
	if out.Color != nil || out.HDR != nil {
		for i, e := range w.node.Inbound {
			if e.Type != graph.PortVideo {
				continue
			}
			if out.Color != nil {
				if colorErr := newMuxer.SetStreamColor(i, av.ColorParams{
					Range:          out.Color.Range,
					Primaries:      out.Color.Primaries,
					Transfer:       out.Color.Transfer,
					Space:          out.Color.Space,
					ChromaLocation: out.Color.ChromaLocation,
				}); colorErr != nil {
					for _, b := range newBSF {
						if b != nil {
							_ = b.Close()
						}
					}
					newMuxer.Abort()
					return fmt.Errorf("segment rotate set color: %w", colorErr)
				}
			}
			if out.HDR != nil {
				if md := out.HDR.MasteringDisplay; md != nil {
					hasPrim := md.DisplayPrimariesRX != 0 || md.WhitePointX != 0
					hasLum := md.MaxLuminance != 0
					if hasPrim || hasLum {
						if hdrErr := newMuxer.SetStreamMasteringDisplay(i, av.MasteringDisplay{
							HasPrimaries: hasPrim,
							DisplayPrim: [6]int{
								md.DisplayPrimariesRX, md.DisplayPrimariesRY,
								md.DisplayPrimariesGX, md.DisplayPrimariesGY,
								md.DisplayPrimariesBX, md.DisplayPrimariesBY,
							},
							WhitePoint:   [2]int{md.WhitePointX, md.WhitePointY},
							HasLuminance: hasLum,
							MinLuminance: md.MinLuminance,
							MaxLuminance: md.MaxLuminance,
						}); hdrErr != nil {
							for _, b := range newBSF {
								if b != nil {
									_ = b.Close()
								}
							}
							newMuxer.Abort()
							return fmt.Errorf("segment rotate set mastering display: %w", hdrErr)
						}
					}
				}
				if cll := out.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL != 0 || cll.MaxFALL != 0) {
					if hdrErr := newMuxer.SetStreamContentLightLevel(i, av.ContentLightLevel{
						MaxCLL:  cll.MaxCLL,
						MaxFALL: cll.MaxFALL,
					}); hdrErr != nil {
						for _, b := range newBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						newMuxer.Abort()
						return fmt.Errorf("segment rotate set content light level: %w", hdrErr)
					}
				}
				if dv := out.HDR.DoVi; dv != nil && dv.Profile != 0 {
					rpu := true
					if dv.RPUPresent != nil {
						rpu = *dv.RPUPresent
					}
					bl := true
					if dv.BLPresent != nil {
						bl = *dv.BLPresent
					}
					if hdrErr := newMuxer.SetStreamDoViConfig(i, av.DoViConfig{
						Profile:           dv.Profile,
						Level:             dv.Level,
						RPUPresent:        rpu,
						ELPresent:         dv.ELPresent,
						BLPresent:         bl,
						BLCompatibilityID: dv.BLCompatibilityID,
					}); hdrErr != nil {
						for _, b := range newBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						newMuxer.Abort()
						return fmt.Errorf("segment rotate set dovi config: %w", hdrErr)
					}
				}
			}
		}
	}

	// Write the container header.
	if hdrErr := newMuxer.WriteHeaderWithOptions(buildMuxerOptions(out)); hdrErr != nil {
		for _, b := range newBSF {
			if b != nil {
				_ = b.Close()
			}
		}
		newMuxer.Abort()
		return fmt.Errorf("segment rotate write header: %w", hdrErr)
	}

	// Some muxers adjust stream time_base in WriteHeader; refresh.
	for i, rs := range newRescales {
		if rs == nil {
			continue
		}
		if i < len(newBSF) && newBSF[i] != nil {
			continue
		}
		rs.dstTB = newMuxer.StreamTimeBase(i)
	}

	// Commit the new muxer state.
	sink.muxer = newMuxer
	sink.streamRescale = newRescales
	sink.streamBSF = newBSF
	w.segCounter++
	return nil
}

func (r *graphRunner) openSink(_ *graph.Graph, node *graph.Node) (*sinkResources, error) {
	out := r.findOutputConfig(node.ID)
	if out == nil {
		return nil, fmt.Errorf("sink node %q: no matching output config", node.ID)
	}

	// Per-output timing flags (FFmpeg's output-side `-ss` / `-t` /
	// `-to`). Stripped from the AVDict before any libav consumer sees
	// them; enforced by handleSink against per-packet PTS, mirroring
	// `fftools/ffmpeg_mux.c::of_streamcopy`.
	outTiming, err := resolveOutputTiming(out.Options, func(format string, args ...any) {
		log.Printf("output %q: "+format, append([]any{out.URL}, args...)...)
	})
	if err != nil {
		return nil, fmt.Errorf("output %q timing: %w", out.URL, err)
	}

	var muxer *av.OutputFormatContext
	switch out.Kind {
	case "tee":
		slavesURL, terr := buildTeeSlavesURL(out.Targets)
		if terr != nil {
			return nil, fmt.Errorf("output %q: %w", out.ID, terr)
		}
		m, oerr := av.OpenTeeOutput(slavesURL)
		if oerr != nil {
			return nil, fmt.Errorf("open tee output %q: %w", out.ID, oerr)
		}
		muxer = m
	default:
		openURL := out.URL
		if out.SegmentOnMetadata != "" {
			openURL = fmt.Sprintf(out.URL, 0)
		}
		m, oerr := av.OpenOutputWithFormat(openURL, out.Format)
		if oerr != nil {
			return nil, fmt.Errorf("open output %q: %w", openURL, oerr)
		}
		muxer = m
	}

	rescales := make([]*sinkRescale, len(node.Inbound))

	// streamsByType records, for each media-type letter ("v"/"a"/"s"/
	// "d"), the absolute output stream indices of the streams of that
	// type in the order they were added to the muxer. This is the same
	// counting convention FFmpeg's `check_stream_specifier` uses for
	// `s:<type>:<idx>`, so `Output.Streams[k] = {Type:"a", Index:1}`
	// resolves to the second audio stream of this output. Built up
	// inside the AddStream loop below.
	streamsByType := map[string][]int{}
	typeLetterFor := func(t graph.PortType) string {
		switch t {
		case "video":
			return "v"
		case "audio":
			return "a"
		case "subtitle":
			return "s"
		case "data":
			return "d"
		}
		return ""
	}

	// codecTagFor returns the configured FourCC codec_tag override for
	// the given edge's stream kind, or "" if none is configured.
	codecTagFor := func(t graph.PortType) string {
		switch t {
		case "video":
			return out.CodecTagVideo
		case "audio":
			return out.CodecTagAudio
		case "subtitle":
			return out.CodecTagSubtitle
		}
		return ""
	}

	// Add one stream per inbound edge. Encoder predecessors register
	// the stream from the encoder context; stream-copy predecessors
	// copy the input codecpar directly so the muxer never sees an
	// encoder for that stream. Topological order guarantees both kinds
	// of predecessor are already prepared.
	for i, e := range node.Inbound {
		from := e.From
		var outIdx int
		switch from.Kind {
		case graph.KindEncoder:
			enc := r.encoders[from.ID]
			if enc == nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q: inbound from %q has no encoder", node.ID, from.ID)
			}
			idx, err := muxer.AddStream(enc)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add stream: %w", node.ID, err)
			}
			outIdx = idx
			// Capture the encoder's time_base. AddStream copies it onto
			// the output stream, but some muxers (notably MP4) overwrite
			// the stream's time_base in WriteHeader, leaving encoder
			// packets (whose PTS is in encoder TB) misinterpreted by the
			// muxer in the new TB. We rescale per-packet to compensate.
			rescales[i] = &sinkRescale{srcTB: enc.TimeBase(), dstTB: muxer.StreamTimeBase(outIdx)}
		case graph.KindCopy:
			srcInput, srcIdx, srcTB, err := r.copySourceFor(from)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q copy from %q: %w", node.ID, from.ID, err)
			}
			idx, err := muxer.AddStreamFromInput(srcInput, srcIdx)
			if err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q add copy stream: %w", node.ID, err)
			}
			outIdx = idx
			rescales[i] = &sinkRescale{srcTB: srcTB, dstTB: muxer.StreamTimeBase(outIdx)}
		default:
			muxer.Abort()
			return nil, fmt.Errorf("sink %q: inbound from %q (kind=%v) is not an encoder or copy node", node.ID, from.ID, from.Kind)
		}

		// Apply optional codec_tag override (e.g. -tag:v hvc1).
		if tag := codecTagFor(e.Type); tag != "" {
			if err := muxer.SetStreamCodecTag(outIdx, tag); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q set codec_tag for %s stream: %w", node.ID, e.Type, err)
			}
		}

		// Record this stream's absolute index under its media-type
		// letter for FFmpeg-style `s:<type>:<idx>` resolution below.
		if letter := typeLetterFor(e.Type); letter != "" {
			streamsByType[letter] = append(streamsByType[letter], outIdx)
		}
	}

	// Apply per-stream metadata + disposition (`Output.Streams`).
	// Mirrors FFmpeg's `-metadata:s:<type>:<idx>` and
	// `-disposition:s:<type>:<idx>`. Resolution counts streams of the
	// requested media type in muxer-add order, so a job with one video
	// + two audio streams resolves `{Type:"a", Index:1}` to the
	// second audio AVStream regardless of the order video / audio
	// edges were declared on the sink.
	for j, ss := range out.Streams {
		idxs, ok := streamsByType[ss.Type]
		if !ok || ss.Index < 0 || ss.Index >= len(idxs) {
			muxer.Abort()
			return nil, fmt.Errorf("sink %q streams[%d]: no stream matches %s:%d (have %d %s stream(s))",
				node.ID, j, ss.Type, ss.Index, len(idxs), ss.Type)
		}
		streamIdx := idxs[ss.Index]
		if len(ss.Metadata) > 0 {
			if err := muxer.SetStreamMetadata(streamIdx, ss.Metadata); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set metadata: %w", node.ID, j, err)
			}
		}
		if ss.Disposition != "" {
			if err := muxer.SetStreamDisposition(streamIdx, ss.Disposition); err != nil {
				muxer.Abort()
				return nil, fmt.Errorf("sink %q streams[%d] set disposition: %w", node.ID, j, err)
			}
		}
	}

	// Per-stream bitstream-filter chains (Output.BSFVideo /
	// BSFAudio / BSFSubtitle, FFmpeg `-bsf:v` / `-bsf:a` / `-bsf:s`).
	// Each value is the FFmpeg chain spec `f1[=k=v[:k=v]][,f2]` parsed
	// by libavcodec's av_bsf_list_parse_str. Attached after stream
	// creation and before WriteHeader so the chain's par_out replaces
	// the muxer's stream codecpar — exactly the order
	// fftools/ffmpeg_mux.c::bsf_init follows. Stored per inbound
	// channel for processOne to drive packets through.
	bsfSpecFor := func(t graph.PortType) string {
		switch t {
		case graph.PortVideo:
			return out.BSFVideo
		case graph.PortAudio:
			return out.BSFAudio
		case graph.PortSubtitle:
			return out.BSFSubtitle
		}
		return ""
	}
	streamBSF := make([]*av.BitstreamFilter, len(node.Inbound))
	for i, e := range node.Inbound {
		spec := bsfSpecFor(e.Type)
		if spec == "" {
			continue
		}
		bsf, err := muxer.AttachStreamBSF(i, spec)
		if err != nil {
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			muxer.Abort()
			return nil, fmt.Errorf("sink %q attach %s bsf chain %q: %w", node.ID, e.Type, spec, err)
		}
		streamBSF[i] = bsf
	}

	// Container metadata + chapters. Resolution rules:
	//   - Output.Metadata, when non-nil, fully replaces any
	//     metadata mapped from inputs (mirrors FFmpeg's behaviour
	//     when both `-metadata` and `-map_metadata` are given:
	//     `-metadata` is applied last and wins).
	//   - When Output.Metadata is nil, every input with
	//     MapMetadata=true contributes its container-level
	//     metadata in declaration order; the last writer wins
	//     per key.
	//   - Chapters follow the same precedence: an explicit
	//     Output.Chapters wins; otherwise the first input with
	//     MapChapters=true contributes its chapter table.
	if err := r.applyOutputMetadata(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply metadata: %w", node.ID, err)
	}
	if err := r.applyOutputChapters(muxer, out); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q apply chapters: %w", node.ID, err)
	}

	// Per-stream color metadata + HDR10 (Output.Color / Output.HDR,
	// FFmpeg `-color_*` / `-mastering_display_metadata` /
	// `-content_light_level`). Applied to every inbound video edge's
	// stream codecpar (and codecpar.coded_side_data for HDR side
	// data) before WriteHeader so the muxer writes the corresponding
	// container boxes / SEI passthrough. Audio + subtitle edges are
	// skipped — color metadata is meaningless for them.
	if out.Color != nil || out.HDR != nil {
		for i, e := range node.Inbound {
			if e.Type != graph.PortVideo {
				continue
			}
			if out.Color != nil {
				if err := muxer.SetStreamColor(i, av.ColorParams{
					Range:          out.Color.Range,
					Primaries:      out.Color.Primaries,
					Transfer:       out.Color.Transfer,
					Space:          out.Color.Space,
					ChromaLocation: out.Color.ChromaLocation,
				}); err != nil {
					for _, b := range streamBSF {
						if b != nil {
							_ = b.Close()
						}
					}
					muxer.Abort()
					return nil, fmt.Errorf("sink %q set color: %w", node.ID, err)
				}
			}
			if out.HDR != nil {
				if md := out.HDR.MasteringDisplay; md != nil {
					hasPrim := md.DisplayPrimariesRX != 0 || md.WhitePointX != 0
					hasLum := md.MaxLuminance != 0
					if hasPrim || hasLum {
						if err := muxer.SetStreamMasteringDisplay(i, av.MasteringDisplay{
							HasPrimaries: hasPrim,
							DisplayPrim: [6]int{
								md.DisplayPrimariesRX, md.DisplayPrimariesRY,
								md.DisplayPrimariesGX, md.DisplayPrimariesGY,
								md.DisplayPrimariesBX, md.DisplayPrimariesBY,
							},
							WhitePoint:   [2]int{md.WhitePointX, md.WhitePointY},
							HasLuminance: hasLum,
							MinLuminance: md.MinLuminance,
							MaxLuminance: md.MaxLuminance,
						}); err != nil {
							for _, b := range streamBSF {
								if b != nil {
									_ = b.Close()
								}
							}
							muxer.Abort()
							return nil, fmt.Errorf("sink %q set mastering display: %w", node.ID, err)
						}
					}
				}
				if cll := out.HDR.ContentLightLevel; cll != nil && (cll.MaxCLL != 0 || cll.MaxFALL != 0) {
					if err := muxer.SetStreamContentLightLevel(i, av.ContentLightLevel{
						MaxCLL:  cll.MaxCLL,
						MaxFALL: cll.MaxFALL,
					}); err != nil {
						for _, b := range streamBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						muxer.Abort()
						return nil, fmt.Errorf("sink %q set content light level: %w", node.ID, err)
					}
				}
				if dv := out.HDR.DoVi; dv != nil && dv.Profile != 0 {
					rpu := true
					if dv.RPUPresent != nil {
						rpu = *dv.RPUPresent
					}
					bl := true
					if dv.BLPresent != nil {
						bl = *dv.BLPresent
					}
					if err := muxer.SetStreamDoViConfig(i, av.DoViConfig{
						Profile:           dv.Profile,
						Level:             dv.Level,
						RPUPresent:        rpu,
						ELPresent:         dv.ELPresent,
						BLPresent:         bl,
						BLCompatibilityID: dv.BLCompatibilityID,
					}); err != nil {
						for _, b := range streamBSF {
							if b != nil {
								_ = b.Close()
							}
						}
						muxer.Abort()
						return nil, fmt.Errorf("sink %q set dovi config: %w", node.ID, err)
					}
				}
			}
		}
	}

	// Wave 6 #31: muxed file attachments (matroska / mkv / webm only).
	// Files are read once and copied into the new stream's
	// codecpar->extradata. Mirrors fftools/ffmpeg_mux_init.c
	// of_add_attachments.
	for ai, att := range out.Attachments {
		cleanPath := filepath.Clean(att.Path)
		if strings.Contains(cleanPath, "..") {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: path traversal in %q", node.ID, ai, att.Path)
		}
		data, err := os.ReadFile(cleanPath)
		if err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: read %s: %w", node.ID, ai, cleanPath, err)
		}
		filename := att.Filename
		if filename == "" {
			filename = filepath.Base(cleanPath)
		}
		if _, err := muxer.AddAttachment(data, filename, att.MimeType); err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q attachments[%d]: %w", node.ID, ai, err)
		}
	}

	// Wave 11 #64: cover art embedded as AV_DISPOSITION_ATTACHED_PIC.
	// The stream must be added before WriteHeader; the returned packet is
	// written immediately after WriteHeader below.
	var coverPkt *av.Packet
	if out.CoverArt != "" {
		cleanCA := filepath.Clean(out.CoverArt)
		if strings.Contains(cleanCA, "..") {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q cover_art: path traversal in %q", node.ID, out.CoverArt)
		}
		_, pkt, err := muxer.AddCoverArt(cleanCA)
		if err != nil {
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q cover_art: %w", node.ID, err)
		}
		coverPkt = pkt
	}

	if err := muxer.WriteHeaderWithOptions(buildMuxerOptions(out)); err != nil {
		muxer.Abort()
		return nil, fmt.Errorf("sink %q write header: %w", node.ID, err)
	}

	// Wave 11 #64: write the cover art packet immediately after WriteHeader.
	// The stream was registered above; the single-frame packet must be
	// written before regular content so interleaved muxers see it early.
	if coverPkt != nil {
		if err := muxer.WritePacket(coverPkt); err != nil {
			_ = coverPkt.Close()
			muxer.Abort()
			for _, b := range streamBSF {
				if b != nil {
					_ = b.Close()
				}
			}
			return nil, fmt.Errorf("sink %q write cover art: %w", node.ID, err)
		}
		_ = coverPkt.Close()
	}

	// Some muxers adjust stream time_base in WriteHeader; refresh the
	// post-header value so per-packet rescaling targets the actual
	// container timebase. BSF-attached streams keep the pre-header
	// rescale target (= bsf.time_base_in), since the BSF chain was
	// initialised against that TB and av_interleaved_write_frame
	// rescales the BSF's output PTS to the post-header stream TB.
	for i, rs := range rescales {
		if rs == nil {
			continue
		}
		if i < len(streamBSF) && streamBSF[i] != nil {
			continue
		}
		rs.dstTB = muxer.StreamTimeBase(i)
	}

	sr := &sinkResources{
		muxer:         muxer,
		cfg:           *out,
		streamRescale: rescales,
		streamBSF:     streamBSF,
		timing:        outTiming,
		copyTS:        r.cfg != nil && r.cfg.CopyTS,
		maxFileSize:   out.MaxFileSize,
		shortest:      out.Shortest || r.hasCopyTrimPath(node),
		shortestPTSus: noLimitUS,
		preroll:       r.buildPreroll(out, rescales),
	}
	// Pre-register the pending-cut flag for metadata-driven segmentation.
	// This is done before goroutines start so r.segmentCuts needs no mutex.
	if out.SegmentOnMetadata != "" {
		g := newCutGate()
		sr.pendingCut = g
		r.segmentCuts[out.SegmentOnMetadata] = append(r.segmentCuts[out.SegmentOnMetadata], g)
	}
	return sr, nil
}

// copySourceFor resolves a stream-copy node back to the input it reads
// from, returning the InputFormatContext, the demuxer stream index, and
// the input stream's time_base. The copy node is required to have
// exactly one inbound edge from a KindSource.

// buildPreroll constructs the Phase 7 OutputPreroll for `out` when
// real-time mode is enabled. Returns nil when prerolling is disabled
// (realtime off or prebuffer_duration_seconds <= 0). Per-output
// overrides on `out.Realtime` take precedence over the global defaults.
// Default fill target is 4 s (1 s for audio-only outputs); hard cap
// defaults to 2 × target.
//
// rescales carries the per-channel time-base info for the packets that
// will arrive at the preroll during Phase A. Phase A packets are
// unrescaled encoder output, so we must use rescales[i].srcTB (the
// encoder's output time_base) rather than the post-WriteHeader mux
// stream time_base. Using the mux TB on encoder-TB PTS would produce
// a span calculation that is off by a factor of ~(muxTB.den / encTB.den)
// — typically hundreds of times too small — causing the preroll to
// never reach its fill target.
func (r *graphRunner) buildPreroll(out *Output, rescales []*sinkRescale) *OutputBuffer {
	if r.cfg == nil || !r.cfg.GlobalOptions.Realtime {
		return nil
	}
	target := r.cfg.GlobalOptions.PrebufferDurationSeconds
	maxDur := r.cfg.GlobalOptions.PrebufferMaxSeconds
	if out.Realtime != nil {
		if out.Realtime.PrebufferDurationSeconds > 0 {
			target = out.Realtime.PrebufferDurationSeconds
		}
		if out.Realtime.PrebufferMaxSeconds > 0 {
			maxDur = out.Realtime.PrebufferMaxSeconds
		}
	}
	if target == 0 {
		if out.DisableVideo && !out.DisableAudio {
			target = 1.0
		} else {
			target = 4.0
		}
	}
	if target <= 0 {
		return nil
	}
	if maxDur <= 0 {
		maxDur = 2 * target
	}
	// Use the encoder output (source) time bases, not the post-WriteHeader
	// mux stream time bases. Packets in Phase A are unrescaled.
	const avTimeBase = 1_000_000 // AV_TIME_BASE fallback
	tbs := make([][2]int, len(rescales))
	for i, rs := range rescales {
		if rs != nil && rs.srcTB[0] > 0 && rs.srcTB[1] > 0 {
			tbs[i] = rs.srcTB
		} else {
			tbs[i] = [2]int{1, avTimeBase}
		}
	}
	log.Printf("preroll %q: target=%.1fs max=%.1fs tbs=%v", out.ID, target, maxDur, tbs)
	pre := NewOutputBuffer(out.ID,
		time.Duration(target*float64(time.Second)),
		time.Duration(maxDur*float64(time.Second)),
		tbs,
	)
	if r.pipe != nil {
		r.pipe.mu.Lock()
		ready := r.pipe.ready
		r.pipe.mu.Unlock()
		if ready != nil {
			ready.Add(pre)
		}
	}
	return pre
}

// hasCopyTrimPath reports whether any inbound edge of sinkNode follows a
// stream-copy path (KindCopy → KindSource) where the source input has
// input-side timing options (ss / t / to). When true, the caller should
// implicitly enable -shortest: keyframe-aligned seeking causes the video
// track to end slightly before the audio track, and -shortest truncates
// the longer stream so both tracks share the same container duration.
func (r *graphRunner) hasCopyTrimPath(sinkNode *graph.Node) bool {
	for _, e := range sinkNode.Inbound {
		if e.From == nil || e.From.Kind != graph.KindCopy {
			continue
		}
		if len(e.From.Inbound) == 0 || e.From.Inbound[0].From == nil {
			continue
		}
		srcNode := e.From.Inbound[0].From
		src, ok := r.sources[srcNode.ID]
		if !ok {
			continue
		}
		opts := src.cfg.Options
		if opts["ss"] != nil || opts["t"] != nil || opts["to"] != nil {
			return true
		}
	}
	return false
}
