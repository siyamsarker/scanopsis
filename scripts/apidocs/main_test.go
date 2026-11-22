// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"flag"
	"os"
	"strings"
	"testing"
)

var update = flag.Bool("update", false, "rewrite testdata/golden.md")

func render(t *testing.T) []byte {
	t.Helper()
	spec, err := os.ReadFile("testdata/spec.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := generate(spec, &out); err != nil {
		t.Fatal(err)
	}
	return out.Bytes()
}

func TestGenerateMatchesGolden(t *testing.T) {
	got := render(t)
	if *update {
		if err := os.WriteFile("testdata/golden.md", got, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile("testdata/golden.md")
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("output differs from testdata/golden.md; run `go test ./scripts/apidocs -update` and review the diff\n--- got ---\n%s", got)
	}
}

func TestGenerateIsDeterministic(t *testing.T) {
	first := render(t)
	for range 5 {
		if !bytes.Equal(first, render(t)) {
			t.Fatal("two runs produced different output")
		}
	}
}

func TestAuthentication(t *testing.T) {
	out := string(render(t))
	for header, want := range map[string]string{
		"### `GET /healthz`":          "Authentication: None",
		"### `GET /api/v1/items`":     "Authentication: Session\n",
		"### `POST /api/v1/items`":    "Authentication: Session, admin",
		"### `POST /api/v1/upload`":   "Authentication: CI ID token",
		"### `POST /api/v1/hook`":     "Authentication: HMAC signature (`X-Example-HMAC-SHA256` header)",
		"### `GET /api/v1/admin/log`": "Authentication: Session, admin",
	} {
		section := out[strings.Index(out, header):]
		if next := strings.Index(section[len(header):], "\n### "); next >= 0 {
			section = section[:len(header)+next]
		}
		if !strings.Contains(section, want) {
			t.Errorf("%s: want %q in\n%s", header, want, section)
		}
	}
}

// TestHeaderAndFooter checks the parts every page in docs/ shares.
func TestHeaderAndFooter(t *testing.T) {
	out := string(render(t))
	if !strings.HasPrefix(out, "# Example API reference\n\n") {
		t.Errorf("the page must start with its title, got %q", out[:min(len(out), 40)])
	}
	for _, want := range []string{
		"\n**Audience:** Developers · **Back to:** [README](../README.md) · [All guides](../README.md#documentation)\n\n---\n",
		"A small API used to test the documentation generator.\n\nA second paragraph stays a paragraph of its own.\n",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("want %q in the output", want)
		}
	}
	footer := "\n---\n\n[Back to the README](../README.md) · [All guides](../README.md#documentation) · Found a mistake? [Open an issue](https://github.com/siyamsarker/scanopsis/issues)\n"
	if !strings.HasSuffix(out, footer) {
		t.Errorf("the page must end with the shared footer, got %q", out[max(0, len(out)-200):])
	}
}

func TestInvalidSpec(t *testing.T) {
	if err := generate([]byte("paths: [unclosed"), &bytes.Buffer{}); err == nil {
		t.Fatal("want an error for invalid YAML")
	}
}
