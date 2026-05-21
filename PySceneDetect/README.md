# PySceneDetect — Go Port

This package is a direct Go port of **[PySceneDetect](https://github.com/Breakthrough/PySceneDetect)**
by **Brandon Castellano**, licensed under the BSD 3-Clause License.

> PySceneDetect: Python-Based Video Scene Detector  
> Site: https://scenedetect.com  
> Docs: https://scenedetect.com/docs/  
> GitHub: https://github.com/Breakthrough/PySceneDetect  
> Copyright (C) 2014 Brandon Castellano. All rights reserved.  
> License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE

---

## What this is

Every algorithm, data structure, and default parameter in this package is ported directly
from PySceneDetect v0.7. The Python source code is the authoritative reference; the Go port
aims to produce identical scene boundaries for the same input video and detector settings.

## Package layout

```
PySceneDetect/
  LICENSE                 BSD 3-Clause (verbatim from upstream)
  README.md               This file
  doc.go                  Package godoc
  common.go               Scene, SceneList, CutList, CropRegion, Interpolation, FrameData
  frame_timecode.go       FrameTimecode — frame-accurate timestamp (from common.py)
  scene_detector.go       SceneDetector interface + FlashFilter (from detector.py)
  stats_manager.go        StatsManager — per-frame metric store (from stats_manager.py)
  detectors/              ContentDetector, AdaptiveDetector, ThresholdDetector,
                          HashDetector, HistogramDetector (Phases 3–6)
  internal/               Image math primitives: colorspace, DCT, Canny, histogram (Phase 2)
  processors/             processors.Processor wrappers for use in MediaMolder graphs (Phase 7)
  cmd/                    `mediamolder py-scene-detect` CLI subcommand (Phase 7)
```

## Usage in MediaMolder

Scene detection via this package is exposed through:

1. **Graph processor nodes** — add a `scene_change_content` (or `scene_change_adaptive`,
   `scene_change_threshold`, etc.) node to a pipeline JSON config.
2. **CLI** — `mediamolder py-scene-detect --detector content input.mp4`

Both surfaces credit PySceneDetect in their help text and output metadata.

## Correspondence to Python source

| Go file                | Python source file                          | Copyright year |
|------------------------|---------------------------------------------|----------------|
| `frame_timecode.go`    | `scenedetect/common.py`                     | 2025           |
| `common.go`            | `scenedetect/common.py`                     | 2025           |
| `scene_detector.go`    | `scenedetect/detector.py`                   | 2025           |
| `stats_manager.go`     | `scenedetect/stats_manager.py`              | 2018           |
| `detectors/content_*`  | `scenedetect/detectors/content_detector.py` | 2018           |
| `detectors/adaptive_*` | `scenedetect/detectors/adaptive_detector.py`| 2021           |
| `detectors/threshold_*`| `scenedetect/detectors/threshold_detector.py`| 2018          |
| `detectors/hash_*`     | `scenedetect/detectors/hash_detector.py`    | 2022           |
| `detectors/histogram_*`| `scenedetect/detectors/histogram_detector.py`| 2024          |

## Building

This package has no additional dependencies beyond those already present in MediaMolder.
The image processing primitives in `internal/` use libswscale via the existing CGO bindings
in `av/`. No new Go modules or C libraries are required.

```sh
go test ./PySceneDetect/...
```
