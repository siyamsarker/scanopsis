// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// Scan kinds, as stored in scans.kind.
const (
	KindImage    = "image"
	KindSecretFS = "secret-fs"
)

// errServiceArchived means the project's service is archived, and archived
// services receive no new data.
var errServiceArchived = errors.New("the service is archived")

// errPathTaken means a new service cannot be created because another
// service already uses the project path.
var errPathTaken = errors.New("another service already uses this project path")

// errJobArtifacts means the job already stored MaxArtifactsPerJob other
// artifacts.
var errJobArtifacts = errors.New("the job has uploaded too many different artifacts")

// saveInput is everything one upload stores.
type saveInput struct {
	claims *Claims
	kind   string
	// key is the artifact key of the scan (see ArtifactKey).
	key    string
	report *Report // everything but the findings
	raw    []byte  // gzip-compressed report
	// findings reads the findings of the report again, in order, into a sink.
	findings func(sink) error
	scanTime time.Time
}

// Result is the body of a successful upload (the IngestResult schema).
type Result struct {
	ScanID         int64          `json:"scan_id"`
	ServiceID      int64          `json:"service_id"`
	ServiceCreated bool           `json:"service_created"`
	Replaced       bool           `json:"replaced"`
	Counts         SeverityCounts `json:"counts"`
	Fixable        int32          `json:"fixable"`
	Secrets        int32          `json:"secrets"`
	Suppressed     int32          `json:"suppressed"`
}

// SeverityCounts is the SeverityCounts schema.
type SeverityCounts struct {
	Critical int32 `json:"critical"`
	High     int32 `json:"high"`
	Medium   int32 `json:"medium"`
	Low      int32 `json:"low"`
	Unknown  int32 `json:"unknown"`
}

// saver stores an upload. The PostgreSQL implementation is pgSaver; tests of
// the HTTP layer replace it.
type saver interface {
	save(ctx context.Context, in *saveInput) (Result, error)
}

type pgSaver struct {
	pool *pgxpool.Pool
	log  *slog.Logger
}

var (
	vulnColumns = []string{
		"scan_id", "target", "class", "pkg_type", "pkg_name", "pkg_path", "installed_version",
		"fixed_version", "vuln_id", "severity", "status", "title", "primary_url", "suppressed", "suppression",
	}
	secretColumns = []string{
		"scan_id", "target", "rule_id", "category", "severity", "title", "start_line", "end_line", "suppressed",
	}
)

// discardConnAbove is the size of a stored report from which the database
// connection that sent it is closed afterwards (see save).
const discardConnAbove = 4 << 20

