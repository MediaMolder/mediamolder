// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Public entry point for the per-pair frame cost computation.
// Milestone 2: ports slicetype_frame_cost (x264/encoder/slicetype.c).

package imgmath

// FrameCost returns the aggregate lookahead costs for predicting cur from ref.
//
//   - interCost: sum over interior 8×8 MBs of min(inter_SATD, intra_SATD),
//     matching i_cost_est[lag][0] in x264_frame_t.
//   - intraCost: sum over interior 8×8 MBs of intra_SATD,
//     matching i_cost_est[0][0] in x264_frame_t.
//
// lag is 0-based: lag=0 means ref is the immediately preceding frame (the
// most common case); lag=k means ref is k+1 frames before cur. The value
// selects the MV cache slot in cur, matching x264's lowres_mvs[0][b-p0-1]
// indexing. All (ref, cur) pairs in the cost matrix (Milestone 3) must use
// a consistent lag derived from their temporal distance.
//
// MV results for each interior MB are cached on the first call; subsequent
// calls with the same lag return cached values. Intra costs are also cached
// after the first call. This enables x264-style predictor reuse when the same
// (ref, cur) pair is queried multiple times (e.g. from different analysis
// passes).
//
// Mirrors slicetype_frame_cost(h, a, frames, p0=refIdx, p1=curIdx, b=curIdx)
// (x264/encoder/slicetype.c).
func FrameCost(ref, cur *LowresFrame, lag int) (interCost, intraCost int32) {
	return LowresFrameCost(cur, ref, lag)
}
