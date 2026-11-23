// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package catalog

import (
	"cmp"
	"context"
	"net/http"
	"slices"
	"strconv"
	"time"

	"github.com/jackc/pgx/v5"
)

const (
	// historyPoints is the number of recent pipelines listed per service.
	historyPoints = 20
	// historyWindow is the number of recent scans read per (artifact, kind).
	// A group has at most one scan per pipeline unless a job was retried, so
	// a few more than historyPoints reach back before the oldest point.
	historyWindow = 24
	// historyDays is the number of daily portfolio states, one per 24 hours
	// back from now.
	historyDays = 7
)

// historySQL returns, for every non-archived service, the latest
// historyWindow scans of each (artifact_name, kind) of its dashboard ref,
// with the counts the portfolio sums. The dashboard ref and the skip scan
// over the groups are those of portfolioSQL. Rows come in no particular
// order; the handler groups them. The per-group read is an ordered index
// scan of scans_current_cover_idx; the planner, which expects a couple of
// dozen rows per group, otherwise prefers a bitmap scan that reads every
// scan of the group, so the handler turns bitmap scans off for this
// transaction.
var historySQL = `
WITH svc AS MATERIALIZED (
    SELECT s.id,
           COALESCE(s.default_ref,
                    (SELECT ref FROM scans WHERE service_id = s.id ORDER BY scanned_at DESC, ref LIMIT 1)) AS ref
    FROM services s
    WHERE NOT s.archived
), grp AS MATERIALIZED (
    SELECT svc.id AS service_id, svc.ref, g.artifact_name, g.kind
    FROM svc
    CROSS JOIN LATERAL (
        WITH RECURSIVE c AS (
            (SELECT artifact_name, kind FROM scans
             WHERE service_id = svc.id AND ref = svc.ref
             ORDER BY artifact_name, kind LIMIT 1)
            UNION ALL
            SELECT n.* FROM c, LATERAL (
                SELECT artifact_name, kind FROM scans
                WHERE service_id = svc.id AND ref = svc.ref AND (artifact_name, kind) > (c.artifact_name, c.kind)
                ORDER BY artifact_name, kind LIMIT 1
            ) n
        )
        SELECT artifact_name, kind FROM c
    ) g
    WHERE svc.ref IS NOT NULL
)
SELECT g.service_id, g.ref, g.artifact_name, g.kind,
       x.id, x.gitlab_pipeline_id, x.scanned_at,
       x.count_critical, x.count_high, x.count_medium, x.count_low, x.count_unknown
FROM grp g
CROSS JOIN LATERAL (
    SELECT id, gitlab_pipeline_id, scanned_at,
           count_critical, count_high, count_medium, count_low, count_unknown
    FROM scans
    WHERE service_id = g.service_id AND ref = g.ref AND artifact_name = g.artifact_name AND kind = g.kind
    ORDER BY service_id, ref, artifact_name, kind, scanned_at DESC, id DESC
    LIMIT ` + strconv.Itoa(historyWindow) + `
) x`

// probeSQL returns, for each (group, time) given as parallel arrays, the
// counts of the group's latest scan at or before that time; pairs without
// such a scan are absent. One index probe per pair.
const probeSQL = `
SELECT q.i, x.count_critical, x.count_high, x.count_medium, x.count_low, x.count_unknown
FROM unnest($1::bigint[], $2::text[], $3::text[], $4::text[], $5::timestamptz[])
     WITH ORDINALITY AS q(service_id, ref, artifact_name, kind, at, i)
CROSS JOIN LATERAL (
    SELECT count_critical, count_high, count_medium, count_low, count_unknown
    FROM scans
    WHERE service_id = q.service_id AND ref = q.ref AND artifact_name = q.artifact_name AND kind = q.kind
      AND scanned_at <= q.at
    ORDER BY service_id, ref, artifact_name, kind, scanned_at DESC, id DESC
    LIMIT 1
) x`

type historyPoint struct {
	PipelineID int64          `json:"pipeline_id"`
	At         time.Time      `json:"at"`
	Counts     severityCounts `json:"counts"`
}

type serviceHistory struct {
	ServiceID int64          `json:"service_id"`
	Ref       string         `json:"ref"`
	Points    []historyPoint `json:"points"`
}

