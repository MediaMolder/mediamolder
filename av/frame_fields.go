package av

// #include "libavutil/frame.h"
// #include "libavutil/dict.h"
// static int mm_frame_interlaced(const AVFrame *f) { return (f->flags & AV_FRAME_FLAG_INTERLACED) != 0; }
// static int mm_frame_tff(const AVFrame *f)        { return (f->flags & AV_FRAME_FLAG_TOP_FIELD_FIRST) != 0; }
// static void mm_frame_set_fields(AVFrame *f, int interlaced, int tff) {
//     if (interlaced) f->flags |= AV_FRAME_FLAG_INTERLACED; else f->flags &= ~AV_FRAME_FLAG_INTERLACED;
//     if (tff)        f->flags |= AV_FRAME_FLAG_TOP_FIELD_FIRST; else f->flags &= ~AV_FRAME_FLAG_TOP_FIELD_FIRST;
// }
// static const char *mm_frame_meta(const AVFrame *f, const char *key) {
//     AVDictionaryEntry *e = av_dict_get(f->metadata, key, NULL, 0);
//     return e ? e->value : NULL;
// }
import "C"
import "unsafe"

// Interlaced reports AV_FRAME_FLAG_INTERLACED: the decoder says this picture is two fields.
// Decoders set it from the bitstream (DV, MPEG-2, interlaced H.264); a source re-encoded
// without field signalling can be interlaced content with the flag clear — see DetectInterlace.
func (f *Frame) Interlaced() bool {
	if f == nil || f.p == nil {
		return false
	}
	return C.mm_frame_interlaced(f.p) != 0
}

// TopFieldFirst reports AV_FRAME_FLAG_TOP_FIELD_FIRST. Meaningful only when Interlaced.
func (f *Frame) TopFieldFirst() bool {
	if f == nil || f.p == nil {
		return false
	}
	return C.mm_frame_tff(f.p) != 0
}

// SetFieldFlags sets or clears the interlaced / top-field-first frame flags.
func (f *Frame) SetFieldFlags(interlaced, topFieldFirst bool) {
	if f == nil || f.p == nil {
		return
	}
	i, t := 0, 0
	if interlaced {
		i = 1
	}
	if topFieldFirst {
		t = 1
	}
	C.mm_frame_set_fields(f.p, C.int(i), C.int(t))
}

// MetadataValue returns a frame-metadata entry (e.g. "lavfi.idet.multiple.current_frame",
// written by the idet filter) and whether it exists.
func (f *Frame) MetadataValue(key string) (string, bool) {
	if f == nil || f.p == nil {
		return "", false
	}
	ck := C.CString(key)
	defer C.free(unsafe.Pointer(ck))
	v := C.mm_frame_meta(f.p, ck)
	if v == nil {
		return "", false
	}
	return C.GoString(v), true
}
