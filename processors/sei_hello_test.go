// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package processors

import (
	"bytes"
	"testing"

	"github.com/MediaMolder/MediaMolder/av"
)

func TestSEIHello_DefaultPayloadAttachedToVideo(t *testing.T) {
	p := &SEIHello{}
	if err := p.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	frame := av.NewTestFrame(t, 64, 48, 0) // YUV420P
	defer frame.Close()

	ctx := ProcessorContext{StreamID: "v:0", MediaType: av.MediaTypeVideo}
	out, md, err := p.Process(frame, ctx)
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if out != frame {
		t.Fatalf("expected frame passthrough, got %v", out)
	}
	if md != nil {
		t.Fatalf("expected no metadata for the simple SEI example, got %+v", md)
	}

	got := frame.SEIUnregisteredSideData()
	if len(got) != 1 {
		t.Fatalf("expected 1 SEI side data entry, got %d", len(got))
	}
	want := append(append([]byte{}, seiHelloUUID[:]...), []byte("hello")...)
	if !bytes.Equal(got[0], want) {
		t.Fatalf("SEI payload = %x, want %x", got[0], want)
	}
}

func TestSEIHello_CustomTextOverride(t *testing.T) {
	p := &SEIHello{}
	if err := p.Init(map[string]any{"text": "world"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	frame := av.NewTestFrame(t, 32, 32, 0)
	defer frame.Close()

	_, _, err := p.Process(frame, ProcessorContext{MediaType: av.MediaTypeVideo})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}

	got := frame.SEIUnregisteredSideData()
	if len(got) != 1 {
		t.Fatalf("expected 1 SEI side data entry, got %d", len(got))
	}
	want := append(append([]byte{}, seiHelloUUID[:]...), []byte("world")...)
	if !bytes.Equal(got[0], want) {
		t.Fatalf("SEI payload = %x, want %x", got[0], want)
	}
}

func TestSEIHello_NonVideoPassthrough(t *testing.T) {
	p := &SEIHello{}
	if err := p.Init(nil); err != nil {
		t.Fatalf("Init: %v", err)
	}

	frame := av.NewTestFrame(t, 16, 16, 0)
	defer frame.Close()

	_, _, err := p.Process(frame, ProcessorContext{MediaType: av.MediaTypeAudio})
	if err != nil {
		t.Fatalf("Process: %v", err)
	}
	if got := frame.SEIUnregisteredSideData(); got != nil {
		t.Fatalf("audio frame must not get SEI side data, got %v", got)
	}
}

func TestSEIHello_BadTextType(t *testing.T) {
	p := &SEIHello{}
	if err := p.Init(map[string]any{"text": 42}); err == nil {
		t.Fatal("expected error for non-string text param")
	}
}

func TestSEIHello_Registered(t *testing.T) {
	p, err := Get("sei_hello")
	if err != nil {
		t.Fatalf("sei_hello not registered: %v", err)
	}
	if p == nil {
		t.Fatal("expected non-nil processor")
	}
}
