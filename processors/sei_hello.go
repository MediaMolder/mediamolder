// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"fmt"

	"github.com/MediaMolder/MediaMolder/av"
)

// seiHelloUUID is a fixed 16-byte identifier for the user_data_unregistered
// SEI messages emitted by this example processor. Anything starting with this
// value in the encoded bitstream came from MediaMolder's "sei_hello" demo.
//
// Note: this is an ASCII string, not a structured RFC 4122 UUID. For
// production processors, generate a proper UUID (e.g. uuid.New() from
// github.com/google/uuid) to avoid collisions with other SEI producers.
var seiHelloUUID = [16]byte{
	'M', 'M', 'H', 'e', 'l', 'l', 'o', 'S',
	'E', 'I', 'E', 'x', 'a', 'm', 'p', '1',
}

// SEIHello is a minimal example processor demonstrating how to attach a
// user_data_unregistered SEI to every video frame using
// av.Frame.AddSEIUnregisteredSideData. H.264 / HEVC encoders that honour SEI
// side data (libx265 by default, libx264 with `udu_sei=1`) will serialise the
// payload into the output bitstream as a user_data_unregistered SEI NAL.
//
// Non-video frames (audio, subtitles) are passed through untouched.
//
// Params:
//
//	"text": string — payload appended after the 16-byte UUID (default "hello").
//
// Example JSON config:
//
//	{
//	  "id": "stamp_sei",
//	  "type": "go_processor",
//	  "processor": "sei_hello",
//	  "params": { "text": "hello" }
//	}
type SEIHello struct {
	payload []byte
}

func (p *SEIHello) Init(params map[string]any) error {
	text := "hello"
	if v, ok := params["text"]; ok {
		s, ok := v.(string)
		if !ok {
			return fmt.Errorf("sei_hello: text must be a string, got %T", v)
		}
		text = s
	}
	// Wire format for FrameSideDataSEIUnregistered: 16-byte UUID + user data.
	p.payload = make([]byte, 0, len(seiHelloUUID)+len(text))
	p.payload = append(p.payload, seiHelloUUID[:]...)
	p.payload = append(p.payload, text...)
	return nil
}

func (p *SEIHello) Process(frame *av.Frame, ctx ProcessorContext) (*av.Frame, *Metadata, error) {
	if ctx.MediaType != av.MediaTypeVideo || frame == nil {
		return frame, nil, nil
	}
	if err := frame.AddSEIUnregisteredSideData(p.payload); err != nil {
		return frame, nil, fmt.Errorf("sei_hello: attach SEI: %w", err)
	}
	return frame, nil, nil
}

func (p *SEIHello) Close() error { return nil }

func init() {
	Register("sei_hello", func() Processor { return &SEIHello{} })
}
