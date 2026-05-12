// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include <stdint.h>
// #include <string.h>
// #include "libavutil/error.h"
// #include "libavutil/frame.h"
//
// // Generic side data attach: allocates a new AVFrameSideData of the given
// // type on the frame and copies `size` bytes from `data` into it. Returns 0
// // on success or an AVERROR code (typically AVERROR(ENOMEM)).
// static int frame_add_side_data(AVFrame *frame, int type,
//                                const uint8_t *data, size_t size) {
//     AVFrameSideData *sd = av_frame_new_side_data(
//         frame, (enum AVFrameSideDataType)type, size);
//     if (!sd) return AVERROR(ENOMEM);
//     if (data && size > 0) memcpy(sd->data, data, size);
//     return 0;
// }
//
// static void frame_remove_side_data(AVFrame *frame, int type) {
//     av_frame_remove_side_data(frame, (enum AVFrameSideDataType)type);
// }
//
// static const char *frame_side_data_name_for(int type) {
//     return av_frame_side_data_name((enum AVFrameSideDataType)type);
// }
//
// static int frame_nb_side_data(const AVFrame *frame) {
//     return frame->nb_side_data;
// }
//
// static AVFrameSideData *frame_side_data_at(const AVFrame *frame, int idx) {
//     return frame->side_data[idx];
// }
//
// static int frame_side_data_type(const AVFrameSideData *sd) {
//     return sd->type;
// }
//
// static uint8_t *frame_side_data_data(const AVFrameSideData *sd) {
//     return sd->data;
// }
//
// static size_t frame_side_data_size(const AVFrameSideData *sd) {
//     return sd->size;
// }
import "C"

import (
	"math"
	"unsafe"
)

// FrameSideDataType mirrors libavutil's enum AVFrameSideDataType. Values are
// taken from the linked FFmpeg headers, so they always match the runtime
// libavutil that mediamolder is built against.
//
// Most of these types are general-purpose frame side data (motion vectors,
// pan-and-scan, downmix info, ...). The H.264 / HEVC encoders in libavcodec
// additionally serialise a well-defined subset of them into SEI NAL units on
// the output bitstream:
//
//   - FrameSideDataA53CC                    -> user_data_registered_itu_t_t35 SEI (CEA-608/708)
//   - FrameSideDataMasteringDisplayMetadata -> mastering_display_colour_volume SEI (HDR10)
//   - FrameSideDataContentLightLevel        -> content_light_level_info SEI (HDR10)
//   - FrameSideDataDynamicHDRPlus           -> SMPTE 2094-40 / HDR10+ SEI (T.35)
//   - FrameSideDataDynamicHDRVivid          -> CUVA HDR Vivid SEI
//   - FrameSideDataAmbientViewingEnvironment-> ambient_viewing_environment SEI
//   - FrameSideDataS12MTimecode             -> SMPTE ST 12-1 timecode SEI / pic_timing
//   - FrameSideDataSEIUnregistered          -> user_data_unregistered SEI
//   - FrameSideDataFilmGrainParams          -> film_grain_characteristics SEI
//   - FrameSideDataDoViRPUBuffer            -> Dolby Vision RPU NAL (HEVC) / metadata OBU (AV1)
//
// Whether a particular encoder honours a given side data type depends on the
// codec implementation (and sometimes on encoder options like libx264's
// `udu_sei`). Side data not understood by the encoder is silently dropped at
// encode time.
type FrameSideDataType int

