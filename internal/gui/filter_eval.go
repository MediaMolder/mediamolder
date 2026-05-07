// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/MediaMolder/MediaMolder/av"
)

// filterExprVars is the curated table of well-known constant names
// each filter exposes inside its expression options. Authoritative
// source: each filter's `var_names[]` array in libavfilter (e.g.
// vf_drawtext.c, vf_overlay.c, vf_crop.c, vf_scale.c).
//
// Keep entries minimal — every name added becomes part of the public
// validation contract. Unknown filter names fall back to a generic
// time/frame-counter set so users can still validate trivial
// `enable=` expressions.
var filterExprVars = map[string][]string{
	// drawtext: text rendering with overlay-style positioning.
	"drawtext": {
		"x", "y", "w", "h", "t", "n",
		"tw", "text_w", "th", "text_h",
		"main_w", "main_h", "W", "H",
		"sar", "dar", "hsub", "vsub",
		"line_h", "lh",
		"max_glyph_w", "max_glyph_h",
		"max_glyph_a", "ascent",
		"max_glyph_d", "descent",
		"pict_type", "pkt_pos", "pkt_duration", "pkt_size",
	},
	// overlay: overlay one video on another.
	"overlay": {
		"x", "y",
		"main_w", "W", "main_h", "H",
		"overlay_w", "w", "overlay_h", "h",
		"hsub", "vsub", "n", "pos", "t",
	},
	// crop: pick a rectangular sub-region.
	"crop": {
		"x", "y", "w", "h",
		"in_w", "iw", "in_h", "ih",
		"out_w", "ow", "out_h", "oh",
		"a", "sar", "dar", "hsub", "vsub", "n", "pos", "t",
	},
	// scale: resize.
	"scale": {
		"in_w", "iw", "in_h", "ih",
		"out_w", "ow", "out_h", "oh",
		"a", "sar", "dar", "hsub", "vsub", "ohsub", "ovsub",
	},
	// pad: extend canvas.
	"pad": {
		"in_w", "iw", "in_h", "ih",
		"out_w", "ow", "out_h", "oh",
		"x", "y", "a", "sar", "dar", "hsub", "vsub",
	},
	// rotate.
	"rotate": {
		"in_w", "iw", "in_h", "ih",
		"out_w", "ow", "out_h", "oh",
		"hsub", "vsub", "n", "t",
	},
	// zoompan.
	"zoompan": {
		"in_w", "iw", "in_h", "ih",
		"out_w", "ow", "out_h", "oh",
		"x", "y", "zoom", "pzoom", "px", "py",
		"on", "in", "duration", "pduration", "time", "frame",
	},
	// setpts / asetpts: wrap PTS arithmetic.
	"setpts": {
		"PTS", "PREV_INPTS", "PREV_OUTPTS", "STARTPTS",
		"INTERLACED", "N", "NB_CONSUMED_SAMPLES", "NB_SAMPLES",
		"POS", "PREV_INT", "PREV_OUT",
		"RTCTIME", "RTCSTART", "S", "SR", "T", "TB", "FR",
	},
	"asetpts": {
		"PTS", "PREV_INPTS", "PREV_OUTPTS", "STARTPTS",
		"N", "NB_CONSUMED_SAMPLES", "NB_SAMPLES",
		"S", "SR", "T", "TB", "FR",
	},
	// volume.
	"volume": {
		"n", "nb_channels", "nb_consumed_samples",
		"nb_samples", "pos", "pts", "sample_rate", "startpts", "t", "tb",
	},
	// select / aselect: frame-selection expressions.
	// Source: libavfilter/vf_select.c var_names[],
	//         libavfilter/af_aselect.c var_names[].
	"select": {
		"n", "pts", "t", "pos",
		"key", "scene",
		"interlace_type", "pict_type",
		"pkt_pts", "pkt_dts", "pkt_duration", "pkt_pos",
		"PICT_TYPE_I", "PICT_TYPE_P", "PICT_TYPE_B",
		"PICT_TYPE_S", "PICT_TYPE_SI", "PICT_TYPE_SP", "PICT_TYPE_BI",
		"concatdec_select",
	},
	"aselect": {
		"n", "pts", "t", "pos",
		"nb_samples", "sample_rate",
		"pkt_pts", "pkt_dts", "pkt_duration", "pkt_pos",
		"concatdec_select",
	},
	// hue: colour adjustment with expression options.
	// Source: libavfilter/vf_hue.c var_names[].
	"hue": {
		"n", "pts", "t", "r", "tb",
	},
	// geq: general pixel-equation filter.
	// Source: libavfilter/vf_geq.c var_names[].
	"geq": {
		"X", "Y", "W", "H", "N", "T",
		"BYTES", "MAXVAL",
		"SW", "SH",
		"lum", "cb", "cr", "alpha",
		"p", "l",
	},
	// trim / atrim: timeline clipping (enable= and inline time exprs).
	"trim": {
		"t", "n", "pos",
	},
	"atrim": {
		"t", "n", "pos", "s", "sr",
	},
}

// fallbackExprVars is used when the requested filter has no curated
// variable table. Covers the universal `enable=` timeline expression
// surface (see libavfilter/avfilter.c `enable_var_names[]`).
var fallbackExprVars = []string{"t", "n", "pos", "w", "h"}

