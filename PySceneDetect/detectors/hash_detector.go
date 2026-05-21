//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2021 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

package detectors

// Ported from scenedetect/detectors/hash_detector.py.

import (
	"fmt"
	"sort"

	imgmath "github.com/MediaMolder/MediaMolder/PySceneDetect/internal"

	psd "github.com/MediaMolder/MediaMolder/PySceneDetect"
)

// HashDetector detects scene cuts by computing a perceptual hash (DCT-based)
// for each frame, then measuring the normalised Hamming distance between
// consecutive hashes.  Mirrors HashDetector in hash_detector.py.
//
// Algorithm:
//  1. Convert frame to grayscale (BT.601 luma).
//  2. Resize to (size*lowpass) × (size*lowpass) with INTER_AREA.
//  3. Normalise pixel values to [0,1] using the maximum pixel value.
//  4. Apply 2-D DCT.
//  5. Keep the top-left size×size block (low-frequency coefficients).
//  6. Binarise: each bit = (coeff > median of the block).
//  7. Hamming distance between consecutive hashes, normalised by size².
//  8. Fire a cut when distance ≥ threshold and min_scene_len is satisfied.
type HashDetector struct {
	threshold float64 // normalised Hamming distance threshold (default 0.395)
	size      int     // hash dimension; hash is size×size bits (default 16)
	lowpass   int     // resize factor; frame is resized to (size*lowpass)² (default 2)
	sizeSq    float64 // float64(size*size) — denominator for normalisation

	metricKey string // e.g. "hash_dist [size=16 lowpass=2]"

	minSceneLenRaw any
	minFrames      int64
	minFramesReady bool

	stats *psd.StatsManager

	// State
	lastHash        []bool // binary hash of the previous frame; nil = first frame
	lastSceneCut    psd.FrameTimecode
	hasLastSceneCut bool

	lastHashDist float64 // most recently computed normalised Hamming distance
}

// NewHashDetector constructs a HashDetector.
//
//   - threshold:    normalised Hamming distance in [0,1] that triggers a cut
//     (default 0.395).
//   - minSceneLen:  minimum scene length; accepts int frame count or "HH:MM:SS"
//     timecode (default 15).
//   - size:         hash dimension (default 16).
//   - lowpass:      resize factor (default 2).
func NewHashDetector(threshold float64, minSceneLen any, size, lowpass int) (*HashDetector, error) {
	if size < 1 {
		return nil, fmt.Errorf("hash_detector: size must be ≥ 1, got %d", size)
	}
	if lowpass < 1 {
		return nil, fmt.Errorf("hash_detector: lowpass must be ≥ 1, got %d", lowpass)
	}
	return &HashDetector{
		threshold:      threshold,
		size:           size,
		lowpass:        lowpass,
		sizeSq:         float64(size * size),
		metricKey:      fmt.Sprintf("hash_dist [size=%d lowpass=%d]", size, lowpass),
		minSceneLenRaw: minSceneLen,
	}, nil
}

// SetStats attaches a StatsManager for per-frame metric recording.
func (d *HashDetector) SetStats(s *psd.StatsManager) { d.stats = s }

// GetMetrics returns the metric keys written by this detector.
func (d *HashDetector) GetMetrics() []string { return []string{d.metricKey} }

// EventBufferLength returns 0; HashDetector emits cuts immediately.
func (d *HashDetector) EventBufferLength() int64 { return 0 }

// LastHashDist returns the normalised Hamming distance computed for the most
// recently processed frame pair.  Returns 0 before the second frame.
func (d *HashDetector) LastHashDist() float64 { return d.lastHashDist }

// ProcessFrame implements psd.SceneDetector.
func (d *HashDetector) ProcessFrame(t psd.FrameTimecode, frame *psd.FrameData) ([]psd.FrameTimecode, error) {
	if !d.minFramesReady {
		n, err := resolveMinSceneLen(d.minSceneLenRaw, t.FrameRate())
		if err != nil {
			return nil, fmt.Errorf("hash_detector: min_scene_len: %w", err)
		}
		d.minFrames = n
		d.minFramesReady = true
	}

	if !d.hasLastSceneCut {
		d.lastSceneCut = t
		d.hasLastSceneCut = true
	}

	luma := imgmath.BGRToLuma(frame.BGR, frame.Width, frame.Height)
	currHash, err := d.hashFrame(luma, frame.Width, frame.Height)
	if err != nil {
		return nil, fmt.Errorf("hash_detector: hashFrame: %w", err)
	}

	var cuts []psd.FrameTimecode

	if d.lastHash != nil {
		dist := hammingDist(d.lastHash, currHash)
		hashDistNorm := float64(dist) / d.sizeSq
		d.lastHashDist = hashDistNorm

		if d.stats != nil {
			d.stats.SetMetrics(t.FrameNum(), map[string]float64{d.metricKey: hashDistNorm})
		}

		elapsed := t.FrameNum() - d.lastSceneCut.FrameNum()
		if hashDistNorm >= d.threshold && (d.minFrames == 0 || elapsed >= d.minFrames) {
			cuts = append(cuts, t)
			d.lastSceneCut = t
		}
	}

	d.lastHash = currHash
	return cuts, nil
}

// PostProcess implements psd.SceneDetector.  HashDetector has no deferred cuts.
func (d *HashDetector) PostProcess(_ psd.FrameTimecode) ([]psd.FrameTimecode, error) {
	return nil, nil
}

// hashFrame computes the DCT-based perceptual hash for a grayscale image.
// luma must be a BGRToLuma output: width×height bytes in row-major order.
func (d *HashDetector) hashFrame(luma []byte, w, h int) ([]bool, error) {
	imsize := d.size * d.lowpass

	resized, err := imgmath.ResizeGRAY8(luma, w, h, imsize, imsize, imgmath.InterpArea)
	if err != nil {
		return nil, err
	}

	// Find max value for normalisation (avoid divide-by-zero).
	maxVal := byte(1)
	for _, v := range resized {
		if v > maxVal {
			maxVal = v
		}
	}

	// Convert to float32 normalised to [0, 1].
	f32 := make([]float32, imsize*imsize)
	inv := float32(1.0) / float32(maxVal)
	for i, v := range resized {
		f32[i] = float32(v) * inv
	}

	// 2-D DCT.
	dct := imgmath.DCT2D(f32, imsize, imsize)

	// Extract top-left size×size block (low-frequency coefficients).
	lowFreq := make([]float32, d.size*d.size)
	for row := 0; row < d.size; row++ {
		copy(lowFreq[row*d.size:(row+1)*d.size], dct[row*imsize:row*imsize+d.size])
	}

	// Binarise: each bit = (coeff > median).
	med := float32Median(lowFreq)
	hash := make([]bool, d.size*d.size)
	for i, v := range lowFreq {
		hash[i] = v > med
	}
	return hash, nil
}

// float32Median returns the median of a float32 slice.
// The input slice is not modified.
func float32Median(data []float32) float32 {
	if len(data) == 0 {
		return 0
	}
	tmp := make([]float32, len(data))
	copy(tmp, data)
	sort.Slice(tmp, func(i, j int) bool { return tmp[i] < tmp[j] })
	n := len(tmp)
	if n%2 == 0 {
		return (tmp[n/2-1] + tmp[n/2]) / 2
	}
	return tmp[n/2]
}

// hammingDist counts the number of positions where h1[i] != h2[i].
func hammingDist(h1, h2 []bool) int {
	count := 0
	for i := range h1 {
		if h1[i] != h2[i] {
			count++
		}
	}
	return count
}
