// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// Hardware and software codec benchmark harness.
//
// RunBenchmark encodes and decodes synthetic video frames through one or more
// codecs at a set of standard resolutions, measuring encode and decode
// throughput (frames-per-second).  The output BenchmarkReport is designed to
// be shared with the MediaMolder community so empirically measured results
// from many GPUs/systems can be aggregated into a capability database.
//
// DISCLAIMER: results vary by system load, thermal state, and driver version.
// Reports include sufficient metadata (GPU model, driver, OS) for meaningful
// grouping in the lookup table.

// #include <stdint.h>
// #include <string.h>
// #include <stdlib.h>
// #include "libavcodec/avcodec.h"
// #include "libavutil/avutil.h"
// #include "libavutil/frame.h"
// #include "libavutil/imgutils.h"
//
// // Allocate a video frame with allocated buffers filled to near-white YUV.
// // pixFmt: 0 = AV_PIX_FMT_YUV420P, 23 = AV_PIX_FMT_NV12.
// // Returns NULL on failure.
// static AVFrame *mm_bench_alloc_frame(int w, int h, int pix_fmt, int64_t pts) {
//     AVFrame *f = av_frame_alloc();
//     if (!f) return NULL;
//     f->width   = w;
//     f->height  = h;
//     f->format  = pix_fmt;
//     f->pts     = pts;
//     if (av_frame_get_buffer(f, 0) < 0) { av_frame_free(&f); return NULL; }
//
//     // Fill with near-white: Y=235, chroma=128.
//     // AV_PIX_FMT_YUV420P: 3 planes; AV_PIX_FMT_NV12: 2 planes.
//     memset(f->data[0], 235, (size_t)(f->linesize[0] * h));
//     memset(f->data[1], 128, (size_t)(f->linesize[1] * h / 2));
//     if (pix_fmt == 0 /* YUV420P */ && f->data[2]) {
//         memset(f->data[2], 128, (size_t)(f->linesize[2] * h / 2));
//     }
//     return f;
// }
//
// // Standalone software decoder context for round-trip decode benchmarks.
// typedef struct {
//     AVCodecContext *ctx;
//     AVFrame        *frame;
// } mm_bench_dec;
//
// // Open a standalone software decoder.  extradata may be NULL.
// // Returns NULL on failure.
// static mm_bench_dec *mm_bench_open_decoder(const char *codec_name,
//                                             int w, int h,
//                                             const uint8_t *extradata,
//                                             int extradata_size) {
//     const AVCodec *codec = avcodec_find_decoder_by_name(codec_name);
//     if (!codec) return NULL;
//
//     AVCodecContext *ctx = avcodec_alloc_context3(codec);
//     if (!ctx) return NULL;
//
//     ctx->width  = w;
//     ctx->height = h;
//     if (extradata && extradata_size > 0) {
//         ctx->extradata = (uint8_t *)av_malloc(
//             (size_t)extradata_size + AV_INPUT_BUFFER_PADDING_SIZE);
//         if (ctx->extradata) {
//             memcpy(ctx->extradata, extradata, (size_t)extradata_size);
//             memset(ctx->extradata + extradata_size, 0,
//                    AV_INPUT_BUFFER_PADDING_SIZE);
//             ctx->extradata_size = extradata_size;
//         }
//     }
//
//     if (avcodec_open2(ctx, codec, NULL) < 0) {
//         avcodec_free_context(&ctx);
//         return NULL;
//     }
//
//     mm_bench_dec *d = (mm_bench_dec *)calloc(1, sizeof(*d));
//     if (!d) { avcodec_free_context(&ctx); return NULL; }
//     d->ctx   = ctx;
//     d->frame = av_frame_alloc();
//     if (!d->frame) { avcodec_free_context(&ctx); free(d); return NULL; }
//     return d;
// }
//
// // Send one packet and drain all output frames.
// // Returns frames decoded (≥ 0) or a negative AVERROR on hard failure.
// static int mm_bench_decode_packet(mm_bench_dec *d, AVPacket *pkt) {
//     int ret = avcodec_send_packet(d->ctx, pkt);
//     if (ret < 0) return ret;
//     int n = 0;
//     for (;;) {
//         ret = avcodec_receive_frame(d->ctx, d->frame);
//         if (ret == AVERROR(EAGAIN) || ret == AVERROR_EOF) break;
//         if (ret < 0) return ret;
//         av_frame_unref(d->frame);
//         n++;
//     }
//     return n;
// }
//
// static void mm_bench_close_decoder(mm_bench_dec *d) {
//     if (!d) return;
//     avcodec_free_context(&d->ctx);
//     av_frame_free(&d->frame);
//     free(d);
// }
//
// // Check whether a codec name is registered in this build.
// static int mm_codec_exists_enc(const char *name) {
//     return avcodec_find_encoder_by_name(name) != NULL;
// }
// static int mm_codec_exists_dec(const char *name) {
//     return avcodec_find_decoder_by_name(name) != NULL;
// }
import "C"