type dayState struct {
	At     time.Time      `json:"at"`
	Counts severityCounts `json:"counts"`
}

type portfolioHistory struct {
	Services    []serviceHistory `json:"services"`
	Days        []dayState       `json:"days"`
	GeneratedAt time.Time        `json:"generated_at"`
}

func (a severityCounts) plus(b severityCounts) severityCounts {
	return severityCounts{a.Critical + b.Critical, a.High + b.High, a.Medium + b.Medium, a.Low + b.Low, a.Unknown + b.Unknown}
}

func (a severityCounts) minus(b severityCounts) severityCounts {
	return severityCounts{a.Critical - b.Critical, a.High - b.High, a.Medium - b.Medium, a.Low - b.Low, a.Unknown - b.Unknown}
}

// historyScan is one scan of a group.
type historyScan struct {
	id, pipeline int64
	at           time.Time
	counts       severityCounts
}

// after orders scans by (scanned_at, id), the order every "latest scan" rule uses.
func (s historyScan) after(o historyScan) bool {
	return s.at.After(o.at) || (s.at.Equal(o.at) && s.id > o.id)
}

func compareScans(a, b historyScan) int {
	return cmp.Or(a.at.Compare(b.at), cmp.Compare(a.id, b.id))
}

// historyGroup is one (artifact_name, kind) of a service's dashboard ref
// with its latest scans, newest first. full means more scans may exist
// before the oldest one read.
type historyGroup struct {
	artifact, kind string
	scans          []historyScan
	full           bool
}

// stateAt is the group's latest scan at or before t, if the scans read
// decide it: ok is false only when every scan read is later than t and
// older ones may exist.
func (g *historyGroup) stateAt(t time.Time) (counts severityCounts, ok bool) {
	for _, s := range g.scans {
		if !s.at.After(t) {
			return s.counts, true
		}
	}
	return severityCounts{}, !g.full
}

type historyService struct {
	id     int64
	ref    string
	groups []*historyGroup
}

// points carries every group's state forward through the service's scans
// in (scanned_at, id) order and returns the state right after the last scan
// of each pipeline, for the latest historyPoints pipelines, oldest first.
// A pipeline is reported only when every group's state is known at its last
// scan: a group whose window is full is known from its oldest scan read on.
func (s *historyService) points() []historyPoint {
	type event struct {
		scan  historyScan
		group int
	}
	var events []event
	var seeded historyScan
	for gi, g := range s.groups {
		for _, sc := range g.scans {
			events = append(events, event{sc, gi})
		}
		if g.full {
			if oldest := g.scans[len(g.scans)-1]; oldest.after(seeded) {
				seeded = oldest
			}
		}
	}
	slices.SortFunc(events, func(a, b event) int { return compareScans(a.scan, b.scan) })

	state := make([]severityCounts, len(s.groups))
	var total severityCounts
	var after []historyPoint // the state after each scan
	for _, e := range events {
		total = total.minus(state[e.group]).plus(e.scan.counts)
		state[e.group] = e.scan.counts
		if !seeded.after(e.scan) {
			after = append(after, historyPoint{PipelineID: e.scan.pipeline, At: e.scan.at, Counts: total})
		}
	}
	// Keep the last scan of each pipeline, newest first, then reverse.
	out := []historyPoint{}
	seen := map[int64]bool{}
	for i := len(after) - 1; i >= 0 && len(out) < historyPoints; i-- {
		if p := after[i]; !seen[p.PipelineID] {
			seen[p.PipelineID] = true
			out = append(out, p)
		}
	}
	slices.Reverse(out)
	return out
}

// getPortfolioHistory returns the recent pipelines of every service and the
// portfolio's daily state over the last week, for sparklines and deltas.
func (c *Catalog) getPortfolioHistory(w http.ResponseWriter, r *http.Request) {
	body, err := c.history.get(r.Context())
	if err != nil {
		c.internalError(w, r, "cannot read portfolio history", err)
		return
	}
	// As httpapi.WriteJSON writes every other response.
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(body)
}

