// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package runtime provides the multi-lane scheduler for concurrent pipeline
// execution. It takes a resolved graph.Graph and runs each node as an
// independent goroutine connected by buffered channels.
package runtime
