// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
)

const (
	verifyTimeout = 10 * time.Second
	maxClaimLen   = 1024
)

// Claims are the values of a verified GitLab CI/CD ID token that Scanopsis
// uses. Every number stored for an upload is traceable to them.
type Claims struct {
	ProjectID     int64
	ProjectPath   string
	NamespacePath string
	PipelineID    int64
	JobID         int64
	Ref           string
	RefProtected  bool
	SHA           string
}

// tokenError is a failed token check. Its message names the check and is
// safe to show to the caller: it never contains the token or any value from
// it.
type tokenError struct {
	msg string
	// unavailable is set when the check could not be made because GitLab's
	// signing keys could not be fetched, not because the token is bad.
	unavailable bool
	// detail is for the server log only: it may name the GitLab URL or carry
	// the upstream error text, which the caller must not see.
	detail string
}

func (e *tokenError) Error() string { return e.msg }

// verifier checks ID tokens against GitLab's published signing keys. The
// underlying go-oidc verifier is built on first use and again whenever the
// configured GitLab URL changes.
type verifier struct {
	audience string
	client   *http.Client
	now      func() time.Time

	mu  sync.Mutex
	url string
	v   *oidc.IDTokenVerifier
}

func (v *verifier) forURL(gitlabURL string) *oidc.IDTokenVerifier {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.v == nil || v.url != gitlabURL {
		// The key set keeps this context for its HTTP client only, so it
		// must outlive any request.
		ctx := oidc.ClientContext(context.Background(), v.client)
		keys := fetchProbe{oidc.NewRemoteKeySet(ctx, gitlabURL+"/oauth/discovery/keys")}
		v.v = oidc.NewVerifier(gitlabURL, keys, &oidc.Config{ClientID: v.audience, Now: v.now})
		v.url = gitlabURL
	}
	return v.v
}

// verify checks the Authorization header value and returns the token's claims.
func (v *verifier) verify(ctx context.Context, gitlabURL, authorization string) (*Claims, *tokenError) {
	raw, te := bearer(authorization)
	if te != nil {
		return nil, te
	}
	ctx, cancel := context.WithTimeout(ctx, verifyTimeout)
	defer cancel()
	fetchFailed := new(atomic.Bool)
	tok, err := v.forURL(gitlabURL).Verify(context.WithValue(ctx, fetchFailedKey{}, fetchFailed), raw)
	if err != nil {
		if fetchFailed.Load() {
			return nil, &tokenError{unavailable: true, detail: err.Error() + " (" + gitlabURL + "/oauth/discovery/keys)",
				msg: "The signature could not be checked: Scanopsis could not fetch the signing keys from GitLab."}
		}
		return nil, v.explain(err)
	}
	var wire struct {
		ProjectID     flex `json:"project_id"`
		ProjectPath   flex `json:"project_path"`
		NamespacePath flex `json:"namespace_path"`
		PipelineID    flex `json:"pipeline_id"`
		JobID         flex `json:"job_id"`
		Ref           flex `json:"ref"`
		RefProtected  flex `json:"ref_protected"`
		SHA           flex `json:"sha"`
	}
	if err := tok.Claims(&wire); err != nil {
		return nil, &tokenError{msg: "The token claims are not a JSON object of the expected shape."}
	}
	c := &Claims{}
	for _, f := range []struct {
		name string
		in   flex
		out  *int64
	}{
		{"project_id", wire.ProjectID, &c.ProjectID},
		{"pipeline_id", wire.PipelineID, &c.PipelineID},
		{"job_id", wire.JobID, &c.JobID},
	} {
		if f.in == "" {
			return nil, missing(f.name)
		}
		n, err := strconv.ParseInt(string(f.in), 10, 64)
		if err != nil || n <= 0 {
			return nil, invalid(f.name, "a positive integer")
		}
		*f.out = n
	}
	for _, f := range []struct {
		name string
		in   flex
		out  *string
	}{
		{"project_path", wire.ProjectPath, &c.ProjectPath},
		{"namespace_path", wire.NamespacePath, &c.NamespacePath},
		{"sha", wire.SHA, &c.SHA},
	} {
		if f.in == "" {
			return nil, missing(f.name)
		}
		if len(f.in) > maxClaimLen || strings.IndexByte(string(f.in), 0) >= 0 {
			return nil, invalid(f.name, "a string of at most 1024 characters without NUL")
		}
		*f.out = string(f.in)
	}
	// A long ref is clipped by the caller, which logs it.
	if wire.Ref == "" {
		return nil, missing("ref")
	}
	c.Ref = string(wire.Ref)
	if !isCommitSHA(c.SHA) {
		return nil, invalid("sha", "a 40 or 64 character hexadecimal commit hash")
	}
	switch wire.RefProtected {
	case "":
		return nil, missing("ref_protected")
	case "true":
		c.RefProtected = true
	case "false":
	default:
		return nil, invalid("ref_protected", `"true" or "false"`)
	}
	return c, nil
}

