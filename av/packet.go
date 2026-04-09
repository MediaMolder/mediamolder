package av

// #include "libavcodec/packet.h"
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

// raw returns the underlying C pointer. For use within the av package only.
func (pkt *Packet) raw() *C.AVPacket { return pkt.p }
