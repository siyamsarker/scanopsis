// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"strings"
	"unicode/utf8"
)

// minPasswordRunes is the shortest accepted password, in characters.
const minPasswordRunes = 12

// policyError is a password rule violation; its text is a sentence for the
// user, so it is not written like a Go error string.
type policyError string

func (e policyError) Error() string { return string(e) }

// CheckPassword reports why password is not acceptable for the local account
// username, or nil. The messages are plain sentences meant for the user.
func CheckPassword(password, username string) error {
	if utf8.RuneCountInString(password) < minPasswordRunes {
		return policyError("The password must be at least 12 characters long.")
	}
	if len(password) > maxPasswordBytes {
		return policyError("The password must be at most 1024 bytes long.")
	}
	if strings.EqualFold(password, username) {
		return policyError("The password must not be the same as the username.")
	}
	_, size := utf8.DecodeRuneInString(password)
	if strings.Count(password, password[:size]) == utf8.RuneCountInString(password) {
		return policyError("The password must not be a single repeated character.")
	}
	return nil
}
