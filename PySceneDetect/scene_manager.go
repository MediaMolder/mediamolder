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

package pyscenedetect

// Ported from scenedetect/scene_manager.py.

import (
	"context"
	"fmt"
	"sort"
)

// statsAssigner is a non-interface capability implemented by all built-in
// detectors. SceneManager uses a type assertion so that SceneDetector itself
// does not need a SetStats method.
type statsAssigner interface {
	SetStats(*StatsManager)
}

// FrameImg bundles a decoded video frame (BGR24) with its presentation
// timecode.  The caller is responsible for the lifetime of Data; the
// SceneManager reads but never writes or frees it.
type FrameImg struct {
	Timecode FrameTimecode
	Data     *FrameData
}

// SceneManager orchestrates one or more SceneDetectors over a stream of
// decoded video frames, collecting the resulting cut list and exposing it as
// a scene list.
//
// Ported from scenedetect/scene_manager.py, class SceneManager.
type SceneManager struct {
	statsManager *StatsManager // optional; nil means no stats recording

	detectors       []SceneDetector
	cutList         []FrameTimecode
	frameBufferSize int64 // max EventBufferLength across all added detectors

	// Downscale settings.
	downscale     int  // integer factor; 1 = no downscale
	autoDownscale bool // when true, compute factor from frame width
	crop          *CropRegion

	// State (reset by Clear).
	startPos *FrameTimecode
	lastPos  *FrameTimecode
}

// NewSceneManager creates a SceneManager.
// Pass a non-nil StatsManager to record per-frame detector metrics.
//
// Ported from scenedetect/scene_manager.py, SceneManager.__init__.
func NewSceneManager(statsManager *StatsManager) *SceneManager {
	return &SceneManager{
		statsManager:  statsManager,
		downscale:     1,
		autoDownscale: true,
	}
}

// AddDetector registers a SceneDetector with this SceneManager.  The
// SceneManager calls SetStats on the detector (if supported) and registers its
// metric keys with the StatsManager.
//
// Ported from scenedetect/scene_manager.py, SceneManager.add_detector.
func (sm *SceneManager) AddDetector(d SceneDetector) {
	if sm.statsManager != nil {
		if s, ok := d.(statsAssigner); ok {
			s.SetStats(sm.statsManager)
		}
		sm.statsManager.RegisterMetrics(d.GetMetrics())
	}
	sm.detectors = append(sm.detectors, d)
	if n := d.EventBufferLength(); n > sm.frameBufferSize {
		sm.frameBufferSize = n
	}
}

// SetDownscale sets a fixed integer downscale factor (must be ≥ 1, where 1 =
// no scaling).  Disables auto-downscale.
func (sm *SceneManager) SetDownscale(factor int) error {
	if factor < 1 {
		return fmt.Errorf("scene_manager: downscale factor must be ≥ 1, got %d", factor)
	}
	sm.autoDownscale = false
	sm.downscale = factor
	return nil
}

// SetAutoDownscale enables or disables automatic downscale computation based
// on the source frame width.  When enabled, the fixed downscale factor is
// ignored.  Auto-downscale is enabled by default.
func (sm *SceneManager) SetAutoDownscale(v bool) { sm.autoDownscale = v }

// SetCrop restricts scene detection to a rectangular sub-region of each
// frame.  c is specified as (X0, Y0, X1, Y1) where both endpoints are
// inclusive and coordinates start from 0.
//
// Ported from scenedetect/scene_manager.py, SceneManager.crop (setter).
func (sm *SceneManager) SetCrop(c CropRegion) error {
	for _, v := range c {
		if v < 0 {
			return fmt.Errorf("scene_manager: crop coordinates must be ≥ 0")
		}
	}
	// Store as [minX, minY, maxX+1, maxY+1] for slice-based extraction.
	x0, y0 := min(c[0], c[2]), min(c[1], c[3])
	x1, y1 := max(c[0], c[2])+1, max(c[1], c[3])+1
	crop := CropRegion{x0, y0, x1, y1}
	sm.crop = &crop
	return nil
}