// save runs the whole upload in one transaction. Uploads for the same GitLab
// project are serialised by an advisory lock, so two uploads of the same job
// and artifact end with exactly one scan and two first uploads create one
// service.
func (s *pgSaver) save(ctx context.Context, in *saveInput) (Result, error) {
	var res Result
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return res, fmt.Errorf("acquire connection: %w", err)
	}
	defer func() {
		if len(in.raw) >= discardConnAbove {
			// The driver keeps the buffer of the largest message it sent for
			// as long as the connection lives, so a few large reports would
			// leave every pooled connection holding as much memory as the
			// biggest report. Closing the connection makes the pool open a
			// fresh one when it is needed.
			_ = conn.Conn().Close(context.WithoutCancel(ctx))
		}
		conn.Release()
	}()
	err = pgx.BeginFunc(ctx, conn, func(tx pgx.Tx) error {
		q := store.New(tx)
		c := in.claims
		if err := q.LockIngestProject(ctx, strconv.FormatInt(c.ProjectID, 10)); err != nil {
			return fmt.Errorf("lock project: %w", err)
		}
		svc, created, err := s.resolveService(ctx, q, c)
		if err != nil {
			return err
		}
		res.ServiceID, res.ServiceCreated = svc.ID, created

		// The scans of this job other than the one this upload replaces. The
		// project lock held here makes the count exact for concurrent uploads
		// of the job, and the unique key of scans makes it an index lookup.
		var others int
		if err := tx.QueryRow(ctx, `SELECT count(*) FROM scans
			WHERE service_id = $1 AND gitlab_job_id = $2 AND NOT (artifact_name = $3 AND kind = $4)`,
			svc.ID, c.JobID, in.report.ArtifactName, in.kind).Scan(&others); err != nil {
			return fmt.Errorf("count scans of the job: %w", err)
		}
		if others >= MaxArtifactsPerJob {
			return errJobArtifacts
		}

		n, err := q.DeleteScanForJob(ctx, store.DeleteScanForJobParams{
			ServiceID: svc.ID, GitlabJobID: c.JobID, ArtifactName: in.report.ArtifactName, Kind: in.kind,
		})
		if err != nil {
			return fmt.Errorf("replace earlier scan: %w", err)
		}
		res.Replaced = n > 0

		rep := in.report
		cn := rep.Counts
		res.ScanID, err = q.InsertScan(ctx, store.InsertScanParams{
			ServiceID:        svc.ID,
			Kind:             in.kind,
			ArtifactName:     rep.ArtifactName,
			ArtifactKey:      in.key,
			GitlabPipelineID: c.PipelineID,
			GitlabJobID:      c.JobID,
			Ref:              c.Ref,
			RefProtected:     c.RefProtected,
			CommitSha:        c.SHA,
			TrivyVersion:     rep.TrivyVersion,
			SchemaVersion:    SupportedSchemaVersion,
			ScannedAt:        pgtype.Timestamptz{Time: in.scanTime, Valid: true},
			CountCritical:    cn.Critical,
			CountHigh:        cn.High,
			CountMedium:      cn.Medium,
			CountLow:         cn.Low,
			CountUnknown:     cn.Unknown,
			CountFixable:     cn.Fixable,
			CountSecrets:     cn.Secrets,
			CountSuppressed:  cn.Suppressed,
			RawReport:        in.raw,
		})
		if err != nil {
			return fmt.Errorf("insert scan: %w", err)
		}

		cp := &copier{ctx: ctx, tx: tx, scanID: res.ScanID, kind: in.kind, name: rep.ArtifactName, key: in.key}
		if err := in.findings(cp); err != nil {
			return fmt.Errorf("insert findings: %w", err)
		}
		if err := cp.flush(); err != nil {
			return fmt.Errorf("insert findings: %w", err)
		}
		res.Counts = SeverityCounts{Critical: cn.Critical, High: cn.High, Medium: cn.Medium, Low: cn.Low, Unknown: cn.Unknown}
		res.Fixable, res.Secrets, res.Suppressed = cn.Fixable, cn.Secrets, cn.Suppressed
		return nil
	})
	return res, err
}

// copyBatch is the number of findings sent to the database in one COPY.
const copyBatch = 2048

// copier is the sink that writes findings into the database in batches, so
// that only one batch is held in memory. All batches belong to the
// transaction of the upload, so a failure anywhere stores nothing.
type copier struct {
	ctx    context.Context
	tx     pgx.Tx
	scanID int64
	// kind, name and key say how vulnerability targets are stored (see
	// vulnTarget).
	kind, name, key string
	vulns           [][]any
	secrets         [][]any
}

func (c *copier) vuln(v *Vuln) error {
	c.vulns = append(c.vulns, []any{c.scanID, vulnTarget(c.kind, c.name, c.key, v.Target), v.Class, v.PkgType, v.PkgName, v.PkgPath, v.InstalledVersion,
		v.FixedVersion, v.VulnID, v.Severity, v.Status, v.Title, v.PrimaryURL, v.Suppressed, v.Suppression})
	if len(c.vulns) >= copyBatch {
		return c.flushVulns()
	}
	return nil
}

func (c *copier) secret(f *Secret) error {
	c.secrets = append(c.secrets, []any{c.scanID, f.Target, f.RuleID, f.Category, f.Severity, f.Title, f.StartLine, f.EndLine, f.Suppressed})
	if len(c.secrets) >= copyBatch {
		return c.flushSecrets()
	}
	return nil
}

func (c *copier) flushVulns() error {
	if len(c.vulns) == 0 {
		return nil
	}
	_, err := c.tx.CopyFrom(c.ctx, pgx.Identifier{"vulnerabilities"}, vulnColumns, pgx.CopyFromRows(c.vulns))
	c.vulns = c.vulns[:0]
	if err != nil {
		return fmt.Errorf("vulnerabilities: %w", err)
	}
	return nil
}