// fetchFailedKey carries, per verification, a flag that fetchProbe sets when
// the signing keys could not be fetched. go-oidc reports that failure only as
// text (it does not wrap the error), and other messages can contain values
// from the token, so the failure is marked where it happens instead.
type fetchFailedKey struct{}

// fetchProbe is a key set that marks failures to fetch the keys.
type fetchProbe struct{ inner *oidc.RemoteKeySet }

// VerifySignature implements oidc.KeySet.
func (p fetchProbe) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	payload, err := p.inner.VerifySignature(ctx, jwt)
	// RemoteKeySet's own fetch error, produced before any token content is
	// involved.
	if err != nil && strings.HasPrefix(err.Error(), "fetching keys") {
		if f, ok := ctx.Value(fetchFailedKey{}).(*atomic.Bool); ok {
			f.Store(true)
		}
	}
	return payload, err
}

// explain turns a go-oidc error into a message that names the failed check.
// go-oidc has typed errors only for expiry, so the others are recognised by
// the text of the pinned library version; tests cover every case.
func (v *verifier) explain(err error) *tokenError {
	var expired *oidc.TokenExpiredError
	msg := err.Error()
	switch {
	case errors.As(err, &expired):
		return &tokenError{msg: "The token has expired. ID tokens are valid for the job's timeout; upload from within the job."}
	case strings.Contains(msg, "before the nbf"):
		return &tokenError{msg: "The token is not valid yet (nbf is in the future). Check the clocks of the runner and the server."}
	case strings.HasPrefix(msg, "oidc: malformed jwt"), strings.HasPrefix(msg, "oidc: id token not signed"),
		strings.HasPrefix(msg, "oidc: multiple signatures"), strings.HasPrefix(msg, "oidc: failed to unmarshal claims"):
		return &tokenError{msg: "The token is malformed: it is not a signed JWT (RS256)."}
	case strings.HasPrefix(msg, "failed to verify signature"):
		return &tokenError{msg: "The token signature is not valid for the signing keys of the GitLab configured in Scanopsis."}
	case strings.HasPrefix(msg, "oidc: id token issued by a different provider"):
		return &tokenError{msg: "The token issuer (iss) is not the GitLab URL configured in Scanopsis."}
	case strings.HasPrefix(msg, "oidc: expected audience"):
		return &tokenError{msg: "The token audience (aud) does not contain " + v.audience + ". Set aud to the Scanopsis URL in id_tokens."}
	}
	return &tokenError{msg: "The token could not be verified."}
}

func bearer(h string) (string, *tokenError) {
	if strings.TrimSpace(h) == "" {
		return "", &tokenError{msg: "The Authorization header is missing. Send Authorization: Bearer <ID token>."}
	}
	scheme, tok, ok := strings.Cut(h, " ")
	tok = strings.TrimSpace(tok)
	if !ok || !strings.EqualFold(scheme, "Bearer") || tok == "" || strings.ContainsAny(tok, " \t") {
		return "", &tokenError{msg: "The Authorization header must be: Bearer <ID token>."}
	}
	return tok, nil
}

func missing(claim string) *tokenError {
	return &tokenError{msg: "The token has no " + claim + " claim."}
}

func invalid(claim, want string) *tokenError {
	return &tokenError{msg: fmt.Sprintf("The token claim %s must be %s.", claim, want)}
}

func isCommitSHA(s string) bool {
	if len(s) != 40 && len(s) != 64 {
		return false
	}
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') && (c < 'A' || c > 'F') {
			return false
		}
	}
	return true
}

// flex reads a JSON string, number or boolean as its text. GitLab sends
// every claim used here as a string; accepting the other two makes a
// gateway or a future GitLab that sends numbers or booleans work too.
type flex string

func (f *flex) UnmarshalJSON(b []byte) error {
	s := strings.TrimSpace(string(b))
	switch {
	case s == "null":
		return nil
	case strings.HasPrefix(s, `"`):
		var v string
		if err := json.Unmarshal(b, &v); err != nil {
			return err
		}
		*f = flex(v)
	case s == "true" || s == "false" || isNumber(s):
		*f = flex(s)
	default:
		return fmt.Errorf("claim is not a string, number or boolean")
	}
	return nil
}

func isNumber(s string) bool {
	_, err := strconv.ParseFloat(s, 64)
	return err == nil
}

// namespaceAllowed reports whether namespace equals one of the allowed group
// paths or is below one. GitLab paths are case-insensitive.
func namespaceAllowed(allowed []string, namespace string) bool {
	ns := strings.ToLower(strings.Trim(namespace, "/"))
	for _, g := range allowed {
		g = strings.ToLower(strings.Trim(strings.TrimSpace(g), "/"))
		if g != "" && (ns == g || strings.HasPrefix(ns, g+"/")) {
			return true
		}
	}
	return false
}
