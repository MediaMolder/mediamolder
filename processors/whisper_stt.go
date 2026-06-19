// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

package processors

// WhisperSTT is a go_processor that transcribes an audio stream to text using
// whisper.cpp (local, offline). It accumulates resampled 16 kHz mono audio
// during Process and runs a single transcription pass in Close — whisper.cpp
// windows the buffer into 30 s chunks internally. Audio frames pass through
// unchanged so the same stream can still be encoded/muxed downstream.
//
// Results are emitted two ways: one Metadata event per segment on the pipeline
// event bus (with a human LogMessage), and, when "output_file" is set, a sidecar
// transcript written once in Close (srt | vtt | json | txt; see whisper_format.go).
//
// Because all inference happens in Close() after the per-frame progress bar has
// reached 100 %, the processor implements AsyncMetadataProcessor and posts
// progress log updates so the post-frames phase is not silent in the GUI/CLI.
//
// The processor is registered as "whisper_stt"; it is only compiled with the
// "with_whisper" build tag.
//
// Required params:
//
//	"model" — path to a ggml/gguf Whisper model (required)
//
// Optional params:
//
//	"language"        — source language hint, default "auto" (detect)
//	"task"            — "transcribe" (default) or "translate" (to English)
//	"beam_size"       — 0/1 greedy (default), >1 beam search
//	"word_timestamps" — request token-level timestamps, default false
//	"threads"         — inference threads, default runtime.NumCPU()
//	"initial_prompt"  — context/biasing prompt
//	"output_file"     — sidecar transcript path (empty = events only)
//	"output_format"   — "srt" (default) | "vtt" | "json" | "txt"
import (
	"context"
	"fmt"
	"os"
	"runtime"

	"github.com/MediaMolder/MediaMolder/av"
)

type WhisperSTT struct {
	model     *av.WhisperModel
	resampler *av.Resampler
	samples   []float32

	opts         av.WhisperOptions
	outputFile   string
	outputFormat string

	emit MetadataEmitter
}

func (p *WhisperSTT) Init(params map[string]any) error {
	modelPath, _ := params["model"].(string)
	if modelPath == "" {
		return fmt.Errorf("whisper_stt: \"model\" param is required (path to a ggml/gguf model)")
	}
	if _, err := os.Stat(modelPath); err != nil {
		return fmt.Errorf("whisper_stt: model file: %w", err)
	}

	// Defaults.
	p.opts.Language = "auto"
	p.outputFormat = "srt"

	if v, ok := params["language"].(string); ok && v != "" {
		p.opts.Language = v
	}
	if v, ok := params["task"].(string); ok {
		switch v {
		case "", "transcribe":
		case "translate":
			p.opts.Translate = true
		default:
			return fmt.Errorf("whisper_stt: task %q is not valid (want transcribe or translate)", v)
		}
	}
	if n, ok := numToInt(params["beam_size"]); ok {
		p.opts.BeamSize = n
	}
	if b, ok := params["word_timestamps"].(bool); ok {
		p.opts.WordTimestamps = b
	}
	if n, ok := numToInt(params["threads"]); ok && n > 0 {
		p.opts.Threads = n
	}
	if v, ok := params["initial_prompt"].(string); ok {
		p.opts.InitialPrompt = v
	}
	if v, ok := params["output_file"].(string); ok {
		p.outputFile = v
	}
	if v, ok := params["output_format"].(string); ok && v != "" {
		switch v {
		case "srt", "vtt", "json", "txt":
			p.outputFormat = v
		default:
			return fmt.Errorf("whisper_stt: output_format %q is not valid (want srt, vtt, json, txt)", v)
		}
	}
	if p.opts.Threads == 0 {
		p.opts.Threads = runtime.NumCPU()
	}
	// Validate the sidecar path up front so a bad output_file fails Init rather
	// than wasting an entire (possibly many-minute) transcription before Close.
	if p.outputFile != "" {
		if _, err := sanitizeOutputPath(p.outputFile); err != nil {
			return err
		}
	}

	model, err := av.NewWhisperModel(modelPath)
	if err != nil {
		return fmt.Errorf("whisper_stt: %w", err)
	}
	p.model = model
	return nil
}

// SetMetadataEmitter implements processors.AsyncMetadataProcessor. The engine
// installs the emitter once after Init; it carries transcription progress and
// the per-segment results emitted from Close().
func (p *WhisperSTT) SetMetadataEmitter(emit MetadataEmitter) {
	p.emit = emit
}

func (p *WhisperSTT) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if frame == nil || ctx.MediaType != av.MediaTypeAudio {
		return frame, nil, nil
	}
	if frame.SampleRate() <= 0 || frame.Channels() <= 0 {
		return frame, nil, nil // not a usable audio frame yet
	}
	if err := p.accumulate(frame); err != nil {
		return nil, nil, err
	}
	return frame, nil, nil
}

