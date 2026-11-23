// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"github.com/go-jose/go-jose/v4"
)

const (
	verifyTimeout = 10 * time.Second
	maxClaimLen   = 1024
	// clockLeeway is how far in the future the nbf and iat of a token may be,
	// for clocks that differ a little. go-oidc checks exp without leeway,
	// nbf with five minutes and iat not at all; a token of a job that
	// started is never meant to be valid in the future.
	clockLeeway = 10 * time.Second
	// keyRefetchInterval is the shortest time between two fetches of GitLab's
	// signing keys. Every token with a key ID that is not in the cached set
	// would otherwise make Scanopsis fetch them, so one forged token per
	// request would turn into a request to GitLab per upload.
	keyRefetchInterval = 10 * time.Second
	// maxKeySetBytes bounds the key set read from GitLab.
	maxKeySetBytes = 1 << 20
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
	// timeout bounds a verification and a fetch of the keys. 0 means
	// verifyTimeout.
	timeout time.Duration

	mu  sync.Mutex
	url string
	v   *oidc.IDTokenVerifier
}

func (v *verifier) forURL(gitlabURL string) *oidc.IDTokenVerifier {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.v == nil || v.url != gitlabURL {
		keys := &keySet{url: gitlabURL + "/oauth/discovery/keys", client: v.client, now: v.now, timeout: v.timeout}
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
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(v.timeout, verifyTimeout))
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
		NBF           flex `json:"nbf"`
		IAT           flex `json:"iat"`
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
	// go-oidc accepts an nbf up to five minutes ahead and ignores iat. A token
	// that is not valid yet, or was issued in the future, is refused with a
	// small leeway for the clocks.
	for _, f := range []struct {
		in  flex
		msg string
	}{
		{wire.NBF, "The token is not valid yet (nbf is in the future). Check that the clocks of GitLab and the Scanopsis server agree (NTP)."},
		{wire.IAT, "The token was issued in the future (iat is ahead of the clock). Check that the clocks of GitLab and the Scanopsis server agree (NTP)."},
	} {
		if f.in == "" {
			continue
		}
		secs, err := strconv.ParseFloat(string(f.in), 64)
		if err != nil {
			return nil, &tokenError{msg: "The token claims nbf and iat must be numeric dates."}
		}
		if v.now().Add(clockLeeway).Before(time.Unix(int64(secs), 0)) {
			return nil, &tokenError{msg: f.msg}
		}
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

// fetchFailedKey carries, per verification, a flag that the key set sets when
// the signing keys could not be fetched. go-oidc reports that failure only as
// text (it does not wrap the error), and other messages can contain values
// from the token, so the failure is marked where it happens instead.
type fetchFailedKey struct{}

// keySet is the signing keys of one GitLab, fetched from its key URL. It is
// go-oidc's key set with these changes: it fetches at most once in
// keyRefetchInterval, it never fetches for a token whose key ID it already
// has (that token is wrong, not the cache), and all requests that need a
// fetch at the same time share one.
//
//   - A key ID that is in the cached set and a signature that does not verify
//     is a forged or damaged token. Fetching again cannot change that.
//   - A key ID that is not in the set may be a new key (GitLab rotated), so
//     the keys are fetched, but only if the last fetch ended more than the
//     interval ago. Inside it the token is answered like any with a bad
//     signature.
//   - Every failed fetch (timeout, transport error, bad status, bad body) is
//     remembered for the interval, so a GitLab that is down or hangs is asked
//     once, not once per request, and the requests that wait for that fetch
//     all get its answer when it ends. The cached keys stay in use.
type keySet struct {
	url     string
	client  *http.Client
	now     func() time.Time
	timeout time.Duration // of one fetch; 0 means verifyTimeout

	mu        sync.Mutex
	keys      []jose.JSONWebKey
	fetchedAt time.Time // when the last fetch ended, successful or not; zero before the first
	fetchErr  error     // what the last fetch returned
	flight    *keyFetch // the fetch in progress, if any
}

// keyFetch is one fetch of the key set, which every request that needs the
// keys while it runs waits for. keys and err are set before done is closed.
type keyFetch struct {
	done chan struct{}
	keys []jose.JSONWebKey
	err  error
}

var errBadSignature = errors.New("failed to verify id token signature")

// VerifySignature implements oidc.KeySet.
func (k *keySet) VerifySignature(ctx context.Context, jwt string) ([]byte, error) {
	jws, err := jose.ParseSignedCompact(jwt, []jose.SignatureAlgorithm{jose.RS256})
	if err != nil {
		return nil, fmt.Errorf("oidc: malformed jwt: %w", err)
	}
	kid := ""
	if len(jws.Signatures) > 0 {
		kid = jws.Signatures[0].Header.KeyID
	}

	k.mu.Lock()
	if payload, ok := verifyWithKeys(k.keys, kid, jws); ok {
		k.mu.Unlock()
		return payload, nil
	}
	if kid != "" && hasKey(k.keys, kid) {
		k.mu.Unlock()
		return nil, errBadSignature
	}
	f := k.flight
	if f == nil {
		if !k.fetchedAt.IsZero() && k.now().Sub(k.fetchedAt) < keyRefetchInterval {
			err := k.fetchErr
			k.mu.Unlock()
			if err != nil {
				markFetchFailed(ctx)
				return nil, fmt.Errorf("fetching keys: %w", err)
			}
			return nil, errBadSignature
		}
		f = &keyFetch{done: make(chan struct{})}
		k.flight = f
		go k.run(context.WithoutCancel(ctx), f)
	}
	k.mu.Unlock()

	select {
	case <-f.done:
	case <-ctx.Done():
		markFetchFailed(ctx)
		return nil, fmt.Errorf("fetching keys: %w", ctx.Err())
	}
	if f.err != nil {
		markFetchFailed(ctx)
		return nil, fmt.Errorf("fetching keys: %w", f.err)
	}
	if payload, ok := verifyWithKeys(f.keys, kid, jws); ok {
		return payload, nil
	}
	return nil, errBadSignature
}

// run fetches the key set for f and publishes the outcome. It runs in its own
// goroutine, bounded by the fetch timeout, so no request's cancellation ends it
// and a request that gives up does not stop the others from getting the keys.
func (k *keySet) run(ctx context.Context, f *keyFetch) {
	keys, err := k.fetch(ctx)
	k.mu.Lock()
	k.fetchedAt = k.now()
	k.fetchErr = err
	if err == nil {
		k.keys = keys
	}
	k.flight = nil
	k.mu.Unlock()
	f.keys, f.err = keys, err
	close(f.done)
}

func markFetchFailed(ctx context.Context) {
	if f, ok := ctx.Value(fetchFailedKey{}).(*atomic.Bool); ok {
		f.Store(true)
	}
}

// verifyWithKeys checks the signature with every key that has the key ID, or
// every key when the token has none.
func verifyWithKeys(keys []jose.JSONWebKey, kid string, jws *jose.JSONWebSignature) ([]byte, bool) {
	for i := range keys {
		if kid != "" && keys[i].KeyID != kid {
			continue
		}
		if payload, err := jws.Verify(&keys[i]); err == nil {
			return payload, true
		}
	}
	return nil, false
}

func hasKey(keys []jose.JSONWebKey, kid string) bool {
	for i := range keys {
		if keys[i].KeyID == kid {
			return true
		}
	}
	return false
}

// fetch reads the key set, in at most the fetch timeout.
func (k *keySet) fetch(ctx context.Context) ([]jose.JSONWebKey, error) {
	ctx, cancel := context.WithTimeout(ctx, cmp.Or(k.timeout, verifyTimeout))
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, k.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := k.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxKeySetBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading the key set: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unable to fetch keys: %s", resp.Status)
	}
	if len(body) > maxKeySetBytes {
		return nil, errors.New("the key set is too large")
	}
	var raw struct {
		Keys []json.RawMessage `json:"keys"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("the key set is not JSON: %w", err)
	}
	// A key this library cannot read (another type or algorithm) is left out
	// instead of failing the whole set, as go-oidc does.
	var keys []jose.JSONWebKey
	for _, r := range raw.Keys {
		var key jose.JSONWebKey
		if err := key.UnmarshalJSON(r); err != nil || key.Use == "enc" {
			continue
		}
		keys = append(keys, key)
	}
	return keys, nil
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
