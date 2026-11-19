// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/argon2"
)

var phcPattern = regexp.MustCompile(`^\$argon2id\$v=19\$m=65536,t=3,p=4\$[A-Za-z0-9+/]{22}\$[A-Za-z0-9+/]{43}$`)

func TestHashPasswordRoundTrip(t *testing.T) {
	enc, err := HashPassword(context.Background(), "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if !phcPattern.MatchString(enc) {
		t.Fatalf("encoded hash %q does not match the specified format", enc)
	}
	ok, err := VerifyPassword(context.Background(), enc, "correct horse")
	if err != nil || !ok {
		t.Fatalf("VerifyPassword(context.Background(), right) = %v, %v; want true, nil", ok, err)
	}
	ok, err = VerifyPassword(context.Background(), enc, "correct horsf")
	if err != nil || ok {
		t.Fatalf("VerifyPassword(context.Background(), wrong) = %v, %v; want false, nil", ok, err)
	}
}

func TestHashPasswordSaltIsUnique(t *testing.T) {
	a, err := HashPassword(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword(context.Background(), "same")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("two hashes of the same password are identical")
	}
	if strings.Split(a, "$")[4] == strings.Split(b, "$")[4] {
		t.Fatal("two hashes share a salt")
	}
}

// cheapHash builds a valid hash with small parameters so tests stay fast.
func cheapHash(password string, m, tm uint32, p uint8) string {
	salt := []byte("0123456789abcdef")
	key := argon2.IDKey([]byte(password), salt, tm, m, p, 32)
	return fmt.Sprintf("$argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", m, tm, p,
		base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(key))
}

func TestVerifyPasswordUsesEncodedParameters(t *testing.T) {
	enc := cheapHash("pw", 8, 1, 1)
	if ok, err := VerifyPassword(context.Background(), enc, "pw"); err != nil || !ok {
		t.Fatalf("cheap hash: %v, %v; want true, nil", ok, err)
	}
	if ok, err := VerifyPassword(context.Background(), enc, "other"); err != nil || ok {
		t.Fatalf("cheap hash wrong password: %v, %v; want false, nil", ok, err)
	}
}

