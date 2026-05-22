//go:build !ffstatic

package imgmath

// #cgo pkg-config: libswscale libavutil
//
// #include "libswscale/swscale.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/mem.h"
import "C"
