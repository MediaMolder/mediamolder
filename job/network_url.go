// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package job

// network_url.go — Wave 11 #67: URL-scheme detection and advisory
// warnings for network-protocol inputs (RTSP, RTMP, SRT, RIST, RTP).
//
// Design notes
//
// None of the conditions detected here are hard errors: libavformat
// will still attempt to open the input, possibly with degraded
// reliability (e.g. UDP RTSP on a NATed network). The rules are
// therefore expressed as NormalizeWarnings (non-fatal observations)
// surfaced through the pipeline events channel rather than as
// validate() errors that would abort job submission.
//
// Typed GUI controls (rtsp_transport dropdown, stimeout field, SRT
// mode dropdown, listen_timeout field) write into Input.Options so
// the values reach libavformat as AVOption entries without requiring
// new typed fields on the Input struct. The compat/ffcli layer
// recognises the corresponding CLI flags and routes them into
// Input.Options on import; on export it emits them as per-input
// flags before -i.

import (
	"fmt"
	"strings"
)

// urlScheme returns the lower-cased URL scheme (the token before "://"),
// or "" if the URL contains no recognisable "://"-delimited scheme.
func urlScheme(url string) string {
	if i := strings.Index(url, "://"); i > 0 {
		return strings.ToLower(url[:i])
	}
	return ""
}

// isNetworkInput reports whether url identifies a live-network source
// that may benefit from protocol-specific AVOptions.
//
// Recognised schemes: rtsp, rtsps, rtmp, rtmps, rtmpe, rtmpt, rtmpte,
// srt, rist, rtp.
func isNetworkInput(url string) bool {
	switch urlScheme(url) {
	case "rtsp", "rtsps",
		"rtmp", "rtmps", "rtmpe", "rtmpt", "rtmpte",
		"srt",
		"rist",
		"rtp":
		return true
	}
	return false
}

// networkInputWarnings returns advisory NormalizeWarnings for network-URL
// inputs based on protocol-specific option-combo heuristics.
//
// Current rules (Wave 11 #67):
//
//   - rtsp / rtsps without rtsp_transport set: UDP is the libavformat
//     default but fails silently on many NAT/firewall environments;
//     TCP is more reliable in production deployments.
//
//   - srt in listener mode (Options["mode"] == "listener") without
//     listen_timeout: the demuxer will block indefinitely waiting for
//     an incoming connection, stalling the pipeline.
func networkInputWarnings(inputs []Input) []NormalizeWarning {
	var ws []NormalizeWarning
	for i, inp := range inputs {
		path := fmt.Sprintf("inputs[%d]", i)
		switch urlScheme(inp.URL) {
		case "rtsp", "rtsps":
			if _, ok := inp.Options["rtsp_transport"]; !ok {
				ws = append(ws, NormalizeWarning{
					Code: "input.rtsp.no_transport",
					Message: fmt.Sprintf(
						"RTSP input %q: rtsp_transport not set; "+
							"libavformat defaults to UDP, which may fail "+
							"on NAT/firewall environments — "+
							`set Options["rtsp_transport"]="tcp" for `+
							"reliable connections",
						inp.ID),
					Path: path + ".options.rtsp_transport",
				})
			}
		case "srt":
			mode, _ := inp.Options["mode"].(string)
			if mode == "listener" {
				if _, ok := inp.Options["listen_timeout"]; !ok {
					ws = append(ws, NormalizeWarning{
						Code: "input.srt.listener_no_timeout",
						Message: fmt.Sprintf(
							"SRT listener input %q: listen_timeout not set; "+
								"the demuxer will block indefinitely waiting "+
								"for an incoming connection — "+
								`set Options["listen_timeout"] in microseconds `+
								"(e.g. 30000000 for 30 s)",
							inp.ID),
						Path: path + ".options.listen_timeout",
					})
				}
			}
		}
	}
	return ws
}
