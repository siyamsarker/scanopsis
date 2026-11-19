// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Command apidocs writes the API reference (docs/api.md) from the OpenAPI
// document. The output is deterministic: paths, parameters, responses and
// schemas are sorted, so a change in the file means a change in the API.
//
// Usage:
//
//	go run ./scripts/apidocs > docs/api.md
//	go run ./scripts/apidocs -spec other.yaml
package main

import (
	"bytes"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

func main() {
	spec := flag.String("spec", "api/openapi.yaml", "path of the OpenAPI document")
	flag.Parse()
	if err := run(*spec, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "apidocs:", err)
		os.Exit(1)
	}
}

func run(path string, w io.Writer) error {
	b, err := os.ReadFile(path) //nolint:gosec // the path is a command-line argument of a developer tool.
	if err != nil {
		return err
	}
	return generate(b, w)
}

// object is a decoded YAML mapping.
type object = map[string]any

func generate(spec []byte, w io.Writer) error {
	var doc object
	if err := yaml.Unmarshal(spec, &doc); err != nil {
		return fmt.Errorf("parse OpenAPI document: %w", err)
	}
	g := &gen{doc: doc}
	var buf bytes.Buffer
	g.out = &buf
	g.render()
	_, err := w.Write(buf.Bytes())
	return err
}

type gen struct {
	doc object
	out *bytes.Buffer
}

func (g *gen) p(format string, args ...any) { fmt.Fprintf(g.out, format+"\n", args...) }

func obj(v any) object {
	m, _ := v.(object)
	return m
}

func list(v any) []any {
	l, _ := v.([]any)
	return l
}

func str(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case nil:
		return ""
	}
	return fmt.Sprint(v)
}

func sortedKeys(m object) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// oneLine joins the lines of a YAML text block into one line for a table cell.
func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	return strings.ReplaceAll(s, "|", "\\|")
}

func (g *gen) render() {
	info := obj(g.doc["info"])
	g.p("# %s reference", str(info["title"]))
	g.p("")
	g.p("<!-- Generated from api/openapi.yaml by `go run ./scripts/apidocs > docs/api.md`. Do not edit by hand. -->")
	g.p("")
	g.p("This page lists every endpoint of the HTTP API, generated from the OpenAPI")
	g.p("document `api/openapi.yaml`. API version: %s.", str(info["version"]))
	g.p("")
	if d := strings.TrimSpace(str(info["description"])); d != "" {
		g.p("%s", strings.Join(strings.Fields(d), " "))
		g.p("")
	}
	g.p("## Authentication")
	g.p("")
	g.p("Each endpoint names how it is authenticated.")
	g.p("")
	g.p("| Label | Meaning |")
	g.p("|---|---|")
	g.p("| None | No credentials are needed. |")
	g.p("| Session | A session cookie from a completed sign-in (password or GitLab, then two-factor code). |")
	g.p("| Session, admin | A session whose user has the admin role. |")
	g.p("| CI ID token | A GitLab CI/CD ID token in `Authorization: Bearer <token>`. |")
	g.p("| HMAC signature | The HMAC-SHA256 signature of the body, in a request header. |")
	g.p("")
	g.p("Endpoints that need a session answer `401` without one and `403` while the")
	g.p("session still needs two-factor authentication or a password change.")
	g.p("")
	g.p("## Endpoints")
	g.p("")
	g.paths()
	g.p("## Schemas")
	g.p("")
	g.schemas()
}

var methodOrder = []string{"get", "post", "put", "patch", "delete"}

func (g *gen) paths() {
	paths := obj(g.doc["paths"])
	for _, path := range sortedKeys(paths) {
		item := obj(paths[path])
		for _, m := range methodOrder {
			op := obj(item[m])
			if op == nil {
				continue
			}
			g.operation(strings.ToUpper(m), path, op, list(item["parameters"]))
		}
	}
}

