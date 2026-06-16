// Copyright (C) 2025-2026 MediaMolder contributors.
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package lookahead builds the cost-ratio matrix used by the motion-
// compensated scene detector. It wraps the pure-Go x264 lookahead engine in
// lookahead/internal. The primary API is batch-oriented: accumulate frames
// via AddFrame (or NewLookaheadScannerWithLags + AddFrame), then run
// LookaheadAnalyzer.Analyze on the completed CostMatrix (see also the
// staged coarse+refine path for performance on long-dissolve workloads).
package lookahead

import (
	"fmt"
	"sort"

	imgmath "github.com/MediaMolder/MediaMolder/lookahead/internal"
)

// CostMatrix holds the per-frame inter/intra cost data computed by a
// LookaheadScanner.
//
// Ratio[j][i] is the inter/intra cost ratio for frame j at lag Lags[i].
// The reference frame for Ratio[j][i] is frame j − Lags[i].
// Entries where Lags[i] ≥ j are zero (not enough history yet).
//
// Lags is the Fibonacci lag schedule used when scanning: a dense prefix
// {1,2,3,5,8} plus Fibonacci values up to L.  All inner loops that
// previously iterated over k ∈ [0, L) now iterate over enumerate(Lags).
//
// IntraCost[j] is the aggregate intra SATD for frame j at lowres (sum over
// interior 8×8 MBs). It is in raw SATD units; normalise by the number of MBs
// before comparing across resolutions.
//
// InterCost[j][i] is the raw aggregate inter-frame (P-frame / motion-compensated)
// prediction cost for frame j predicted from the reference at lag Lags[i]
// (lowres SATD after integer ME + penalties, or intra if better). Parallel to Ratio.
// These are the values actually used inside the x264-ported lookahead for
// the inter vs intra decision (min(inter, intra) per MB summed).
//
// AvgLuma[j] is the mean 8-bit luma value of the active lowres region for
// frame j, in [0, 255]. It is cheap to compute (one pass over the half-res
// plane already built by the scanner) and provides a direct brightness signal
// for fade-to-black / fade-to-white detection.
type CostMatrix struct {
	N         int         // frames accumulated so far
	L         int         // maximum lag (lookahead window length)
	Lags      []int       // Fibonacci lag schedule, e.g. [1,2,3,5,8,13,21,34]
	IntraCost []float32   // [N] — aggregate intra cost per frame
	InterCost [][]float32 // [N][len(Lags)] — raw inter prediction costs per lag (x264 pCost)
	AvgLuma   []float32   // [N] — mean luma [0,255] per frame
	Energy    []float32   // [N] — psy AC energy (SATD−SAD/2 vs zero), raw interior-MB sum
	Ratio     [][]float32 // [N][len(Lags)] — Ratio[j][i] for lag Lags[i]

	// Full-resolution edge measurements (see fullres.go), populated only
	// around detected dissolve edges when a FrameProvider is configured.
	// Same row/column conventions as RevRatio.
	FrLags     []int       // forward full-res distances (first-seen order)
	FrRatio    [][]float32 // [N][len(FrLags)] — 0 where not computed
	FrRevLags  []int       // reverse full-res distances
	FrRevRatio [][]float32 // [N][len(FrRevLags)]

	// AvgU/AvgV are per-frame mean chroma (full-range BT.601, centred on
	// 128). The scanner itself sees luma only; callers with colour frames
	// (the scene_change_mc processor) append these alongside AddFrame.
	// Empty/short slices simply exclude the chroma channels from the
	// mean-step edge refinement (see meanstep.go).
	AvgU []float32 // [N] when populated
	AvgV []float32 // [N] when populated

	// Backward-looking (reverse) prediction, populated only where dissolve-end
	// refinement ran (zero elsewhere). RevRatio[j][i] is the inter/intra ratio
	// for predicting frame j from the FUTURE reference j + RevLags[i]. Indexed by
	// the predicted frame j, mirroring Ratio.
	RevLags  []int       // reverse lag values (in first-seen order)
	RevRatio [][]float32 // [N][len(RevLags)] — reverse ratios, 0 where not computed
}

