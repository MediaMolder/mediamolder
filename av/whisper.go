// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

//go:build with_whisper

// Thin CGO wrapper over whisper.cpp's C API (whisper.h), in the style of
// resample.go. Compiled only with the "with_whisper" build tag. The actual
// link flags live in cgo_flags_whisper.go (pkg-config) and
// cgo_flags_whisper_static.go (local source tree).
//
// The progress and abort callbacks are bridged to Go via runtime/cgo.Handle:
// the C trampolines below forward to the //export'd functions in whisper_cb.go
// (which must live in a separate file, since a file using //export may not
// define functions in its preamble).
package av

// #include <whisper.h>
// #include <stdint.h>
// #include <stdlib.h>
//
// // Go-exported trampolines (defined in whisper_cb.go).
// extern void mmWhisperProgressGo(int progress, void *ud);
// extern int  mmWhisperAbortGo(void *ud);
//
// static void mm_progress_cb(struct whisper_context *c, struct whisper_state *s, int progress, void *ud) {
//     (void)c; (void)s;
//     mmWhisperProgressGo(progress, ud);
// }
// static bool mm_abort_cb(void *ud) {
//     return mmWhisperAbortGo(ud) != 0;
// }
//
// static struct whisper_context *mm_whisper_init(const char *path) {
//     struct whisper_context_params cp = whisper_context_default_params();
//     return whisper_init_from_file_with_params(path, cp);
// }
//
// // Average per-token probability over a segment, used as a 0..1 confidence.
// static float mm_segment_avg_prob(struct whisper_context *ctx, int i) {
//     int n = whisper_full_n_tokens(ctx, i);
//     if (n <= 0) return 0.0f;
//     float sum = 0.0f;
//     for (int j = 0; j < n; j++) {
//         struct whisper_token_data td = whisper_full_get_token_data(ctx, i, j);
//         sum += td.p;
//     }
//     return sum / (float)n;
// }
//
// static int mm_whisper_run(struct whisper_context *ctx,
//                           const float *samples, int n_samples,
//                           int strategy, int n_threads, int translate,
//                           const char *language, int token_timestamps,
//                           int beam_size, const char *initial_prompt,
//                           int max_text_ctx, uintptr_t user_data) {
//     struct whisper_full_params p =
//         whisper_full_default_params((enum whisper_sampling_strategy)strategy);
//     p.n_threads        = n_threads;
//     p.translate        = translate != 0;
//     p.language         = language; // "auto" => autodetect
//     p.token_timestamps = token_timestamps != 0;
//     p.print_progress   = false;
//     p.print_realtime   = false;
//     p.print_timestamps = false;
//     p.print_special    = false;
//     if (strategy == WHISPER_SAMPLING_BEAM_SEARCH && beam_size > 0) {
//         p.beam_search.beam_size = beam_size;
//     }
//     if (initial_prompt != NULL && initial_prompt[0] != '\0') {
//         p.initial_prompt = initial_prompt;
//     }
//     // Negative means "leave whisper.cpp's default" — 0 is a MEANINGFUL value here
//     // (it disables cross-window prompting), so it cannot double as "unset".
//     if (max_text_ctx >= 0) {
//         p.n_max_text_ctx = max_text_ctx;
//     }
//     p.progress_callback           = mm_progress_cb;
//     p.progress_callback_user_data = (void *)user_data;
//     p.abort_callback              = mm_abort_cb;
//     p.abort_callback_user_data    = (void *)user_data;
//     return whisper_full(ctx, p, samples, n_samples);
// }
import "C"

import (
	"context"
	"fmt"
	"math"
	"runtime"
	"runtime/cgo"
	"strings"
	"time"
	"unsafe"
)

// WhisperSampleRate is the input sample rate whisper.cpp requires: 16 kHz mono.
const WhisperSampleRate = 16000

// WhisperModel wraps a loaded whisper.cpp context. It must be closed via Close.
type WhisperModel struct {
	ctx *C.struct_whisper_context
}

