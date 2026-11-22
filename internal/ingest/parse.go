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

// total is the number of findings counted, suppressed ones included.
func (c Counts) total() int {
	return int(c.Critical + c.High + c.Medium + c.Low + c.Unknown + c.Secrets + c.Suppressed)
}

// Report is the part of a Trivy report that Scanopsis keeps.
type Report struct {
	SchemaVersion int
	TrivyVersion  string
	ArtifactName  string
	CreatedAt     time.Time // zero when the report has none or it is unreadable
	// Vulns and Secrets are filled by Parse only. The upload path reads a
	// report with parseTo and never holds its findings.
	Vulns   []Vuln
	Secrets []Secret
	Counts  Counts
	// ordered is set when every result named its Target, Class and Type
	// before its findings and listed them as vulnerabilities, secrets and
	// modified findings, in that order, as Trivy writes them. Findings of such
	// a report can be handed on as they are read (see parseTo).
	ordered bool
}

// sink receives the findings of a report, normalised, in the order Parse
// returns them. A sink error stops the parse and is returned as it is.
type sink interface {
	vuln(*Vuln) error
	secret(*Secret) error
}

// nopSink drops findings: the parse then only validates and counts.
type nopSink struct{}

func (nopSink) vuln(*Vuln) error     { return nil }
func (nopSink) secret(*Secret) error { return nil }

// collectSink keeps every finding, as Parse returns them.
type collectSink struct{ rep *Report }

func (c collectSink) vuln(v *Vuln) error {
	c.rep.Vulns = append(c.rep.Vulns, *v)
	return nil
}

