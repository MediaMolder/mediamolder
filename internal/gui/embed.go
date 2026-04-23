// Copyright (C) 2026 Thomas Vaughan
// SPDX-License-Identifier: LGPL-2.1-or-later

package gui

import (
	"embed"
	"io/fs"
)

// frontendDist holds the built Vite output. The Makefile target
// `make frontend-build` populates ./dist before `go build`. A placeholder
// index.html is committed so `go build ./...` always succeeds even without
// running the frontend build.
//
//go:embed all:dist
var frontendDist embed.FS

// frontendAssets returns the embedded dist/ tree rooted at "dist".
func frontendAssets() (fs.FS, error) {
	return fs.Sub(frontendDist, "dist")
}
