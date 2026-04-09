//go:build !ffstatic

package av

// #cgo pkg-config: libavcodec libavformat libavfilter libavutil libswscale libswresample
//
// #include "libavcodec/avcodec.h"
// #include "libavformat/avformat.h"
// #include "libavfilter/avfilter.h"
// #include "libavutil/avutil.h"
// #include "libswscale/swscale.h"
// #include "libswresample/swresample.h"
import "C"
