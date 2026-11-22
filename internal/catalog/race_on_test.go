// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration && race

package catalog

// The race detector slows Go code 5 to 10 times. The portfolio history does
// most of its work in Go, so its time limits are scaled; `make perf` runs
// without the race detector and holds it to the real limit.
const raceSlowdown = 4