// WhisperOptions configures a single transcription run.
type WhisperOptions struct {
	Language       string // "auto" (default) or an ISO code like "en"
	Translate      bool   // translate to English instead of transcribing
	Threads        int    // inference threads (<=0 => runtime.NumCPU())
	BeamSize       int    // <=1 greedy; >1 beam search
	WordTimestamps bool   // request token-level timestamps
	InitialPrompt  string // optional context/biasing prompt

	// MaxTextCtx caps how many tokens of ALREADY-DECODED text are fed back as the prompt
	// for the next 30-second window (whisper.cpp `n_max_text_ctx`, the CLI's -mc). nil
	// leaves whisper.cpp's default of 16384; 0 disables the feedback entirely.
	//
	// A pointer, not an int, precisely because 0 is the interesting value: it cannot double
	// as "unset" the way Threads<=0 can, and a plain int would silently change behaviour for
	// every existing caller the moment this field was added.
	//
	// Why it matters: whisper decodes long audio as a sliding window and, by default,
	// prompts each window with the previous window's text. On long or noisy recordings one
	// bad line becomes the next window's prompt and locks — the well-known Whisper long-form
	// repetition failure, where a single sentence repeats until end of file. Measured on a
	// 48-minute tape capture: worst verbatim repeat 1,250 at the default, 27 with 0. The
	// trade is real — cross-window context genuinely helps phrasing, and disabling it cost
	// ~9% unique lines on clean audio — which is why this is an option and not a new default.
	// Batch transcription of whole files generally wants 0; streaming/interactive callers,
	// where each call is already a short window, generally do not.
	MaxTextCtx *int
}

// Validate rejects option combinations whisper.cpp accepts and then silently mishandles.
// Both cases below fail QUIETLY inside whisper_full — the caller gets a plausible transcript
// and no indication that what they asked for was discarded — so refusing up front is the only
// way the API can be honest.
func (o WhisperOptions) Validate() error {
	if o.MaxTextCtx == nil {
		return nil
	}
	switch n := *o.MaxTextCtx; {
	case n < 0:
		return fmt.Errorf("whisper: MaxTextCtx %d is invalid (use nil for the default, 0 to disable context)", n)
	case n == 0 && o.InitialPrompt != "":
		// InitialPrompt reaches the decoder ONLY through the same prompt-history path that
		// n_max_text_ctx gates, so 0 discards it entirely — verified by identical output with
		// and without a prompt at MaxTextCtx 0. Erroring beats honouring one and dropping the
		// other without saying so.
		return fmt.Errorf("whisper: MaxTextCtx 0 disables the prompt history that InitialPrompt " +
			"is delivered through, so the prompt would be silently ignored — set MaxTextCtx to a " +
			"positive value (>= 2), or drop InitialPrompt")
	case n == 1:
		// whisper.cpp 1.8.4 computes a take-count of (max_prompt_ctx - 1 - 1) and indexes
		// backwards from prompt_past.end() by it; at 1 that is -1, walking past end(). The
		// domain is {0} union [2, inf).
		return fmt.Errorf("whisper: MaxTextCtx 1 is not a usable value (whisper.cpp reads out of " +
			"bounds); use 0 to disable context or >= 2")
	}
	return nil
}

// WhisperSegment is one transcribed span returned by Full.
type WhisperSegment struct {
	Start      time.Duration
	End        time.Duration
	Text       string
	Confidence float64 // mean per-token probability, 0..1
}

// NewWhisperModel loads a ggml/gguf Whisper model from path.
func NewWhisperModel(path string) (*WhisperModel, error) {
	cPath := C.CString(path)
	defer C.free(unsafe.Pointer(cPath))
	ctx := C.mm_whisper_init(cPath)
	if ctx == nil {
		return nil, fmt.Errorf("whisper: failed to load model %q", path)
	}
	return &WhisperModel{ctx: ctx}, nil
}

