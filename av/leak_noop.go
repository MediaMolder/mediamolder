//go:build !avleakcheck

package av

import "unsafe"

func leakTrack(_ unsafe.Pointer, _ string)   {}
func leakUntrack(_ unsafe.Pointer)           {}

// LeakReport is a no-op in production builds.
// Build with -tags=avleakcheck to enable resource leak detection.
func LeakReport() int { return 0 }
