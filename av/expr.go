// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// Standalone wrapper for libavutil's expression evaluator
// (libavutil/eval.h). Used by the GUI to syntax-check filter
// expressions like drawtext's `enable=between(t,1,8)` or overlay's
// `x=if(gte(t,2),W-w,0)` without instantiating the filter graph.
//
// The evaluator only knows about the constants (variables) the caller
// supplies. It does NOT know about the filter-private variables
// (drawtext's `tw`, `th`, overlay's `main_w` …) unless the caller
// names them. The HTTP layer is responsible for the filter→variable
// table; this package stays filter-agnostic.

// #include <stdlib.h>
// #include "libavutil/eval.h"
//
// // eval_expr is a thin shim so the Go side can pass parallel
// // (names[], values[]) arrays without juggling void* function tables.
// // Returns 0 on success, a negative AVERROR on parse / unknown-name /
// // div-by-zero failure. The result is written to *out.
// static int eval_expr(const char *expr,
//                      const char **names,
//                      const double *values,
//                      double *out) {
//     return av_expr_parse_and_eval(out, expr,
//         names, values,
//         NULL, NULL,
//         NULL, NULL,
//         NULL, 0, NULL);
// }
//
// // parse_expr compiles an expression once for repeated evaluation,
// // mirroring the cached AVExpr* fftools/ffmpeg_mux_init.c stores on
// // each MuxStream for `-force_key_frames "expr:..."`. Returns 0 on
// // success and writes the compiled expression to *out (caller must
// // free with av_expr_free).
// static int parse_expr(const char *expr,
//                       const char **names,
//                       AVExpr **out) {
//     return av_expr_parse(out, expr, names, NULL, NULL, NULL, NULL, 0, NULL);
// }
//
// // eval_compiled runs a previously-parsed expression with a fresh
// // value vector. Faster than av_expr_parse_and_eval when the same
// // expression is evaluated thousands of times (e.g. once per frame).
// static double eval_compiled(AVExpr *e, const double *values) {
//     return av_expr_eval(e, values, NULL);
// }
//
// static void free_expr(AVExpr *e) { av_expr_free(e); }
import "C"

import (
	"sort"
	"unsafe"
)

// EvalExpression validates and evaluates a single libavutil expression
// using the supplied named constants. The result is the numeric value
// the expression evaluates to under the given variable bindings.
//
// Returns a non-nil error when the expression fails to parse, refers
// to a name not in vars, or hits a runtime fault (e.g. division by
// zero). Callers that only care about syntactic validity can ignore
// the returned value.
//
// This is the engine behind the GUI's `eval-expression` smoke-test
// endpoint and is intentionally filter-agnostic: pass whatever
// constants the calling filter is documented to expose.
func EvalExpression(expr string, vars map[string]float64) (float64, error) {
	cExpr := C.CString(expr)
	defer C.free(unsafe.Pointer(cExpr))

	// Stable iteration order so the parallel names[]/values[] pair is
	// deterministic (helps when we eventually log failures).
	names := make([]string, 0, len(vars))
	for k := range vars {
		names = append(names, k)
	}
	sort.Strings(names)

	// Build a NULL-terminated **char and a parallel double[]. Cgo
	// won't let us pass Go slices of *C.char directly to a C function
	// expecting `const char **`, so we allocate a malloc'd table.
	cNames := C.malloc(C.size_t(len(names)+1) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(cNames)
	nameSlice := (*[1 << 16]*C.char)(cNames)[: len(names)+1 : len(names)+1]
	for i, n := range names {
		nameSlice[i] = C.CString(n)
		defer C.free(unsafe.Pointer(nameSlice[i]))
	}
	nameSlice[len(names)] = nil

	var cValues unsafe.Pointer
	if len(names) > 0 {
		cValues = C.malloc(C.size_t(len(names)) * C.size_t(unsafe.Sizeof(C.double(0))))
		defer C.free(cValues)
		valSlice := (*[1 << 16]C.double)(cValues)[:len(names):len(names)]
		for i, n := range names {
			valSlice[i] = C.double(vars[n])
		}
	}

	var out C.double
	ret := C.eval_expr(cExpr, (**C.char)(cNames), (*C.double)(cValues), &out)
	if err := newErr(ret); err != nil {
		return 0, err
	}
	return float64(out), nil
}

// ParsedExpression wraps a compiled libavutil AVExpr for repeated
// evaluation. Created by ParseExpression; freed by Close. The names
// passed at parse time fix the variable order — Eval takes a values
// slice in the same order.
//
// This mirrors the per-MuxStream cached AVExpr fftools/ffmpeg_mux_init.c
// builds for `-force_key_frames "expr:..."` so the per-frame hot loop
// only does av_expr_eval (no re-parse).
type ParsedExpression struct {
	p     *C.AVExpr
	names []string
}

// ParseExpression compiles expr once. names enumerates the variable
// constants the expression may reference (any other identifier causes
// a parse-time error). Caller must Close the returned expression.
func ParseExpression(expr string, names []string) (*ParsedExpression, error) {
	cExpr := C.CString(expr)
	defer C.free(unsafe.Pointer(cExpr))

	cNames := C.malloc(C.size_t(len(names)+1) * C.size_t(unsafe.Sizeof(uintptr(0))))
	defer C.free(cNames)
	nameSlice := (*[1 << 16]*C.char)(cNames)[: len(names)+1 : len(names)+1]
	for i, n := range names {
		nameSlice[i] = C.CString(n)
		defer C.free(unsafe.Pointer(nameSlice[i]))
	}
	nameSlice[len(names)] = nil

	var out *C.AVExpr
	ret := C.parse_expr(cExpr, (**C.char)(cNames), &out)
	if err := newErr(ret); err != nil {
		return nil, err
	}
	pe := &ParsedExpression{p: out, names: append([]string(nil), names...)}
	leakTrack(unsafe.Pointer(out), "AVExpr")
	return pe, nil
}

// Eval runs the parsed expression with values lined up against the
// names passed to ParseExpression. Returns NaN if libavutil's evaluator
// hits an internal fault (rare for well-formed expressions).
func (e *ParsedExpression) Eval(values []float64) float64 {
	if e == nil || e.p == nil {
		return 0
	}
	if len(values) != len(e.names) {
		return 0
	}
	cVals := C.malloc(C.size_t(len(values)) * C.size_t(unsafe.Sizeof(C.double(0))))
	defer C.free(cVals)
	vs := (*[1 << 16]C.double)(cVals)[:len(values):len(values)]
	for i, v := range values {
		vs[i] = C.double(v)
	}
	return float64(C.eval_compiled(e.p, (*C.double)(cVals)))
}

// Close releases the underlying AVExpr. Safe to call on a nil receiver.
func (e *ParsedExpression) Close() {
	if e == nil || e.p == nil {
		return
	}
	leakUntrack(unsafe.Pointer(e.p))
	C.free_expr(e.p)
	e.p = nil
}