// Close frees the underlying whisper context.
func (m *WhisperModel) Close() error {
	if m != nil && m.ctx != nil {
		C.whisper_free(m.ctx)
		m.ctx = nil
	}
	return nil
}

// whisperCallbacks carries the Go state the C trampolines recover via a
// cgo.Handle: the cancellation context (abort) and the progress sink.
type whisperCallbacks struct {
	ctx      context.Context
	progress func(pct int)
}

// Full transcribes 16 kHz mono float32 samples and returns the resulting
// segments. progress, if non-nil, is called with 0..100 percentages during
// inference. A cancelled ctx aborts the run (whisper returns non-zero and Full
// returns ctx.Err()).
func (m *WhisperModel) Full(ctx context.Context, samples []float32, opts WhisperOptions, progress func(pct int)) ([]WhisperSegment, error) {
	if m == nil || m.ctx == nil {
		return nil, fmt.Errorf("whisper: model is closed")
	}
	if len(samples) == 0 {
		return nil, nil
	}
	if len(samples) > math.MaxInt32 {
		// whisper_full takes the sample count as a C int; guard the narrowing
		// rather than passing a wrapped/negative count (~37 h at 16 kHz mono).
		return nil, fmt.Errorf("whisper: too many samples (%d exceeds int32 max)", len(samples))
	}
	if ctx == nil {
		ctx = context.Background()
	}
	if err := opts.Validate(); err != nil {
		return nil, err
	}

	cb := &whisperCallbacks{ctx: ctx, progress: progress}
	h := cgo.NewHandle(cb)
	defer h.Delete()

	strategy := C.WHISPER_SAMPLING_GREEDY
	if opts.BeamSize > 1 {
		strategy = C.WHISPER_SAMPLING_BEAM_SEARCH
	}

	lang := opts.Language
	if lang == "" {
		lang = "auto"
	}
	cLang := C.CString(lang)
	defer C.free(unsafe.Pointer(cLang))

	var cPrompt *C.char
	if opts.InitialPrompt != "" {
		cPrompt = C.CString(opts.InitialPrompt)
		defer C.free(unsafe.Pointer(cPrompt))
	}

	threads := opts.Threads
	if threads <= 0 {
		threads = runtime.NumCPU()
	}

	maxTextCtx := -1 // negative => leave whisper.cpp's default
	if opts.MaxTextCtx != nil {
		maxTextCtx = *opts.MaxTextCtx
		if maxTextCtx < 0 {
			maxTextCtx = 0 // an explicit negative means "no context", not "default"
		}
	}

	ret := C.mm_whisper_run(m.ctx,
		(*C.float)(unsafe.Pointer(&samples[0])), C.int(len(samples)),
		C.int(strategy), C.int(threads), boolToCInt(opts.Translate),
		cLang, boolToCInt(opts.WordTimestamps),
		C.int(opts.BeamSize), cPrompt, C.int(maxTextCtx), C.uintptr_t(h))
	if ret != 0 {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("whisper: whisper_full failed (code %d)", int(ret))
	}

	n := int(C.whisper_full_n_segments(m.ctx))
	segs := make([]WhisperSegment, 0, n)
	for i := 0; i < n; i++ {
		ci := C.int(i)
		t0 := int64(C.whisper_full_get_segment_t0(m.ctx, ci))
		t1 := int64(C.whisper_full_get_segment_t1(m.ctx, ci))
		text := C.GoString(C.whisper_full_get_segment_text(m.ctx, ci))
		segs = append(segs, WhisperSegment{
			// whisper t0/t1 are in centiseconds (10 ms units).
			Start:      time.Duration(t0) * 10 * time.Millisecond,
			End:        time.Duration(t1) * 10 * time.Millisecond,
			Text:       strings.TrimSpace(text),
			Confidence: float64(C.mm_segment_avg_prob(m.ctx, ci)),
		})
	}
	return segs, nil
}

func boolToCInt(b bool) C.int {
	if b {
		return 1
	}
	return 0
}