import (
	"context"
	"fmt"
	"runtime"
	"sort"
	"time"
	"unsafe"
)

// ── Public types ─────────────────────────────────────────────────────────────

// Resolution is a (width, height) pair for benchmark targets.
type Resolution struct {
	Width  int `json:"width"`
	Height int `json:"height"`
}

func (r Resolution) String() string { return fmt.Sprintf("%dx%d", r.Width, r.Height) }

// StandardResolutions are the default benchmark targets (360p → 4K).
var StandardResolutions = []Resolution{
	{640, 360},
	{1280, 720},
	{1920, 1080},
	{2560, 1440},
	{3840, 2160},
}

// BenchmarkConfig controls what RunBenchmark tests.
type BenchmarkConfig struct {
	// Codecs lists the encoder names to benchmark (e.g. "libx264", "h264_nvenc").
	// nil = DefaultBenchmarkCodecs.
	Codecs []string

	// Resolutions lists the frame sizes to test.
	// nil = StandardResolutions.
	Resolutions []Resolution

	// HWDevice is the hardware device context to use for HW codecs.
	// nil = SW codecs only.
	HWDevice *HWDeviceContext

	// WarmupFrames is the number of frames to encode/decode before timing.
	// Default: 20.
	WarmupFrames int

	// MeasureFrames is the number of frames to time.
	// Default: 200.
	MeasureFrames int

	// FrameRate is the encode frame rate {num, den}.  Default: {30, 1}.
	FrameRate [2]int
}

// DefaultBenchmarkCodecs is the set of encoders tested when BenchmarkConfig.Codecs is nil.
var DefaultBenchmarkCodecs = []string{
	"libx264",        // H.264 SW
	"libx265",        // HEVC SW (may not be present in all builds)
	"h264_nvenc",     // H.264 NVENC  (NVIDIA)
	"hevc_nvenc",     // HEVC NVENC   (NVIDIA)
	"av1_nvenc",      // AV1  NVENC   (Ada Lovelace+)
	"h264_videotoolbox", // H.264 VT  (macOS)
	"hevc_videotoolbox", // HEVC VT   (macOS)
	"h264_vaapi",     // H.264 VAAPI  (Linux Intel/AMD)
	"hevc_vaapi",     // HEVC VAAPI   (Linux Intel/AMD)
	"h264_qsv",       // H.264 QSV    (Intel oneVPL)
	"hevc_qsv",       // HEVC QSV     (Intel oneVPL)
}

// BenchmarkResult holds the measured encode and decode throughput for one
// codec × resolution combination.
type BenchmarkResult struct {
	// Codec is the FFmpeg encoder name (e.g. "h264_nvenc").
	Codec string `json:"codec"`

	// DecoderName is the FFmpeg decoder name used for the round-trip decode
	// benchmark (e.g. "h264" or "h264_cuvid").
	DecoderName string `json:"decoder_name"`

	// Resolution is the frame size.
	Resolution Resolution `json:"resolution"`

	// EncodeFPS is the mean encode throughput (frames/second) over MeasureFrames.
	EncodeFPS float64 `json:"encode_fps"`

	// DecodeFPS is the mean decode throughput (frames/second) over MeasureFrames.
	// Zero if decode benchmarking failed (e.g. no decoder found).
	DecodeFPS float64 `json:"decode_fps"`

	// EncodeBitrateMbps is the mean bitrate emitted by the encoder in Mbit/s.
	EncodeBitrateMbps float64 `json:"encode_bitrate_mbps"`

	// Note contains any limitation or warning for this result.
	Note string `json:"note,omitempty"`

	// Err is set when the benchmark for this combination could not be run.
	Err string `json:"error,omitempty"`
}

