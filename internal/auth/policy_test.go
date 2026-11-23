// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"strings"
	"testing"
)

func TestCheckPassword(t *testing.T) {
	cases := []struct {
		name, password, username string
		want                     string // substring of the message; "" means accepted
	}{
		{"ok", "correct horse battery", "admin", ""},
		{"exactly 12 characters", "abcdefghijkl", "admin", ""},
		{"11 characters", "abcdefghijk", "admin", "at least 12"},
		{"empty", "", "admin", "at least 12"},
		{"counts runes not bytes", strings.Repeat("é", 11), "admin", "at least 12"},
		{"12 multibyte runes", "éèêëàâäôöûüç", "admin", ""},
		{"1024 bytes", strings.Repeat("ab", 512), "admin", ""},
		{"1025 bytes", strings.Repeat("a", 1024) + "b", "admin", "at most 1024"},
		{"equals username", "administrator", "administrator", "same as the username"},
		{"equals username ignoring case", "Administrator", "aDMINISTRATOR", "same as the username"},
		{"contains but is not the username", "administrator1", "administrator", ""},
		{"one repeated character", strings.Repeat("a", 20), "admin", "single repeated"},
		{"one repeated multibyte character", strings.Repeat("é", 14), "admin", "single repeated"},
		{"two characters repeated", strings.Repeat("ab", 10), "admin", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := CheckPassword(c.password, c.username)
			if c.want == "" {
				if err != nil {
					t.Fatalf("rejected: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), c.want) {
				t.Fatalf("error %v; want one containing %q", err, c.want)
			}
			if msg := err.Error(); !strings.HasSuffix(msg, ".") || msg[0] < 'A' || msg[0] > 'Z' {
				t.Fatalf("message %q is not a plain sentence", msg)
			}
		})
	}
}
