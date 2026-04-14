// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

// Package graph implements DAG construction and validation for multi-input,
// multi-output media processing pipelines.
//
// A Graph is built from a Def (definition) that describes inputs, processing
// nodes, outputs, and the edges connecting them. Build validates edge
// references, checks type compatibility, detects cycles, and produces a
// topologically-sorted execution order.
package graph