// ensureRevCol returns the RevRatio column index for reverse lag k, appending a
// new (zero-filled) column across all rows the first time k is seen.
func (m *CostMatrix) ensureRevCol(k int) int {
	for i, kk := range m.RevLags {
		if kk == k {
			return i
		}
	}
	m.RevLags = append(m.RevLags, k)
	col := len(m.RevLags) - 1
	if m.RevRatio == nil {
		m.RevRatio = make([][]float32, m.N)
	}
	for j := 0; j < m.N; j++ {
		for len(m.RevRatio[j]) <= col {
			m.RevRatio[j] = append(m.RevRatio[j], 0)
		}
	}
	return col
}

// LookaheadScanner accumulates 8-bit luma frames and builds a CostMatrix
// incrementally. It maintains a ring buffer of the last L+1 lowres frames
// so that each new frame can be compared against the configured lag schedule.
//
// After the removal of streaming mode the normal usage is batch-oriented:
// accumulate the whole input with a cheap small-lag schedule, optionally call
// RetainAllLowres() so that AllLowres() can later supply frames for targeted
// extra measurements, then run LookaheadAnalyzer.Analyze or AnalyzeStaged.
//
// Refine lets a caller add columns for specific lags only for frames inside
// chosen temporal windows (the core of the staged/incremental strategy for
// long-dissolve accuracy at low average cost).
//
// The scanner is not safe for concurrent use.
type LookaheadScanner struct {
	l         int                    // lookahead length (= matrix L)
	lags      []int                  // configured lag schedule (fib or custom)
	buf       []*imgmath.LowresFrame // ring buffer; capacity = l+1 (or maxL+1)
	n         int                    // total frames added
	m         *CostMatrix
	retainAll bool                   // when true, every downscaled frame is also kept in all[]
	all       []*imgmath.LowresFrame // retained lowres frames (only when retainAll)
}

// NewLookaheadScanner returns a scanner with the given lookahead length.
// l must be in [1, MaxLag] where MaxLag = 80 (matches x264's lookahead cap).
func NewLookaheadScanner(l int) (*LookaheadScanner, error) {
	if l < 1 || l > imgmath.MaxLag {
		return nil, fmt.Errorf("lookahead length %d out of range [1, %d]", l, imgmath.MaxLag)
	}
	lags := fibLags(l)
	return &LookaheadScanner{
		l:         l,
		lags:      lags,
		buf:       make([]*imgmath.LowresFrame, l+1),
		m:         &CostMatrix{L: l, Lags: lags},
		retainAll: false,
	}, nil
}

// AddFrame adds one video frame to the scanner.  luma is a packed 8-bit
// luma (Y) plane of dimensions width×height with the given row stride.
// Internally the frame is downscaled to half-resolution, the SATD-based
// intra cost is computed, and inter costs are computed against all buffered
// reference frames (up to l frames back).
//
// The CostMatrix returned by Matrix() is updated before AddFrame returns.
func (s *LookaheadScanner) AddFrame(luma []byte, width, height, stride int) error {
	lrFrame, err := imgmath.InitLowres(luma, width, height, stride)
	if err != nil {
		return err
	}

	j := s.n // 0-based index of this frame

	// Store in ring buffer (overwrites the slot that is now l+1 frames old).
	s.buf[j%(s.l+1)] = lrFrame
	if s.retainAll {
		s.all = append(s.all, lrFrame)
	}
	s.n++

	// Intra cost: pass lag=-1 so LowresFrameCost skips all inter work.
	// ref is not accessed when lag < 0.
	_, iCost := imgmath.LowresFrameCost(lrFrame, nil, -1)

	// Build the ratio row for frame j using the Fibonacci lag schedule.
	// numRefs is the maximum available history (limited by both j and l).
	numRefs := j
	if numRefs > s.l {
		numRefs = s.l
	}
	row := make([]float32, len(s.lags))
	interRow := make([]float32, len(s.lags))
	cap := s.l + 1
	for i, lag := range s.lags {
		if lag > numRefs {
			break // lags are sorted; remaining lags exceed available history
		}
		// Reference frame is j−lag; lag-1 selects the MV-cache slot (0-based).
		refSlot := (j - lag) % cap
		if refSlot < 0 {
			refSlot += cap
		}
		ref := s.buf[refSlot]
		interCost, _ := imgmath.LowresFrameCost(lrFrame, ref, lag-1)
		interRow[i] = float32(interCost)
		if iCost > 0 {
			row[i] = float32(interCost) / float32(iCost)
		}
		// iCost == 0 means a blank frame; row[i] stays 0.
	}

	s.m.IntraCost = append(s.m.IntraCost, float32(iCost))
	s.m.InterCost = append(s.m.InterCost, interRow)
	s.m.AvgLuma = append(s.m.AvgLuma, lrFrame.AvgLuma)
	s.m.Energy = append(s.m.Energy, float32(imgmath.LowresFrameACEnergy(lrFrame)))
	s.m.Ratio = append(s.m.Ratio, row)
	s.m.N = s.n
	return nil
}