func (g *gen) operation(method, path string, op object, shared []any) {
	g.p("### `%s %s`", method, path)
	g.p("")
	if s := str(op["summary"]); s != "" {
		g.p("%s", s)
		g.p("")
	}
	if d := strings.TrimSpace(str(op["description"])); d != "" {
		g.p("%s", strings.Join(strings.Fields(d), " "))
		g.p("")
	}
	g.p("- Operation ID: `%s`", str(op["operationId"]))
	g.p("- Authentication: %s", g.auth(op))
	g.p("")

	params := append(append([]any{}, shared...), list(op["parameters"])...)
	if len(params) > 0 {
		g.p("Parameters:")
		g.p("")
		g.p("| Name | In | Required | Type | Description |")
		g.p("|---|---|---|---|---|")
		sort.SliceStable(params, func(i, j int) bool {
			a, b := obj(g.resolve(params[i])), obj(g.resolve(params[j]))
			if str(a["in"]) != str(b["in"]) {
				return str(a["in"]) < str(b["in"])
			}
			return str(a["name"]) < str(b["name"])
		})
		for _, raw := range params {
			p := obj(g.resolve(raw))
			req := "no"
			if r, _ := p["required"].(bool); r {
				req = "yes"
			}
			g.p("| `%s` | %s | %s | %s | %s |", str(p["name"]), str(p["in"]), req,
				g.typeOf(obj(p["schema"])), oneLine(str(p["description"])))
		}
		g.p("")
	}

	if rb := obj(g.resolve(op["requestBody"])); rb != nil {
		req := "optional"
		if r, _ := rb["required"].(bool); r {
			req = "required"
		}
		g.p("Request body (%s):", req)
		g.p("")
		for _, ct := range sortedKeys(obj(rb["content"])) {
			g.p("- `%s`: %s", ct, g.typeOf(obj(obj(obj(rb["content"])[ct])["schema"])))
		}
		g.p("")
	}

	resp := obj(op["responses"])
	if len(resp) > 0 {
		g.p("Responses:")
		g.p("")
		g.p("| Status | Description | Body |")
		g.p("|---|---|---|")
		for _, code := range sortedKeys(resp) {
			r := obj(g.resolve(resp[code]))
			body := "none"
			if c := obj(r["content"]); len(c) > 0 {
				var parts []string
				for _, ct := range sortedKeys(c) {
					parts = append(parts, fmt.Sprintf("`%s` %s", ct, g.typeOf(obj(obj(c[ct])["schema"]))))
				}
				body = strings.Join(parts, ", ")
			}
			g.p("| %s | %s | %s |", code, oneLine(str(r["description"])), body)
		}
		g.p("")
	}
}

// auth derives the authentication of an operation from its security field and
// its text. The default (no security field) is the document-wide session.
func (g *gen) auth(op object) string {
	text := strings.ToLower(str(op["summary"]) + " " + str(op["description"]))
	sec, has := op["security"]
	if has {
		reqs := list(sec)
		if len(reqs) == 0 {
			if strings.Contains(text, "hmac") {
				return "HMAC signature (" + hmacHeader(str(op["description"])) + ")"
			}
			return "None"
		}
		var names []string
		for _, r := range reqs {
			for k := range obj(r) {
				names = append(names, k)
			}
		}
		sort.Strings(names)
		for i, n := range names {
			names[i] = g.schemeLabel(n)
		}
		return strings.Join(names, " or ")
	}
	if strings.Contains(text, "admin only") {
		return "Session, admin"
	}
	for _, t := range list(op["tags"]) {
		if str(t) == "admin" {
			return "Session, admin"
		}
	}
	return "Session"
}

func (g *gen) schemeLabel(name string) string {
	s := obj(obj(obj(g.doc["components"])["securitySchemes"])[name])
	switch {
	case str(s["type"]) == "http" && str(s["scheme"]) == "bearer" && str(s["bearerFormat"]) == "JWT":
		return "CI ID token"
	case str(s["type"]) == "apiKey" && str(s["in"]) == "cookie":
		return "Session"
	}
	return name
}

// hmacHeader finds the header name in a description such as "Verified with
// the X-Sonar-Webhook-HMAC-SHA256 header".
func hmacHeader(desc string) string {
	for _, w := range strings.Fields(desc) {
		w = strings.Trim(w, ".,;:()`")
		if strings.HasPrefix(strings.ToUpper(w), "X-") && strings.Contains(strings.ToUpper(w), "HMAC") {
			return "`" + w + "` header"
		}
	}
	return "request header"
}

