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