func TestVerifyPasswordRejectsMalformedHashes(t *testing.T) {
	const (
		salt  = "MDEyMzQ1Njc4OWFiY2RlZg"                         // 16 bytes
		key   = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"    // 32 bytes
		key31 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHg"     // 31 bytes
		key33 = "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8gIA" // 33 bytes
	)
	good := "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + key
	if _, err := VerifyPassword(context.Background(), good, "x"); err != nil {
		t.Fatalf("control hash rejected: %v", err)
	}
	cases := map[string]string{
		"empty":                "",
		"garbage":              "not a hash",
		"bare dollar":          "$",
		"wrong algorithm i":    "$argon2i$v=19$m=8,t=1,p=1$" + salt + "$" + key,
		"wrong algorithm d":    "$argon2d$v=19$m=8,t=1,p=1$" + salt + "$" + key,
		"bcrypt":               "$2a$10$abcdefghijklmnopqrstuuABCDEFGHIJKLMNOPQRSTUVWXYZ01234",
		"wrong version 16":     "$argon2id$v=16$m=8,t=1,p=1$" + salt + "$" + key,
		"wrong version 18":     "$argon2id$v=18$m=8,t=1,p=1$" + salt + "$" + key,
		"missing version":      "$argon2id$m=8,t=1,p=1$" + salt + "$" + key,
		"no leading dollar":    "argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + key,
		"extra field":          good + "$extra",
		"missing hash":         "$argon2id$v=19$m=8,t=1,p=1$" + salt,
		"memory over 1GiB":     "$argon2id$v=19$m=1048577,t=1,p=1$" + salt + "$" + key,
		"memory overflow":      "$argon2id$v=19$m=99999999999,t=1,p=1$" + salt + "$" + key,
		"memory zero":          "$argon2id$v=19$m=0,t=1,p=1$" + salt + "$" + key,
		"memory below 8p":      "$argon2id$v=19$m=15,t=1,p=2$" + salt + "$" + key,
		"time over 10":         "$argon2id$v=19$m=8,t=11,p=1$" + salt + "$" + key,
		"time zero":            "$argon2id$v=19$m=8,t=0,p=1$" + salt + "$" + key,
		"threads over 64":      "$argon2id$v=19$m=1024,t=1,p=65$" + salt + "$" + key,
		"threads 256 wraps":    "$argon2id$v=19$m=4096,t=1,p=256$" + salt + "$" + key,
		"threads zero":         "$argon2id$v=19$m=8,t=1,p=0$" + salt + "$" + key,
		"negative memory":      "$argon2id$v=19$m=-8,t=1,p=1$" + salt + "$" + key,
		"plus sign":            "$argon2id$v=19$m=+8,t=1,p=1$" + salt + "$" + key,
		"leading zero":         "$argon2id$v=19$m=08,t=1,p=1$" + salt + "$" + key,
		"non-numeric":          "$argon2id$v=19$m=x,t=1,p=1$" + salt + "$" + key,
		"swapped params":       "$argon2id$v=19$t=1,m=8,p=1$" + salt + "$" + key,
		"missing param":        "$argon2id$v=19$m=8,t=1$" + salt + "$" + key,
		"extra param":          "$argon2id$v=19$m=8,t=1,p=1,k=2$" + salt + "$" + key,
		"empty salt":           "$argon2id$v=19$m=8,t=1,p=1$$" + key,
		"short salt":           "$argon2id$v=19$m=8,t=1,p=1$AAAA$" + key,
		"salt not base64":      "$argon2id$v=19$m=8,t=1,p=1$" + strings.Repeat("!", 22) + "$" + key,
		"salt padded":          "$argon2id$v=19$m=8,t=1,p=1$" + salt + "==$" + key,
		"key not base64":       "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + strings.Repeat("!", 43),
		"key 31 bytes":         "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + key31,
		"key 33 bytes":         "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + key33,
		"empty key":            "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$",
		"url-safe alphabet":    "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + strings.Replace(key, "AAEC", "-_-_", 1),
		"trailing whitespace":  good + " ",
		"trailing newline":     good + "\n",
		"non-canonical base64": "$argon2id$v=19$m=8,t=1,p=1$" + salt + "$" + key[:42] + "9",
	}
	for name, enc := range cases {
		t.Run(name, func(t *testing.T) {
			match, err := VerifyPassword(context.Background(), enc, "x")
			if err == nil || match {
				t.Fatalf("VerifyPassword(context.Background(), %q) = %v, %v; want false and an error", enc, match, err)
			}
		})
	}
}

func TestDummyHashIsValid(t *testing.T) {
	if _, err := parseHash(dummyHash); err != nil {
		t.Fatalf("dummyHash does not parse: %v", err)
	}
	if !strings.Contains(dummyHash, "m=65536,t=3,p=4") {
		t.Fatal("dummyHash must cost the same as real hashes")
	}
}

func holdSlots(t *testing.T) {
	t.Helper()
	for range cap(hashSlots) {
		hashSlots <- struct{}{}
	}
	t.Cleanup(func() {
		for range cap(hashSlots) {
			<-hashSlots
		}
	})
}

func TestPasswordHashingStopsWaitingWhenContextEnds(t *testing.T) {
	holdSlots(t)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := HashPassword(ctx, "pw"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("HashPassword err = %v; want deadline exceeded", err)
	}
	if ok, err := VerifyPassword(ctx, cheapHash("pw", 8, 1, 1), "pw"); ok || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("VerifyPassword = %v, %v; want false, deadline exceeded", ok, err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("waiting for a slot did not stop with the context")
	}
}

func TestPasswordHashingRestoresSlots(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := HashPassword(ctx, "pw"); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	if _, err := VerifyPassword(context.Background(), "garbage", "pw"); err == nil {
		t.Fatal("malformed hash accepted")
	}
	if _, err := VerifyPassword(context.Background(), cheapHash("pw", 8, 1, 1), "pw"); err != nil {
		t.Fatal(err)
	}
	if n := len(hashSlots); n != 0 {
		t.Fatalf("%d slots still held", n)
	}
	// A panic inside the computation releases the slot too (deferred release).
	func() {
		defer func() { _ = recover() }()
		// threads = 0 makes argon2.IDKey panic.
		_, _ = deriveKey(context.Background(), []byte("pw"), []byte("saltsalt"), 1, 8, 0)
	}()
	if n := len(hashSlots); n != 0 {
		t.Fatalf("a panic leaked %d slots", n)
	}
}
