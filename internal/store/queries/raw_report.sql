-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- ScanRawReport returns the stored original report of a scan, gzip-compressed.
-- It is empty once data retention has cleared it. It is read only by
-- "scanopsis admin export-report"; no list or API query selects raw_report.
-- name: ScanRawReport :one
SELECT raw_report FROM scans WHERE id = $1;
