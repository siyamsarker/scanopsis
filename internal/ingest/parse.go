// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package ingest receives Trivy reports uploaded by GitLab CI jobs. A job
// proves its identity with a GitLab CI/CD ID token; the report is parsed,
// counted and stored in one transaction together with the pipeline, job and
// commit the token names.
package ingest

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"
	"unicode/utf8"
)

// Parse errors. ErrInvalidJSON means the body is not valid JSON;
// ErrNotTrivy means it is valid JSON but not a Trivy report this package
// understands.
var (
	ErrInvalidJSON = errors.New("the body is not valid JSON")
	ErrNotTrivy    = errors.New("the JSON is not a Trivy report")
)

// SupportedSchemaVersion is the only Trivy report schema version accepted.
const SupportedSchemaVersion = 2

// Severities, as Trivy spells them. Anything else is stored as UNKNOWN.
const (
	SevCritical = "CRITICAL"
	SevHigh     = "HIGH"
	SevMedium   = "MEDIUM"
	SevLow      = "LOW"
	SevUnknown  = "UNKNOWN"
)

// Bounds on stored strings. A value longer than the bound is cut at a
// character boundary. vuln_id is indexed, and PostgreSQL refuses index rows
// above about 2.7 kB, so it gets a tighter bound than the rest.
const (
	maxFieldLen       = 4096
	maxVulnIDLen      = 256
	maxSuppressionLen = 512
)

// Vuln is one vulnerability finding, as Trivy reported it.
type Vuln struct {
	Target           string
	Class            string
	PkgType          string
	PkgName          string
	PkgPath          string
	InstalledVersion string
	FixedVersion     string
	VulnID           string
	Severity         string
	Status           string
	Title            string
	PrimaryURL       string
	Suppressed       bool
	Suppression      string
}

// Secret is one secret finding: rule and location only. The matched text and
// the code excerpt are never read from the report.
type Secret struct {
	Target     string
	RuleID     string
	Category   string
	Severity   string
	Title      string
	StartLine  int32
	EndLine    int32
	Suppressed bool
}

// Counts are the numbers kept on the scan row.
type Counts struct {
	Critical, High, Medium, Low, Unknown int32
	Fixable                              int32 // unsuppressed vulnerabilities with a fixed version
	Secrets                              int32 // unsuppressed secrets
	Suppressed                           int32 // suppressed vulnerabilities and secrets
}

// Report is the part of a Trivy report that Scanopsis keeps.
type Report struct {
	SchemaVersion int
	TrivyVersion  string
	ArtifactName  string
	CreatedAt     time.Time // zero when the report has none or it is unreadable
	Vulns         []Vuln
	Secrets       []Secret
	Counts        Counts
}

// The wire types list only what is stored. Fields not listed (Match, Code,
// descriptions, CVSS data and so on) are skipped by the decoder and never
// held in memory.
type wireReport struct {
	SchemaVersion *int   `json:"SchemaVersion"`
	CreatedAt     string `json:"CreatedAt"`
	ArtifactName  string `json:"ArtifactName"`
	Trivy         struct{ Version string }
	Results       *[]wireResult `json:"Results"`
}

type wireResult struct {
	Target                       string         `json:"Target"`
	Class                        string         `json:"Class"`
	Type                         string         `json:"Type"`
	Vulnerabilities              []wireFinding  `json:"Vulnerabilities"`
	Secrets                      []wireFinding  `json:"Secrets"`
	ExperimentalModifiedFindings []wireModified `json:"ExperimentalModifiedFindings"`
}

// wireFinding is the union of the vulnerability and secret fields; a
// finding fills the ones it has.
type wireFinding struct {
	VulnerabilityID  string `json:"VulnerabilityID"`
	PkgName          string `json:"PkgName"`
	PkgPath          string `json:"PkgPath"`
	InstalledVersion string `json:"InstalledVersion"`
	FixedVersion     string `json:"FixedVersion"`
	Status           string `json:"Status"`
	Severity         string `json:"Severity"`
	Title            string `json:"Title"`
	PrimaryURL       string `json:"PrimaryURL"`
	RuleID           string `json:"RuleID"`
	Category         string `json:"Category"`
	StartLine        int32  `json:"StartLine"`
	EndLine          int32  `json:"EndLine"`
}

type wireModified struct {
	Type      string      `json:"Type"`
	Status    string      `json:"Status"`
	Statement string      `json:"Statement"`
	Source    string      `json:"Source"`
	Finding   wireFinding `json:"Finding"`
}

