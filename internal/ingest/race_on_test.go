// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration && race

package ingest

// The race detector slows the code 5 to 10 times; time limits are scaled.
const raceSlowdown = 4
