// Package runtime provides the multi-lane scheduler for concurrent pipeline
// execution. It takes a resolved graph.Graph and runs each node as an
// independent goroutine connected by buffered channels.
package runtime
