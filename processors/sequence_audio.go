// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"github.com/MediaMolder/MediaMolder/acrossfade"
	"github.com/MediaMolder/MediaMolder/av"
)

// This file adds the audio half of the sequence_editor: a per-source audio
// reader that decodes and resamples a clip's audio to the sequence's working
// format (planar float / fltp), and the per-step mixer that produces the
// sequence's audio stream — including the crossfade across a transition window,
// auto-coupled to the clip's video transition. See sequence_editor.go for the
// video half and docs/architecture/transitions.md for the design.

// Output stream indices, matching the order of OutputStreams(): video is always
// stream 0; audio (when enabled) is stream 1.
const (
	seqStreamVideo = 0
	seqStreamAudio = 1
)

// seqActiveLayer represents one contributing source (clip) for the current
// output time t, with the computed opacity used for video blending. It is
// hoisted to package scope (rather than declared inside Run) so the audio mixer
// can consume the same layer set the video compositor computed.
type seqActiveLayer struct {
	trackIdx int
	trackID  string
	clipIdx  int
	clip     *seqClip
	srcT     float64
	opacity  float64
}

// SupportedAudioTransitions returns the sorted set of audio crossfade curve
// names the sequence_editor accepts (the registered acrossfade curves). Exposed
// so the GUI's audio crossfade picker stays in lockstep with what the engine
// implements, mirroring SupportedTransitions for video.
func SupportedAudioTransitions() []string {
	return acrossfade.Names()
}

// audioEnabled reports whether the sequence emits an audio stream. Audio is
// opt-in: a format without a positive sample_rate stays video-only (zero
// behaviour change for existing jobs).
func (se *SequenceEditor) audioEnabled() bool {
	return se.format.SampleRate > 0 && se.format.Channels > 0
}

// OutputStreams reports every stream the sequence emits: always the composited
// video stream, plus the mixed audio stream when audio is enabled. Implements
// processors.MultiStreamSource. Stream 0 is video, stream 1 (if present) audio.
func (se *SequenceEditor) OutputStreams() []av.StreamInfo {
	vi, err := se.OutputStreamInfo()
	if err != nil {
		return nil
	}
	streams := []av.StreamInfo{vi}
	if se.audioEnabled() {
		streams = append(streams, av.StreamInfo{
			Type:       av.MediaTypeAudio,
			SampleRate: se.format.SampleRate,
			SampleFmt:  av.SampleFmtFLTP,
			Channels:   se.format.Channels,
			TimeBase:   [2]int{1, se.format.SampleRate},
		})
	}
	return streams
}

// renderAudioStep produces the n-sample audio frame for one output step covering
// sequence time [t, t+dt). It mirrors the video compositor's layer logic: a
// single covering clip yields that clip's audio; a declared transition window
// crossfades the outgoing and incoming clips; an uncovered gap yields silence.
// The returned frame is fltp at the sequence sample rate/channels with n
// samples; the caller stamps its PTS. Returns nil only on allocation failure.
func (se *SequenceEditor) renderAudioStep(layers []seqActiveLayer, transType string, transClip *seqClip, dStart, dDur, t, dt float64, n int) *av.Frame {
	if n <= 0 {
		n = 1
	}
	crossfade := len(layers) >= 2 && transType != "" &&
		transClip != nil && transClip.Transition != nil && !transClip.Transition.AudioOff

	if crossfade {
		outL := layers[0]
		inL := layers[len(layers)-1]
		aOut := se.fetchAudioSamples(outL.clip.URL, outL.srcT, n)
		aIn := se.fetchAudioSamples(inL.clip.URL, inL.srcT, n)

		// Audio crossfade window: the tail aDur seconds of the (video)
		// transition window, so a shorter AudioDuration ramps faster while
		// still finishing exactly when the incoming clip takes over the
		// picture. aDur == dDur (the default) ramps across the whole window.
		aDur := dDur
		if ad := transClip.Transition.AudioDuration; ad > 0 && ad < dDur {
			aDur = ad
		}
		aStart := dStart + dDur - aDur
		p0 := clampUnit((t - aStart) / aDur)
		p1 := clampUnit((t + dt - aStart) / aDur)

		out := se.silenceFrame(n)
		if out == nil {
			closeFrame(aOut)
			closeFrame(aIn)
			return nil
		}
		curve := transClip.Transition.AudioCurve
		if curve == "" {
			curve = acrossfade.DefaultCurve
		}
		acrossfade.Mix(curve, out, aOut, aIn, p0, p1)
		closeFrame(aOut)
		closeFrame(aIn)
		return out
	}

	if len(layers) >= 1 {
		// Single clip, or an undeclared overlap: follow the dominant (first /
		// outgoing) layer, matching the video compositor's choice.
		if f := se.fetchAudioSamples(layers[0].clip.URL, layers[0].srcT, n); f != nil {
			return f
		}
	}
	return se.silenceFrame(n)
}

