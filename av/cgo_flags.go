package av

// #cgo CFLAGS: -I${SRCDIR}/../../ffmpeg
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavcodec    -lavcodec
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavformat   -lavformat
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavfilter   -lavfilter
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libavutil     -lavutil
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libswscale    -lswscale
// #cgo LDFLAGS: -L${SRCDIR}/../../ffmpeg/libswresample -lswresample
// #cgo LDFLAGS: -lbz2 -lz -liconv
// #cgo LDFLAGS: -L/opt/homebrew/Cellar/libxcb/1.17.0/lib -lxcb -lxcb-shm -lxcb-xfixes -lxcb-shape
// #cgo LDFLAGS: -L/opt/homebrew/lib -lSDL2
// #cgo LDFLAGS: -framework AudioToolbox
// #cgo LDFLAGS: -framework VideoToolbox
// #cgo LDFLAGS: -framework CoreFoundation
// #cgo LDFLAGS: -framework CoreMedia
// #cgo LDFLAGS: -framework CoreVideo
// #cgo LDFLAGS: -framework CoreImage
// #cgo LDFLAGS: -framework AppKit
// #cgo LDFLAGS: -framework AVFoundation
// #cgo LDFLAGS: -framework OpenGL
// #cgo LDFLAGS: -framework Security
//
// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
// #include "libswscale/swscale.h"
// #include "libswresample/swresample.h"
import "C"
