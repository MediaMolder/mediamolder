//go:build avleakcheck

package av

import (
	"fmt"
	"os"
	"runtime"
	"sync"
	"unsafe"
)

var leakMu sync.Mutex
var leakMap = map[unsafe.Pointer]leakEntry{}

type leakEntry struct {
	kind  string
	stack string
}

func leakTrack(p unsafe.Pointer, kind string) {
	buf := make([]byte, 2048)
	n := runtime.Stack(buf, false)
	leakMu.Lock()
	leakMap[p] = leakEntry{kind: kind, stack: string(buf[:n])}
	leakMu.Unlock()
}

func leakUntrack(p unsafe.Pointer) {
	leakMu.Lock()
	delete(leakMap, p)
	leakMu.Unlock()
}

func init() {
	// Register a finalizer on a sentinel object to print leaks at GC time.
	// For a process-exit report, call LeakReport() explicitly.
	_ = leakMap
}

// LeakReport prints all unclosed av resources to stderr; returns the count.
// Call this at the end of tests or main() when -tags=avleakcheck is active.
func LeakReport() int {
	leakMu.Lock()
	defer leakMu.Unlock()
	if len(leakMap) == 0 {
		return 0
	}
	fmt.Fprintf(os.Stderr, "[avleakcheck] %d unclosed resource(s):\n", len(leakMap))
	for p, e := range leakMap {
		fmt.Fprintf(os.Stderr, "  %s @ %p\n    allocated at:\n", e.kind, p)
		for _, line := range splitLines(e.stack) {
			fmt.Fprintf(os.Stderr, "      %s\n", line)
		}
	}
	return len(leakMap)
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