// Parse reads one Trivy JSON report (schema version 2) from r. It returns
// ErrInvalidJSON for a body that is not JSON and ErrNotTrivy for JSON that
// is not a supported Trivy report. Any other error is from r itself and is
// returned as is. Parse never reads more than the one JSON value and the
// white space after it; text after it is an ErrInvalidJSON.
func Parse(r io.Reader) (*Report, error) {
	dec := json.NewDecoder(r)
	var w wireReport
	if err := dec.Decode(&w); err != nil {
		return nil, classify(err)
	}
	// Anything but white space after the value makes the body invalid.
	var extra json.RawMessage
	switch err := dec.Decode(&extra); {
	case errors.Is(err, io.EOF):
	case err == nil:
		return nil, fmt.Errorf("%w: unexpected data after the report", ErrInvalidJSON)
	default:
		return nil, classify(err)
	}

	switch {
	case w.SchemaVersion == nil:
		return nil, fmt.Errorf("%w: SchemaVersion is missing", ErrNotTrivy)
	case *w.SchemaVersion != SupportedSchemaVersion:
		return nil, fmt.Errorf("%w: SchemaVersion %d is not supported, only %d is", ErrNotTrivy, *w.SchemaVersion, SupportedSchemaVersion)
	case w.Results == nil && w.ArtifactName == "":
		// A scan that finds nothing has no Results key but still names its
		// artifact; a document with neither is something else.
		return nil, fmt.Errorf("%w: neither Results nor ArtifactName is present", ErrNotTrivy)
	}

	rep := &Report{
		SchemaVersion: *w.SchemaVersion,
		TrivyVersion:  clip(w.Trivy.Version, maxFieldLen),
		ArtifactName:  clip(w.ArtifactName, maxFieldLen),
	}
	if t, err := time.Parse(time.RFC3339Nano, w.CreatedAt); err == nil {
		rep.CreatedAt = t
	}
	if w.Results != nil {
		for i := range *w.Results {
			rep.addResult(&(*w.Results)[i])
		}
	}
	return rep, nil
}

func classify(err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: %v", ErrInvalidJSON, err)
	case errors.Is(err, io.EOF):
		return fmt.Errorf("%w: the body is empty", ErrInvalidJSON)
	case errors.As(err, &typ):
		return fmt.Errorf("%w: %v", ErrNotTrivy, err)
	}
	return err
}

func (rep *Report) addResult(res *wireResult) {
	target := clip(res.Target, maxFieldLen)
	class := clip(res.Class, maxFieldLen)
	pkgType := clip(res.Type, maxFieldLen)
	for i := range res.Vulnerabilities {
		rep.addVuln(&res.Vulnerabilities[i], target, class, pkgType, "")
	}
	for i := range res.Secrets {
		rep.addSecret(&res.Secrets[i], target, false)
	}
	for i := range res.ExperimentalModifiedFindings {
		m := &res.ExperimentalModifiedFindings[i]
		switch m.Type {
		case "vulnerability":
			rep.addVuln(&m.Finding, target, class, pkgType, suppression(m))
		case "secret":
			rep.addSecret(&m.Finding, target, true)
		}
	}
}

func (rep *Report) addVuln(f *wireFinding, target, class, pkgType, suppressed string) {
	v := Vuln{
		Target:           target,
		Class:            class,
		PkgType:          pkgType,
		PkgName:          clip(f.PkgName, maxFieldLen),
		PkgPath:          clip(f.PkgPath, maxFieldLen),
		InstalledVersion: clip(f.InstalledVersion, maxFieldLen),
		FixedVersion:     clip(f.FixedVersion, maxFieldLen),
		VulnID:           clip(f.VulnerabilityID, maxVulnIDLen),
		Severity:         severity(f.Severity),
		Status:           clip(f.Status, maxFieldLen),
		Title:            clip(f.Title, maxFieldLen),
		PrimaryURL:       clip(f.PrimaryURL, maxFieldLen),
		Suppressed:       suppressed != "",
		Suppression:      suppressed,
	}
	rep.Vulns = append(rep.Vulns, v)
	c := &rep.Counts
	if v.Suppressed {
		c.Suppressed++
		return
	}
	switch v.Severity {
	case SevCritical:
		c.Critical++
	case SevHigh:
		c.High++
	case SevMedium:
		c.Medium++
	case SevLow:
		c.Low++
	default:
		c.Unknown++
	}
	if v.FixedVersion != "" {
		c.Fixable++
	}
}

func (rep *Report) addSecret(f *wireFinding, target string, suppressed bool) {
	rep.Secrets = append(rep.Secrets, Secret{
		Target:     target,
		RuleID:     clip(f.RuleID, maxFieldLen),
		Category:   clip(f.Category, maxFieldLen),
		Severity:   severity(f.Severity),
		Title:      clip(f.Title, maxFieldLen),
		StartLine:  f.StartLine,
		EndLine:    f.EndLine,
		Suppressed: suppressed,
	})
	if suppressed {
		rep.Counts.Suppressed++
	} else {
		rep.Counts.Secrets++
	}
}

// suppression describes why Trivy hid a finding: "<Status>: <Statement>
// (<Source>)", leaving out the parts the report does not give. It is never
// empty, because an empty value would mean "not suppressed".
func suppression(m *wireModified) string {
	var b strings.Builder
	status := strings.TrimSpace(m.Status)
	if status == "" {
		status = "suppressed"
	}
	b.WriteString(status)
	if s := strings.TrimSpace(m.Statement); s != "" {
		b.WriteString(": ")
		b.WriteString(s)
	}
	if s := strings.TrimSpace(m.Source); s != "" {
		b.WriteString(" (")
		b.WriteString(s)
		b.WriteString(")")
	}
	return clip(b.String(), maxSuppressionLen)
}

func severity(s string) string {
	switch s {
	case SevCritical, SevHigh, SevMedium, SevLow:
		return s
	}
	return SevUnknown
}

// clip makes s safe to store: PostgreSQL text cannot hold NUL, so each NUL
// becomes U+FFFD, and a value longer than max bytes is cut at a character
// boundary.
func clip(s string, max int) string {
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	if strings.IndexByte(s, 0) >= 0 {
		s = strings.ReplaceAll(s, "\x00", "�")
	}
	return s
}
