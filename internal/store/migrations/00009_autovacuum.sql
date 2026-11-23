-- Copyright 2026 Siyam Sarker
-- SPDX-License-Identifier: Apache-2.0

-- Autovacuum settings for the two large tables. At the 90-day volume of 500
-- services (about 600000 scans and 9 million findings) the PostgreSQL
-- defaults wait for a fifth of the table to change before vacuuming and a
-- tenth before analyzing, which is 120000 scans and 1.8 million findings
-- (900000 to analyze). Data retention changes far more than that in one run
-- (it deleted 426000 raw reports and the findings of the scans it removed),
-- and until autovacuum and the statistics caught up, the queries that read
-- these tables were two to five times slower.
--
-- Vacuum at 5% dead rows (30000 scans, 450000 findings) and analyze at 2%
-- changed rows (12000 scans, 180000 findings): a fourth and a fifth of the
-- defaults, so the work is done in smaller, shorter runs soon after a change
-- and the planner's statistics follow the data. The insert threshold also
-- applies, because vacuum is what sets the visibility map that the covering
-- indexes (scans_current_cover_idx) need to be answered without reading the
-- table, and at the default 20% a table of this size waits for 120000 new
-- scans (1.8 million findings) before vacuum sets it again.
--
-- The settings are storage parameters of the tables, so they need no change
-- to postgresql.conf and apply to every installation. Setting them takes a
-- brief lock that does not block reads or writes.

-- +goose Up
ALTER TABLE scans SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_vacuum_insert_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);
ALTER TABLE vulnerabilities SET (
    autovacuum_vacuum_scale_factor = 0.05,
    autovacuum_vacuum_insert_scale_factor = 0.05,
    autovacuum_analyze_scale_factor = 0.02
);

-- +goose Down
ALTER TABLE scans RESET (
    autovacuum_vacuum_scale_factor,
    autovacuum_vacuum_insert_scale_factor,
    autovacuum_analyze_scale_factor
);
ALTER TABLE vulnerabilities RESET (
    autovacuum_vacuum_scale_factor,
    autovacuum_vacuum_insert_scale_factor,
    autovacuum_analyze_scale_factor
);
