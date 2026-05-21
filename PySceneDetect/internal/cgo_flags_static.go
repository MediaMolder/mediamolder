//go:build ffstatic

package imgmath

// Static linking mirrors av/cgo_flags_static.go, adjusted for ${SRCDIR}
// being three levels deep (PySceneDetect/internal/) rather than one (av/).

// #cgo CFLAGS: -I${SRCDIR}/../../../ffmpeg
// #cgo LDFLAGS: -L${SRCDIR}/../../../ffmpeg/libswscale    -lswscale
// #cgo LDFLAGS: -L${SRCDIR}/../../../ffmpeg/libavutil     -lavutil
// #cgo LDFLAGS: -lm -lpthread
//
// #include "libswscale/swscale.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/mem.h"
import "C"