// Clear resets the cut list, scene state, and detector list.  Statistics
// already stored in the StatsManager are preserved.
//
// Ported from scenedetect/scene_manager.py, SceneManager.clear.
func (sm *SceneManager) Clear() {
	sm.cutList = sm.cutList[:0]
	sm.detectors = sm.detectors[:0]
	sm.frameBufferSize = 0
	sm.startPos = nil
	sm.lastPos = nil
}

// DetectScenes consumes the provided frame channel and runs all registered
// detectors on each frame.  It blocks until the channel is closed or ctx is
// cancelled.  Returns the number of frames processed.
//
// The caller is responsible for producing frames and closing the channel.
//
// Ported from scenedetect/scene_manager.py, SceneManager.detect_scenes.
func (sm *SceneManager) DetectScenes(ctx context.Context, frames <-chan FrameImg) (int, error) {
	count := 0
	for {
		select {
		case <-ctx.Done():
			return count, ctx.Err()
		case img, ok := <-frames:
			if !ok {
				// Channel closed: run PostProcess on each detector.
				if sm.lastPos != nil {
					if err := sm.postProcess(*sm.lastPos); err != nil {
						return count, err
					}
				}
				return count, nil
			}

			frame := img.Data

			// Apply crop region if configured.
			if sm.crop != nil {
				frame = sm.applyCrop(frame)
			}

			// Apply downscale.
			factor := sm.resolveDownscale(frame.Width, frame.Height)
			if factor > 1 {
				frame = sm.downscaleFrame(frame, factor)
			}

			if err := sm.processFrame(img.Timecode, frame); err != nil {
				return count, err
			}
			count++

			t := img.Timecode
			if sm.startPos == nil {
				sm.startPos = &t
			}
			sm.lastPos = &t
		}
	}
}

// GetSceneList returns an ordered list of detected scenes as start/end
// FrameTimecode pairs.
//
// If startInScene is true, the video is assumed to begin inside a scene, so
// even if no cuts are detected a single scene spanning the entire input is
// returned.  When false, an empty cut list yields an empty scene list.
//
// Ported from scenedetect/scene_manager.py, SceneManager.get_scene_list.
func (sm *SceneManager) GetSceneList(startInScene bool) SceneList {
	if sm.startPos == nil || sm.lastPos == nil {
		return nil
	}
	cuts := sm.GetCutList()
	endPos := sm.lastPos.AddFrames(1) // exclusive end, matching Python's _last_pos + 1
	scenes := scenesFromCuts(cuts, *sm.startPos, endPos)
	if len(cuts) == 0 && !startInScene {
		return nil
	}
	return scenes
}

// GetCutList returns a sorted, deduplicated list of FrameTimecodes where cuts
// were detected.
//
// Ported from scenedetect/scene_manager.py, SceneManager._get_cutting_list.
func (sm *SceneManager) GetCutList() CutList {
	if len(sm.cutList) == 0 {
		return nil
	}
	seen := make(map[int64]FrameTimecode, len(sm.cutList))
	for _, cut := range sm.cutList {
		seen[cut.FrameNum()] = cut
	}
	unique := make(CutList, 0, len(seen))
	for _, cut := range seen {
		unique = append(unique, cut)
	}
	sort.Slice(unique, func(i, j int) bool {
		return unique[i].FrameNum() < unique[j].FrameNum()
	})
	return unique
}

// scenesFromCuts converts a cut list into a scene list.
// Each scene spans from one cut (or the start) to the next cut (or end).
//
// Ported from scenedetect/scene_manager.py, get_scenes_from_cuts.
func scenesFromCuts(cuts CutList, start, end FrameTimecode) SceneList {
	if len(cuts) == 0 {
		return SceneList{{Start: start, End: end}}
	}
	scenes := make(SceneList, 0, len(cuts)+1)
	last := start
	for _, cut := range cuts {
		scenes = append(scenes, Scene{Start: last, End: cut})
		last = cut
	}
	scenes = append(scenes, Scene{Start: last, End: end})
	return scenes
}

