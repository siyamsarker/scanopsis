// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package domain holds the pure rules of the findings read API: what makes
// two findings "the same", how scans are diffed, how versions compare and how
// packages are ranked. Nothing here performs I/O.
package domain

// Key identifies a finding across scans: the same vulnerability in the same
// package at the same target. The installed version is deliberately not part
// of it, so upgrading a package does not make its remaining findings "new".
type Key struct {
	VulnID  string
	PkgName string
	Target  string
}

// Diff compares the findings of a scan (cur) with those of the previous scan
// (prev). added holds the findings of cur whose key is absent from prev,
// removed those of prev whose key is absent from cur, and unchanged counts
// the keys present on both sides. Each side is a set: a key that occurs more
// than once within one scan is reported (and counted) once, using its first
// occurrence. Input order is preserved in the results.
func Diff[T any](prev, cur []T, key func(T) Key) (added, removed []T, unchanged int) {
	prevKeys := make(map[Key]struct{}, len(prev))
	for _, f := range prev {
		prevKeys[key(f)] = struct{}{}
	}
	curKeys := make(map[Key]struct{}, len(cur))
	for _, f := range cur {
		k := key(f)
		if _, dup := curKeys[k]; dup {
			continue
		}
		curKeys[k] = struct{}{}
		if _, ok := prevKeys[k]; ok {
			unchanged++
			continue
		}
		added = append(added, f)
	}
	seen := make(map[Key]struct{}, len(prevKeys))
	for _, f := range prev {
		k := key(f)
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		if _, ok := curKeys[k]; !ok {
			removed = append(removed, f)
		}
	}
	return added, removed, unchanged
}
