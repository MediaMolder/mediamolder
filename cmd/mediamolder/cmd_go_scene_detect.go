// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdGoSceneDetect implements the `mediamolder go-scene-detect` subcommand.
//
// go-scene-detect uses algorithms ported directly from PySceneDetect by Brandon Castellano.
// See https://github.com/Breakthrough/PySceneDetect and https://scenedetect.com for details.
//
// Usage:
//
//	mediamolder go-scene-detect [flags] <input>
//
// Flags:
//
//	--detector <name>     content (default), adaptive, threshold, hash, histogram
//	--threshold <f>       override detector threshold (0 = use detector default)
//	--luma-only           content/adaptive: use luma channel only
//	--min-scene-len <v>   minimum scene length (frames, seconds, or timecode) (default: "0.6s")
//	--output <path>       write scene list to file (- = stdout, default)
//	--format <fmt>        output format: jsonl (default), csv, timecodes
//	--stats <path>        write per-frame statistics CSV to this file
//	--downscale <n>       downscale factor: 0=auto (default), 1=disabled, N=N×

import (
	"context"
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"

	psd "github.com/MediaMolder/MediaMolder/go_scene_detect"
	"github.com/MediaMolder/MediaMolder/go_scene_detect/detectors"
	"github.com/MediaMolder/MediaMolder/av"
)