// NewLookaheadScannerWithLags creates a scanner that samples inter-frame
// prediction costs at exactly the provided lag values (in frames).
// The lags do not need to be Fibonacci; any ascending list of positive ints is
// allowed. This is useful for targeting specific dissolve durations (e.g. the
// 15,30,45,60,90,120 schedule for the synthetic blends in dissolve_test_xfs.json).
//
// The internal buffer is sized to max(lags)+1. Lags larger than available history
// are automatically skipped (as in the Fibonacci case).
func NewLookaheadScannerWithLags(lags []int) (*LookaheadScanner, error) {
	if len(lags) == 0 {
		return nil, fmt.Errorf("lags list must not be empty")
	}
	ls := make([]int, len(lags))
	copy(ls, lags)
	sort.Ints(ls)
	maxL := 0
	for _, k := range ls {
		if k < 1 {
			return nil, fmt.Errorf("lag must be >= 1, got %d", k)
		}
		if k > maxL {
			maxL = k
		}
	}
	if maxL > imgmath.MaxLag {
		return nil, fmt.Errorf("maximum lag %d exceeds supported MaxLag %d", maxL, imgmath.MaxLag)
	}
	return &LookaheadScanner{
		l:         maxL,
		lags:      ls,
		buf:       make([]*imgmath.LowresFrame, maxL+1),
		m:         &CostMatrix{L: maxL, Lags: ls},
		retainAll: false,
	}, nil
}

// Matrix returns the CostMatrix built so far.  The caller must not modify
// the returned value; its contents are updated on every AddFrame call.
func (s *LookaheadScanner) Matrix() *CostMatrix {
	return s.m
}

// RetainAllLowres enables retention of every downscaled LowresFrame (in
// addition to the normal ring buffer used for lag references). After the
// coarse pass the caller can obtain the full list via AllLowres and use it
// to compute targeted additional (j, k) costs only inside candidate regions.
// Call before the first AddFrame for a clean batch run. This is a no-op
// after frames have already been added.
func (s *LookaheadScanner) RetainAllLowres() {
	if s.n == 0 {
		s.retainAll = true
	}
}

// AllLowres returns the retained lowres frames when RetainAllLowres was
// enabled before accumulation. The slice is indexed by frame number (0..N-1).
// Returns nil if retention was not enabled or no frames have been added.
func (s *LookaheadScanner) AllLowres() []*imgmath.LowresFrame {
	if !s.retainAll {
		return nil
	}
	return s.all
}

