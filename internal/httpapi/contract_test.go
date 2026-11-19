// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package httpapi

import (
	"os"
	"sort"
	"strings"
	"testing"

	"go.yaml.in/yaml/v3"
)

// TestRoutesMatchOpenAPI fails when a route is registered but undocumented,
// or documented but not registered.
func TestRoutesMatchOpenAPI(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var spec struct {
		Paths map[string]map[string]yaml.Node `yaml:"paths"`
	}
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		t.Fatal(err)
	}
	documented := map[string]bool{}
	for p, ops := range spec.Paths {
		for method := range ops {
			switch method {
			case "get", "put", "post", "delete", "options", "head", "patch", "trace":
				documented[strings.ToUpper(method)+" "+p] = true
			}
		}
	}
	registered := map[string]bool{}
	for _, rt := range (&server{}).routes() {
		registered[rt.method+" "+rt.pattern] = true
	}
	if diff := difference(registered, documented); len(diff) > 0 {
		t.Errorf("registered but not in api/openapi.yaml: %v", diff)
	}
	if diff := difference(documented, registered); len(diff) > 0 {
		t.Errorf("in api/openapi.yaml but not registered: %v", diff)
	}
	if len(documented) == 0 {
		t.Fatal("no operations parsed from the specification")
	}
}

func difference(a, b map[string]bool) []string {
	var out []string
	for k := range a {
		if !b[k] {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// TestOpenAPIHasNoCutDescriptions catches a plain description in a flow
// mapping ({ ... }) that contains a comma: YAML ends the value at the comma
// and reads the rest of the sentence as a key without a value.
func TestOpenAPIHasNoCutDescriptions(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.yaml")
	if err != nil {
		t.Fatal(err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	var walk func(n *yaml.Node)
	walk = func(n *yaml.Node) {
		if n.Kind == yaml.MappingNode {
			for i := 0; i+1 < len(n.Content); i += 2 {
				k, v := n.Content[i], n.Content[i+1]
				if v.Tag == "!!null" && v.Value == "" && n.Style&yaml.FlowStyle != 0 {
					t.Errorf("line %d: %q is a key without a value; quote the description before it", k.Line, k.Value)
				}
			}
		}
		for _, c := range n.Content {
			walk(c)
		}
	}
	walk(&doc)
}
