// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavformat/avformat.h"
// #include "libavcodec/avcodec.h"
// #include "libavcodec/bsf.h"
//
// // Allocate a BSF chain by parsing an FFmpeg `-bsf` chain spec
// // (`f1[=k=v[:k=v]][,f2[=k=v]]`) via av_bsf_list_parse_str. Returns
// // the AVBSFContext on success; the caller owns the result and must
// // free it with av_bsf_free.
// static int bsf_list_parse(const char *spec, AVBSFContext **out) {
//     return av_bsf_list_parse_str(spec, out);
// }
// // Seed a BSF context's input parameters from an output muxer
// // stream's codecpar / time_base, init the chain, then copy the
// // BSF's output parameters back onto the stream so the muxer's
// // WriteHeader sees the post-BSF codec configuration (matches
// // fftools/ffmpeg_mux.c::bsf_init).
// static int bsf_attach_to_stream(AVBSFContext *bsf, AVFormatContext *fc,
//                                 int idx) {
//     if (!bsf || !fc || idx < 0 || idx >= (int)fc->nb_streams) return -1;
//     AVStream *st = fc->streams[idx];
//     int ret = avcodec_parameters_copy(bsf->par_in, st->codecpar);
//     if (ret < 0) return ret;
//     bsf->time_base_in = st->time_base;
//     ret = av_bsf_init(bsf);
//     if (ret < 0) return ret;
//     ret = avcodec_parameters_copy(st->codecpar, bsf->par_out);
//     if (ret < 0) return ret;
//     st->time_base = bsf->time_base_out;
//     return 0;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// AttachStreamBSF parses an FFmpeg `-bsf` chain spec
// (`f1[=k=v[:k=v]][,f2]`), seeds the chain's input parameters from
// output stream `idx`'s codecpar / time_base, initializes the chain,
// and copies the chain's output parameters back onto the stream so
// the muxer's WriteHeader sees the post-BSF codec configuration.
// Mirrors fftools/ffmpeg_mux.c::bsf_init. Must be called after
// AddStream / AddStreamFromInput and before WriteHeader.
func (f *OutputFormatContext) AttachStreamBSF(idx int, spec string) (*BitstreamFilter, error) {
	if f == nil || f.p == nil {
		return nil, fmt.Errorf("AttachStreamBSF: nil format context")
	}
	if spec == "" {
		return nil, fmt.Errorf("AttachStreamBSF: empty chain spec")
	}
	cSpec := C.CString(spec)
	defer C.free(unsafe.Pointer(cSpec))

	var ctx *C.AVBSFContext
	if ret := C.bsf_list_parse(cSpec, &ctx); ret < 0 {
		return nil, fmt.Errorf("av_bsf_list_parse_str(%q): %w", spec, newErr(ret))
	}
	if ret := C.bsf_attach_to_stream(ctx, f.p, C.int(idx)); ret < 0 {
		C.av_bsf_free(&ctx)
		return nil, fmt.Errorf("attach bsf chain %q to stream %d: %w", spec, idx, newErr(ret))
	}
	return &BitstreamFilter{p: ctx}, nil
}

// OutputTimeBase returns the BSF's output time_base as {num, den}.
// Valid only after Init / AttachStreamBSF.
func (b *BitstreamFilter) OutputTimeBase() [2]int {
	if b == nil || b.p == nil {
		return [2]int{0, 0}
	}
	tb := b.p.time_base_out
	return [2]int{int(tb.num), int(tb.den)}
}
