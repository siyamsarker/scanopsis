// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"context"
	"errors"
	"fmt"
	"testing"
)

func TestMessage(t *testing.T) {
	for err, want := range map[error]string{
		errors.New("take backup: the folder is full"):         "the folder is full",
		errors.New("restore backup: the file is damaged"):     "the file is damaged",
		fmt.Errorf("take backup: copy: %w", context.Canceled): "Scanopsis stopped before the job finished; nothing was changed.",
		ErrMaintenance: "another maintenance job is running; try again in a few minutes",
	} {
		if got := message(err); got != want {
			t.Errorf("message(%v) = %q, want %q", err, got, want)
		}
	}
}
