// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters for new hashes (RFC 9106 second recommended option).
const (
	argonTime    = 3
	argonMemory  = 64 * 1024 // KiB
	argonThreads = 4
	argonKeyLen  = 32
	argonSaltLen = 16

	// Upper bounds accepted when verifying, so a corrupted or hostile
	// stored hash cannot make the server allocate or compute without limit.
	maxArgonTime    = 10
	maxArgonMemory  = 1024 * 1024 // KiB, 1 GiB
	maxArgonThreads = 64
)

// hashSlots limits concurrent hash computations: each uses 64 MiB or more.
var hashSlots = make(chan struct{}, 2)

var errInvalidHash = errors.New("invalid password hash")

// deriveKey computes an Argon2id key while holding one of the hashSlots. It
// waits for a slot until ctx is done and then returns ctx.Err(). The slot is
// released by defer, so a panic in the computation cannot leak it.
func deriveKey(ctx context.Context, password, salt []byte, time, memory uint32, threads uint8) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	select {
	case hashSlots <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-hashSlots }()
	return argon2.IDKey(password, salt, time, memory, threads, argonKeyLen), nil
}

// HashPassword returns the Argon2id hash of password in PHC string format. It
// returns ctx.Err() if ctx ends while waiting for a free hashing slot.
func HashPassword(ctx context.Context, password string) (string, error) {
	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}
	key, err := deriveKey(ctx, []byte(password), salt, argonTime, argonMemory, argonThreads)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s", argon2.Version,
		argonMemory, argonTime, argonThreads,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key)), nil
}

// VerifyPassword reports whether password matches the PHC-encoded Argon2id
// hash. It returns an error, and false, if encoded is malformed or uses
// parameters outside the accepted bounds, and ctx.Err() if ctx ends while
// waiting for a free hashing slot.
func VerifyPassword(ctx context.Context, encoded, password string) (bool, error) {
	p, err := parseHash(encoded)
	if err != nil {
		return false, err
	}
	key, err := deriveKey(ctx, []byte(password), p.salt, p.time, p.memory, p.threads)
	if err != nil {
		return false, err
	}
	return subtle.ConstantTimeCompare(key, p.key) == 1, nil
}

type parsedHash struct {
	time, memory uint32
	threads      uint8
	salt, key    []byte
}

func parseHash(encoded string) (parsedHash, error) {
	// The base64 decoder silently skips line breaks; do not.
	if strings.ContainsAny(encoded, "\r\n") {
		return parsedHash{}, errInvalidHash
	}
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return parsedHash{}, errInvalidHash
	}
	if parts[2] != "v="+strconv.Itoa(argon2.Version) {
		return parsedHash{}, errInvalidHash
	}
	params := strings.Split(parts[3], ",")
	if len(params) != 3 {
		return parsedHash{}, errInvalidHash
	}
	var vals [3]uint64
	for i, name := range []string{"m=", "t=", "p="} {
		num, ok := strings.CutPrefix(params[i], name)
		if !ok {
			return parsedHash{}, errInvalidHash
		}
		v, err := strconv.ParseUint(num, 10, 32)
		if err != nil || strconv.FormatUint(v, 10) != num { // no signs, no leading zeros
			return parsedHash{}, errInvalidHash
		}
		vals[i] = v
	}
	m, t, th := vals[0], vals[1], vals[2]
	if t < 1 || t > maxArgonTime || th < 1 || th > maxArgonThreads || m < 8*th || m > maxArgonMemory {
		return parsedHash{}, errInvalidHash
	}
	enc := base64.RawStdEncoding.Strict()
	salt, err := enc.DecodeString(parts[4])
	if err != nil || len(salt) < 8 {
		return parsedHash{}, errInvalidHash
	}
	key, err := enc.DecodeString(parts[5])
	if err != nil || len(key) != argonKeyLen {
		return parsedHash{}, errInvalidHash
	}
	// The conversions are safe: every value was bounded above.
	return parsedHash{time: uint32(t), memory: uint32(m), threads: uint8(th), salt: salt, key: key}, nil //nolint:gosec
}
