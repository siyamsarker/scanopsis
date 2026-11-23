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
//   - checksums.json comes last. It holds the row count, byte count and
//     SHA-256 of every table.
//
// A tar entry needs its size before its content, so the writer holds one
// piece (at most ChunkSize) in memory at a time, never a whole table. The
// readers stream in the same way, so a large database fits in a small
// container.
//
// A backup file can come from anywhere, so the readers trust nothing in it.
// They cap the size of the JSON parts, accept only the entry names the format
// allows, in the order it allows, and refuse anything else (other names, links,
// pieces out of order). Failures wrap ErrNotBackup, ErrCorrupt or ErrFormat
// with a sentence that is meant to be shown to a person.
package backup
