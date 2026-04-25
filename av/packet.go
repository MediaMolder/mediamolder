// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package av

// #include "libavcodec/packet.h"
//
// static void packet_rescale_ts(AVPacket *pkt,
//                                int src_num, int src_den,
//                                int dst_num, int dst_den) {
//     AVRational src = {src_num, src_den};
//     AVRational dst = {dst_num, dst_den};
//     av_packet_rescale_ts(pkt, src, dst);
// }
// static AVPacket *packet_clone(const AVPacket *src) {
//     return av_packet_clone(src);
// }
import "C"

import "unsafe"

// Packet wraps an AVPacket. It must be closed after use via Close() or defer Close().
type Packet struct {
	p *C.AVPacket
}

// AllocPacket allocates a new AVPacket. The caller must call Close().
func AllocPacket() (*Packet, error) {
	p := C.av_packet_alloc()
	if p == nil {
		return nil, &Err{Code: -12, Message: "av_packet_alloc: out of memory"}
	}
	pkt := &Packet{p: p}
	leakTrack(unsafe.Pointer(p), "AVPacket")
	return pkt, nil
}

// Close unrefs the packet data and frees the AVPacket.
func (pkt *Packet) Close() error {
	if pkt.p != nil {
		leakUntrack(unsafe.Pointer(pkt.p))
		C.av_packet_free(&pkt.p)
		pkt.p = nil
	}
	return nil
}

// Unref releases the packet's buffer references without freeing the struct,
// making the packet ready for reuse.
func (pkt *Packet) Unref() {
	if pkt.p != nil {
		C.av_packet_unref(pkt.p)
	}
}

// StreamIndex returns the stream index this packet belongs to.
func (pkt *Packet) StreamIndex() int { return int(pkt.p.stream_index) }

// Size returns the packet data size in bytes.
func (pkt *Packet) Size() int { return int(pkt.p.size) }

// PTS returns the packet presentation timestamp.
func (pkt *Packet) PTS() int64 { return int64(pkt.p.pts) }

// DTS returns the packet decode timestamp.
func (pkt *Packet) DTS() int64 { return int64(pkt.p.dts) }

// SetStreamIndex sets the packet's stream index.
func (pkt *Packet) SetStreamIndex(i int) { pkt.p.stream_index = C.int(i) }

// Rescale converts the packet's timestamps from srcTB to dstTB using
// av_packet_rescale_ts. Both rationals are {num, den}; a zero denominator
// is silently ignored to make this safe to call when one side is unknown.
func (pkt *Packet) Rescale(srcTB, dstTB [2]int) {
	if pkt.p == nil {
		return
	}
	if srcTB[1] == 0 || dstTB[1] == 0 {
		return
	}
	C.packet_rescale_ts(pkt.p,
		C.int(srcTB[0]), C.int(srcTB[1]),
		C.int(dstTB[0]), C.int(dstTB[1]))
}

// Clone returns a new Packet that shares buffer references with pkt
// (via av_packet_clone). The returned packet must be Close()d
// independently.
func ClonePacket(src *Packet) (*Packet, error) {
	if src == nil || src.p == nil {
		return nil, &Err{Code: -22, Message: "ClonePacket: nil source"}
	}
	c := C.packet_clone(src.p)
	if c == nil {
		return nil, &Err{Code: -12, Message: "av_packet_clone: out of memory"}
	}
	leakTrack(unsafe.Pointer(c), "AVPacket")
	return &Packet{p: c}, nil
}

// raw returns the underlying C pointer. For use within the av package only.
func (pkt *Packet) raw() *C.AVPacket { return pkt.p }