// resolve follows a local $ref.
func (g *gen) resolve(v any) any {
	m := obj(v)
	ref := str(m["$ref"])
	if ref == "" {
		return v
	}
	cur := any(g.doc)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#/"), "/") {
		cur = obj(cur)[part]
	}
	return cur
}

func refName(ref string) string { return ref[strings.LastIndex(ref, "/")+1:] }

// typeOf describes a schema in one short phrase. Named schemas link to the
// schema section.
func (g *gen) typeOf(s object) string {
	if s == nil {
		return ""
	}
	if ref := str(s["$ref"]); ref != "" {
		n := refName(ref)
		return fmt.Sprintf("[%s](#%s)", n, strings.ToLower(n))
	}
	if all := list(s["allOf"]); len(all) > 0 {
		var parts []string
		for _, a := range all {
			parts = append(parts, g.typeOf(obj(a)))
		}
		return strings.Join(parts, " + ")
	}
	var t string
	switch v := s["type"].(type) {
	case string:
		t = v
	case []any:
		var ts []string
		for _, x := range v {
			ts = append(ts, str(x))
		}
		t = strings.Join(ts, " or ")
	}
	switch {
	case t == "array":
		t = "array of " + g.typeOf(obj(s["items"]))
	case t == "object" && obj(s["additionalProperties"]) != nil:
		t = "map of " + g.typeOf(obj(s["additionalProperties"]))
	case t == "":
		t = "any"
	}
	var extra []string
	if f := str(s["format"]); f != "" {
		extra = append(extra, f)
	}
	if e := list(s["enum"]); len(e) > 0 {
		var vals []string
		for _, x := range e {
			vals = append(vals, "`"+str(x)+"`")
		}
		extra = append(extra, "one of "+strings.Join(vals, ", "))
	}
	for _, k := range []string{"minimum", "maximum", "minLength", "maxLength", "default"} {
		if v, ok := s[k]; ok {
			extra = append(extra, fmt.Sprintf("%s %v", k, v))
		}
	}
	if len(extra) > 0 {
		t += " (" + strings.Join(extra, ", ") + ")"
	}
	return t
}

func (g *gen) schemas() {
	schemas := obj(obj(g.doc["components"])["schemas"])
	for _, name := range sortedKeys(schemas) {
		s := obj(schemas[name])
		g.p("### %s", name)
		g.p("")
		if d := strings.TrimSpace(str(s["description"])); d != "" {
			g.p("%s", strings.Join(strings.Fields(d), " "))
			g.p("")
		}
		g.p("Type: %s", g.typeOf(withoutProperties(s)))
		g.p("")
		g.properties(s)
	}
}

func withoutProperties(s object) object {
	out := object{}
	for k, v := range s {
		if k != "properties" && k != "description" {
			out[k] = v
		}
	}
	return out
}

// properties writes the property table of an object schema, including the
// properties of allOf members.
func (g *gen) properties(s object) {
	props := object{}
	required := map[string]bool{}
	var collect func(object)
	collect = func(o object) {
		for k, v := range obj(o["properties"]) {
			props[k] = v
		}
		for _, r := range list(o["required"]) {
			required[str(r)] = true
		}
		for _, a := range list(o["allOf"]) {
			collect(obj(g.resolve(a)))
		}
	}
	collect(s)
	if len(props) == 0 {
		return
	}
	g.p("| Property | Type | Required | Description |")
	g.p("|---|---|---|---|")
	for _, k := range sortedKeys(props) {
		ps := obj(props[k])
		req := "no"
		if required[k] {
			req = "yes"
		}
		g.p("| `%s` | %s | %s | %s |", k, g.typeOf(ps), req, oneLine(str(ps["description"])))
	}
	g.p("")
	for _, k := range sortedKeys(props) {
		ps := obj(props[k])
		if len(obj(ps["properties"])) > 0 {
			g.p("Properties of `%s`:", k)
			g.p("")
			g.properties(ps)
		}
	}
}
