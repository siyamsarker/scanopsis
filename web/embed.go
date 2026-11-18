// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package web exposes the compiled web interface that is embedded into the
// scanopsis binary.
package web

import (
	"embed"
	"io/fs"
)

// dist holds the production build that Vite writes to dist/app. The tracked
// file dist/.gitkeep keeps the directory present, so the Go code compiles
// before the web interface has been built.
//
//go:embed all:dist
var dist embed.FS

// Assets returns the built web interface rooted at its index.html. The
// boolean is false when the binary was compiled without a web build.
func Assets() (fs.FS, bool) {
	return assetsFrom(dist)
}

func assetsFrom(root fs.FS) (fs.FS, bool) {
	sub, err := fs.Sub(root, "dist/app")
	if err != nil {
		return nil, false
	}
	if _, err := fs.Stat(sub, "index.html"); err != nil {
		return nil, false
	}
	return sub, true
}