func cmdGoSceneDetect(args []string) error {
	fs := flag.NewFlagSet("go-scene-detect", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder go-scene-detect [flags] <input>

go-scene-detect uses algorithms ported directly from PySceneDetect by Brandon Castellano.
See https://github.com/Breakthrough/PySceneDetect and https://scenedetect.com for details.

Flags:
  --detector <name>     Detector to use: content (default), adaptive, threshold, hash, histogram
  --threshold <f>       Override detector threshold (0 = use detector default)
  --luma-only           content/adaptive: analyse luma channel only
  --min-scene-len <v>   Minimum scene length in frames (int), seconds (e.g. "0.6s"), or timecode (default "0.6s")
  --output <path>       Write scene list to path (- for stdout, default)
  --format <fmt>        Output format: jsonl (default), csv, timecodes
  --stats <path>        Write per-frame statistics CSV to this file
  --downscale <n>       Downscale factor: 0=auto (default), 1=disabled, N=N×

`)
	}

	detectorFlag := fs.String("detector", "content",
		"detector: content, adaptive, threshold, hash, histogram")
	thresholdFlag := fs.Float64("threshold", 0,
		"override detector threshold (0 = use detector default)")
	lumaOnlyFlag := fs.Bool("luma-only", false,
		"content/adaptive: use luma channel only")
	minSceneLenFlag := fs.String("min-scene-len", "0.6s",
		"minimum scene length")
	outputFlag := fs.String("output", "-",
		"write scene list to file (- for stdout)")
	formatFlag := fs.String("format", "jsonl",
		"output format: jsonl, csv, timecodes")
	statsFlag := fs.String("stats", "",
		"write per-frame statistics CSV to this file")
	downscaleFlag := fs.Int("downscale", 0,
		"downscale factor: 0=auto, 1=disabled, N=N×")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("go-scene-detect: input file required")
	}
	inputPath := fs.Arg(0)

	// ── Open input ──────────────────────────────────────────────────────────
	input, err := av.OpenInput(inputPath, nil)
	if err != nil {
		return fmt.Errorf("go-scene-detect: open %q: %w", inputPath, err)
	}
	defer input.Close()

	// ── Find video stream ───────────────────────────────────────────────────
	streams, err := input.AllStreams()
	if err != nil {
		return fmt.Errorf("go-scene-detect: enumerate streams: %w", err)
	}
	vidIdx := -1
	for _, s := range streams {
		if s.Type == av.MediaTypeVideo {
			vidIdx = s.Index
			break
		}
	}
	if vidIdx < 0 {
		return fmt.Errorf("go-scene-detect: no video stream in %q", inputPath)
	}

	si, err := input.StreamInfo(vidIdx)
	if err != nil {
		return fmt.Errorf("go-scene-detect: stream info: %w", err)
	}

	fps := 25.0
	if si.FrameRate[0] > 0 && si.FrameRate[1] > 0 {
		fps = float64(si.FrameRate[0]) / float64(si.FrameRate[1])
	}

	// ── Open decoder ────────────────────────────────────────────────────────
	dec, err := av.OpenDecoder(input, vidIdx)
	if err != nil {
		return fmt.Errorf("go-scene-detect: open decoder: %w", err)
	}
	defer dec.Close()

	// ── Create detector ─────────────────────────────────────────────────────
	d, err := pysdCreateDetector(*detectorFlag, *thresholdFlag, *lumaOnlyFlag, *minSceneLenFlag)
	if err != nil {
		return fmt.Errorf("go-scene-detect: %w", err)
	}

	// ── Create stats manager (optional) ────────────────────────────────────
	var stats *psd.StatsManager
	if *statsFlag != "" {
		stats = psd.NewStatsManager(fps)
	}

	// ── Create scene manager ────────────────────────────────────────────────
	sm := psd.NewSceneManager(stats)
	if *downscaleFlag > 0 {
		if err := sm.SetDownscale(*downscaleFlag); err != nil {
			return err
		}
	}
	sm.AddDetector(d)

	// ── Decode loop (goroutine → channel → DetectScenes) ───────────────────
	frameCh := make(chan psd.FrameImg, 4)
	decodeErrCh := make(chan error, 1)

	go func() {
		defer close(frameCh)
		decodeErrCh <- pysdDecode(input, dec, vidIdx, fps, frameCh)
	}()

	ctx := context.Background()
	processed, detErr := sm.DetectScenes(ctx, frameCh)

	decErr := <-decodeErrCh

	if detErr != nil {
		return fmt.Errorf("go-scene-detect: detect: %w", detErr)
	}
	if decErr != nil {
		return fmt.Errorf("go-scene-detect: decode: %w", decErr)
	}

	fmt.Fprintf(os.Stderr, "Processed %d frames. Detected %d cuts.\n",
		processed, len(sm.GetCutList()))

	// ── Write stats CSV ─────────────────────────────────────────────────────
	if *statsFlag != "" && stats != nil {
		if err := stats.SaveToCSV(*statsFlag); err != nil {
			return fmt.Errorf("go-scene-detect: save stats: %w", err)
		}
	}

	// ── Output scenes ───────────────────────────────────────────────────────
	scenes := sm.GetSceneList(true)

	var out *os.File
	if *outputFlag == "-" {
		out = os.Stdout
	} else {
		f, err := os.Create(*outputFlag)
		if err != nil {
			return fmt.Errorf("go-scene-detect: create output %q: %w", *outputFlag, err)
		}
		defer f.Close()
		out = f
	}

	switch strings.ToLower(*formatFlag) {
	case "jsonl":
		return pysdWriteJSONL(out, scenes)
	case "csv":
		return pysdWriteCSV(out, scenes)
	case "timecodes":
		return pysdWriteTimecodes(out, scenes)
	default:
		return fmt.Errorf("go-scene-detect: unknown format %q (want jsonl, csv, timecodes)", *formatFlag)
	}
}

// pysdDecode runs the demux+decode loop, converting each decoded frame to a
// psd.FrameImg and sending it on ch.  ch is not closed here; the caller's
// goroutine closes it via defer close(frameCh).
func pysdDecode(
	input *av.InputFormatContext,
	dec *av.DecoderContext,
	vidIdx int,
	fps float64,
	ch chan<- psd.FrameImg,
) error {
	pkt, err := av.AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()

	f, err := av.AllocFrame()
	if err != nil {
		return err
	}
	defer f.Close()

	frameNum := int64(0)

	// receiveFrames drains the decoder and sends each decoded frame to ch.
	// Returns false on a fatal error (stored in *rerr), true otherwise.
	var receiveErr error
	receiveFrames := func() bool {
		for {
			err := dec.ReceiveFrame(f)
			if av.IsEAgain(err) {
				return true // need more packets
			}
			if av.IsEOF(err) {
				f.Unref()
				return false // flushed — all frames consumed
			}
			if err != nil {
				receiveErr = err
				f.Unref()
				return false
			}
			w, h := f.Width(), f.Height()
			bgr, bgrErr := f.ToBGR24()
			f.Unref()
			if bgrErr != nil {
				receiveErr = bgrErr
				return false
			}
			tc, _ := psd.NewFrameTimecode(frameNum, fps)
			frameNum++
			ch <- psd.FrameImg{
				Timecode: tc,
				Data:     &psd.FrameData{Width: w, Height: h, BGR: bgr},
			}
		}
	}

	// Main demux loop.
	for {
		if err := input.ReadPacket(pkt); err != nil {
			pkt.Unref()
			if av.IsEOF(err) {
				// Flush decoder and drain remaining frames.
				if flushErr := dec.Flush(); flushErr == nil {
					receiveFrames()
				}
				return receiveErr
			}
			return err
		}
		if pkt.StreamIndex() != vidIdx {
			pkt.Unref()
			continue
		}
		if err := dec.SendPacket(pkt); err != nil {
			pkt.Unref()
			return err
		}
		pkt.Unref()
		if !receiveFrames() {
			return receiveErr
		}
	}
}

// pysdCreateDetector constructs the requested SceneDetector.
// threshold==0 means "use the detector's own default".
func pysdCreateDetector(name string, threshold float64, lumaOnly bool, minSceneLen any) (psd.SceneDetector, error) {
	switch name {
	case "content":
		t := 27.0
		if threshold != 0 {
			t = threshold
		}
		return detectors.NewContentDetector(t, minSceneLen, detectors.DefaultContentWeights, 0, psd.FlashFilterModeMerge)
	case "adaptive":
		t := 3.0
		if threshold != 0 {
			t = threshold
		}
		return detectors.NewAdaptiveDetector(t, minSceneLen, 2, 15.0, detectors.DefaultContentWeights, lumaOnly, -1)
	case "threshold":
		t := 12.0
		if threshold != 0 {
			t = threshold
		}
		return detectors.NewThresholdDetector(t, minSceneLen, 0.0, false, detectors.ThresholdMethodFloor)
	case "hash":
		t := 0.395
		if threshold != 0 {
			t = threshold
		}
		return detectors.NewHashDetector(t, minSceneLen, 16, 2)
	case "histogram":
		t := 0.05
		if threshold != 0 {
			t = threshold
		}
		return detectors.NewHistogramDetector(t, 256, minSceneLen)
	default:
		return nil, fmt.Errorf("unknown detector %q (want: content, adaptive, threshold, hash, histogram)", name)
	}
}

// pysdSceneJSON is the JSON representation of a single scene.
type pysdSceneJSON struct {
	Scene         int    `json:"scene"`
	StartFrame    int64  `json:"start_frame"`
	StartTimecode string `json:"start_timecode"`
	EndFrame      int64  `json:"end_frame"`
	EndTimecode   string `json:"end_timecode"`
}

func pysdWriteJSONL(out *os.File, scenes psd.SceneList) error {
	enc := json.NewEncoder(out)
	for i, s := range scenes {
		rec := pysdSceneJSON{
			Scene:         i + 1,
			StartFrame:    s.Start.FrameNum(),
			StartTimecode: s.Start.Timecode(),
			EndFrame:      s.End.FrameNum(),
			EndTimecode:   s.End.Timecode(),
		}
		if err := enc.Encode(rec); err != nil {
			return err
		}
	}
	return nil
}

func pysdWriteCSV(out *os.File, scenes psd.SceneList) error {
	w := csv.NewWriter(out)
	if err := w.Write([]string{"Scene Number", "Start Frame", "Start Timecode", "End Frame", "End Timecode"}); err != nil {
		return err
	}
	for i, s := range scenes {
		if err := w.Write([]string{
			fmt.Sprintf("%d", i+1),
			fmt.Sprintf("%d", s.Start.FrameNum()),
			s.Start.Timecode(),
			fmt.Sprintf("%d", s.End.FrameNum()),
			s.End.Timecode(),
		}); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}

func pysdWriteTimecodes(out *os.File, scenes psd.SceneList) error {
	tcs := make([]string, 0, len(scenes))
	for _, s := range scenes {
		if s.Start.FrameNum() > 0 {
			tcs = append(tcs, s.Start.Timecode())
		}
	}
	if len(tcs) > 0 {
		_, err := fmt.Fprintln(out, strings.Join(tcs, ","))
		return err
	}
	return nil
}