// accumulate resamples frame to 16 kHz mono float32 and appends the samples.
// It resamples from a ref-counted clone so canonicalizing the clone's channel
// layout for libswresample never mutates the frame we pass through downstream,
// and it rebuilds the resampler if the input's rate/format/layout changes
// mid-stream (e.g. concatenated sources).
func (p *WhisperSTT) accumulate(frame *av.Frame) error {
	in, err := frame.Clone()
	if err != nil {
		return fmt.Errorf("whisper_stt: clone: %w", err)
	}
	defer in.Close()
	// Canonicalize the clone's channel layout: some decoders emit an
	// "unspecified" layout (channel count set, no order/mask) which
	// swr_convert_frame rejects against the resampler's default-mask layout
	// with AVERROR_INPUT_CHANGED. Done on the clone, not the pass-through frame.
	in.SetAudioParams(in.SampleFmt(), in.Channels(), in.SampleRate())

	if p.resampler == nil {
		if err := p.buildResampler(in); err != nil {
			return err
		}
	}
	if err := p.resampleAppend(in); err != nil {
		// The input parameters changed since the resampler was built: rebuild
		// from the current frame and retry once.
		p.resampler.Close()
		p.resampler = nil
		if err := p.buildResampler(in); err != nil {
			return err
		}
		if err := p.resampleAppend(in); err != nil {
			return fmt.Errorf("whisper_stt: resample: %w", err)
		}
	}
	return nil
}

func (p *WhisperSTT) buildResampler(in *av.Frame) error {
	r, err := av.NewResampler(av.ResamplerOptions{
		InSampleRate:  in.SampleRate(),
		InSampleFmt:   in.SampleFmt(),
		InChannels:    in.Channels(),
		OutSampleRate: av.WhisperSampleRate,
		OutSampleFmt:  av.SampleFmtFLTP,
		OutChannels:   1,
	})
	if err != nil {
		return fmt.Errorf("whisper_stt: resampler: %w", err)
	}
	p.resampler = r
	return nil
}

func (p *WhisperSTT) resampleAppend(in *av.Frame) error {
	out, err := av.AllocFrame()
	if err != nil {
		return fmt.Errorf("whisper_stt: alloc: %w", err)
	}
	defer out.Close()
	out.SetAudioParams(av.SampleFmtFLTP, 1, av.WhisperSampleRate)
	if err := p.resampler.ConvertFrame(out, in); err != nil {
		return err
	}
	if n := out.NbSamples(); n > 0 {
		p.samples = append(p.samples, out.SamplePlaneF32(0)...)
	}
	return nil
}

func (p *WhisperSTT) Close() error {
	p.drainResampler()

	var firstErr error
	if p.model != nil && len(p.samples) > 0 {
		// Transcribe with a fresh context. Close runs at end-of-stream, where the
		// per-frame ProcessorContext is already cancelled; passing a Done context
		// would make whisper's abort callback fire on the first encode and drop
		// the whole transcript of a normally-completed job. (Cancelling a long
		// transcription mid-flight is therefore not supported — a v1 trade-off.)
		ctx := context.Background()
		progress := func(pct int) {
			if p.emit != nil {
				p.emit(&Metadata{Progress: true, LogMessage: fmt.Sprintf("whisper: transcribing %d%%", pct)})
			}
		}
		wsegs, err := p.model.Full(ctx, p.samples, p.opts, progress)
		if err != nil {
			firstErr = fmt.Errorf("whisper_stt: transcription: %w", err)
		} else {
			segs := make([]whisperSeg, 0, len(wsegs))
			for _, ws := range wsegs {
				sg := whisperSeg{Start: ws.Start, End: ws.End, Text: ws.Text, Confidence: ws.Confidence}
				segs = append(segs, sg)
				if p.emit != nil && sg.Text != "" {
					p.emit(&Metadata{
						Custom: map[string]any{
							"start":      sg.Start.Seconds(),
							"end":        sg.End.Seconds(),
							"text":       sg.Text,
							"confidence": sg.Confidence,
						},
						LogMessage: fmt.Sprintf("[%s] %s", formatTimestamp(sg.Start, false), sg.Text),
					})
				}
			}
			if p.outputFile != "" {
				if err := writeTranscript(p.outputFile, p.outputFormat, segs); err != nil {
					firstErr = fmt.Errorf("whisper_stt: write output: %w", err)
				}
			}
		}
	}

	if p.model != nil {
		p.model.Close()
		p.model = nil
	}
	return firstErr
}

// drainResampler flushes any samples buffered inside the resampler and frees it.
func (p *WhisperSTT) drainResampler() {
	if p.resampler == nil {
		return
	}
	for {
		out, err := av.AllocFrame()
		if err != nil {
			break
		}
		out.SetAudioParams(av.SampleFmtFLTP, 1, av.WhisperSampleRate)
		err = p.resampler.Flush(out)
		n := out.NbSamples()
		if err == nil && n > 0 {
			p.samples = append(p.samples, out.SamplePlaneF32(0)...)
			out.Close()
			continue
		}
		out.Close()
		break
	}
	p.resampler.Close()
	p.resampler = nil
}

func init() {
	Register("whisper_stt", func() Processor { return &WhisperSTT{} })
}
