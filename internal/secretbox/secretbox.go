// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package secretbox encrypts secrets that must be stored in the database
// (TOTP secrets, credentials entered on the Settings page) with AES-256-GCM.
package secretbox

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"strings"
)

// KeySize is the key length in bytes (AES-256).
const KeySize = 32

// ErrDecrypt is returned when a value cannot be decrypted: it was encrypted
// with another key, for another purpose, or it was modified.
var ErrDecrypt = errors.New("cannot decrypt secret: wrong key or corrupted value")

// Box encrypts and decrypts values. It is safe for concurrent use.
type Box struct {
	aead cipher.AEAD
}

// ParseKey decodes a base64 key (standard or URL alphabet, padded or not)
// of exactly KeySize bytes, as produced by `openssl rand -base64 32`.
func ParseKey(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	for _, enc := range []*base64.Encoding{base64.StdEncoding, base64.RawStdEncoding, base64.URLEncoding, base64.RawURLEncoding} {
		if k, err := enc.DecodeString(s); err == nil {
			if len(k) != KeySize {
				return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(k))
			}
			return k, nil
		}
	}
	return nil, errors.New("key is not valid base64")
}

// New returns a Box for a KeySize-byte key.
func New(key []byte) (*Box, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &Box{aead: aead}, nil
}

// Seal encrypts plaintext. purpose is authenticated but not stored (for
// example "totp:42" or "setting:gitlab.client_secret"), so a ciphertext
// copied to another row or column fails to decrypt. The result is
// nonce || ciphertext.
func (b *Box) Seal(plaintext []byte, purpose string) ([]byte, error) {
	nonce := make([]byte, b.aead.NonceSize(), b.aead.NonceSize()+len(plaintext)+b.aead.Overhead())
	if _, err := rand.Read(nonce); err != nil {
		return nil, err
	}
	return b.aead.Seal(nonce, nonce, plaintext, []byte(purpose)), nil
}

// Open decrypts a value produced by Seal with the same purpose.
func (b *Box) Open(sealed []byte, purpose string) ([]byte, error) {
	n := b.aead.NonceSize()
	if len(sealed) < n+b.aead.Overhead() {
		return nil, ErrDecrypt
	}
	out, err := b.aead.Open(nil, sealed[:n], sealed[n:], []byte(purpose))
	if err != nil {
		return nil, ErrDecrypt
	}
	return out, nil
}