// BenchmarkReport is the top-level output of RunBenchmark.  It is designed to
// be serialised as JSON and shared for community contribution.
type BenchmarkReport struct {
	// SchemaVersion identifies the report format (for future compatibility).
	SchemaVersion string `json:"schema_version"`

	// Timestamp is when the benchmark was run (UTC).
	Timestamp time.Time `json:"timestamp"`

	// OS is the operating system name (e.g. "linux", "darwin", "windows").
	OS string `json:"os"`

	// Arch is the CPU architecture (e.g. "amd64", "arm64").
	Arch string `json:"arch"`

	// NumCPU is the number of logical CPU cores visible to the process.
	NumCPU int `json:"num_cpu"`

	// DeviceName is the display name of the hardware device under test, or
	// "software" when no HW device was provided.
	DeviceName string `json:"device_name"`

	// DeviceType is the FFmpeg device type string (e.g. "cuda", "videotoolbox").
	DeviceType string `json:"device_type,omitempty"`

	// CUDAArch is the NVIDIA architecture name (e.g. "Ada Lovelace") when the
	// device is CUDA.
	CUDAArch string `json:"cuda_arch,omitempty"`

	// FFmpegVersion is the version string from av_version_info().
	FFmpegVersion string `json:"ffmpeg_version"`

	// WarmupFrames / MeasureFrames are the config values used.
	WarmupFrames  int `json:"warmup_frames"`
	MeasureFrames int `json:"measure_frames"`

	// Disclaimer reminds recipients that results vary by thermal/clock state.
	Disclaimer string `json:"disclaimer"`

	// Results is the per-codec × per-resolution measurement list.
	Results []BenchmarkResult `json:"results"`
}

// ── Internal codec → decoder mappings ────────────────────────────────────────

// benchDecoderFor returns the FFmpeg decoder name to use for round-trip
// benchmarking of the given encoder.  HW-accelerated decoders are preferred
// when available.
var benchDecoderFor = map[string]string{
	"libx264":           "h264",
	"libx265":           "hevc",
	"h264_nvenc":        "h264_cuvid",
	"hevc_nvenc":        "hevc_cuvid",
	"av1_nvenc":         "av1_cuvid",
	"h264_videotoolbox": "h264",
	"hevc_videotoolbox": "hevc",
	"h264_vaapi":        "h264",
	"hevc_vaapi":        "hevc",
	"h264_qsv":          "h264_qsv",
	"hevc_qsv":          "hevc_qsv",
}

// hwEncoders is the set of codecs that require a HWDeviceContext.
var hwEncoders = map[string]bool{
	"h264_nvenc": true, "hevc_nvenc": true, "av1_nvenc": true,
	"h264_videotoolbox": true, "hevc_videotoolbox": true,
	"h264_vaapi": true, "hevc_vaapi": true,
	"h264_qsv": true, "hevc_qsv": true,
}

// swPixFmtForEncoder returns the software pixel format expected by the encoder.
// 0 = AV_PIX_FMT_YUV420P (default for SW + VT).
// 23 = AV_PIX_FMT_NV12 (NVENC, VAAPI, QSV prefer NV12).
func swPixFmtForEncoder(codecName string) int {
	switch codecName {
	case "h264_nvenc", "hevc_nvenc", "av1_nvenc",
		"h264_vaapi", "hevc_vaapi",
		"h264_qsv", "hevc_qsv":
		return 23 // AV_PIX_FMT_NV12
	default:
		return 0 // AV_PIX_FMT_YUV420P
	}
}

// ── Public API ────────────────────────────────────────────────────────────────

