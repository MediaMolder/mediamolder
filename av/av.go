// Package av provides Go bindings for the libav* family of libraries
// (libavcodec, libavformat, libavfilter, libavutil, libswscale, libswresample).
//
// All types wrapping C resources implement io.Closer. Callers must call
// Close() -- typically via defer -- to release C memory. In development builds
// (built with -tags=avleakcheck) unclosed resources are logged at process exit.
package av