func (c *copier) flushSecrets() error {
	if len(c.secrets) == 0 {
		return nil
	}
	_, err := c.tx.CopyFrom(c.ctx, pgx.Identifier{"secret_findings"}, secretColumns, pgx.CopyFromRows(c.secrets))
	c.secrets = c.secrets[:0]
	if err != nil {
		return fmt.Errorf("secret findings: %w", err)
	}
	return nil
}

func (c *copier) flush() error {
	if err := c.flushVulns(); err != nil {
		return err
	}
	return c.flushSecrets()
}

// resolveService finds the service for the token's project: by project ID;
// else a service an administrator added by the same path (which learns the
// ID); else a new one. A renamed project updates the path of its service.
func (s *pgSaver) resolveService(ctx context.Context, q *store.Queries, c *Claims) (svc store.Service, created bool, err error) {
	projectID := pgtype.Int8{Int64: c.ProjectID, Valid: true}
	svc, err = q.GetServiceByProjectID(ctx, projectID)
	switch {
	case err == nil:
		if svc.Archived {
			return svc, false, errServiceArchived
		}
		if svc.GitlabProjectPath != c.ProjectPath {
			n, err := q.RenameServicePath(ctx, store.RenameServicePathParams{ID: svc.ID, ProjectPath: c.ProjectPath})
			if err != nil {
				return svc, false, fmt.Errorf("rename service: %w", err)
			}
			if n == 0 {
				s.log.Warn("project path not updated: another service uses it",
					"service_id", svc.ID, "stored_path", svc.GitlabProjectPath, "token_path", c.ProjectPath)
			}
		}
		return svc, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return svc, false, fmt.Errorf("find service: %w", err)
	}

	adopted, err := q.AdoptServiceByPath(ctx, store.AdoptServiceByPathParams{ProjectID: projectID, ProjectPath: c.ProjectPath})
	svc = adopted.Service
	switch {
	case err == nil:
		d := map[string]any{
			"id": svc.ID, "gitlab_project_path": svc.GitlabProjectPath, "fields": []string{"gitlab_project_id"},
			"gitlab_project_id": c.ProjectID, "reason": "adopted by the first verified upload",
		}
		if adopted.PreviousPath != svc.GitlabProjectPath {
			// The administrator's spelling was replaced by GitLab's.
			d["fields"] = []string{"gitlab_project_id", "gitlab_project_path"}
			d["old_gitlab_project_path"] = adopted.PreviousPath
		}
		detail, err := json.Marshal(d)
		if err == nil {
			err = q.InsertAudit(ctx, store.InsertAuditParams{Actor: "ci:" + c.ProjectPath, Action: audit.ActionServiceUpdated, Detail: detail})
		}
		if err != nil {
			return svc, false, fmt.Errorf("audit adoption: %w", err)
		}
		return svc, false, nil
	case !errors.Is(err, pgx.ErrNoRows):
		return svc, false, fmt.Errorf("adopt service: %w", err)
	}

	svc, err = q.InsertIngestService(ctx, store.InsertIngestServiceParams{
		Name: lastSegment(c.ProjectPath), ProjectID: projectID, ProjectPath: c.ProjectPath,
	})
	switch {
	case err == nil:
		// Recorded in the transaction of the upload, so a service that is
		// rolled back leaves no record and one that stays always has it.
		detail, err := json.Marshal(map[string]any{
			"id": svc.ID, "gitlab_project_path": svc.GitlabProjectPath,
			"gitlab_project_id": c.ProjectID, "reason": "created by the first verified upload",
		})
		if err == nil {
			err = q.InsertAudit(ctx, store.InsertAuditParams{Actor: "ci:" + c.ProjectPath, Action: audit.ActionServiceCreated, Detail: detail})
		}
		if err != nil {
			return svc, false, fmt.Errorf("audit creation: %w", err)
		}
		return svc, true, nil
	case errors.Is(err, pgx.ErrNoRows):
		// An archived service an administrator added by this path, which was
		// therefore not adopted, is the one case worth naming.
		holder, gerr := q.GetServiceByPath(ctx, c.ProjectPath)
		if gerr == nil && holder.Archived && !holder.GitlabProjectID.Valid {
			return svc, false, errServiceArchived
		}
		return svc, false, errPathTaken
	}
	return svc, false, fmt.Errorf("create service: %w", err)
}

func lastSegment(path string) string {
	if i := strings.LastIndexByte(path, '/'); i >= 0 && i < len(path)-1 {
		return path[i+1:]
	}
	return path
}