// RunBenchmark runs encode + decode throughput benchmarks according to cfg and
// returns a BenchmarkReport suitable for JSON serialisation and community
// contribution.
//
// ctx cancellation stops the benchmark after the current codec × resolution
// pair completes.
func RunBenchmark(ctx context.Context, cfg BenchmarkConfig) (*BenchmarkReport, error) {
	// Apply defaults.
	if cfg.WarmupFrames <= 0 {
		cfg.WarmupFrames = 20
	}
	if cfg.MeasureFrames <= 0 {
		cfg.MeasureFrames = 200
	}
	if cfg.FrameRate[1] == 0 {
		cfg.FrameRate = [2]int{30, 1}
	}
	codecs := cfg.Codecs
	if len(codecs) == 0 {
		codecs = DefaultBenchmarkCodecs
	}
	resolutions := cfg.Resolutions
	if len(resolutions) == 0 {
		resolutions = StandardResolutions
	}

	// Build report header.
	report := &BenchmarkReport{
		SchemaVersion: "1.0",
		Timestamp:     time.Now().UTC(),
		OS:            runtime.GOOS,
		Arch:          runtime.GOARCH,
		NumCPU:        runtime.NumCPU(),
		FFmpegVersion: ffmpegVersionString(),
		WarmupFrames:  cfg.WarmupFrames,
		MeasureFrames: cfg.MeasureFrames,
		Disclaimer: "Results vary by system load, thermal state, and driver version. " +
			"Always report GPU model, driver version, and OS when contributing.",
	}

	if cfg.HWDevice != nil {
		devcaps := cfg.HWDevice.QueryCapabilities()
		report.DeviceName = devcaps.DisplayName
		report.DeviceType = cfg.HWDevice.deviceType.String()
		report.CUDAArch = devcaps.CUDAArch
	} else {
		report.DeviceName = "software"
	}

	// Run each codec × resolution pair.
	var results []BenchmarkResult
	for _, codecName := range codecs {
		// Check if the encoder exists in this FFmpeg build.
		cName := C.CString(codecName)
		exists := C.mm_codec_exists_enc(cName) != 0
		C.free(unsafe.Pointer(cName))
		if !exists {
			continue
		}

		// Skip HW codecs when no device is provided.
		if hwEncoders[codecName] && cfg.HWDevice == nil {
			continue
		}

		for _, res := range resolutions {
			if err := ctx.Err(); err != nil {
				return report, nil
			}
			r := benchOneCodecResolution(ctx, cfg, codecName, res)
			results = append(results, r)
		}
	}

	// Sort results for deterministic output: codec → width → height.
	sort.Slice(results, func(i, j int) bool {
		if results[i].Codec != results[j].Codec {
			return results[i].Codec < results[j].Codec
		}
		if results[i].Resolution.Width != results[j].Resolution.Width {
			return results[i].Resolution.Width < results[j].Resolution.Width
		}
		return results[i].Resolution.Height < results[j].Resolution.Height
	})
	report.Results = results
	return report, nil
}

// ── Internal: single codec × resolution benchmark ────────────────────────────

func benchOneCodecResolution(
	ctx context.Context,
	cfg BenchmarkConfig,
	codecName string,
	res Resolution,
) BenchmarkResult {
	result := BenchmarkResult{
		Codec:      codecName,
		Resolution: res,
	}

	pixFmt := swPixFmtForEncoder(codecName)
	isHW := hwEncoders[codecName]

	// ── 1. Encode benchmark ──────────────────────────────────────────────────

	var encErr error
	var packets []*Packet
	var extradata []byte

	if isHW && cfg.HWDevice != nil {
		packets, extradata, result.EncodeFPS, result.EncodeBitrateMbps, encErr =
			benchEncodeHW(ctx, cfg, codecName, res, pixFmt)
	} else if !isHW {
		packets, extradata, result.EncodeFPS, result.EncodeBitrateMbps, encErr =
			benchEncodeSW(ctx, cfg, codecName, res, pixFmt)
	}

	if encErr != nil {
		result.Err = fmt.Sprintf("encode: %v", encErr)
		freePackets(packets)
		return result
	}
	if len(packets) == 0 {
		result.Err = "encode: no packets produced"
		return result
	}

	// ── 2. Decode benchmark ──────────────────────────────────────────────────

	decName, ok := benchDecoderFor[codecName]
	if !ok {
		decName = "" // no known decoder
	}
	// Fall back to SW decoder if HW decoder is not available.
	if decName != "" {
		cDecName := C.CString(decName)
		decExists := C.mm_codec_exists_dec(cDecName) != 0
		C.free(unsafe.Pointer(cDecName))
		if !decExists {
			// Try the SW fallback name (strip _cuvid/_qsv suffix).
			for _, sw := range []string{"h264", "hevc", "av1", "vp9", "mpeg2video"} {
				if benchDecoderFor[codecName] != sw {
					continue
				}
				cSW := C.CString(sw)
				if C.mm_codec_exists_dec(cSW) != 0 {
					decName = sw
					C.free(unsafe.Pointer(cSW))
					break
				}
				C.free(unsafe.Pointer(cSW))
			}
		}
	}
	result.DecoderName = decName

	if decName != "" {
		result.DecodeFPS, _ = benchDecode(ctx, cfg, decName, res, packets, extradata)
	}

	freePackets(packets)
	return result
}

