// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// H.265 NAL unit type codes (libavcodec/hevc/hevc.h) and the name table
// from h2645_parse.c. The raw structures are ported in the H.265 phase.

package cbs

const (
	hevcNALTrailN       = 0
	hevcNALTrailR       = 1
	hevcNALTSAN         = 2
	hevcNALTSAR         = 3
	hevcNALSTSAN        = 4
	hevcNALSTSAR        = 5
	hevcNALRADLN        = 6
	hevcNALRADLR        = 7
	hevcNALRASLN        = 8
	hevcNALRASLR        = 9
	hevcNALBLAWLP       = 16
	hevcNALBLAWRADL     = 17
	hevcNALBLANLP       = 18
	hevcNALIDRWRADL     = 19
	hevcNALIDRNLP       = 20
	hevcNALCRANUT       = 21
	hevcNALRsvIrapVcl22 = 22
	hevcNALRsvIrapVcl23 = 23
	hevcNALVPS          = 32
	hevcNALSPS          = 33
	hevcNALPPS          = 34
	hevcNALAUD          = 35
	hevcNALEOSNUT       = 36
	hevcNALEOBNUT       = 37
	hevcNALFDNUT        = 38
	hevcNALSEIPrefix    = 39
	hevcNALSEISuffix    = 40
)

// hevcNALUnitName mirrors hevc_nal_type_name (libavcodec/h2645_parse.c).
func hevcNALUnitName(typ uint32) string {
	names := [64]string{
		"TRAIL_N", "TRAIL_R", "TSA_N", "TSA_R", "STSA_N", "STSA_R",
		"RADL_N", "RADL_R", "RASL_N", "RASL_R",
		"RSV_VCL_N10", "RSV_VCL_R11", "RSV_VCL_N12", "RSV_VLC_R13",
		"RSV_VCL_N14", "RSV_VCL_R15",
		"BLA_W_LP", "BLA_W_RADL", "BLA_N_LP", "IDR_W_RADL", "IDR_N_LP",
		"CRA_NUT", "RSV_IRAP_VCL22", "RSV_IRAP_VCL23",
		"RSV_VCL24", "RSV_VCL25", "RSV_VCL26", "RSV_VCL27", "RSV_VCL28",
		"RSV_VCL29", "RSV_VCL30", "RSV_VCL31",
		"VPS", "SPS", "PPS", "AUD", "EOS_NUT", "EOB_NUT", "FD_NUT",
		"SEI_PREFIX", "SEI_SUFFIX",
		"RSV_NVCL41", "RSV_NVCL42", "RSV_NVCL43", "RSV_NVCL44",
		"RSV_NVCL45", "RSV_NVCL46", "RSV_NVCL47",
		"UNSPEC48", "UNSPEC49", "UNSPEC50", "UNSPEC51", "UNSPEC52",
		"UNSPEC53", "UNSPEC54", "UNSPEC55", "UNSPEC56", "UNSPEC57",
		"UNSPEC58", "UNSPEC59", "UNSPEC60", "UNSPEC61", "UNSPEC62",
		"UNSPEC63",
	}
	if typ < 64 {
		return names[typ]
	}
	return "unknown"
}
