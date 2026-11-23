// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"io/fs"
	"net/http"
	"path"
	"strings"
)

// static serves the built web interface. Hidden files and directory
// listings are never served; unknown paths without a file extension get
// index.html so that client-side routes survive a reload.
func (s *server) static() http.Handler {
	assets := s.Assets
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if assets == nil {
			http.Error(w, "The web interface is not included in this build.", http.StatusServiceUnavailable)
			return
		}
		if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
			return
		}
		name := strings.Trim(path.Clean("/"+r.URL.Path), "/")
		for _, seg := range strings.Split(name, "/") {
			if strings.HasPrefix(seg, ".") {
				http.NotFound(w, r)
				return
			}
		}
		if name == "" {
			name = "."
		}
		info, err := fs.Stat(assets, name)
		switch {
		case err != nil && path.Ext(name) == "":
			name = "index.html"
		case err != nil:
			http.NotFound(w, r)
			return
		case info.IsDir():
			name = path.Join(name, "index.html")
			if _, err := fs.Stat(assets, name); err != nil {
				http.NotFound(w, r)
				return
			}
		}
		switch {
		case strings.HasPrefix(name, "assets/"):
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		case name == "index.html":
			w.Header().Set("Cache-Control", "no-cache")
		}
		// name is cleaned, has no dot-prefixed segment and is resolved inside an fs.FS.
		http.ServeFileFS(w, r, assets, name) //nolint:gosec // G703: see comment above.
	})
}
