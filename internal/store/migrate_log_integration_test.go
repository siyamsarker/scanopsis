// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

//go:build integration

package store_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"testing"
	"time"

	"github.com/siyamsarker/scanopsis/internal/store"
)

// TestMigrateLogsReadableDuration checks that the JSON log gives the time a
// migration took as a duration with its unit ("41ms"), as the retention log
// does, not as a bare number of nanoseconds an operator has to interpret.
func TestMigrateLogsReadableDuration(t *testing.T) {
	t.Parallel()
	pool := emptyDatabase(t, "migrate_log")
	var logs bytes.Buffer
	if err := store.MigrateLogged(context.Background(), pool, slog.New(slog.NewJSONHandler(&logs, nil))); err != nil {
		t.Fatal(err)
	}
	applied := 0
	sc := bufio.NewScanner(&logs)
	for sc.Scan() {
		var line map[string]any
		if err := json.Unmarshal(sc.Bytes(), &line); err != nil {
			t.Fatalf("not JSON: %s", sc.Text())
		}
		if line["msg"] != "applied migration" {
			continue
		}
		applied++
		s, ok := line["duration"].(string)
		if !ok {
			t.Fatalf("duration is %T %v, want a string with a unit: %s", line["duration"], line["duration"], sc.Text())
		}
		d, err := time.ParseDuration(s)
		if err != nil || d < 0 || d != d.Round(time.Millisecond) {
			t.Errorf("duration %q (%v): want a duration rounded to milliseconds", s, err)
		}
	}
	if applied == 0 {
		t.Fatalf("no applied migration logged:\n%s", logs.String())
	}
}
