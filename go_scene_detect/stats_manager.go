//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2018 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE
//

package goscenedetect

// Ported from scenedetect/stats_manager.py.

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strconv"
)

// StatsManager stores per-frame statistics produced by SceneDetectors.
// Detectors register their metric names via RegisterMetrics, then write
// values via SetMetrics.  Results can be flushed to CSV (matching the
// PySceneDetect stats file format) or to JSONL (MediaMolder extension).
//
// Ported from scenedetect/stats_manager.py, class StatsManager.
type StatsManager struct {
	// metrics maps zero-based frame number → metric name → value.
	metrics map[int64]map[string]float64

	// keys is the ordered set of registered metric names.
	keys   []string
	keySet map[string]struct{}

	// fps is stored so that CSV/JSONL output can reconstruct timecodes.
	fps float64

	// dirty is true when metrics have been written since the last save.
	dirty bool
}

// NewStatsManager creates a StatsManager with the given frame rate.
// fps is used only for timecode reconstruction in CSV/JSONL output.
func NewStatsManager(fps float64) *StatsManager {
	return &StatsManager{
		metrics: make(map[int64]map[string]float64),
		keySet:  make(map[string]struct{}),
		fps:     fps,
	}
}

// RegisterMetrics declares the metric names that a detector will write.
// Calling RegisterMetrics after SetMetrics is valid; previously stored
// values for those keys remain accessible.
//
// Ported from scenedetect/stats_manager.py, StatsManager.register_metrics.
func (s *StatsManager) RegisterMetrics(keys []string) {
	for _, k := range keys {
		if _, exists := s.keySet[k]; !exists {
			s.keySet[k] = struct{}{}
			s.keys = append(s.keys, k)
		}
	}
}

// SetMetrics stores the given key→value pairs for the frame at frameNum.
// frameNum is zero-based. Existing values for the same frame and key are
// overwritten.
//
// Ported from scenedetect/stats_manager.py, StatsManager.set_metrics.
func (s *StatsManager) SetMetrics(frameNum int64, m map[string]float64) {
	if _, ok := s.metrics[frameNum]; !ok {
		s.metrics[frameNum] = make(map[string]float64, len(m))
	}
	for k, v := range m {
		s.metrics[frameNum][k] = v
	}
	s.dirty = true
}

// GetMetrics returns the values for the requested keys at frameNum.
// The returned slice has the same length and order as keys.
// Missing values are returned as 0.
//
// Ported from scenedetect/stats_manager.py, StatsManager.get_metrics.
func (s *StatsManager) GetMetrics(frameNum int64, keys []string) []float64 {
	out := make([]float64, len(keys))
	frame, ok := s.metrics[frameNum]
	if !ok {
		return out
	}
	for i, k := range keys {
		out[i] = frame[k]
	}
	return out
}

// MetricsExist reports whether all requested keys have been written for frameNum.
//
// Ported from scenedetect/stats_manager.py, StatsManager.metrics_exist.
func (s *StatsManager) MetricsExist(frameNum int64, keys []string) bool {
	frame, ok := s.metrics[frameNum]
	if !ok {
		return false
	}
	for _, k := range keys {
		if _, exists := frame[k]; !exists {
			return false
		}
	}
	return true
}

// IsSaveRequired reports whether metrics have been written since the last
// successful call to SaveToCSV or SaveToJSONL.
func (s *StatsManager) IsSaveRequired() bool { return s.dirty }

// SaveToCSV writes all stored metrics to a CSV file at path.
//
// The CSV format matches PySceneDetect's stats file:
//   - Header row: Frame Number, Timecode, <key1>, <key2>, ...
//   - Data rows:  1-based frame number, HH:MM:SS.nnn timecode, metric values
//
// Ported from scenedetect/stats_manager.py, StatsManager.save_to_csv.
func (s *StatsManager) SaveToCSV(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("goscenedetect: StatsManager.SaveToCSV: %w", err)
	}
	defer f.Close()

	w := csv.NewWriter(f)

	// Build header.
	header := make([]string, 0, 2+len(s.keys))
	header = append(header, "Frame Number", "Timecode")
	header = append(header, s.keys...)
	if err := w.Write(header); err != nil {
		return fmt.Errorf("goscenedetect: StatsManager.SaveToCSV: write header: %w", err)
	}

	// Write rows in ascending frame-number order.
	frameNums := s.sortedFrameNums()
	for _, fn := range frameNums {
		tc, err := s.timecodeFor(fn)
		if err != nil {
			tc = "00:00:00.000"
		}
		row := make([]string, 0, 2+len(s.keys))
		row = append(row, strconv.FormatInt(fn+1, 10), tc) // 1-based frame number
		for _, k := range s.keys {
			v := s.metrics[fn][k]
			row = append(row, strconv.FormatFloat(v, 'f', -1, 64))
		}
		if err := w.Write(row); err != nil {
			return fmt.Errorf("goscenedetect: StatsManager.SaveToCSV: write row %d: %w", fn, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return fmt.Errorf("goscenedetect: StatsManager.SaveToCSV: flush: %w", err)
	}
	s.dirty = false
	return nil
}

// SaveToJSONL writes all stored metrics to a JSONL file at path.
// Each line is a JSON object with fields "frame" (1-based), "timecode",
// and one field per registered metric key.
//
// This format is a MediaMolder extension with no Python equivalent.
func (s *StatsManager) SaveToJSONL(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return fmt.Errorf("goscenedetect: StatsManager.SaveToJSONL: %w", err)
	}
	defer f.Close()

	enc := json.NewEncoder(f)
	frameNums := s.sortedFrameNums()
	for _, fn := range frameNums {
		tc, err := s.timecodeFor(fn)
		if err != nil {
			tc = "00:00:00.000"
		}
		obj := make(map[string]any, 2+len(s.keys))
		obj["frame"] = fn + 1 // 1-based
		obj["timecode"] = tc
		for k, v := range s.metrics[fn] {
			obj[k] = v
		}
		if err := enc.Encode(obj); err != nil {
			return fmt.Errorf("goscenedetect: StatsManager.SaveToJSONL: encode frame %d: %w", fn, err)
		}
	}
	s.dirty = false
	return nil
}

// sortedFrameNums returns all stored frame numbers in ascending order.
func (s *StatsManager) sortedFrameNums() []int64 {
	nums := make([]int64, 0, len(s.metrics))
	for fn := range s.metrics {
		nums = append(nums, fn)
	}
	sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
	return nums
}

// timecodeFor reconstructs the HH:MM:SS.nnn timecode for a zero-based frame number.
func (s *StatsManager) timecodeFor(frameNum int64) (string, error) {
	ft, err := NewFrameTimecode(frameNum, s.fps)
	if err != nil {
		return "", err
	}
	return ft.Timecode(), nil
}