// benchEncodeSW runs the encode benchmark using a software EncoderContext.
// Returns the MeasureFrames-worth of encoded packets and extradata for later decode.
func benchEncodeSW(
	ctx context.Context,
	cfg BenchmarkConfig,
	codecName string,
	res Resolution,
	pixFmt int,
) ([]*Packet, []byte, float64, float64, error) {
	opts := EncoderOptions{
		CodecName:   codecName,
		Width:       res.Width,
		Height:      res.Height,
		PixFmt:      pixFmt,
		FrameRate:   cfg.FrameRate,
		TimeBase:    [2]int{cfg.FrameRate[1], cfg.FrameRate[0]},
		GlobalHeader: true,
		ExtraOpts:   map[string]string{"preset": "ultrafast", "tune": "zerolatency"},
	}
	enc, err := OpenEncoder(opts)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	defer enc.Close()

	fps, bitrate, pkts, extradata, err := runEncodeLoop(ctx,
		enc.SendFrame, enc.ReceivePacket, enc, cfg, res, pixFmt)
	return pkts, extradata, fps, bitrate, err
}

// benchEncodeHW runs the encode benchmark using a HWEncoderContext.
func benchEncodeHW(
	ctx context.Context,
	cfg BenchmarkConfig,
	codecName string,
	res Resolution,
	pixFmt int,
) ([]*Packet, []byte, float64, float64, error) {
	opts := HWEncoderOptions{
		EncoderOptions: EncoderOptions{
			CodecName:   codecName,
			Width:       res.Width,
			Height:      res.Height,
			FrameRate:   cfg.FrameRate,
			TimeBase:    [2]int{cfg.FrameRate[1], cfg.FrameRate[0]},
			GlobalHeader: true,
		},
		HWDevice: cfg.HWDevice,
		SWPixFmt: pixFmt,
	}
	enc, err := OpenHWEncoder(opts)
	if err != nil {
		return nil, nil, 0, 0, err
	}
	defer enc.Close()

	fps, bitrate, pkts, extradata, err := runEncodeLoop(ctx,
		enc.SendFrame, enc.ReceivePacket, nil, cfg, res, pixFmt)
	return pkts, extradata, fps, bitrate, err
}

// benchSendFn and benchReceiveFn are the encoder method signatures used by
// the generic encode loop; they are satisfied by both EncoderContext and
// HWEncoderContext.
type benchSendFn func(*Frame) error
type benchReceiveFn func(*Packet) error