// fetchAudioSamples returns n fltp samples for the given source URL at source
// time srcSec, or a silence frame when the source has no audio / cannot be read.
func (se *SequenceEditor) fetchAudioSamples(url string, srcSec float64, n int) *av.Frame {
	ar := se.getOrOpenAudioReader(url)
	if ar == nil {
		return se.silenceFrame(n)
	}
	f, err := ar.getSamples(srcSec, n)
	if err != nil || f == nil {
		return se.silenceFrame(n)
	}
	return f
}

// silenceFrame allocates a zeroed fltp frame of n samples at the sequence audio
// format.
func (se *SequenceEditor) silenceFrame(n int) *av.Frame {
	if n <= 0 {
		n = 1
	}
	f, err := av.NewAudioFrame(av.SampleFmtFLTP, se.format.Channels, n, se.format.SampleRate)
	if err != nil {
		return nil
	}
	for c := 0; c < se.format.Channels; c++ {
		p := f.SamplePlaneF32(c)
		for i := range p {
			p[i] = 0
		}
	}
	return f
}

func (se *SequenceEditor) getOrOpenAudioReader(url string) *audioReader {
	if se.audioReaders == nil {
		se.audioReaders = make(map[string]*audioReader)
	}
	if r, ok := se.audioReaders[url]; ok {
		return r // may be a silent (no-audio) reader; still valid
	}
	r := openAudioReader(url, se.format.SampleRate, se.format.Channels)
	se.audioReaders[url] = r
	return r
}

// audioReader decodes one source file's audio independently of the video
// clipReader (its own demuxer + decoder, so the two never contend for the shared
// demuxer read position). Decoded audio is resampled to the sequence's fltp
// format and buffered in a per-channel sample FIFO that getSamples drains.
type audioReader struct {
	url    string
	demux  *av.InputFormatContext
	dec    *av.DecoderContext
	pkt    *av.Packet
	audIdx int // source audio stream index, -1 if the file has no audio

	resampler *av.Resampler
	outRate   int
	outCh     int

	buf  [][]float32 // per-channel sample FIFO (front = oldest, see head)
	head int         // read offset into buf (samples already consumed)

	servedSamples int64 // absolute source sample index served/discarded so far
	eof           bool
}

// openAudioReader opens url for audio decoding. A file with no audio stream
// yields a reader with audIdx < 0 that getSamples reports as silent (the caller
// substitutes silence). Returns nil only if the file cannot be opened at all.
func openAudioReader(url string, outRate, outCh int) *audioReader {
	demux, err := av.OpenInput(url, nil)
	if err != nil {
		return nil
	}
	audIdx := -1
	for i := 0; i < demux.NumStreams(); i++ {
		if info, e := demux.StreamInfo(i); e == nil && info.Type == av.MediaTypeAudio {
			audIdx = i
			break
		}
	}
	ar := &audioReader{url: url, demux: demux, audIdx: audIdx, outRate: outRate, outCh: outCh}
	if audIdx < 0 {
		demux.Close()
		ar.demux = nil
		return ar // silent reader
	}
	dec, err := av.OpenDecoder(demux, audIdx)
	if err != nil {
		demux.Close()
		ar.demux, ar.audIdx = nil, -1
		return ar
	}
	pkt, err := av.AllocPacket()
	if err != nil {
		dec.Close()
		demux.Close()
		ar.demux, ar.dec, ar.audIdx = nil, nil, -1
		return ar
	}
	ar.dec, ar.pkt = dec, pkt
	return ar
}

func (ar *audioReader) close() {
	if ar == nil {
		return
	}
	if ar.resampler != nil {
		ar.resampler.Close()
		ar.resampler = nil
	}
	if ar.pkt != nil {
		ar.pkt.Close()
		ar.pkt = nil
	}
	if ar.dec != nil {
		ar.dec.Close()
		ar.dec = nil
	}
	if ar.demux != nil {
		ar.demux.Close()
		ar.demux = nil
	}
	ar.buf = nil
}

// getSamples returns n fltp samples beginning at source time srcSec, decoding
// forward as needed. Returns nil (caller serves silence) for a file with no
// audio. Source positioning is forward-only: clips play at 1x from source_in,
// so the first call discards the prefix and subsequent calls continue.
func (ar *audioReader) getSamples(srcSec float64, n int) (*av.Frame, error) {
	if ar == nil || ar.audIdx < 0 {
		return nil, nil
	}
	want := int64(srcSec*float64(ar.outRate) + 0.5)
	if want > ar.servedSamples {
		ar.discard(want - ar.servedSamples)
		ar.servedSamples = want
	}
	// A backward jump (want < servedSamples) would need a demuxer seek; in a
	// forward 1x timeline it does not occur, so we serve from the current
	// position rather than rewinding.
	f := ar.pop(n)
	ar.servedSamples += int64(n)
	return f, nil
}

func (ar *audioReader) avail() int {
	if ar.buf == nil {
		return 0
	}
	return len(ar.buf[0]) - ar.head
}

// fill pumps the demuxer until the FIFO holds at least min samples or EOF.
func (ar *audioReader) fill(min int) {
	for ar.avail() < min && !ar.eof {
		ar.pump()
	}
}

