// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package findings serves the read API over stored scans: overviews, scan
// lists and diffs, vulnerability and secret lists, trends and the
// cross-service vulnerability search.
//
// The "current state" of a service for a ref is the latest scan per
// (artifact, kind). Every number returned is read from, or summed over,
// scans, so it traces back to a pipeline, job and commit.
package findings

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/siyamsarker/scanopsis/internal/domain"
	"github.com/siyamsarker/scanopsis/internal/httpapi"
	"github.com/siyamsarker/scanopsis/internal/settings"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const maxRefLen = 500

// SettingsSource gives the current settings (the GitLab URL for links).
type SettingsSource interface{ Current() settings.Snapshot }

// Findings holds the handlers of the read API.
type Findings struct {
	q   *store.Queries
	cfg SettingsSource
	log *slog.Logger
}

// New returns the read API backed by pool.
func New(pool *pgxpool.Pool, cfg SettingsSource, log *slog.Logger) *Findings {
	return &Findings{q: store.New(pool), cfg: cfg, log: log}
}

// Handlers returns the handlers keyed by OpenAPI operation ID. The router
// is responsible for authentication.
func (f *Findings) Handlers() map[string]http.Handler {
	return map[string]http.Handler{
		"getServiceOverview":         http.HandlerFunc(f.overview),
		"listServiceScans":           http.HandlerFunc(f.listScans),
		"getScan":                    http.HandlerFunc(f.getScan),
		"getScanDiff":                http.HandlerFunc(f.scanDiff),
		"listServiceVulnerabilities": http.HandlerFunc(f.listVulnerabilities),
		"listServiceSecrets":         http.HandlerFunc(f.listSecrets),
		"getServiceTrends":           http.HandlerFunc(f.trends),
		"searchVulnerabilities":      http.HandlerFunc(f.search),
	}
}

// JSON shapes, as in api/openapi.yaml.

type countsJSON struct {
	Critical int `json:"critical"`
	High     int `json:"high"`
	Medium   int `json:"medium"`
	Low      int `json:"low"`
	Unknown  int `json:"unknown"`
}

func (c countsJSON) add(o countsJSON) countsJSON {
	return countsJSON{c.Critical + o.Critical, c.High + o.High, c.Medium + o.Medium, c.Low + o.Low, c.Unknown + o.Unknown}
}

func fromDomain(c domain.SeverityCounts) countsJSON { return countsJSON(c) }

type serviceJSON struct {
	ID                int64     `json:"id"`
	Name              string    `json:"name"`
	GitlabProjectID   *int64    `json:"gitlab_project_id"`
	GitlabProjectPath string    `json:"gitlab_project_path"`
	SonarProjectKey   *string   `json:"sonar_project_key"`
	CreatedVia        string    `json:"created_via"`
	Archived          bool      `json:"archived"`
	CreatedAt         time.Time `json:"created_at"`
}

func newServiceJSON(id int64, name string, projectID pgtype.Int8, path string, sonarKey pgtype.Text,
	createdVia string, archived bool, createdAt pgtype.Timestamptz) serviceJSON {
	s := serviceJSON{
		ID: id, Name: name, GitlabProjectPath: path, CreatedVia: createdVia,
		Archived: archived, CreatedAt: createdAt.Time,
	}
	if projectID.Valid {
		s.GitlabProjectID = &projectID.Int64
	}
	if sonarKey.Valid {
		s.SonarProjectKey = &sonarKey.String
	}
	return s
}

// scanRow is the column set shared by every query that returns scans.
type scanRow = store.CurrentScansRow

type scanSummaryJSON struct {
	ID               int64      `json:"id"`
	ServiceID        int64      `json:"service_id"`
	Kind             string     `json:"kind"`
	ArtifactName     string     `json:"artifact_name"`
	GitlabPipelineID int64      `json:"gitlab_pipeline_id"`
	GitlabJobID      int64      `json:"gitlab_job_id"`
	Ref              string     `json:"ref"`
	RefProtected     bool       `json:"ref_protected"`
	CommitSha        string     `json:"commit_sha"`
	TrivyVersion     string     `json:"trivy_version"`
	ScannedAt        time.Time  `json:"scanned_at"`
	ReceivedAt       time.Time  `json:"received_at"`
	Counts           countsJSON `json:"counts"`
	Fixable          int        `json:"fixable"`
	Secrets          int        `json:"secrets"`
	Suppressed       int        `json:"suppressed"`
}

func newScanSummary(r scanRow) scanSummaryJSON {
	return scanSummaryJSON{
		ID: r.ID, ServiceID: r.ServiceID, Kind: r.Kind, ArtifactName: r.ArtifactName,
		GitlabPipelineID: r.GitlabPipelineID, GitlabJobID: r.GitlabJobID, Ref: r.Ref,
		RefProtected: r.RefProtected, CommitSha: r.CommitSha, TrivyVersion: r.TrivyVersion,
		ScannedAt: r.ScannedAt.Time, ReceivedAt: r.ReceivedAt.Time,
		Counts: countsJSON{
			Critical: int(r.CountCritical), High: int(r.CountHigh), Medium: int(r.CountMedium),
			Low: int(r.CountLow), Unknown: int(r.CountUnknown),
		},
		Fixable: int(r.CountFixable), Secrets: int(r.CountSecrets), Suppressed: int(r.CountSuppressed),
	}
}