const (
	FrameSideDataPanScan                   FrameSideDataType = C.AV_FRAME_DATA_PANSCAN
	FrameSideDataA53CC                     FrameSideDataType = C.AV_FRAME_DATA_A53_CC
	FrameSideDataStereo3D                  FrameSideDataType = C.AV_FRAME_DATA_STEREO3D
	FrameSideDataMatrixEncoding            FrameSideDataType = C.AV_FRAME_DATA_MATRIXENCODING
	FrameSideDataDownmixInfo               FrameSideDataType = C.AV_FRAME_DATA_DOWNMIX_INFO
	FrameSideDataReplayGain                FrameSideDataType = C.AV_FRAME_DATA_REPLAYGAIN
	FrameSideDataDisplayMatrix             FrameSideDataType = C.AV_FRAME_DATA_DISPLAYMATRIX
	FrameSideDataAFD                       FrameSideDataType = C.AV_FRAME_DATA_AFD
	FrameSideDataMotionVectors             FrameSideDataType = C.AV_FRAME_DATA_MOTION_VECTORS
	FrameSideDataSkipSamples               FrameSideDataType = C.AV_FRAME_DATA_SKIP_SAMPLES
	FrameSideDataAudioServiceType          FrameSideDataType = C.AV_FRAME_DATA_AUDIO_SERVICE_TYPE
	FrameSideDataMasteringDisplayMetadata  FrameSideDataType = C.AV_FRAME_DATA_MASTERING_DISPLAY_METADATA
	FrameSideDataGOPTimecode               FrameSideDataType = C.AV_FRAME_DATA_GOP_TIMECODE
	FrameSideDataSpherical                 FrameSideDataType = C.AV_FRAME_DATA_SPHERICAL
	FrameSideDataContentLightLevel         FrameSideDataType = C.AV_FRAME_DATA_CONTENT_LIGHT_LEVEL
	FrameSideDataICCProfile                FrameSideDataType = C.AV_FRAME_DATA_ICC_PROFILE
	FrameSideDataS12MTimecode              FrameSideDataType = C.AV_FRAME_DATA_S12M_TIMECODE
	FrameSideDataDynamicHDRPlus            FrameSideDataType = C.AV_FRAME_DATA_DYNAMIC_HDR_PLUS
	FrameSideDataRegionsOfInterest         FrameSideDataType = C.AV_FRAME_DATA_REGIONS_OF_INTEREST
	FrameSideDataVideoEncParams            FrameSideDataType = C.AV_FRAME_DATA_VIDEO_ENC_PARAMS
	FrameSideDataSEIUnregistered           FrameSideDataType = C.AV_FRAME_DATA_SEI_UNREGISTERED
	FrameSideDataFilmGrainParams           FrameSideDataType = C.AV_FRAME_DATA_FILM_GRAIN_PARAMS
	FrameSideDataDetectionBBoxes           FrameSideDataType = C.AV_FRAME_DATA_DETECTION_BBOXES
	FrameSideDataDoViRPUBuffer             FrameSideDataType = C.AV_FRAME_DATA_DOVI_RPU_BUFFER
	FrameSideDataDoViMetadata              FrameSideDataType = C.AV_FRAME_DATA_DOVI_METADATA
	FrameSideDataDynamicHDRVivid           FrameSideDataType = C.AV_FRAME_DATA_DYNAMIC_HDR_VIVID
	FrameSideDataAmbientViewingEnvironment FrameSideDataType = C.AV_FRAME_DATA_AMBIENT_VIEWING_ENVIRONMENT
	// FrameSideDataVideoHint requires FFmpeg 7.0 (libavutil 59.x) or later.
	FrameSideDataVideoHint FrameSideDataType = C.AV_FRAME_DATA_VIDEO_HINT
)

// Name returns the human-readable name FFmpeg associates with this side data
// type (e.g. "SEI unregistered", "ATSC A53 Part 4 Closed Captions"). Returns
// an empty string for unknown / unmapped enum values.
func (t FrameSideDataType) Name() string {
	cstr := C.frame_side_data_name_for(C.int(t))
	if cstr == nil {
		return ""
	}
	return C.GoString(cstr)
}

// FrameSideDataEntry is a (type, payload) pair returned by Frame.AllSideData.
// Data is an independent copy of the underlying AVFrameSideData buffer, so the
// caller may keep it after the Frame has been freed.
type FrameSideDataEntry struct {
	Type FrameSideDataType
	Data []byte
}

