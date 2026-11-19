// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"strings"
	"testing"
)

func TestStaticFiles(t *testing.T) {
	e := newEnv(t, nil)
	tests := []struct {
		target, body, cache string
	}{
		{"/", "<html>app</html>", "no-cache"},
		{"/admin/users", "<html>app</html>", "no-cache"},
		{"/login", "<html>app</html>", "no-cache"},
		{"/assets/app.js", "console.log(1)", "public, max-age=31536000, immutable"},
		{"/favicon.svg", "<svg/>", ""},
		{"/docs/", "<html>docs</html>", ""},
		{"/docs", "<html>docs</html>", ""},
	}
	for _, tt := range tests {
		t.Run(tt.target, func(t *testing.T) {
			rec := e.do("GET", tt.target, "", "")
			if rec.Code != 200 || rec.Body.String() != tt.body {
				t.Fatalf("status %d body %q, want 200 %q", rec.Code, rec.Body.String(), tt.body)
			}
			if got := rec.Header().Get("Cache-Control"); got != tt.cache {
				t.Fatalf("Cache-Control = %q, want %q", got, tt.cache)
			}
		})
	}
}

func TestStaticHead(t *testing.T) {
	rec := newEnv(t, nil).do("HEAD", "/admin/users", "", "")
	if rec.Code != 200 || rec.Body.Len() != 0 {
		t.Fatalf("HEAD = %d with %d body bytes", rec.Code, rec.Body.Len())
	}
}

func TestStaticNotFound(t *testing.T) {
	e := newEnv(t, nil)
	for _, target := range []string{
		"/.env", "/assets/.secret", "/source/.git/config", "/.git", "/%2e%65nv",
		"/assets/", "/assets", "/bare", "/bare/", "/source",
		"/missing.js", "/assets/gone.css", "/nested/missing.png",
	} {
		t.Run(target, func(t *testing.T) {
			rec := e.do("GET", target, "", "")
			if rec.Code != 404 {
				t.Fatalf("status = %d, want 404; body %q", rec.Code, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), "SECRET") || strings.Contains(rec.Body.String(), "app.js") {
				t.Fatalf("body leaks content: %q", rec.Body.String())
			}
		})
	}
}

func TestStaticRedirects(t *testing.T) {
	e := newEnv(t, nil)
	rec := e.do("GET", "/index.html", "", "")
	if rec.Code != 301 || rec.Header().Get("Location") != "./" {
		t.Fatalf("/index.html = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// Path cleaning redirects to the canonical path, which is then refused.
	rec = e.do("GET", "/assets/../.env", "", "")
	if rec.Code != 307 || rec.Header().Get("Location") != "/.env" {
		t.Fatalf("traversal = %d %q", rec.Code, rec.Header().Get("Location"))
	}
	if rec := e.do("GET", "/.env", "", ""); rec.Code != 404 {
		t.Fatalf("/.env = %d", rec.Code)
	}
}

func TestStaticMethodNotAllowed(t *testing.T) {
	rec := newEnv(t, nil).do("POST", "/admin", "", "")
	if rec.Code != 405 || rec.Header().Get("Allow") != "GET, HEAD" {
		t.Fatalf("POST = %d Allow %q", rec.Code, rec.Header().Get("Allow"))
	}
}

func TestStaticWithoutAssets(t *testing.T) {
	e := newEnv(t, func(d *Deps) { d.Assets = nil })
	for _, target := range []string{"/", "/admin", "/assets/app.js"} {
		rec := e.do("GET", target, "", "")
		if rec.Code != 503 || strings.TrimSpace(rec.Body.String()) != "The web interface is not included in this build." {
			t.Fatalf("%s = %d %q", target, rec.Code, rec.Body.String())
		}
	}
	// The API keeps working.
	if rec := e.do("GET", "/healthz", "", ""); rec.Code != 200 {
		t.Fatalf("healthz = %d", rec.Code)
	}
}

func TestUnknownAPIPathIsNotIndex(t *testing.T) {
	rec := newEnv(t, nil).do("GET", "/api/v2/anything", "", "")
	assertError(t, rec, 404, "not_found")
	if strings.Contains(rec.Body.String(), "<html>") {
		t.Fatal("served index.html for an API path")
	}
}