// loadHistory reads everything in one read-only snapshot, which is closed
// before the response is written.
func (c *Catalog) loadHistory(ctx context.Context, now time.Time) (*portfolioHistory, error) {
	tx, err := c.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback(context.WithoutCancel(ctx)) }()
	if _, err := tx.Exec(ctx, "SET LOCAL enable_bitmapscan = off"); err != nil {
		return nil, err
	}
	services, err := readHistory(ctx, tx)
	if err != nil {
		return nil, err
	}

	out := &portfolioHistory{Services: make([]serviceHistory, 0, len(services)), GeneratedAt: now}
	for _, s := range services {
		out.Services = append(out.Services, serviceHistory{ServiceID: s.id, Ref: s.ref, Points: s.points()})
	}

	// The portfolio at each day boundary: every group's latest scan at or
	// before it, from the scans read where they reach back far enough, else
	// from one probe statement.
	days := make([]dayState, historyDays)
	var probe struct {
		svc                  []int64
		ref, artifact, kinds []string
		at                   []time.Time
		day                  []int
	}
	for k := range historyDays {
		at := now.Add(-time.Duration(historyDays-k) * 24 * time.Hour)
		days[k].At = at
		for _, s := range services {
			for _, g := range s.groups {
				counts, ok := g.stateAt(at)
				if ok {
					days[k].Counts = days[k].Counts.plus(counts)
					continue
				}
				probe.svc = append(probe.svc, s.id)
				probe.ref = append(probe.ref, s.ref)
				probe.artifact = append(probe.artifact, g.artifact)
				probe.kinds = append(probe.kinds, g.kind)
				probe.at = append(probe.at, at)
				probe.day = append(probe.day, k)
			}
		}
	}
	if len(probe.svc) > 0 {
		rows, err := tx.Query(ctx, probeSQL, probe.svc, probe.ref, probe.artifact, probe.kinds, probe.at)
		if err != nil {
			return nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var i int64
			var n severityCounts
			if err := rows.Scan(&i, &n.Critical, &n.High, &n.Medium, &n.Low, &n.Unknown); err != nil {
				return nil, err
			}
			k := probe.day[i-1]
			days[k].Counts = days[k].Counts.plus(n)
		}
		if err := rows.Err(); err != nil {
			return nil, err
		}
	}
	out.Days = days
	return out, nil
}

// readHistory runs historySQL and groups its rows by service, ordered by
// service id, and by (artifact_name, kind), scans newest first.
func readHistory(ctx context.Context, tx pgx.Tx) ([]*historyService, error) {
	rows, err := tx.Query(ctx, historySQL)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	type groupKey struct {
		svc            int64
		artifact, kind string
	}
	byService := map[int64]*historyService{}
	byGroup := map[groupKey]*historyGroup{}
	for rows.Next() {
		var svcID int64
		var ref, artifact, kind string
		var s historyScan
		var n [5]int32
		if err := rows.Scan(&svcID, &ref, &artifact, &kind, &s.id, &s.pipeline, &s.at,
			&n[0], &n[1], &n[2], &n[3], &n[4]); err != nil {
			return nil, err
		}
		s.at = s.at.UTC()
		s.counts = severityCounts{int64(n[0]), int64(n[1]), int64(n[2]), int64(n[3]), int64(n[4])}
		svc := byService[svcID]
		if svc == nil {
			svc = &historyService{id: svcID, ref: ref}
			byService[svcID] = svc
		}
		k := groupKey{svcID, artifact, kind}
		g := byGroup[k]
		if g == nil {
			g = &historyGroup{artifact: artifact, kind: kind}
			byGroup[k] = g
			svc.groups = append(svc.groups, g)
		}
		g.scans = append(g.scans, s)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	services := make([]*historyService, 0, len(byService))
	for _, s := range byService {
		for _, g := range s.groups {
			slices.SortFunc(g.scans, func(a, b historyScan) int { return compareScans(b, a) })
			g.full = len(g.scans) >= historyWindow
		}
		slices.SortFunc(s.groups, func(a, b *historyGroup) int {
			return cmp.Or(cmp.Compare(a.artifact, b.artifact), cmp.Compare(a.kind, b.kind))
		})
		services = append(services, s)
	}
	slices.SortFunc(services, func(a, b *historyService) int { return cmp.Compare(a.id, b.id) })
	return services, nil
}
