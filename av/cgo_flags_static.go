//go:build ffstatic

package av

// Static linking against a local FFmpeg source tree build.
// Set FFMPEG_SRC to the FFmpeg source directory (defaults to ~/ffmpeg).
// Build with: go build -tags=ffstatic ./...

// #cgo CFLAGS: -I${SRCDIR}/../../ffmpeg
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavcodec    -lavcodec
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavformat   -lavformat
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavfilter   -lavfilter
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavutil     -lavutil
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libswscale    -lswscale
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libswresample -lswresample
// #cgo LDFLAGS: -lbz2 -lz -liconv -lm -lpthread
// #cgo darwin LDFLAGS: -L/opt/homebrew/Cellar/libxcb/1.17.0/lib -lxcb -lxcb-shm -lxcb-xfixes -lxcb-shape
// #cgo darwin LDFLAGS: -L/opt/homebrew/lib -lSDL2
// #cgo darwin LDFLAGS: -framework AudioToolbox
// #cgo darwin LDFLAGS: -framework VideoToolbox
// #cgo darwin LDFLAGS: -framework CoreFoundation
// #cgo darwin LDFLAGS: -framework CoreMedia
// #cgo darwin LDFLAGS: -framework CoreVideo
// #cgo darwin LDFLAGS: -framework CoreImage
// #cgo darwin LDFLAGS: -framework AppKit
// #cgo darwin LDFLAGS: -framework AVFoundation
// #cgo darwin LDFLAGS: -framework OpenGL
// #cgo darwin LDFLAGS: -framework Security
//
// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
// #include "libswscale/swscale.h"
// #include "libswresample/swresample.h"
import "C"