// Request helpers.

func (f *Findings) internalError(w http.ResponseWriter, r *http.Request, msg string, err error) {
	f.log.ErrorContext(r.Context(), msg, "error", err)
	httpapi.WriteError(w, http.StatusInternalServerError, "internal_error", "Something went wrong. Try again later.")
}

func notFound(w http.ResponseWriter, what string) {
	httpapi.WriteError(w, http.StatusNotFound, "not_found", "No such "+what+".")
}

func invalid(w http.ResponseWriter, msg string) {
	httpapi.WriteError(w, http.StatusBadRequest, "invalid_parameter", msg)
}

// pathID parses the {id} path value; a malformed or non-positive id names
// nothing, so it is reported as not found.
func pathID(r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	return id, err == nil && id >= 1
}

// intParam reads an optional integer query parameter within [lo, hi].
func intParam(r *http.Request, name string, def, lo, hi int64) (int64, error) {
	v := r.URL.Query().Get(name)
	if v == "" {
		return def, nil
	}
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil || n < lo || n > hi {
		return 0, errors.New("The " + name + " parameter must be an integer from " +
			strconv.FormatInt(lo, 10) + " to " + strconv.FormatInt(hi, 10) + ".")
	}
	return n, nil
}

// boolParam reads an optional boolean query parameter ("true" or "false").
func boolParam(r *http.Request, name string) (value, present bool, err error) {
	switch v := r.URL.Query().Get(name); v {
	case "":
		return false, false, nil
	case "true":
		return true, true, nil
	case "false":
		return false, true, nil
	}
	return false, false, errors.New("The " + name + " parameter must be true or false.")
}

// service loads the service named by the path, writing the response and
// returning false when it is unknown or the lookup fails.
func (f *Findings) service(w http.ResponseWriter, r *http.Request) (store.Service, bool) {
	id, ok := pathID(r)
	if !ok {
		notFound(w, "service")
		return store.Service{}, false
	}
	svc, err := f.q.FindingsGetService(r.Context(), id)
	if errors.Is(err, pgx.ErrNoRows) {
		notFound(w, "service")
		return svc, false
	}
	if err != nil {
		f.internalError(w, r, "cannot load service", err)
		return svc, false
	}
	return svc, true
}

// refParam returns the ref query parameter, "" when absent. A ref that is
// too long cannot exist and is reported as invalid.
func refParam(w http.ResponseWriter, r *http.Request) (string, bool) {
	ref := r.URL.Query().Get("ref")
	if len(ref) > maxRefLen {
		invalid(w, "The ref parameter is too long.")
		return "", false
	}
	return ref, true
}

// refs lists the refs of a service with scans, most recent first.
func (f *Findings) refs(ctx context.Context, serviceID int64) ([]string, error) {
	rows, err := f.q.ListRefs(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	refs := make([]string, len(rows))
	for i, r := range rows {
		refs[i] = r.Ref
	}
	return refs, nil
}

// defaultRef returns the ref to use: the requested one, else the ref with the
// most recent scan, else "" when the service has none.
func (f *Findings) defaultRef(ctx context.Context, serviceID int64, requested string) (string, error) {
	if requested != "" {
		return requested, nil
	}
	refs, err := f.refs(ctx, serviceID)
	if err != nil || len(refs) == 0 {
		return "", err
	}
	return refs[0], nil
}

// currentScans returns the current scans of a ref and their ids.
func (f *Findings) currentScans(ctx context.Context, serviceID int64, ref string) ([]scanRow, []int64, error) {
	if ref == "" {
		return nil, nil, nil
	}
	rows, err := f.q.CurrentScans(ctx, store.CurrentScansParams{ServiceID: serviceID, Ref: ref})
	if err != nil {
		return nil, nil, err
	}
	ids := make([]int64, len(rows))
	for i, r := range rows {
		ids[i] = r.ID
	}
	return rows, ids, nil
}

// gitlabLink joins the configured GitLab URL, the project path and a suffix;
// it is nil when no GitLab URL is configured.
// gitlabLink builds <base>/<project path>/-/<kind>/<id>. Every path segment
// is escaped, so a project path or ID can never change the link's host,
// query or fragment. It returns nil when no GitLab URL is configured or id
// is empty.
func gitlabLink(base, projectPath, kind, id string) *string {
	base = strings.TrimRight(base, "/")
	if base == "" || projectPath == "" || id == "" {
		return nil
	}
	var b strings.Builder
	b.WriteString(base)
	for _, seg := range strings.Split(projectPath, "/") {
		b.WriteString("/")
		b.WriteString(url.PathEscape(seg))
	}
	b.WriteString("/-/")
	b.WriteString(kind)
	b.WriteString("/")
	b.WriteString(url.PathEscape(id))
	u := b.String()
	return &u
}

// commitID returns sha when it is a hexadecimal Git object ID, else "".
func commitID(sha string) string {
	if len(sha) < 7 || len(sha) > 64 {
		return ""
	}
	for _, c := range sha {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return ""
		}
	}
	return sha
}

const invalidSeverityMsg = "The severity parameter must be a comma-separated list of CRITICAL, HIGH, MEDIUM, LOW and UNKNOWN."