// Refine computes additional inter/intra ratios for the given frames and lags
// using the retained lowres frames (see RetainAllLowres / AllLowres). It is
// intended for the staged/incremental dissolve detection path: after a cheap
// coarse pass over the whole video, call Refine only in the temporal windows
// around candidate dissolves, using intelligently chosen lags (typically one
// near the estimated dissolve duration D, plus a narrow set 1-5 for edge
// precision).
//
// Behaviour:
//   - The union of m.Lags and extraLags becomes the new lag schedule.
//   - All existing rows in m are extended (padded with 0) to the new column count.
//   - For j in [lo, hi] (clipped to available data), for each k in extraLags
//     where history exists (j >= k), LowresFrameCost is called and the
//     InterCost / Ratio columns for that lag are filled.
//   - Columns for new lags remain 0 for all j outside the requested windows
//     (and for j < k).
//   - Lags above MaxLag are skipped (the per-frame MV-cache slot space).
//     Reverse-prediction measurements use a separate per-frame cache
//     (imgmath.LowresFrameCostReverse), so they never collide with forward
//     lags.
//
// The caller is responsible for passing a lowres slice whose indices
// correspond to the rows 0..m.N-1 in the matrix (i.e. the value returned by
// AllLowres() from the same scanner after the coarse accumulation).
func (s *LookaheadScanner) Refine(m *CostMatrix, lowres []*imgmath.LowresFrame, lo, hi int, extraLags []int) error {
	if m == nil || len(lowres) == 0 || len(extraLags) == 0 {
		return nil
	}

	// Build the desired full set of lags (union)
	lagSet := make(map[int]struct{}, len(m.Lags)+len(extraLags))
	for _, k := range m.Lags {
		if k >= 1 {
			lagSet[k] = struct{}{}
		}
	}
	for _, k := range extraLags {
		if k >= 1 && k <= imgmath.MaxLag {
			lagSet[k] = struct{}{}
		}
	}

	newLags := make([]int, 0, len(lagSet))
	for k := range lagSet {
		newLags = append(newLags, k)
	}
	sort.Ints(newLags)

	newCols := len(newLags)

	if newCols > len(m.Lags) {
		// Rebuild every row with the data RELOCATED to each lag's position in
		// the new sorted union. New lags usually sort into the middle of the
		// schedule (e.g. adding 2,3,5 to coarse [1,10,30]), so padding at the
		// end would silently re-label the existing columns: the old lag-10
		// data would be read as lag 2 everywhere outside the refined windows.
		newIdx := make(map[int]int, newCols)
		for i, k := range newLags {
			newIdx[k] = i
		}
		for j := 0; j < m.N; j++ {
			nr := make([]float32, newCols)
			ni := make([]float32, newCols)
			for oi, k := range m.Lags {
				ncol := newIdx[k]
				if oi < len(m.Ratio[j]) {
					nr[ncol] = m.Ratio[j][oi]
				}
				if oi < len(m.InterCost[j]) {
					ni[ncol] = m.InterCost[j][oi]
				}
			}
			m.Ratio[j] = nr
			m.InterCost[j] = ni
		}
		m.Lags = newLags
		if newLags[len(newLags)-1] > m.L {
			m.L = newLags[len(newLags)-1]
		}
	}

	// lag -> column index in the (possibly newly extended) m.Lags
	lagToCol := make(map[int]int, len(m.Lags))
	for i, k := range m.Lags {
		lagToCol[k] = i
	}

	nLow := len(lowres)
	jStart := lo
	if jStart < 0 {
		jStart = 0
	}
	jEnd := hi
	if jEnd >= m.N {
		jEnd = m.N - 1
	}
	if jEnd >= nLow {
		jEnd = nLow - 1
	}

	for j := jStart; j <= jEnd; j++ {
		cur := lowres[j]
		if cur == nil {
			continue
		}
		intra := float64(m.IntraCost[j])

		for _, k := range extraLags {
			if k < 1 || k > j {
				continue
			}
			col, ok := lagToCol[k]
			if !ok {
				continue
			}
			refIdx := j - k
			if refIdx < 0 || refIdx >= nLow {
				continue
			}
			ref := lowres[refIdx]
			if ref == nil {
				continue
			}

			interCost, _ := imgmath.LowresFrameCost(cur, ref, k-1)
			m.InterCost[j][col] = float32(interCost)
			if intra > 0 {
				m.Ratio[j][col] = float32(float64(interCost) / intra)
			} else {
				m.Ratio[j][col] = 0
			}
		}
	}

	return nil
}
