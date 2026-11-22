// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package findings

import (
	"fmt"
	"reflect"
	"testing"
)

func TestFixedVersions(t *testing.T) {
	got := fixedVersions([]string{"1.0.5, 1.1.2", "1.1.2", " ", "2.0", "1.0.5,3.0"})
	if want := []string{"1.0.5", "1.1.2", "2.0", "3.0"}; !reflect.DeepEqual(got, want) {
		t.Errorf("fixedVersions = %v, want %v", got, want)
	}
	if got := fixedVersions(nil); got == nil || len(got) != 0 {
		t.Errorf("fixedVersions(nil) = %#v, want an empty list", got)
	}
	many := make([]string, 0, 30)
	for i := range 30 {
		many = append(many, fmt.Sprintf("1.%d", i))
	}
	if got := fixedVersions(many); len(got) != maxFixedVersions || got[0] != "1.0" {
		t.Errorf("fixedVersions(30) = %v", got)
	}
}