// runEncodeLoop performs warmup + timed encode, returning fps, bitrate,
// a slice of the timed packets, and codec extradata (for decode setup).
func runEncodeLoop(
	_ context.Context,
	sendFrame benchSendFn,
	receivePacket benchReceiveFn,
	swEnc *EncoderContext, // nil for HW encoders
	cfg BenchmarkConfig,
	res Resolution,
	pixFmt int,
) (fps float64, bitrateMbps float64, packets []*Packet, extradata []byte, err error) {
	total := cfg.WarmupFrames + cfg.MeasureFrames

	// Pre-allocate the pool of synthetic frames.
	frames := make([]*Frame, total)
	for i := range frames {
		cf := C.mm_bench_alloc_frame(C.int(res.Width), C.int(res.Height), C.int(pixFmt), C.int64_t(int64(i)))
		if cf == nil {
			err = fmt.Errorf("failed to allocate synthetic frame %d", i)
			goto cleanup
		}
		frames[i] = &Frame{p: cf}
	}

	// Warmup.
	for i := 0; i < cfg.WarmupFrames; i++ {
		if e := sendFrame(frames[i]); e != nil && !IsEAgain(e) {
			err = e
			goto cleanup
		}
		drainPackets(receivePacket)
	}
	// Flush warmup.
	_ = sendFrame(nil)
	drainPackets(receivePacket)

	// Reset encoder state if possible (flush + re-open not feasible; warmup
	// is sufficient to prime HW pipelines).

	// Get extradata from the first flush for decoder setup.
	if swEnc != nil {
		ed := swEnc.ExtraData()
		if len(ed) > 0 {
			extradata = append([]byte(nil), ed...)
		}
	}

	// Timed measurement pass.
	{
		start := time.Now()
		var totalBytes int64
		for i := cfg.WarmupFrames; i < total; i++ {
			if e := sendFrame(frames[i]); e != nil && !IsEAgain(e) {
				err = e
				goto cleanup
			}
			// Drain all packets from this frame.
			for {
				pkt, allocErr := AllocPacket()
				if allocErr != nil {
					break
				}
				re := receivePacket(pkt)
				if IsEAgain(re) || re == ErrEOF {
					_ = pkt.Close()
					break
				}
				if re != nil {
					_ = pkt.Close()
					break
				}
				totalBytes += int64(pkt.Size())
				packets = append(packets, pkt)
			}
		}
		// Final flush.
		_ = sendFrame(nil)
		for {
			pkt, allocErr := AllocPacket()
			if allocErr != nil {
				break
			}
			re := receivePacket(pkt)
			if IsEAgain(re) || re == ErrEOF {
				_ = pkt.Close()
				break
			}
			if re != nil {
				_ = pkt.Close()
				break
			}
			totalBytes += int64(pkt.Size())
			packets = append(packets, pkt)
		}

		elapsed := time.Since(start).Seconds()
		measured := float64(cfg.MeasureFrames)
		if elapsed > 0 {
			fps = measured / elapsed
		}
		if elapsed > 0 && len(packets) > 0 {
			bitrateMbps = float64(totalBytes) * 8.0 / elapsed / 1_000_000
		}
	}

cleanup:
	for _, f := range frames {
		if f != nil {
			_ = f.Close()
		}
	}
	return
}

// benchDecode runs a decode benchmark by feeding the provided packets through a
// standalone software (or hardware) decoder.
func benchDecode(
	ctx context.Context,
	cfg BenchmarkConfig,
	decoderName string,
	res Resolution,
	packets []*Packet,
	extradata []byte,
) (fps float64, err error) {
	if len(packets) == 0 {
		return 0, nil
	}

	cName := C.CString(decoderName)
	defer C.free(unsafe.Pointer(cName))

	var extPtr *C.uint8_t
	extLen := C.int(0)
	if len(extradata) > 0 {
		extPtr = (*C.uint8_t)(unsafe.Pointer(&extradata[0]))
		extLen = C.int(len(extradata))
	}

	dec := C.mm_bench_open_decoder(cName,
		C.int(res.Width), C.int(res.Height),
		extPtr, extLen)
	if dec == nil {
		return 0, fmt.Errorf("decoder %q not available", decoderName)
	}
	defer C.mm_bench_close_decoder(dec)

	// Warmup pass.
	warmupPkts := cfg.WarmupFrames
	if warmupPkts > len(packets) {
		warmupPkts = len(packets)
	}
	for i := 0; i < warmupPkts; i++ {
		C.mm_bench_decode_packet(dec, packets[i%len(packets)].p)
	}

	// Timed pass.
	total := cfg.MeasureFrames
	start := time.Now()
	for i := 0; i < total; i++ {
		if ctx.Err() != nil {
			break
		}
		C.mm_bench_decode_packet(dec, packets[i%len(packets)].p)
	}
	elapsed := time.Since(start).Seconds()
	if elapsed > 0 {
		fps = float64(total) / elapsed
	}
	return
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// drainPackets discards all packets pending from the encoder.
func drainPackets(recv benchReceiveFn) {
	for {
		pkt, err := AllocPacket()
		if err != nil {
			return
		}
		if err := recv(pkt); err != nil {
			_ = pkt.Close()
			return
		}
		_ = pkt.Close()
	}
}

// freePackets closes and nils a slice of packets.
func freePackets(pkts []*Packet) {
	for _, p := range pkts {
		if p != nil {
			_ = p.Close()
		}
	}
}

// ffmpegVersionString returns the FFmpeg build version via av_version_info().
func ffmpegVersionString() string {
	return C.GoString(C.av_version_info())
}

// ExtraData returns the encoder's global-header extradata (AVCodecContext.extradata).
func (e *EncoderContext) ExtraData() []byte {
	if e.p == nil || e.p.extradata == nil || e.p.extradata_size == 0 {
		return nil
	}
	return C.GoBytes(unsafe.Pointer(e.p.extradata), e.p.extradata_size)
}
