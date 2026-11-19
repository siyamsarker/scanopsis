// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Package ingest receives Trivy reports uploaded by GitLab CI jobs. A job
// proves its identity with a GitLab CI/CD ID token; the report is parsed,
// counted and stored in one transaction together with the pipeline, job and
// commit the token names.
package ingest

import (
	"bufio"
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
	// ErrTooManyFindings means the report holds more than MaxFindings
	// vulnerabilities and secrets, suppressed ones included.
	ErrTooManyFindings = errors.New("the report has too many findings")
)

// MaxFindings is the most vulnerabilities and secrets (suppressed ones
// included) one report may hold. The byte limit alone does not bound memory:
// a report of tiny findings holds many more of them per megabyte.
const MaxFindings = 200_000

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
	maxArtifactLen    = 512 // indexed (scans_current_idx)
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
// descriptions, CVSS data and so on) are skipped token by token while reading
// (see skipValue) and are never held in memory, however large they are.
type wireResult struct {
	Target          string
	Class           string
	Type            string
	Vulnerabilities []wireFinding
	Secrets         []wireFinding
	Modified        []wireModified
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
// ErrInvalidJSON for a body that is not JSON, ErrNotTrivy for JSON that is
// not a supported Trivy report and ErrTooManyFindings for a report with more
// than MaxFindings vulnerabilities and secrets. Any other error is from r
// itself and is returned as is. Text after the JSON value is an
// ErrInvalidJSON.
func Parse(r io.Reader) (*Report, error) { return parse(r, MaxFindings) }

// parser reads the report with the decoder's token API, one result and one
// finding at a time, so only the normalised findings are kept and the
// finding cap is enforced while reading. Type errors (valid JSON of the
// wrong shape) are remembered and reported after the whole value was read,
// so that a syntax error anywhere in the body wins over them.
type parser struct {
	dec     *json.Decoder
	rep     *Report
	max     int
	typeErr error
}

func parse(r io.Reader, maxFindings int) (*Report, error) {
	br := bufio.NewReader(r)
	for {
		b, err := br.ReadByte()
		if err != nil {
			return nil, classify(err)
		}
		if b == ' ' || b == '\t' || b == '\n' || b == '\r' {
			continue
		}
		_ = br.UnreadByte()
		if b != '{' {
			// Not an object: check it is JSON at all, then it is not a report.
			var v json.RawMessage
			if err := json.NewDecoder(br).Decode(&v); err != nil {
				return nil, classify(err)
			}
			return nil, fmt.Errorf("%w: the JSON value is not an object", ErrNotTrivy)
		}
		break
	}
	p := &parser{dec: json.NewDecoder(br), rep: &Report{}, max: maxFindings}
	var (
		schema     *int
		haveResult bool
		created    string
	)
	if _, err := p.dec.Token(); err != nil { // {
		return nil, classify(err)
	}
	for p.dec.More() {
		key, err := p.key()
		if err != nil {
			return nil, err
		}
		switch strings.ToLower(key) {
		case "schemaversion":
			err = p.value(&schema)
		case "createdat":
			err = p.value(&created)
		case "artifactname":
			var s string
			if err = p.value(&s); err == nil {
				p.rep.ArtifactName = clip(s, maxArtifactLen)
			}
		case "trivy":
			var t struct{ Version string }
			if err = p.value(&t); err == nil {
				p.rep.TrivyVersion = clip(t.Version, maxFieldLen)
			}
		case "results":
			err = p.results(&haveResult)
		default:
			err = p.skipValue()
		}
		if err != nil {
			return nil, err
		}
	}
	if _, err := p.dec.Token(); err != nil { // }
		return nil, classify(err)
	}
	// Anything but white space after the value makes the body invalid.
	switch _, err := p.dec.Token(); {
	case errors.Is(err, io.EOF):
	case err == nil:
		return nil, fmt.Errorf("%w: unexpected data after the report", ErrInvalidJSON)
	default:
		return nil, classify(err)
	}

	switch {
	case p.typeErr != nil:
		return nil, fmt.Errorf("%w: %w", ErrNotTrivy, p.typeErr)
	case schema == nil:
		return nil, fmt.Errorf("%w: SchemaVersion is missing", ErrNotTrivy)
	case *schema != SupportedSchemaVersion:
		return nil, fmt.Errorf("%w: SchemaVersion %d is not supported, only %d is", ErrNotTrivy, *schema, SupportedSchemaVersion)
	case !haveResult && p.rep.ArtifactName == "":
		// A scan that finds nothing has no Results key but still names its
		// artifact; a document with neither is something else.
		return nil, fmt.Errorf("%w: neither Results nor ArtifactName is present", ErrNotTrivy)
	}
	p.rep.SchemaVersion = *schema
	if t, err := time.Parse(time.RFC3339Nano, created); err == nil {
		p.rep.CreatedAt = t
	}
	return p.rep, nil
}

// key reads an object key.
func (p *parser) key() (string, error) {
	tok, err := p.dec.Token()
	if err != nil {
		return "", classify(err)
	}
	s, ok := tok.(string)
	if !ok {
		return "", fmt.Errorf("%w: unexpected token", ErrInvalidJSON)
	}
	return s, nil
}

// value decodes the next value into v. A type error is remembered, not
// returned: the value has been consumed and reading goes on.
func (p *parser) value(v any) error {
	err := p.dec.Decode(v)
	var typ *json.UnmarshalTypeError
	if errors.As(err, &typ) {
		if p.typeErr == nil {
			p.typeErr = err
		}
		return nil
	}
	return classify(err)
}

// array calls each for every element of the next value, which must be an
// array; null is an empty array.
func (p *parser) array(each func() error) error {
	tok, err := p.dec.Token()
	if err != nil {
		return classify(err)
	}
	switch d := tok.(type) {
	case nil:
		return nil
	case json.Delim:
		if d != '[' {
			p.wrongShape("a list")
			return p.skip()
		}
		for p.dec.More() {
			if err := each(); err != nil {
				return err
			}
		}
		_, err := p.dec.Token() // ]
		return classify(err)
	default:
		p.wrongShape("a list")
		return nil
	}
}

func (p *parser) wrongShape(want string) {
	if p.typeErr == nil {
		p.typeErr = fmt.Errorf("a JSON value where %s was expected", want)
	}
}

// skipValue reads the next value and drops it. Scalars are dropped as they
// are read; an object or array is read token by token to its end, so a large
// unknown value is never held in memory.
func (p *parser) skipValue() error {
	tok, err := p.dec.Token()
	if err != nil {
		return classify(err)
	}
	if _, ok := tok.(json.Delim); ok {
		return p.skip()
	}
	return nil
}

// skip reads to the end of the object or array whose opening delimiter was
// just read.
func (p *parser) skip() error {
	for depth := 1; depth > 0; {
		tok, err := p.dec.Token()
		if err != nil {
			return classify(err)
		}
		if d, ok := tok.(json.Delim); ok {
			if d == '[' || d == '{' {
				depth++
			} else {
				depth--
			}
		}
	}
	return nil
}

func (p *parser) results(have *bool) error {
	*have = true
	return p.array(p.result)
}

func (p *parser) result() error {
	tok, err := p.dec.Token()
	if err != nil {
		return classify(err)
	}
	d, isDelim := tok.(json.Delim)
	switch {
	case tok == nil:
		return nil
	case !isDelim:
		p.wrongShape("an object")
		return nil
	case d == '[':
		p.wrongShape("an object")
		return p.skip()
	}
	var res wireResult
	pending := 0 // findings held in res, counted towards the cap
	add := func() error {
		pending++
		if len(p.rep.Vulns)+len(p.rep.Secrets)+pending > p.max {
			return ErrTooManyFindings
		}
		return nil
	}
	for p.dec.More() {
		key, err := p.key()
		if err != nil {
			return err
		}
		switch strings.ToLower(key) {
		case "target":
			err = p.value(&res.Target)
		case "class":
			err = p.value(&res.Class)
		case "type":
			err = p.value(&res.Type)
		case "vulnerabilities":
			err = p.array(func() error {
				var f wireFinding
				if err := p.value(&f); err != nil {
					return err
				}
				res.Vulnerabilities = append(res.Vulnerabilities, f)
				return add()
			})
		case "secrets":
			err = p.array(func() error {
				var f wireFinding
				if err := p.value(&f); err != nil {
					return err
				}
				res.Secrets = append(res.Secrets, f)
				return add()
			})
		case "experimentalmodifiedfindings":
			err = p.array(func() error {
				var m wireModified
				if err := p.value(&m); err != nil {
					return err
				}
				if m.Type != "vulnerability" && m.Type != "secret" {
					return nil
				}
				res.Modified = append(res.Modified, m)
				return add()
			})
		default:
			err = p.skipValue()
		}
		if err != nil {
			return err
		}
	}
	if _, err := p.dec.Token(); err != nil { // }
		return classify(err)
	}
	p.rep.addResult(&res)
	return nil
}

func classify(err error) error {
	var syn *json.SyntaxError
	var typ *json.UnmarshalTypeError
	switch {
	case err == nil:
		return nil
	case errors.As(err, &syn), errors.Is(err, io.ErrUnexpectedEOF):
		return fmt.Errorf("%w: %w", ErrInvalidJSON, err)
	case errors.Is(err, io.EOF):
		return fmt.Errorf("%w: the body is empty or ends early", ErrInvalidJSON)
	case errors.As(err, &typ):
		return fmt.Errorf("%w: %w", ErrNotTrivy, err)
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
	for i := range res.Modified {
		m := &res.Modified[i]
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

// clip makes s safe to store: NUL, which PostgreSQL text cannot hold, and the
// other C0 control characters except tab, line feed and carriage return are
// replaced by U+FFFD first, and then a value longer than max bytes is cut at a
// character boundary, so the result never exceeds max.
func clip(s string, max int) string {
	for i := 0; i < len(s); i++ {
		if c := s[i]; c < 0x20 && c != '\t' && c != '\n' && c != '\r' {
			s = strings.Map(func(r rune) rune {
				if r < 0x20 && r != '\t' && r != '\n' && r != '\r' {
					return '\uFFFD'
				}
				return r
			}, s)
			break
		}
	}
	if len(s) > max {
		cut := max
		for cut > 0 && !utf8.RuneStart(s[cut]) {
			cut--
		}
		s = s[:cut]
	}
	return s
}
