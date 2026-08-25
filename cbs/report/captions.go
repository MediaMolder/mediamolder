// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later
//
// Closed-caption carriage detection for the SEI / metadata summaries.
// Captions in H.264/H.265 ride in SEI payload type 4
// (user_data_registered_itu_t_t35) and in AV1 metadata OBU type 4
// (METADATA_TYPE_ITUT_T35), wrapped per ATSC A/53 Part 4 / SCTE-128:
// country 0xB5, provider 0x0031, user_identifier "GA94",
// user_data_type_code 0x03, then cc_count (cc_valid, cc_type, cc_data_1,
// cc_data_2) triplets — cc_type 0/1 = CEA-608 field 1/2, 2/3 = CEA-708
// DTVCC.
//
// This is a dump, not a decoder: the cc_data bytes are reported verbatim;
// rendering 608 characters or 708 services is a separate processor's job.

package report

import "encoding/hex"

// rawHexCap bounds the raw_hex dumps: caption and HDR SEI payloads are
// small; anything longer is truncated with raw_truncated set.
const rawHexCap = 256

// rawHex renders up to rawHexCap bytes as hex; truncated reports whether
// the payload was longer.
func rawHex(data []byte) (hexStr string, truncated bool) {
	if len(data) > rawHexCap {
		return hex.EncodeToString(data[:rawHexCap]), true
	}
	return hex.EncodeToString(data), false
}

// addRawHex attaches the raw payload dump to a summary entry.
func addRawHex(e map[string]any, data []byte) {
	h, trunc := rawHex(data)
	e["raw_hex"] = h
	if trunc {
		e["raw_truncated"] = true
	}
}

// parseA53Captions inspects T.35 payload bytes (starting at the provider
// code, i.e. after the country code byte(s)) for the ATSC A/53 / SCTE-128
// caption wrapper and, when present, attaches the caption fields to e.
// countryCode 0xB5 (United States) is required by A/53.
func parseA53Captions(e map[string]any, countryCode uint8, data []byte) {
	if countryCode != 0xB5 || len(data) < 7 {
		return
	}
	provider := uint16(data[0])<<8 | uint16(data[1])
	if provider != 0x0031 {
		return
	}
	userIdentifier := string(data[2:6])
	if userIdentifier != "GA94" {
		return
	}
	e["provider_code"] = provider
	e["user_identifier"] = userIdentifier
	userDataType := data[6]
	e["user_data_type_code"] = userDataType
	if userDataType != 0x03 { // not cc_data (e.g. 0x06 bar data)
		return
	}
	if len(data) < 9 {
		return
	}
	// cc_data(): process_em_data_flag(1) process_cc_data_flag(1)
	// additional_data_flag(1) cc_count(5), em_data(8), then triplets.
	ccCount := int(data[7] & 0x1f)
	e["cc_count"] = ccCount
	triplets := data[9:]
	var cc []map[string]any
	has608, has708 := false, false
	for i := 0; i < ccCount && (i+1)*3 <= len(triplets); i++ {
		b := triplets[i*3]
		valid := b&0x04 != 0
		ccType := b & 0x03
		if valid {
			if ccType <= 1 {
				has608 = true
			} else {
				has708 = true
			}
		}
		cc = append(cc, map[string]any{
			"valid": valid,
			"type":  ccType,
			"d1":    hex.EncodeToString(triplets[i*3+1 : i*3+2]),
			"d2":    hex.EncodeToString(triplets[i*3+2 : i*3+3]),
		})
	}
	if cc != nil {
		e["cc"] = cc
	}
	var contains []string
	if has608 {
		contains = append(contains, "cea608")
	}
	if has708 {
		contains = append(contains, "cea708")
	}
	if contains != nil {
		e["contains"] = contains
	}
}
