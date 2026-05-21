//go:build ffstatic

package imgmath

// Static linking mirrors av/cgo_flags_static.go, adjusted for ${SRCDIR}
// being three levels deep (PySceneDetect/internal/) rather than one (av/).
//
// -lm and -lpthread are intentionally omitted: av/cgo_flags_static.go already
// specifies them, and on macOS both are absorbed into libSystem. Duplicating
// them here causes the macOS linker to warn.
//
// -lavutil and -lswscale must remain so that `go test ./PySceneDetect/internal/...`
// links correctly in isolation. When the full binary is built with -tags ffstatic
// the macOS linker emits a harmless "ignoring duplicate libraries" warning for
// these two; the Makefile suppresses it via CGO_LDFLAGS=-Wl,-no_warn_duplicate_libraries.

// #cgo CFLAGS: -I${SRCDIR}/../../../ffmpeg
// #cgo LDFLAGS: -L${SRCDIR}/../../../ffmpeg/libswscale    -lswscale
// #cgo LDFLAGS: -L${SRCDIR}/../../../ffmpeg/libavutil     -lavutil
//
// #include "libswscale/swscale.h"
// #include "libavutil/pixfmt.h"
// #include "libavutil/mem.h"
import "C"
