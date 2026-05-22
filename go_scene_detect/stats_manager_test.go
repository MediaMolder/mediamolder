//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2018 Brandon Castellano <http://www.bcastell.com>.
// SPDX-License-Identifier: BSD-3-Clause

package goscenedetect

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestStatsManager_RegisterAndSet(t *testing.T) {
	sm := NewStatsManager(25.0)
	sm.RegisterMetrics([]string{"delta", "score"})

	sm.SetMetrics(0, map[string]float64{"delta": 0.1, "score": 10.5})
	sm.SetMetrics(1, map[string]float64{"delta": 0.2, "score": 20.0})

	vals := sm.GetMetrics(0, []string{"delta", "score"})
	if len(vals) != 2 {
		t.Fatalf("GetMetrics: expected 2 values, got %d", len(vals))
	}
	if vals[0] != 0.1 {
		t.Errorf("GetMetrics[delta]: got %g, want 0.1", vals[0])
	}
	if vals[1] != 10.5 {
		t.Errorf("GetMetrics[score]: got %g, want 10.5", vals[1])
	}
}

func TestStatsManager_GetMetrics_MissingReturnsZero(t *testing.T) {
	sm := NewStatsManager(25.0)
	vals := sm.GetMetrics(99, []string{"notexist"})
	if len(vals) != 1 || vals[0] != 0 {
		t.Errorf("GetMetrics for missing frame: got %v, want [0]", vals)
	}
}

func TestStatsManager_MetricsExist(t *testing.T) {
	sm := NewStatsManager(25.0)
	sm.SetMetrics(5, map[string]float64{"a": 1.0, "b": 2.0})

	if !sm.MetricsExist(5, []string{"a", "b"}) {
		t.Error("MetricsExist: both keys present, expected true")
	}
	if sm.MetricsExist(5, []string{"a", "c"}) {
		t.Error("MetricsExist: key 'c' absent, expected false")
	}
	if sm.MetricsExist(9, []string{"a"}) {
		t.Error("MetricsExist: frame 9 not set, expected false")
	}
}

func TestStatsManager_IsSaveRequired(t *testing.T) {
	sm := NewStatsManager(25.0)
	if sm.IsSaveRequired() {
		t.Error("fresh StatsManager should not need saving")
	}
	sm.SetMetrics(0, map[string]float64{"x": 1.0})
	if !sm.IsSaveRequired() {
		t.Error("after SetMetrics, IsSaveRequired should be true")
	}
}

func TestStatsManager_SaveToCSV(t *testing.T) {
	sm := NewStatsManager(25.0)
	sm.RegisterMetrics([]string{"delta"})
	sm.SetMetrics(0, map[string]float64{"delta": 0.5})
	sm.SetMetrics(25, map[string]float64{"delta": 1.2}) // frame 25 = 1.000s at 25fps

	path := filepath.Join(t.TempDir(), "stats.csv")
	if err := sm.SaveToCSV(path); err != nil {
		t.Fatal(err)
	}
	if sm.IsSaveRequired() {
		t.Error("IsSaveRequired should be false after SaveToCSV")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	rows, err := csv.NewReader(f).ReadAll()
	if err != nil {
		t.Fatal(err)
	}

	// Expect: header + 2 data rows.
	if len(rows) != 3 {
		t.Fatalf("expected 3 rows (header + 2 data), got %d", len(rows))
	}

	// Header.
	if rows[0][0] != "Frame Number" || rows[0][1] != "Timecode" || rows[0][2] != "delta" {
		t.Errorf("unexpected header: %v", rows[0])
	}

	// First data row: frame 0 → "1", timecode "00:00:00.000".
	if rows[1][0] != "1" {
		t.Errorf("row 1 frame number: got %q, want \"1\"", rows[1][0])
	}
	if rows[1][1] != "00:00:00.000" {
		t.Errorf("row 1 timecode: got %q, want \"00:00:00.000\"", rows[1][1])
	}

	// Second data row: frame 25 (0-based) → "26" (1-based), timecode "00:00:01.000".
	if rows[2][0] != "26" {
		t.Errorf("row 2 frame number: got %q, want \"26\"", rows[2][0])
	}
	if rows[2][1] != "00:00:01.000" {
		t.Errorf("row 2 timecode: got %q, want \"00:00:01.000\"", rows[2][1])
	}
}

func TestStatsManager_SaveToJSONL(t *testing.T) {
	sm := NewStatsManager(25.0)
	sm.RegisterMetrics([]string{"score"})
	sm.SetMetrics(0, map[string]float64{"score": 7.5})

	path := filepath.Join(t.TempDir(), "stats.jsonl")
	if err := sm.SaveToJSONL(path); err != nil {
		t.Fatal(err)
	}
	if sm.IsSaveRequired() {
		t.Error("IsSaveRequired should be false after SaveToJSONL")
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	dec := json.NewDecoder(f)
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		t.Fatal(err)
	}

	frameNum, ok := obj["frame"].(float64)
	if !ok || int(frameNum) != 1 {
		t.Errorf("\"frame\": got %v, want 1", obj["frame"])
	}
	if obj["timecode"] != "00:00:00.000" {
		t.Errorf("\"timecode\": got %v, want \"00:00:00.000\"", obj["timecode"])
	}
	score, ok := obj["score"].(float64)
	if !ok || score != 7.5 {
		t.Errorf("\"score\": got %v, want 7.5", obj["score"])
	}
}

func TestStatsManager_DuplicateRegisterIgnored(t *testing.T) {
	sm := NewStatsManager(25.0)
	sm.RegisterMetrics([]string{"a", "b"})
	sm.RegisterMetrics([]string{"b", "c"}) // "b" is a duplicate
	if len(sm.keys) != 3 {
		t.Errorf("expected 3 unique keys after duplicate registration, got %d", len(sm.keys))
	}
}
