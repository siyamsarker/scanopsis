// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package backup

import (
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
)

const (
	fingerprintInfo = "scanopsis backup key fingerprint"
	signingKeyInfo  = "scanopsis backup signature"
)

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

// SigningKey derives, from the secret key, the key that signs backups
// (HKDF-SHA-256 with the label "scanopsis backup signature"). Only an
// installation that has the secret key can make a backup that its readers
// accept, so a file put into the backup folder by someone else is refused.
// Like KeyFingerprint, the construction must never change.
func SigningKey(secretKey []byte) ([]byte, error) {
	if len(secretKey) == 0 {
		return nil, errors.New("derive backup signing key: the secret key is empty")
	}
	k, err := hkdf.Key(sha256.New, secretKey, nil, signingKeyInfo, sha256.Size)
	if err != nil {
		return nil, fmt.Errorf("derive backup signing key: %w", err)
	}
	return k, nil
}

// sign returns the signature of a backup: HMAC-SHA-256 keyed with
// signingKey over the length of the manifest (8 bytes, big-endian), the
// manifest and the trailer, byte for byte as they are stored. The trailer
// holds the SHA-256 of the data of every table, so the signature covers the
// whole backup.
func sign(signingKey, manifest, trailer []byte) []byte {
	mac := hmac.New(sha256.New, signingKey)
	var n [8]byte
	binary.BigEndian.PutUint64(n[:], uint64(len(manifest)))
	mac.Write(n[:])
	mac.Write(manifest)
	mac.Write(trailer)
	return mac.Sum(nil)
}
