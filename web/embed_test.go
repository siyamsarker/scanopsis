// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package web

import (
	"io/fs"
	"os"
	"testing"
	"testing/fstest"
)

func TestAssetsFrom(t *testing.T) {
	tests := []struct {
		name   string
		root   fstest.MapFS
		wantOK bool
	}{
		{
			name:   "no build present",
			root:   fstest.MapFS{"dist/.gitkeep": {}},
			wantOK: false,
		},
		{
			name:   "build without index.html",
			root:   fstest.MapFS{"dist/app/assets/app.js": {Data: []byte("x")}},
			wantOK: false,
		},
		{
			name: "complete build",
			root: fstest.MapFS{
				"dist/app/index.html":    {Data: []byte("<!doctype html>")},
				"dist/app/assets/app.js": {Data: []byte("x")},
			},
			wantOK: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := assetsFrom(tt.root)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			b, err := fs.ReadFile(got, "index.html")
			if err != nil {
				t.Fatalf("index.html must be at the root of the returned FS: %v", err)
			}
			if string(b) != "<!doctype html>" {
				t.Fatalf("unexpected index.html content %q", b)
			}
		})
	}
}

// TestAssetsMatchesDisk checks the embedded FS against the working tree, so the
// test holds both before and after the web interface has been built.
func TestAssetsMatchesDisk(t *testing.T) {
	_, statErr := os.Stat("dist/app/index.html")
	_, ok := Assets()
	if want := statErr == nil; ok != want {
		t.Fatalf("Assets() ok = %v, but dist/app/index.html present on disk = %v", ok, want)
	}
}
