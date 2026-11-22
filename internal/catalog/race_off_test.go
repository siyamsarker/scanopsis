// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

//go:build integration && !race

package catalog

// Without the race detector, time limits apply as they are.
const raceSlowdown = 1
