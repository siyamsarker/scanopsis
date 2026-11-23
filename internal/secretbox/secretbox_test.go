// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package secretbox

import (
	"bytes"
	"encoding/base64"
	"errors"
	"testing"
)

func testBox(t *testing.T, fill byte) *Box {
	t.Helper()
	b, err := New(bytes.Repeat([]byte{fill}, KeySize))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestSealOpen(t *testing.T) {
	b := testBox(t, 1)
	sealed, err := b.Seal([]byte("JBSWY3DPEHPK3PXP"), "totp:1")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, []byte("JBSWY3DPEHPK3PXP")) {
		t.Fatal("ciphertext contains the plaintext")
	}
	got, err := b.Open(sealed, "totp:1")
	if err != nil || string(got) != "JBSWY3DPEHPK3PXP" {
		t.Fatalf("Open = %q, %v", got, err)
	}
	again, _ := b.Seal([]byte("JBSWY3DPEHPK3PXP"), "totp:1")
	if bytes.Equal(sealed, again) {
		t.Fatal("two seals of the same value are identical: nonce reused")
	}
}

func TestOpenRejects(t *testing.T) {
	b := testBox(t, 1)
	sealed, _ := b.Seal([]byte("secret"), "totp:1")
	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 1
	for name, tc := range map[string]struct {
		box     *Box
		value   []byte
		purpose string
	}{
		"other purpose": {b, sealed, "totp:2"},
		"other key":     {testBox(t, 2), sealed, "totp:1"},
		"tampered":      {b, tampered, "totp:1"},
		"too short":     {b, sealed[:10], "totp:1"},
		"empty":         {b, nil, "totp:1"},
	} {
		if _, err := tc.box.Open(tc.value, tc.purpose); !errors.Is(err, ErrDecrypt) {
			t.Errorf("%s: err = %v, want ErrDecrypt", name, err)
		}
	}
}

func TestParseKey(t *testing.T) {
	raw := bytes.Repeat([]byte{0xfb}, KeySize) // encodes differently in std and URL alphabets
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		k, err := ParseKey(" " + enc.EncodeToString(raw) + "\n")
		if err != nil || !bytes.Equal(k, raw) {
			t.Errorf("ParseKey(%v) = %x, %v", enc, k, err)
		}
	}
	if _, err := ParseKey(base64.StdEncoding.EncodeToString(raw[:16])); err == nil {
		t.Error("16-byte key accepted")
	}
	if _, err := ParseKey("not base64 !!"); err == nil {
		t.Error("invalid base64 accepted")
	}
	if _, err := New(raw[:31]); err == nil {
		t.Error("New accepted a short key")
	}
}
