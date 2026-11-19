// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Command covercheck enforces the backend coverage thresholds on a Go cover
// profile. Generated files (*.gen.go) and test helper packages are left out
// of every figure.
//
// Usage: go run ./scripts/covercheck <profile>
package main

import (
	"bufio"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
)

const (
	modulePrefix = "github.com/siyamsarker/scanopsis/"
	overallMin   = 75.0
)

// packageMin lists the packages that carry a stricter threshold.
var packageMin = map[string]float64{
	"internal/ingest": 85,
	"internal/auth":   85,
	"internal/domain": 85,
	"internal/sonar":  85,
}

// excluded lists packages, and everything below them, that hold test helpers
// instead of product code.
var excluded = []string{"internal/store/storetest", "internal/testutil"}

func isExcluded(pkg string) bool {
	for _, e := range excluded {
		if pkg == e || strings.HasPrefix(pkg, e+"/") {
			return true
		}
	}
	return false
}

type tally struct{ covered, total int }

func (t tally) pct() float64 {
	if t.total == 0 {
		return 100
	}
	return 100 * float64(t.covered) / float64(t.total)
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: go run ./scripts/covercheck <profile>")
		os.Exit(2)
	}
	pkgs, err := read(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "covercheck:", err)
		os.Exit(2)
	}
	os.Exit(report(pkgs))
}

// read sums the statements of each package in a cover profile. A block that
// appears several times (one per test binary) counts once, as covered when
// any occurrence ran.
func read(file string) (map[string]*tally, error) {
	f, err := os.Open(file) //nolint:gosec // the profile path is a command-line argument of a developer tool
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	type block struct{ stmts, count int }
	blocks := map[string]block{}
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if strings.HasPrefix(line, "mode:") || line == "" {
			continue
		}
		// name.go:line.col,line.col numStmts count
		sp := strings.Fields(line)
		if len(sp) != 3 {
			return nil, fmt.Errorf("malformed profile line %q", line)
		}
		var stmts, count int
		if _, err := fmt.Sscanf(sp[1]+" "+sp[2], "%d %d", &stmts, &count); err != nil {
			return nil, fmt.Errorf("malformed profile line %q: %w", line, err)
		}
		b := blocks[sp[0]]
		b.stmts = stmts
		b.count += count
		blocks[sp[0]] = b
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	if len(blocks) == 0 {
		return nil, fmt.Errorf("%s holds no coverage data", file)
	}

	pkgs := map[string]*tally{}
	for key, b := range blocks {
		src := key[:strings.LastIndex(key, ":")]
		if strings.HasSuffix(src, ".gen.go") {
			continue
		}
		pkg := strings.TrimPrefix(path.Dir(src), modulePrefix)
		if isExcluded(pkg) {
			continue
		}
		t := pkgs[pkg]
		if t == nil {
			t = &tally{}
			pkgs[pkg] = t
		}
		t.total += b.stmts
		if b.count > 0 {
			t.covered += b.stmts
		}
	}
	return pkgs, nil
}

func report(pkgs map[string]*tally) int {
	names := make([]string, 0, len(pkgs))
	var all tally
	for name, t := range pkgs {
		names = append(names, name)
		all.covered += t.covered
		all.total += t.total
	}
	sort.Strings(names)

	var failures []string
	fmt.Printf("%-32s %9s %9s\n", "package", "coverage", "required")
	for _, name := range names {
		t := pkgs[name]
		req := "-"
		if min, ok := packageMin[name]; ok {
			req = fmt.Sprintf(">= %.0f%%", min)
			if t.pct() < min {
				failures = append(failures, fmt.Sprintf("%s is at %.1f%%, below the %.0f%% minimum", name, t.pct(), min))
			}
		}
		fmt.Printf("%-32s %8.1f%% %9s\n", name, t.pct(), req)
	}
	fmt.Printf("%-32s %8.1f%% %9s\n", "overall", all.pct(), fmt.Sprintf(">= %.0f%%", overallMin))
	if all.pct() < overallMin {
		failures = append(failures, fmt.Sprintf("overall coverage is %.1f%%, below the %.0f%% minimum", all.pct(), overallMin))
	}
	for name := range packageMin {
		if pkgs[name] == nil {
			failures = append(failures, name+" has no coverage data")
		}
	}

	if len(failures) > 0 {
		fmt.Fprintln(os.Stderr)
		for _, f := range failures {
			fmt.Fprintln(os.Stderr, "FAIL:", f)
		}
		fmt.Fprintln(os.Stderr, "Add tests for the uncovered code; view it with: go tool cover -html=<profile>")
		return 1
	}
	return 0
}
