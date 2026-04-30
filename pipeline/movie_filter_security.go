// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package pipeline

import (
	"fmt"
	"strings"
)

// Wave 7 #36e — `movie` / `amovie` second-asset support.
//
// The `movie` and `amovie` libavfilter sources open an external file
// via libavformat and emit it as additional video / audio frames into
// the filter graph. They are the only members of the
// knownFilterSources allow-list that touch the filesystem (or, with a
// permissive protocol_whitelist, the network) — every other source
// (`color`, `testsrc`, `sine`, `anullsrc`, …) is purely synthetic.
//
// This file enforces the security perimeter for those two filters:
//
//   - The `filename` parameter is required and must not contain NUL,
//     CR, or LF bytes (which would either truncate the libavfilter
//     args parser early or break out of the args string entirely and
//     inject a synthetic filter chain).
//
//   - When the node carries a `protocol_whitelist` parameter (a comma-
//     separated list, mirroring the per-Input field of the same name),
//     it is rewritten as a `format_opts` entry on the libavfilter
//     spec so libavformat actually honours it inside the movie demuxer.
//
// The validator runs after validateFilterAvailability so unknown
// filters never reach this code. The format_opts injection runs
// inside createFilterSource (pipeline/handlers.go) before
// buildFilterSpec so the existing quoting machinery escapes any
// commas in the whitelist value.
//
// Asset-manager hook (Wave 8 #51): when a managed asset registry
// lands, this is the integration point — resolve the `filename`
// parameter through the asset manager (caching, deduplication,
// remote-fetch policy) before it reaches libavformat.

// validateMovieFilterParams enforces the security perimeter for
// `movie` / `amovie` filter_source nodes. Called from validate() in
// config.go after validateFilterAvailability.
func validateMovieFilterParams(cfg *Config) error {
	for i, node := range cfg.Graph.Nodes {
		if node.Type != "filter_source" {
			continue
		}
		if node.Filter != "movie" && node.Filter != "amovie" {
			continue
		}
		fn, ok := node.Params["filename"].(string)
		if !ok {
			// movie/amovie also accept a positional filename via the
			// `_pos0` shorthand (e.g. ffcli's `movie=logo.png`); accept
			// either form.
			if pv, ok := node.Params["_pos0"].(string); ok {
				fn = pv
			}
		}
		if strings.TrimSpace(fn) == "" {
			return fmt.Errorf("node[%d] %q: filter %q requires a non-empty filename parameter", i, node.ID, node.Filter)
		}
		if strings.ContainsAny(fn, "\x00\r\n") {
			return fmt.Errorf("node[%d] %q: filter %q filename contains NUL, CR or LF (potential argument injection)", i, node.ID, node.Filter)
		}
		// protocol_whitelist, when provided, is also enforced for
		// well-formedness now so we don't surface an opaque libavformat
		// error at runtime.
		if pwRaw, ok := node.Params["protocol_whitelist"]; ok {
			pw, _ := pwRaw.(string)
			if strings.TrimSpace(pw) == "" {
				return fmt.Errorf("node[%d] %q: filter %q protocol_whitelist must be non-empty when set", i, node.ID, node.Filter)
			}
			for _, p := range strings.Split(pw, ",") {
				if strings.TrimSpace(p) == "" {
					return fmt.Errorf("node[%d] %q: filter %q protocol_whitelist contains empty entry", i, node.ID, node.Filter)
				}
			}
		}
	}
	return nil
}

// movieFilterParamsForSpec returns a copy of params with the
// pipeline-level `protocol_whitelist` shortcut rewritten as a
// `format_opts` entry that libavformat actually honours when invoked
// from inside the `movie` / `amovie` filter. If params already carry
// a `format_opts`, it is left untouched (the user opted into the raw
// libavformat dictionary). Wave 7 #36e.
func movieFilterParamsForSpec(filter string, params map[string]any) map[string]any {
	if filter != "movie" && filter != "amovie" {
		return params
	}
	pwRaw, ok := params["protocol_whitelist"]
	if !ok {
		return params
	}
	if _, has := params["format_opts"]; has {
		return params
	}
	pw, _ := pwRaw.(string)
	if pw == "" {
		return params
	}
	out := make(map[string]any, len(params))
	for k, v := range params {
		if k == "protocol_whitelist" {
			continue
		}
		out[k] = v
	}
	// libavformat's av_dict_parse_string uses '=' for key/value and
	// ',' for pair separator — so within format_opts we use the same
	// syntax: protocol_whitelist=p1|p2 won't work, we need
	// "protocol_whitelist=p1,p2". The outer buildFilterSpec quote()
	// wraps any value containing ',' or ';' in single quotes, so the
	// libavfilter args tokenizer keeps the whole thing as one value.
	out["format_opts"] = "protocol_whitelist=" + pw
	return out
}