// AddSideData attaches a new side data entry of the given type to the frame,
// copying payload into a fresh AVFrameSideData buffer owned by libavutil.
// Multiple entries of the same type may be attached (this is what FFmpeg does
// for repeated SEI messages such as A53 closed captions or S12M timecodes).
//
// The exact byte layout of payload is type-specific:
//
//   - FrameSideDataSEIUnregistered: 16-byte UUID followed by user data.
//   - FrameSideDataA53CC: ATSC A/53 Part 4 cc_data byte stream.
//   - FrameSideDataS12MTimecode: 4 little-endian uint32 (count + up to 3
//     packed timecodes) — see Frame.AddS12MTimecode for a typed helper.
//   - FrameSideDataMasteringDisplayMetadata, FrameSideDataContentLightLevel,
//     FrameSideDataDynamicHDRPlus, FrameSideDataDoVi*, FrameSideDataStereo3D,
//     FrameSideDataDisplayMatrix, FrameSideDataSpherical, ... : payload must
//     match the corresponding libavutil C struct exactly (sizeof and field
//     layout). Use the dedicated av-package helpers when available; this raw
//     byte API is provided as an escape hatch.
//
// Passing an empty payload allocates a zero-byte side data entry, which is
// almost certainly not what callers want, so the call returns EINVAL.
func (f *Frame) AddSideData(typ FrameSideDataType, payload []byte) error {
	if f == nil || f.p == nil {
		return &Err{Code: -22, Message: "AddSideData: nil frame"}
	}
	if len(payload) == 0 {
		return &Err{Code: -22, Message: "AddSideData: empty payload"}
	}
	rc := C.frame_add_side_data(
		f.p,
		C.int(typ),
		(*C.uint8_t)(unsafe.Pointer(&payload[0])),
		C.size_t(len(payload)),
	)
	if rc < 0 {
		return newErr(rc)
	}
	return nil
}

// SideData returns copies of every side data entry of the given type attached
// to the frame, in the order libavutil stored them. The returned slices are
// independent copies and remain valid after the frame is freed. Returns nil
// when no entries of that type are present.
func (f *Frame) SideData(typ FrameSideDataType) [][]byte {
	if f == nil || f.p == nil {
		return nil
	}
	n := int(C.frame_nb_side_data(f.p))
	if n == 0 {
		return nil
	}
	var out [][]byte
	for i := 0; i < n; i++ {
		sd := C.frame_side_data_at(f.p, C.int(i))
		if sd == nil || FrameSideDataType(C.frame_side_data_type(sd)) != typ {
			continue
		}
		size := int(C.frame_side_data_size(sd))
		if size == 0 {
			out = append(out, nil)
			continue
		}
		if size > math.MaxInt32 {
			continue
		}
		out = append(out, C.GoBytes(unsafe.Pointer(C.frame_side_data_data(sd)), C.int(size)))
	}
	return out
}

// AllSideData returns copies of every AVFrameSideData entry attached to the
// frame. This is useful for diagnostics and for processors that need to
// forward unknown side data downstream untouched.
func (f *Frame) AllSideData() []FrameSideDataEntry {
	if f == nil || f.p == nil {
		return nil
	}
	n := int(C.frame_nb_side_data(f.p))
	if n == 0 {
		return nil
	}
	out := make([]FrameSideDataEntry, 0, n)
	for i := 0; i < n; i++ {
		sd := C.frame_side_data_at(f.p, C.int(i))
		if sd == nil {
			continue
		}
		typ := FrameSideDataType(C.frame_side_data_type(sd))
		size := int(C.frame_side_data_size(sd))
		var data []byte
		if size > 0 && size <= math.MaxInt32 {
			data = C.GoBytes(unsafe.Pointer(C.frame_side_data_data(sd)), C.int(size))
		}
		out = append(out, FrameSideDataEntry{Type: typ, Data: data})
	}
	return out
}

// RemoveSideData detaches every side data entry of the given type from the
// frame. No-op if none are attached or the frame is nil.
func (f *Frame) RemoveSideData(typ FrameSideDataType) {
	if f == nil || f.p == nil {
		return
	}
	C.frame_remove_side_data(f.p, C.int(typ))
}

// AddSEIUnregisteredSideData attaches a complete user-data-unregistered SEI
// payload to the frame. The payload must include the leading 16-byte UUID
// followed by the user data; libavcodec turns this side data into a
// codec-specific user_data_unregistered SEI NAL when the encoder supports it
// (libx264 requires `udu_sei=1`, libx265 ships SEI by default).
//
// Convenience wrapper around AddSideData(FrameSideDataSEIUnregistered, ...).
func (f *Frame) AddSEIUnregisteredSideData(payload []byte) error {
	if len(payload) < 16 {
		return &Err{Code: -22, Message: "AddSEIUnregisteredSideData: payload must include 16-byte UUID"}
	}
	return f.AddSideData(FrameSideDataSEIUnregistered, payload)
}