// pump reads one packet and feeds the audio decoder, appending any decoded +
// resampled samples to the FIFO. Non-audio packets are skipped. On EOF it
// drains the resampler and marks the reader exhausted.
func (ar *audioReader) pump() {
	ar.pkt.Unref()
	if err := ar.demux.ReadPacket(ar.pkt); err != nil {
		if av.IsEOF(err) {
			ar.drainResampler()
		}
		ar.eof = true
		return
	}
	if ar.pkt.StreamIndex() != ar.audIdx {
		return
	}
	if err := ar.dec.SendPacket(ar.pkt); err != nil && !av.IsEAgain(err) {
		ar.eof = true
		return
	}
	for {
		inF, err := av.AllocFrame()
		if err != nil {
			return
		}
		if err := ar.dec.ReceiveFrame(inF); err != nil {
			inF.Close()
			return
		}
		ar.ingest(inF)
		inF.Close()
	}
}

// ingest resamples a decoded source frame to the sequence fltp format and
// appends its samples to the FIFO, building the resampler lazily from the first
// frame's actual format.
func (ar *audioReader) ingest(inF *av.Frame) {
	if ar.resampler == nil {
		rs, err := av.NewResampler(av.ResamplerOptions{
			InSampleRate:  inF.SampleRate(),
			InSampleFmt:   inF.SampleFmt(),
			InChannels:    inF.Channels(),
			OutSampleRate: ar.outRate,
			OutSampleFmt:  av.SampleFmtFLTP,
			OutChannels:   ar.outCh,
		})
		if err != nil {
			return
		}
		ar.resampler = rs
	}
	out, err := av.AllocFrame()
	if err != nil {
		return
	}
	defer out.Close()
	// swr_convert_frame allocates the output buffer itself but requires the
	// destination's format/layout/rate to be set first.
	out.SetAudioParams(av.SampleFmtFLTP, ar.outCh, ar.outRate)
	if err := ar.resampler.ConvertFrame(out, inF); err != nil {
		return
	}
	ar.appendFrame(out)
}

// drainResampler flushes any samples buffered inside the resampler at EOS.
func (ar *audioReader) drainResampler() {
	if ar.resampler == nil {
		return
	}
	for {
		out, err := av.AllocFrame()
		if err != nil {
			return
		}
		out.SetAudioParams(av.SampleFmtFLTP, ar.outCh, ar.outRate)
		if err := ar.resampler.Flush(out); err != nil || out.NbSamples() == 0 {
			out.Close()
			return
		}
		ar.appendFrame(out)
		out.Close()
	}
}

func (ar *audioReader) appendFrame(out *av.Frame) {
	m := out.NbSamples()
	if m <= 0 {
		return
	}
	if ar.buf == nil {
		ar.buf = make([][]float32, ar.outCh)
	}
	for c := 0; c < ar.outCh; c++ {
		src := out.SamplePlaneF32(c)
		if src == nil {
			continue
		}
		ar.buf[c] = append(ar.buf[c], src[:m]...)
	}
}

// pop removes and returns n samples from the FIFO as a new fltp frame, padding
// with silence when the source is exhausted.
func (ar *audioReader) pop(n int) *av.Frame {
	ar.fill(n)
	out, err := av.NewAudioFrame(av.SampleFmtFLTP, ar.outCh, n, ar.outRate)
	if err != nil {
		return nil
	}
	got := ar.avail()
	if got > n {
		got = n
	}
	for c := 0; c < ar.outCh; c++ {
		dst := out.SamplePlaneF32(c)
		if dst == nil {
			continue
		}
		if ar.buf != nil && got > 0 {
			copy(dst[:got], ar.buf[c][ar.head:ar.head+got])
		}
		for i := got; i < n; i++ {
			dst[i] = 0
		}
	}
	ar.head += got
	ar.compact()
	return out
}

// discard drops m samples from the FIFO (used to skip to source_in).
func (ar *audioReader) discard(m int64) {
	for m > 0 {
		chunk := m
		if chunk > 1<<16 {
			chunk = 1 << 16
		}
		ar.fill(int(chunk))
		got := int64(ar.avail())
		if got > m {
			got = m
		}
		if got == 0 {
			return // EOF
		}
		ar.head += int(got)
		m -= got
		ar.compact()
	}
}

// compact reclaims the consumed front of the FIFO once the read offset grows
// large, so a long clip's buffer does not grow without bound.
func (ar *audioReader) compact() {
	if ar.head < 1<<16 || ar.buf == nil {
		return
	}
	for c := range ar.buf {
		rem := len(ar.buf[c]) - ar.head
		copy(ar.buf[c], ar.buf[c][ar.head:])
		ar.buf[c] = ar.buf[c][:rem]
	}
	ar.head = 0
}

func clampUnit(x float64) float64 {
	if x < 0 {
		return 0
	}
	if x > 1 {
		return 1
	}
	return x
}

func closeFrame(f *av.Frame) {
	if f != nil {
		f.Close()
	}
}
