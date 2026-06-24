// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package main

// cmdFaceDetect implements the `mediamolder face-detect` subcommand: detect (and optionally
// embed) faces in an image or video, emitting one record per detected face.
//
// The detect→align→embed pipeline lives in the face package and is gated on the with_onnx
// build tag plus bundled, SHA-pinned models (scripts/fetch-face-models.sh). This command is
// compiled into every build; when the models are unavailable it fails with an actionable
// message rather than being silently absent.
//
// Usage:
//
//	mediamolder face-detect [flags] <input>
//
// Flags:
//
//	--format <fmt>     output format: jsonl (default), csv, json
//	--output <path>    write to path (- for stdout, default)
//	--every <n>        video: analyse every Nth frame (default 1)
//	--max-frames <n>   cap frames analysed (0 = all)
//	--embeddings       include the 128-d SFace embedding per face (default off)
//	--conf <f>         detector confidence threshold (0 = package default 0.5)
//	--models-dir <p>   directory of face models (overrides MEDIAMOLDER_FACE_MODELS)

import (
	"encoding/csv"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
	"github.com/MediaMolder/MediaMolder/face"
)

func cmdFaceDetect(args []string) error {
	fs := flag.NewFlagSet("face-detect", flag.ContinueOnError)
	fs.Usage = func() {
		fmt.Fprint(os.Stderr, `Usage: mediamolder face-detect [flags] <input>

Detect faces (YOLOv8-face) in an image or video, align each to the canonical
112x112, and optionally embed it (SFace) for recognition/clustering. Emits one
record per detected face.

Requires a build with -tags with_onnx and bundled models (set
MEDIAMOLDER_FACE_MODELS or --models-dir; see scripts/fetch-face-models.sh).

Flags:
  --format <fmt>     Output format: jsonl (default), csv, json
  --output <path>    Write to path (- for stdout, default)
  --every <n>        Video: analyse every Nth frame (default 1)
  --max-frames <n>   Cap frames analysed (0 = all)
  --embeddings       Include the 128-d SFace embedding per face (default off)
  --conf <f>         Detector confidence threshold (0 = package default 0.5)
  --models-dir <p>   Directory of face models (overrides MEDIAMOLDER_FACE_MODELS)
  --ort-lib <p>      Path to the ONNX Runtime shared library (else auto-discovered)

`)
	}

	formatFlag := fs.String("format", "jsonl", "output format: jsonl, csv, json")
	outputFlag := fs.String("output", "-", "write to file (- for stdout)")
	everyFlag := fs.Uint64("every", 1, "video: analyse every Nth frame")
	maxFramesFlag := fs.Uint64("max-frames", 0, "cap frames analysed (0 = all)")
	embeddingsFlag := fs.Bool("embeddings", false, "include the 128-d embedding per face")
	confFlag := fs.Float64("conf", 0, "detector confidence threshold (0 = default)")
	modelsDirFlag := fs.String("models-dir", "", "directory of face models")
	ortLibFlag := fs.String("ort-lib", "", "path to the ONNX Runtime shared library (else auto-discovered)")

	if err := fs.Parse(args); err != nil {
		return err
	}
	if fs.NArg() < 1 {
		fs.Usage()
		return fmt.Errorf("face-detect: input file required")
	}
	inputPath := fs.Arg(0)

	if *modelsDirFlag != "" {
		face.SetModelsDir(*modelsDirFlag)
	}
	if *ortLibFlag != "" {
		face.SetONNXLib(*ortLibFlag)
	}
	if err := face.Available(); err != nil {
		return fmt.Errorf("face-detect: face analysis unavailable: %w — needs a -tags with_onnx "+
			"build and bundled models via MEDIAMOLDER_FACE_MODELS or --models-dir "+
			"(see scripts/fetch-face-models.sh)", err)
	}
	every := *everyFlag
	if every == 0 {
		every = 1
	}
	opts := face.Options{ConfThresh: *confFlag, Embed: *embeddingsFlag}

	// ── Open input + locate the video/image stream ──────────────────────────
	input, err := av.OpenInput(inputPath, nil)
	if err != nil {
		return fmt.Errorf("face-detect: open %q: %w", inputPath, err)
	}
	defer input.Close()

	streams, err := input.AllStreams()
	if err != nil {
		return fmt.Errorf("face-detect: enumerate streams: %w", err)
	}
	vidIdx := -1
	for _, s := range streams {
		if s.Type == av.MediaTypeVideo {
			vidIdx = s.Index
			break
		}
	}
	if vidIdx < 0 {
		return fmt.Errorf("face-detect: no image/video stream in %q", inputPath)
	}
	si, err := input.StreamInfo(vidIdx)
	if err != nil {
		return fmt.Errorf("face-detect: stream info: %w", err)
	}
	fps := 0.0
	if si.FrameRate[0] > 0 && si.FrameRate[1] > 0 {
		fps = float64(si.FrameRate[0]) / float64(si.FrameRate[1])
	}

	dec, err := av.OpenDecoder(input, vidIdx)
	if err != nil {
		return fmt.Errorf("face-detect: open decoder: %w", err)
	}
	defer dec.Close()

	// ── Decode loop → per-frame analysis ────────────────────────────────────
	var records []face.Record
	var frameIdx, analyzed uint64
	analyzeFrame := func(fr *av.Frame) error {
		if *maxFramesFlag > 0 && analyzed >= *maxFramesFlag {
			return errFaceDone
		}
		idx := frameIdx
		frameIdx++
		if every > 1 && idx%every != 0 {
			return nil
		}
		img, err := fr.ToRGBA()
		if err != nil {
			return fmt.Errorf("to rgba: %w", err)
		}
		faces, err := face.AnalyzeImageOpts(img, opts)
		if err != nil {
			return fmt.Errorf("analyze frame %d: %w", idx, err)
		}
		pts := fr.PTS()
		t := faceTimeSec(pts, si.TimeBase, fps, idx)
		for _, f := range faces {
			records = append(records, f.ToRecord(idx, pts, t))
		}
		analyzed++
		return nil
	}

	if err := faceDecodeLoop(input, dec, vidIdx, analyzeFrame); err != nil {
		return fmt.Errorf("face-detect: %w", err)
	}

	fmt.Fprintf(os.Stderr, "Analysed %d frame(s). Detected %d face(s).\n", analyzed, len(records))

	// ── Output ──────────────────────────────────────────────────────────────
	var out *os.File
	if *outputFlag == "-" {
		out = os.Stdout
	} else {
		f, err := os.Create(*outputFlag)
		if err != nil {
			return fmt.Errorf("face-detect: create output %q: %w", *outputFlag, err)
		}
		defer f.Close()
		out = f
	}

	switch strings.ToLower(*formatFlag) {
	case "jsonl":
		return faceWriteJSONL(out, records)
	case "json":
		return faceWriteJSON(out, records)
	case "csv":
		return faceWriteCSV(out, records)
	default:
		return fmt.Errorf("face-detect: unknown format %q (want jsonl, csv, json)", *formatFlag)
	}
}