// SEIUnregisteredSideData returns copies of all AV_FRAME_DATA_SEI_UNREGISTERED
// payloads attached to the frame. Each payload includes the leading 16-byte
// UUID. Convenience wrapper around SideData(FrameSideDataSEIUnregistered).
func (f *Frame) SEIUnregisteredSideData() [][]byte {
	return f.SideData(FrameSideDataSEIUnregistered)
}

// AddA53ClosedCaptions attaches a CEA-608/708 closed-caption payload (ATSC
// A/53 Part 4 cc_data byte stream) to the frame. H.264 and HEVC encoders that
// support it serialise the payload into a user_data_registered_itu_t_t35 SEI
// with the standard A/53 country/provider/user_identifier prefix.
func (f *Frame) AddA53ClosedCaptions(payload []byte) error {
	return f.AddSideData(FrameSideDataA53CC, payload)
}

// A53ClosedCaptions returns copies of all AV_FRAME_DATA_A53_CC payloads
// attached to the frame.
func (f *Frame) A53ClosedCaptions() [][]byte {
	return f.SideData(FrameSideDataA53CC)
}

// S12MTimecode is a single SMPTE ST 12-1 timecode packed in the canonical
// FFmpeg form (one 32-bit word per timecode, see libavutil/timecode.h).
type S12MTimecode uint32

// AddS12MTimecodes attaches one to three SMPTE ST 12-1 timecodes to the frame
// in the AV_FRAME_DATA_S12M_TIMECODE layout consumed by libavcodec
// (one uint32 count followed by up to three packed timecode words, all in
// host byte order). H.264/HEVC encoders surface these as pic_timing /
// timecode SEI messages.
//
// Returns EINVAL if no timecodes are provided or if more than three are.
func (f *Frame) AddS12MTimecodes(tcs ...S12MTimecode) error {
	if len(tcs) == 0 || len(tcs) > 3 {
		return &Err{Code: -22, Message: "AddS12MTimecodes: must provide 1..3 timecodes"}
	}
	// AV_FRAME_DATA_S12M_TIMECODE layout: uint32[4] = {count, tc0, tc1, tc2}.
	const wordSize = 4
	buf := make([]byte, 4*wordSize)
	writeU32 := func(off int, v uint32) {
		buf[off+0] = byte(v)
		buf[off+1] = byte(v >> 8)
		buf[off+2] = byte(v >> 16)
		buf[off+3] = byte(v >> 24)
	}
	writeU32(0, uint32(len(tcs)))
	for i, tc := range tcs {
		writeU32(wordSize*(i+1), uint32(tc))
	}
	return f.AddSideData(FrameSideDataS12MTimecode, buf)
}

// S12MTimecodes returns the SMPTE ST 12-1 timecode words attached to the
// frame, ignoring the leading count field. Returns nil if no S12M timecode
// side data is attached.
func (f *Frame) S12MTimecodes() []S12MTimecode {
	all := f.SideData(FrameSideDataS12MTimecode)
	if len(all) == 0 {
		return nil
	}
	const wordSize = 4
	buf := all[0]
	if len(buf) < wordSize {
		return nil
	}
	count := uint32(buf[0]) | uint32(buf[1])<<8 | uint32(buf[2])<<16 | uint32(buf[3])<<24
	if count == 0 {
		return nil
	}
	maxAvailable := uint32((len(buf) - wordSize) / wordSize)
	if count > maxAvailable {
		// Buffer is shorter than the count field claims; the side data is
		// malformed. Return nil so callers see no timecodes rather than
		// silently truncated results.
		return nil
	}
	out := make([]S12MTimecode, 0, count)
	for i := uint32(0); i < count; i++ {
		off := wordSize * (1 + int(i))
		v := uint32(buf[off+0]) | uint32(buf[off+1])<<8 |
			uint32(buf[off+2])<<16 | uint32(buf[off+3])<<24
		out = append(out, S12MTimecode(v))
	}
	return out
}
