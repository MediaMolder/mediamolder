// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package report

import (
	"testing"

	"github.com/MediaMolder/MediaMolder/cbs"
)

// a53Payload builds a T.35 payload (provider code onward) carrying the
// ATSC A/53 cc_data wrapper with the given triplets.
func a53Payload(triplets [][3]byte) []byte {
	data := []byte{0x00, 0x31, 'G', 'A', '9', '4', 0x03}
	data = append(data, 0x40|byte(len(triplets))&0x1f) // process_cc_data_flag, cc_count
	data = append(data, 0xFF)                          // em_data
	for _, t := range triplets {
		data = append(data, t[0], t[1], t[2])
	}
	return append(data, 0xFF) // marker_bits
}

func TestA53CaptionDetection(t *testing.T) {
	list := &cbs.SEIRawMessageList{Messages: []cbs.SEIRawMessage{{
		PayloadType: 4,
		Payload: &cbs.SEIRawUserDataRegistered{
			ItuTT35CountryCode: 0xB5,
			Data: a53Payload([][3]byte{
				{0xFC, 0x94, 0x20}, // cc_valid, type 0 → CEA-608 field 1
				{0xFD, 0x00, 0x00}, // cc_valid, type 1 → CEA-608 field 2
				{0xFE, 0x12, 0x34}, // cc_valid, type 2 → CEA-708
				{0xFA, 0x00, 0x00}, // not valid
			}),
		},
	}}}
	e := seiListSummary(list)[0]
	if e["user_identifier"] != "GA94" || e["user_data_type_code"] != uint8(3) {
		t.Fatalf("wrapper not detected: %v", e)
	}
	if e["cc_count"] != 4 {
		t.Fatalf("cc_count: %v", e["cc_count"])
	}
	contains := e["contains"].([]string)
	if len(contains) != 2 || contains[0] != "cea608" || contains[1] != "cea708" {
		t.Fatalf("contains: %v", contains)
	}
	cc := e["cc"].([]map[string]any)
	if len(cc) != 4 || cc[0]["d1"] != "94" || cc[0]["type"] != uint8(0) ||
		cc[2]["type"] != uint8(2) || cc[3]["valid"] != false {
		t.Fatalf("cc triplets: %v", cc)
	}
	if _, ok := e["raw_hex"].(string); !ok {
		t.Fatal("raw_hex missing")
	}
}

func TestA53NonCaptionT35(t *testing.T) {
	// HDR10+-style T.35 (different provider): raw_hex only, no cc fields.
	list := &cbs.SEIRawMessageList{Messages: []cbs.SEIRawMessage{{
		PayloadType: 4,
		Payload: &cbs.SEIRawUserDataRegistered{
			ItuTT35CountryCode: 0xB5,
			Data:               []byte{0x00, 0x3C, 0x00, 0x01, 0x04},
		},
	}}}
	e := seiListSummary(list)[0]
	if _, ok := e["cc_count"]; ok {
		t.Fatalf("cc fields on non-caption T.35: %v", e)
	}
	if e["raw_hex"] != "003c000104" {
		t.Fatalf("raw_hex: %v", e["raw_hex"])
	}
}

func TestRawHexTruncation(t *testing.T) {
	big := make([]byte, rawHexCap+100)
	e := map[string]any{}
	addRawHex(e, big)
	if len(e["raw_hex"].(string)) != 2*rawHexCap || e["raw_truncated"] != true {
		t.Fatalf("truncation: len %d, flag %v", len(e["raw_hex"].(string)), e["raw_truncated"])
	}
}

func TestCaptionUnitsAlias(t *testing.T) {
	f := newUnitFilter([]string{"caption"})
	for _, name := range []string{"SEI", "SEI_PREFIX", "SEI_SUFFIX", "METADATA"} {
		if !f.match(&cbs.Unit{TypeName: name}) {
			t.Fatalf("caption alias must match %s", name)
		}
	}
	if f.match(&cbs.Unit{TypeName: "SPS"}) {
		t.Fatal("caption alias must not match SPS")
	}
}