// processFrame passes the frame to all registered detectors and appends any
// returned cuts to the cut list.
func (sm *SceneManager) processFrame(t FrameTimecode, frame *FrameData) error {
	for _, d := range sm.detectors {
		cuts, err := d.ProcessFrame(t, frame)
		if err != nil {
			return fmt.Errorf("scene_manager: detector %T: %w", d, err)
		}
		sm.cutList = append(sm.cutList, cuts...)
	}
	return nil
}

// postProcess calls PostProcess on all detectors and appends any remaining cuts.
func (sm *SceneManager) postProcess(t FrameTimecode) error {
	for _, d := range sm.detectors {
		cuts, err := d.PostProcess(t)
		if err != nil {
			return fmt.Errorf("scene_manager: detector %T PostProcess: %w", d, err)
		}
		sm.cutList = append(sm.cutList, cuts...)
	}
	return nil
}

// resolveDownscale returns the integer downscale factor to apply to the given
// frame dimensions.  Returns 1 if no scaling is needed.
//
// Ported from scenedetect/scene_manager.py, compute_downscale_factor.
func (sm *SceneManager) resolveDownscale(frameW, frameH int) int {
	if !sm.autoDownscale {
		return max(1, sm.downscale)
	}
	effective := frameW
	if frameH > frameW {
		effective = frameH
	}
	f := int(ComputeDownscaleFactor(effective))
	return max(1, f)
}

// applyCrop extracts the configured sub-region from frame.
// The crop region is stored internally as [x0, y0, x1, y1) (exclusive end).
func (sm *SceneManager) applyCrop(frame *FrameData) *FrameData {
	if sm.crop == nil {
		return frame
	}
	x0, y0 := (*sm.crop)[0], (*sm.crop)[1]
	x1, y1 := (*sm.crop)[2], (*sm.crop)[3]
	// Clamp to frame boundaries.
	x1 = min(x1, frame.Width)
	y1 = min(y1, frame.Height)
	if x0 >= x1 || y0 >= y1 {
		return frame
	}
	dstW := x1 - x0
	dstH := y1 - y0
	dst := make([]byte, dstW*dstH*3)
	for y := y0; y < y1; y++ {
		srcOff := (y*frame.Width + x0) * 3
		dstOff := (y - y0) * dstW * 3
		copy(dst[dstOff:dstOff+dstW*3], frame.BGR[srcOff:srcOff+dstW*3])
	}
	return &FrameData{Width: dstW, Height: dstH, BGR: dst}
}

// downscaleFrame reduces frame by an integer factor using box (area) averaging.
// This is equivalent to cv2.resize with INTER_AREA for exact integer scale
// factors, and avoids a CGO dependency in the root pyscenedetect package.
func (sm *SceneManager) downscaleFrame(frame *FrameData, factor int) *FrameData {
	dstW := max(1, frame.Width/factor)
	dstH := max(1, frame.Height/factor)
	dst := make([]byte, dstW*dstH*3)
	for y := 0; y < dstH; y++ {
		for x := 0; x < dstW; x++ {
			var sumB, sumG, sumR, count int
			for dy := 0; dy < factor; dy++ {
				sy := y*factor + dy
				if sy >= frame.Height {
					continue
				}
				for dx := 0; dx < factor; dx++ {
					sx := x*factor + dx
					if sx >= frame.Width {
						continue
					}
					i := (sy*frame.Width + sx) * 3
					sumB += int(frame.BGR[i])
					sumG += int(frame.BGR[i+1])
					sumR += int(frame.BGR[i+2])
					count++
				}
			}
			if count > 0 {
				di := (y*dstW + x) * 3
				dst[di] = byte(sumB / count)
				dst[di+1] = byte(sumG / count)
				dst[di+2] = byte(sumR / count)
			}
		}
	}
	return &FrameData{Width: dstW, Height: dstH, BGR: dst}
}
