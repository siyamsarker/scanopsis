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

	a1, err := KeyFingerprint(keyA)
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := KeyFingerprint(keyA)
	b, _ := KeyFingerprint(keyB)
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

// TestKeyFingerprintKnownAnswer pins the construction. The value was computed
// independently (HKDF-SHA-256 with an empty salt, then HMAC-SHA-256). If this
// fails, backups made by earlier releases would stop matching their key.
func TestKeyFingerprintKnownAnswer(t *testing.T) {
	key := make([]byte, 32)
	for i := range key {
		key[i] = byte(i)
	}
	got, err := KeyFingerprint(key)
	if err != nil {
		t.Fatal(err)
	}
	if want := "f60c5b4835c52d3f0872fd53937c2766"; got != want {
		t.Fatalf("KeyFingerprint = %s, want %s", got, want)
	}
}

// TestSigningKeyKnownAnswer pins the derivation of the signing key and the
// signature. The values were computed independently (HKDF-SHA-256 with an
// empty salt and the label "scanopsis backup signature", then HMAC-SHA-256
// over the 8-byte big-endian manifest length, the manifest and the trailer).
// If this fails, backups made by earlier releases would no longer verify.
func TestSigningKeyKnownAnswer(t *testing.T) {
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = byte(i)
	}
	key, err := SigningKey(secret)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := hex.EncodeToString(key), "79c763f5aad1e592662dbbaf5310f223eca83ea2847fcbef7c1da3c7d51e8929"; got != want {
		t.Errorf("SigningKey = %s, want %s", got, want)
	}
	sig := sign(key, []byte(`{"format":1}`), []byte(`{"tables":{}}`))
	if got, want := hex.EncodeToString(sig), "e3cbbba9ae6b29b771dc43565fc705371cb65f915d60e7e6c3eba3c511e1347f"; got != want {
		t.Errorf("sign = %s, want %s", got, want)
	}
	if fp, _ := KeyFingerprint(secret); hex.EncodeToString(key)[:32] == fp {
		t.Error("the signing key and the fingerprint are the same value")
	}
	if _, err := SigningKey(nil); err == nil {
		t.Error("SigningKey accepted an empty secret key")
	}
}
