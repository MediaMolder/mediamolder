// Package av provides Go bindings for the libav* family of libraries
// (libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample).
//
// All types wrapping C resources implement io.Closer. Callers must call
// Close() -- typically via defer -- to release C memory. In development builds
// (built with -tags=avleakcheck) unclosed resources are logged at process exit.
package av

import "io"

// Compile-time assertions: every C wrapper type satisfies io.Closer.
var (
	_ io.Closer = (*Frame)(nil)
	_ io.Closer = (*Packet)(nil)
	_ io.Closer = (*DecoderContext)(nil)
	_ io.Closer = (*EncoderContext)(nil)
	_ io.Closer = (*InputFormatContext)(nil)
	_ io.Closer = (*OutputFormatContext)(nil)
	_ io.Closer = (*FilterGraph)(nil)
)