// filterExprOptions is the curated registry of (filter, option) pairs
// whose value is parsed by libavutil's expression evaluator. The GUI
// uses it to render the syntax-highlighted expression input + cookbook
// (Wave 5 #19/#20). Authoritative source: each filter's option table
// in libavfilter (e.g. `vf_drawtext.c` flags `text_x`, `text_y`,
// `enable` as expressions; `vf_overlay.c` flags `x`, `y`; etc.).
//
// Keep entries minimal: every (filter, option) pair listed becomes
// part of the public schema contract surfaced by
// GET /api/filters/{name}/options.
var filterExprOptions = map[string]map[string]bool{
	"drawtext": {
		"x": true, "y": true,
		"text_x": true, "text_y": true,
		"box_w": true, "box_h": true,
		"fontsize": true, "alpha": true,
		"enable": true,
	},
	"overlay": {
		"x": true, "y": true,
		"enable": true,
	},
	"crop": {
		"x": true, "y": true, "w": true, "h": true,
		"out_w": true, "out_h": true,
		"enable": true,
	},
	"scale": {
		"w": true, "h": true,
		"width": true, "height": true,
		"eval": false, // an enum, NOT an expression itself
	},
	"pad": {
		"w": true, "h": true,
		"x": true, "y": true,
		"enable": true,
	},
	"rotate": {
		"angle": true, "a": true,
		"out_w": true, "ow": true,
		"out_h": true, "oh": true,
		"enable": true,
	},
	"zoompan": {
		"zoom": true, "z": true,
		"x": true, "y": true,
		"d": true, "fps": true,
		"enable": true,
	},
	"setpts":  {"expr": true},
	"asetpts": {"expr": true},
	"volume": {
		"volume": true,
		"enable": true,
	},
	"select": {
		"expr": true,
	},
	"aselect": {
		"expr": true,
	},
	"hue": {
		"h": true, "s": true, "b": true,
		"enable": true,
	},
	"geq": {
		"lum_expr": true, "cb_expr": true, "cr_expr": true,
		"r_expr": true, "g_expr": true, "b_expr": true, "a_expr": true,
		"red": true, "green": true, "blue": true, "alpha": true,
	},
	"trim": {
		"enable": true,
	},
	"atrim": {
		"enable": true,
	},
}

// FilterExprVariables returns the curated list of variable names
// recognised inside expression-typed options on the named filter.
// Falls back to the universal timeline set (`t`, `n`, `pos`, `w`,
// `h`) when the filter has no curated table.
func FilterExprVariables(filter string) []string {
	if vs, ok := filterExprVars[filter]; ok {
		return append([]string(nil), vs...)
	}
	return append([]string(nil), fallbackExprVars...)
}

// IsExpressionOption reports whether the (filter, option) pair is in
// the curated registry of expression-typed options. Returns false for
// unknown pairs (the safe default \u2014 a free-form text input is always
// a correct render for an unmarked AVOption).
func IsExpressionOption(filter, option string) bool {
	m, ok := filterExprOptions[filter]
	if !ok {
		return false
	}
	v, ok := m[option]
	return ok && v
}

// evalExpressionResponse mirrors the JSON shape served to the GUI.
//
// Ok=true when the expression parses and evaluates under the supplied
// bindings; Value carries the numeric result. Ok=false when libavutil
// rejects the expression — Error carries the av_strerror() message
// and Value is undefined.
type evalExpressionResponse struct {
	Filter    string             `json:"filter"`
	Expr      string             `json:"expr"`
	Variables map[string]float64 `json:"variables"`
	Ok        bool               `json:"ok"`
	Value     float64            `json:"value,omitempty"`
	Error     string             `json:"error,omitempty"`
}

// handleFilterEvalExpression serves
// GET /api/filters/{name}/eval-expression?expr=…&t=…&x=…&w=…
//
// Required: `expr`. Any other query parameter is treated as a numeric
// variable binding; values that fail to parse as float64 are ignored
// (so callers can leave hint inputs in the URL).
//
// Variables not provided default to 0 — exactly the convention
// libavfilter uses when an expression is evaluated before a frame is
// available. The set of accepted variables is the curated
// filterExprVars table; names outside that set are still passed
// through (so users can validate against custom constants), but the
// response carries the canonical set echoed back so the GUI can
// surface "unknown variable" hints.
//
// HTTP 200 is returned for both successful and failed evaluations —
// the body's `ok` flag is the truth. We only return non-200 when the
// request itself is malformed (missing expr).
func handleFilterEvalExpression(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("filter name is required"))
		return
	}
	expr := strings.TrimSpace(r.URL.Query().Get("expr"))
	if expr == "" {
		writeJSONError(w, http.StatusBadRequest, fmt.Errorf("expr query parameter is required"))
		return
	}

	// Curated variable set for the filter, plus any extras supplied
	// in the query string. Curated names default to 0, query-string
	// names override. This means a caller hitting drawtext with no
	// extra params can validate `enable=between(t,1,8)` immediately.
	known := filterExprVars[name]
	if known == nil {
		known = fallbackExprVars
	}
	vars := make(map[string]float64, len(known))
	for _, n := range known {
		vars[n] = 0
	}
	for k, v := range r.URL.Query() {
		if k == "expr" {
			continue
		}
		if len(v) == 0 {
			continue
		}
		if f, err := strconv.ParseFloat(v[0], 64); err == nil {
			vars[k] = f
		}
	}

	resp := evalExpressionResponse{
		Filter:    name,
		Expr:      expr,
		Variables: vars,
	}
	if val, err := av.EvalExpression(expr, vars); err != nil {
		resp.Ok = false
		resp.Error = err.Error()
	} else {
		resp.Ok = true
		resp.Value = val
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}