// errFaceDone signals the decode loop to stop early once --max-frames is reached.
var errFaceDone = fmt.Errorf("face-detect: frame cap reached")

// faceTimeSec converts a frame PTS (in stream time_base units) to seconds, falling back to
// frameIdx/fps when the PTS is absent (AV_NOPTS_VALUE ⇒ negative) or the time base is unset.
func faceTimeSec(pts int64, tb [2]int, fps float64, frameIdx uint64) float64 {
	if pts >= 0 && tb[0] > 0 && tb[1] > 0 {
		return float64(pts) * float64(tb[0]) / float64(tb[1])
	}
	if fps > 0 {
		return float64(frameIdx) / fps
	}
	return 0
}

// faceDecodeLoop demuxes input and calls onFrame for each decoded frame of stream vidIdx. A
// return of errFaceDone from onFrame stops the loop cleanly (not an error).
func faceDecodeLoop(input *av.InputFormatContext, dec *av.DecoderContext, vidIdx int, onFrame func(*av.Frame) error) error {
	pkt, err := av.AllocPacket()
	if err != nil {
		return err
	}
	defer pkt.Close()
	fr, err := av.AllocFrame()
	if err != nil {
		return err
	}
	defer fr.Close()

	var onFrameErr error
	// drain receives and dispatches all currently-decodable frames; returns false to stop.
	drain := func() bool {
		for {
			err := dec.ReceiveFrame(fr)
			if av.IsEAgain(err) {
				return true
			}
			if av.IsEOF(err) {
				fr.Unref()
				return false
			}
			if err != nil {
				onFrameErr = err
				fr.Unref()
				return false
			}
			e := onFrame(fr)
			fr.Unref()
			if e != nil {
				onFrameErr = e
				return false
			}
		}
	}

	for {
		if err := input.ReadPacket(pkt); err != nil {
			pkt.Unref()
			if av.IsEOF(err) {
				if flushErr := dec.Flush(); flushErr == nil {
					drain()
				}
				break
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
		if !drain() {
			break
		}
	}
	if onFrameErr != nil && onFrameErr != errFaceDone {
		return onFrameErr
	}
	return nil
}

func faceWriteJSONL(out *os.File, records []face.Record) error {
	enc := json.NewEncoder(out)
	for _, r := range records {
		if err := enc.Encode(r); err != nil {
			return err
		}
	}
	return nil
}

func faceWriteJSON(out *os.File, records []face.Record) error {
	if records == nil {
		records = []face.Record{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(records)
}

func faceWriteCSV(out *os.File, records []face.Record) error {
	w := csv.NewWriter(out)
	header := []string{"frame", "pts", "time", "x", "y", "w", "h", "score",
		"lx0", "ly0", "lx1", "ly1", "lx2", "ly2", "lx3", "ly3", "lx4", "ly4"}
	if err := w.Write(header); err != nil {
		return err
	}
	for _, r := range records {
		row := []string{
			strconv.FormatUint(r.Frame, 10),
			strconv.FormatInt(r.PTS, 10),
			strconv.FormatFloat(r.Time, 'f', 3, 64),
			strconv.Itoa(r.BBox[0]), strconv.Itoa(r.BBox[1]),
			strconv.Itoa(r.BBox[2]), strconv.Itoa(r.BBox[3]),
			strconv.FormatFloat(float64(r.Score), 'f', 4, 32),
		}
		for k := 0; k < 5; k++ {
			row = append(row, strconv.Itoa(r.Landmarks[k][0]), strconv.Itoa(r.Landmarks[k][1]))
		}
		if err := w.Write(row); err != nil {
			return err
		}
	}
	w.Flush()
	return w.Error()
}
