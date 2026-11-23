// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package backup saves the Scanopsis database into one file and reads such a
// file back.
//
// # File format
//
// A backup is a gzip-compressed tar archive with a fixed layout:
//
//   - manifest.json comes first. It says which version of Scanopsis wrote the
//     file, when, why (manual, scheduled, uploaded or before a restore), the
//     fingerprint of the secret key, and the tables with their columns.
//   - tables/<name>/<number>.csv follow, one table after the other in the
//     order of the manifest. Each file holds PostgreSQL CSV data. A table is
//     cut into pieces of at most ChunkSize bytes, numbered from 000001; an
//     empty table still has one empty piece.
//   - checksums.json follows the tables. It holds the row count, byte count
//     and SHA-256 of every table.
//   - signature comes last: the HMAC-SHA-256, in hex, of the 8-byte
//     big-endian length of manifest.json, then manifest.json and
//     checksums.json byte for byte as stored. Its key is derived from the
//     installation's secret key (SigningKey). Since checksums.json holds the
//     SHA-256 of every table, the signature covers all of the file.
//
// # Trust
//
// The key fingerprint in the manifest only says which key the backup claims
// to belong to; it gives the clearer message when an installation is
// handed a backup of another key (ErrKeyMismatch). What a restore trusts is
// the signature: anyone who can write into the backup folder can write a
// manifest and matching checksums, but only an installation with the
// secret key can sign them. Verify and ReadTables refuse a file whose
// signature is missing or wrong with ErrSignature. ReadManifest checks no
// signature and is meant for listing files only.
//
// A tar entry needs its size before its content, so the writer holds one
// piece (at most ChunkSize) in memory at a time, never a whole table. The
// readers stream in the same way, so a large database fits in a small
// container.
//
// A backup file can come from anywhere, so the readers trust nothing in it.
// They stop a file that expands past DefaultMaxBytes (or the limit given to
// VerifyLimited and ReadTablesLimited; a restore passes what a file of its
// size can honestly hold), count every decompressed byte, cap the
// number of tables, and cap the size of the JSON parts, accept only the entry names the format
// allows, in the order it allows, and refuse anything else (other names, links,
// pieces out of order). Failures wrap ErrNotBackup, ErrCorrupt, ErrFormat or
// ErrSignature with a sentence that is meant to be shown to a person.
package backup
