// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

const fingerprintInfo = "scanopsis backup key fingerprint"

// KeyFingerprint returns a short label for the secret key. A backup records it
// so a restore can tell whether the encrypted values inside will open with the
// key of this installation. It is derived one way and does not reveal the key.
//
// The construction must never change, or older backups would stop matching.
// A caller must treat an error, or an empty fingerprint on either side of a
// comparison, as a mismatch.
func KeyFingerprint(secretKey []byte) (string, error) {
	derived, err := hkdf.Key(sha256.New, secretKey, nil, fingerprintInfo, sha256.Size)
	if err != nil {
		return "", fmt.Errorf("derive key fingerprint: %w", err)
	}
	mac := hmac.New(sha256.New, derived)
	mac.Write([]byte("fingerprint"))
	return hex.EncodeToString(mac.Sum(nil))[:32], nil
}
