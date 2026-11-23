// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"bytes"
	"encoding/hex"
	"strings"
	"testing"
)

func TestKeyFingerprint(t *testing.T) {
	keyA := bytes.Repeat([]byte{0x11}, 32)
	keyB := bytes.Repeat([]byte{0x22}, 32)

	a1, a2, b := KeyFingerprint(keyA), KeyFingerprint(keyA), KeyFingerprint(keyB)
	if a1 != a2 {
		t.Fatalf("fingerprint is not stable: %q vs %q", a1, a2)
	}
	if a1 == b {
		t.Fatal("two different keys share a fingerprint")
	}
	if len(a1) != 32 {
		t.Fatalf("fingerprint length = %d, want 32 hex characters", len(a1))
	}
	if _, err := hex.DecodeString(a1); err != nil {
		t.Fatalf("fingerprint %q is not hex: %v", a1, err)
	}
	if strings.Contains(a1, hex.EncodeToString(keyA)[:8]) {
		t.Fatal("fingerprint contains part of the key")
	}
}
