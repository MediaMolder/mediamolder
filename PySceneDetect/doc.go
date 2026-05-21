//
//            PySceneDetect: Python-Based Video Scene Detector
//  -------------------------------------------------------------------
//     [  Site:   https://scenedetect.com                           ]
//     [  Docs:   https://scenedetect.com/docs/                     ]
//     [  Github: https://github.com/Breakthrough/PySceneDetect/    ]
//
// Copyright (C) 2014 Brandon Castellano <http://www.bcastell.com>.
// PySceneDetect is licensed under the BSD 3-Clause License; see the
// included LICENSE file, or visit one of the above pages for details.
// License: https://github.com/Breakthrough/PySceneDetect/blob/main/LICENSE
//

// Package pyscenedetect is a direct Go port of PySceneDetect by Brandon
// Castellano (https://github.com/Breakthrough/PySceneDetect).
//
// Every algorithm and default parameter is ported faithfully from PySceneDetect
// v0.7. The Python source is the authoritative reference; this package aims to
// produce identical scene boundaries for the same input and settings.
//
// # Detectors
//
// Five scene detection algorithms are available, matching the PySceneDetect
// command-line options:
//
//   - ContentDetector  — fast cuts via weighted HSV delta (detect-content)
//   - AdaptiveDetector — camera-motion-robust rolling-average variant (detect-adaptive)
//   - ThresholdDetector — fade-in/out via average pixel intensity (detect-threshold)
//   - HashDetector      — perceptual DCT hash comparison (detect-hash)
//   - HistogramDetector — YUV Y-channel histogram correlation (detect-hist)
//
// # Integration
//
// In MediaMolder, detectors are accessed via processor nodes registered as
// "scene_change_content", "scene_change_adaptive", etc., or through the
// `mediamolder py-scene-detect` CLI subcommand.
//
// # Attribution
//
// This package is a port of PySceneDetect, licensed under the BSD 3-Clause
// License. All credit for the detection algorithms belongs to Brandon Castellano
// and the PySceneDetect contributors. See LICENSE for the full license text.
package pyscenedetect