func (c collectSink) secret(f *Secret) error {
	c.rep.Secrets = append(c.rep.Secrets, *f)
	return nil
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
func Parse(r io.Reader) (*Report, error) {
	rep := &Report{}
	return parseTo(r, MaxFindings, collectSink{rep}, true, rep)
}

// parser reads the report with the decoder's token API, one result and one
// finding at a time, so only the normalised findings are kept and the
// finding cap is enforced while reading. Type errors (valid JSON of the
// wrong shape) are remembered and reported after the whole value was read,
// so that a syntax error anywhere in the body wins over them.
type parser struct {
	dec     *json.Decoder
	rep     *Report
	sink    sink
	max     int
	n       int  // findings read so far, counted towards max
	buffer  bool // hold the findings of a result until it ends
	ordered bool
	typeErr error
}

// parseTo reads one report and hands each finding to s as soon as it is read,
// so a report of any size is read in a fixed amount of memory. rep receives
// the rest (artifact, versions, time and counts). buffer holds the findings of
// one result until the result ends instead: only then are they normalised in
// the fixed order of Parse, whatever the order of the keys in the result.
// Without it a finding is normalised with the Target, Class and Type read
// before it, which is only right when rep.ordered is set after a first pass
// over the same body. A caller that streams therefore parses twice: once
// with a nopSink to validate and count and to learn rep.ordered, and again
// to feed the real sink with buffer set to !rep.ordered.
func parseTo(r io.Reader, maxFindings int, s sink, buffer bool, rep *Report) (*Report, error) {
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
	p := &parser{dec: json.NewDecoder(br), rep: rep, sink: s, max: maxFindings, buffer: buffer, ordered: true}
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
	p.rep.ordered = p.ordered
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
	// header is the clipped Target, Class and Type, made when the first
	// finding of the result is handled and again after a later change.
	var header struct {
		target, class, pkgType string
		valid                  bool
	}
	head := func() {
		if !header.valid {
			header.target = clip(res.Target, maxFieldLen)
			header.class = clip(res.Class, maxFieldLen)
			header.pkgType = clip(res.Type, maxFieldLen)
			header.valid = true
		}
	}
	// phase is the last list of findings seen: 0 none, 1 vulnerabilities,
	// 2 secrets, 3 modified findings. A header key after a list, or a list
	// out of that order, makes the report not ordered.
	phase := 0
	list := func(n int) {
		if phase > n {
			p.ordered = false
		}
		phase = max(phase, n)
	}
	headerKey := func() {
		if phase > 0 {
			p.ordered = false
		}
		header.valid = false
	}
	count := func() error {
		p.n++
		if p.n > p.max {
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
			headerKey()
			err = p.value(&res.Target)
		case "class":
			headerKey()
			err = p.value(&res.Class)
		case "type":
			headerKey()
			err = p.value(&res.Type)
		case "vulnerabilities":
			list(1)
			err = p.array(func() error {
				var f wireFinding
				if err := p.value(&f); err != nil {
					return err
				}
				if err := count(); err != nil {
					return err
				}
				if p.buffer {
					res.Vulnerabilities = append(res.Vulnerabilities, f)
					return nil
				}
				head()
				return p.addVuln(&f, header.target, header.class, header.pkgType, "")
			})
		case "secrets":
			list(2)
			err = p.array(func() error {
				var f wireFinding
				if err := p.value(&f); err != nil {
					return err
				}
				if err := count(); err != nil {
					return err
				}
				if p.buffer {
					res.Secrets = append(res.Secrets, f)
					return nil
				}
				head()
				return p.addSecret(&f, header.target, false)
			})
		case "experimentalmodifiedfindings":
			list(3)
			err = p.array(func() error {
				var m wireModified
				if err := p.value(&m); err != nil {
					return err
				}
				if m.Type != "vulnerability" && m.Type != "secret" {
					return nil
				}
				if err := count(); err != nil {
					return err
				}
				if p.buffer {
					res.Modified = append(res.Modified, m)
					return nil
				}
				head()
				return p.addModified(&m, header.target, header.class, header.pkgType)
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
	if p.buffer {
		return p.addResult(&res)
	}
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

// addResult normalises the findings held for one result, in the fixed order:
// vulnerabilities, secrets, modified findings.
func (p *parser) addResult(res *wireResult) error {
	target := clip(res.Target, maxFieldLen)
	class := clip(res.Class, maxFieldLen)
	pkgType := clip(res.Type, maxFieldLen)
	for i := range res.Vulnerabilities {
		if err := p.addVuln(&res.Vulnerabilities[i], target, class, pkgType, ""); err != nil {
			return err
		}
	}
	for i := range res.Secrets {
		if err := p.addSecret(&res.Secrets[i], target, false); err != nil {
			return err
		}
	}
	for i := range res.Modified {
		if err := p.addModified(&res.Modified[i], target, class, pkgType); err != nil {
			return err
		}
	}
	return nil
}

func (p *parser) addModified(m *wireModified, target, class, pkgType string) error {
	if m.Type == "vulnerability" {
		return p.addVuln(&m.Finding, target, class, pkgType, suppression(m))
	}
	return p.addSecret(&m.Finding, target, true)
}

// addVuln normalises a vulnerability, counts it and hands it to the sink.
func (p *parser) addVuln(f *wireFinding, target, class, pkgType, suppressed string) error {
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
	c := &p.rep.Counts
	switch {
	case v.Suppressed:
		c.Suppressed++
	default:
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
	return p.sink.vuln(&v)
}

// addSecret normalises a secret finding, counts it and hands it to the sink.
func (p *parser) addSecret(f *wireFinding, target string, suppressed bool) error {
	if suppressed {
		p.rep.Counts.Suppressed++
	} else {
		p.rep.Counts.Secrets++
	}
	return p.sink.secret(&Secret{
		Target:     target,
		RuleID:     clip(f.RuleID, maxFieldLen),
		Category:   clip(f.Category, maxFieldLen),
		Severity:   severity(f.Severity),
		Title:      clip(f.Title, maxFieldLen),
		StartLine:  f.StartLine,
		EndLine:    f.EndLine,
		Suppressed: suppressed,
	})
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
