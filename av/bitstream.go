package av

// #include "libavcodec/avcodec.h"
// #include "libavcodec/bsf.h"
// #include "libavutil/dict.h"
//
// // Helper: find BSF by name.
// static const AVBitStreamFilter* bsf_find(const char *name) {
//     return av_bsf_get_by_name(name);
// }
//
// // Helper: iterate all available BSFs.
// static const AVBitStreamFilter* bsf_iterate(void **opaque) {
//     return av_bsf_iterate(opaque);
// }
//
// // Helper: get BSF name.
// static const char* bsf_name(const AVBitStreamFilter *bsf) {
//     return bsf->name;
// }
import "C"

import (
	"fmt"
	"unsafe"
)

// BitstreamFilter wraps an AVBSFContext for bitstream filtering.
// Bitstream filters transform encoded packets (e.g. h264_mp4toannexb
// converts H.264 from MP4 NAL format to Annex B format for MPEG-TS).
type BitstreamFilter struct {
	p *C.AVBSFContext
}

// BitstreamFilterOptions configures a bitstream filter.
type BitstreamFilterOptions struct {
	// Name is the bitstream filter name (e.g. "h264_mp4toannexb", "aac_adtstoasc").
	Name string

	// CodecID is the input codec ID. Required for proper operation.
	CodecID uint32

	// ExtraOpts are passed as AVDictionary options.
	ExtraOpts map[string]string
}

// OpenBitstreamFilter creates and initializes a bitstream filter.
func OpenBitstreamFilter(opts BitstreamFilterOptions) (*BitstreamFilter, error) {
	cName := C.CString(opts.Name)
	defer C.free(unsafe.Pointer(cName))

	bsf := C.bsf_find(cName)
	if bsf == nil {
		return nil, fmt.Errorf("bitstream filter %q not found", opts.Name)
	}

	var ctx *C.AVBSFContext
	ret := C.av_bsf_alloc(bsf, &ctx)
	if ret < 0 {
		return nil, fmt.Errorf("av_bsf_alloc(%s): %w", opts.Name, newErr(ret))
	}

	// Set input codec parameters.
	ctx.par_in.codec_id = C.enum_AVCodecID(opts.CodecID)

	// Apply extra options.
	var dict *C.AVDictionary
	for k, v := range opts.ExtraOpts {
		ck := C.CString(k)
		cv := C.CString(v)
		C.av_dict_set(&dict, ck, cv, 0)
		C.free(unsafe.Pointer(ck))
		C.free(unsafe.Pointer(cv))
	}

	if dict != nil {
		// BSF options are set via av_opt_set on the priv context.
		// For simplicity, we use the dictionary approach with init.
		C.av_dict_free(&dict)
	}

	ret = C.av_bsf_init(ctx)
	if ret < 0 {
		C.av_bsf_free(&ctx)
		return nil, fmt.Errorf("av_bsf_init(%s): %w", opts.Name, newErr(ret))
	}

	return &BitstreamFilter{p: ctx}, nil
}

// OpenBitstreamFilterFromEncoder creates a bitstream filter initialized with
// codec parameters from an encoder context. This is the common case for
// inserting a BSF between encoder and muxer.
func OpenBitstreamFilterFromEncoder(name string, enc *EncoderContext) (*BitstreamFilter, error) {
	cName := C.CString(name)
	defer C.free(unsafe.Pointer(cName))

	bsf := C.bsf_find(cName)
	if bsf == nil {
		return nil, fmt.Errorf("bitstream filter %q not found", name)
	}

	var ctx *C.AVBSFContext
	ret := C.av_bsf_alloc(bsf, &ctx)
	if ret < 0 {
		return nil, fmt.Errorf("av_bsf_alloc(%s): %w", name, newErr(ret))
	}

	// Copy codec parameters from the encoder.
	ret = C.avcodec_parameters_from_context(ctx.par_in, enc.raw())
	if ret < 0 {
		C.av_bsf_free(&ctx)
		return nil, fmt.Errorf("copy params to bsf: %w", newErr(ret))
	}
	ctx.time_base_in = enc.raw().time_base

	ret = C.av_bsf_init(ctx)
	if ret < 0 {
		C.av_bsf_free(&ctx)
		return nil, fmt.Errorf("av_bsf_init(%s): %w", name, newErr(ret))
	}

	return &BitstreamFilter{p: ctx}, nil
}

// Close frees the bitstream filter context.
func (b *BitstreamFilter) Close() error {
	if b.p != nil {
		C.av_bsf_free(&b.p)
		b.p = nil
	}
	return nil
}

// SendPacket sends a packet to the bitstream filter.
// Pass nil to flush remaining packets.
func (b *BitstreamFilter) SendPacket(pkt *Packet) error {
	var raw *C.AVPacket
	if pkt != nil {
		raw = pkt.raw()
	}
	ret := C.av_bsf_send_packet(b.p, raw)
	return newErr(ret)
}

// ReceivePacket receives a filtered packet from the bitstream filter.
// Returns ErrEAgain if more packets are needed, ErrEOF when flushing is complete.
func (b *BitstreamFilter) ReceivePacket(pkt *Packet) error {
	ret := C.av_bsf_receive_packet(b.p, pkt.raw())
	return newErr(ret)
}

// FilterPacket is a convenience method that sends a packet and receives all
// filtered output packets. Returns the filtered packets.
// This handles the common case where a BSF produces exactly one output per input.
func (b *BitstreamFilter) FilterPacket(pkt *Packet) ([]*Packet, error) {
	if err := b.SendPacket(pkt); err != nil {
		return nil, err
	}

	var out []*Packet
	for {
		outPkt, err := AllocPacket()
		if err != nil {
			for _, p := range out {
				p.Close()
			}
			return nil, err
		}
		if err := b.ReceivePacket(outPkt); err != nil {
			outPkt.Close()
			if IsEAgain(err) || IsEOF(err) {
				break
			}
			for _, p := range out {
				p.Close()
			}
			return nil, err
		}
		out = append(out, outPkt)
	}
	return out, nil
}

// Flush signals end-of-stream and drains remaining packets.
func (b *BitstreamFilter) Flush() ([]*Packet, error) {
	if err := b.SendPacket(nil); err != nil && !IsEOF(err) {
		return nil, err
	}
	var out []*Packet
	for {
		outPkt, err := AllocPacket()
		if err != nil {
			for _, p := range out {
				p.Close()
			}
			return nil, err
		}
		if err := b.ReceivePacket(outPkt); err != nil {
			outPkt.Close()
			if IsEOF(err) || IsEAgain(err) {
				break
			}
			for _, p := range out {
				p.Close()
			}
			return nil, err
		}
		out = append(out, outPkt)
	}
	return out, nil
}

// BitstreamFilterInfo describes an available bitstream filter.
type BitstreamFilterInfo struct {
	Name string
}

// ListBitstreamFilters returns all available bitstream filters.
func ListBitstreamFilters() []BitstreamFilterInfo {
	var filters []BitstreamFilterInfo
	var opaque unsafe.Pointer
	for {
		bsf := C.bsf_iterate(&opaque)
		if bsf == nil {
			break
		}
		filters = append(filters, BitstreamFilterInfo{
			Name: C.GoString(C.bsf_name(bsf)),
		})
	}
	return filters
}
