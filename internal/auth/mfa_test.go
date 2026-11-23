// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/pquerna/otp/totp"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
)

var _ MFAStore = (*store.Queries)(nil)

// fakeMFAStore mirrors the semantics of the SQL queries in memory.
type fakeMFAStore struct {
	user     store.User
	codes    map[string]bool // hex-less string(hash) -> used
	getErr   error
	setErr   error
	enableEr error
	useErr   error
	delErr   error
	insErr   error
	recErr   error

	failErr    error
	resetErr   error
	delSessErr error
	sessions   *fakeSessionStore // for DeleteUserSessions
}

func (f *fakeMFAStore) RecordMFAFailure(_ context.Context, id int64) (int32, error) {
	if f.failErr != nil {
		return 0, f.failErr
	}
	f.user.MfaFailedAttempts++
	return f.user.MfaFailedAttempts, nil
}

func (f *fakeMFAStore) ResetMFAFailures(_ context.Context, id int64) error {
	if f.resetErr != nil {
		return f.resetErr
	}
	f.user.MfaFailedAttempts = 0
	return nil
}

func (f *fakeMFAStore) DeleteUserSessions(_ context.Context, id int64) error {
	if f.delSessErr != nil {
		return f.delSessErr
	}
	for k, sess := range f.sessions.sessions {
		if sess.UserID == id {
			delete(f.sessions.sessions, k)
		}
	}
	return nil
}

func (f *fakeMFAStore) GetUser(_ context.Context, id int64) (store.User, error) {
	if f.getErr != nil {
		return store.User{}, f.getErr
	}
	if id != f.user.ID {
		return store.User{}, pgx.ErrNoRows
	}
	return f.user, nil
}

func (f *fakeMFAStore) SetPendingTOTP(_ context.Context, a store.SetPendingTOTPParams) error {
	if f.setErr != nil {
		return f.setErr
	}
	f.user.TotpSecretEnc = a.SecretEnc
	f.user.TotpEnabledAt = pgtype.Timestamptz{}
	f.user.TotpLastStep = 0
	return nil
}

func (f *fakeMFAStore) EnableTOTP(_ context.Context, a store.EnableTOTPParams) error {
	if f.enableEr != nil {
		return f.enableEr
	}
	if f.user.TotpSecretEnc != nil {
		f.user.TotpEnabledAt = pgtype.Timestamptz{Time: time.Unix(1, 0), Valid: true}
		f.user.TotpLastStep = a.LastStep
	}
	return nil
}

func (f *fakeMFAStore) UseTOTPStep(_ context.Context, a store.UseTOTPStepParams) (int64, error) {
	if f.useErr != nil {
		return 0, f.useErr
	}
	if f.user.TotpLastStep < a.Step {
		f.user.TotpLastStep = a.Step
		return f.user.ID, nil
	}
	return 0, pgx.ErrNoRows
}

func (f *fakeMFAStore) DeleteRecoveryCodes(context.Context, int64) error {
	if f.delErr != nil {
		return f.delErr
	}
	f.codes = map[string]bool{}
	return nil
}

func (f *fakeMFAStore) InsertRecoveryCode(_ context.Context, a store.InsertRecoveryCodeParams) error {
	if f.insErr != nil {
		return f.insErr
	}
	if f.codes == nil {
		f.codes = map[string]bool{}
	}
	f.codes[codeKey(a.Version, a.CodeHash)] = false
	return nil
}

// codeKey is how the fake store keys a stored code: version and hash.
func codeKey(version int16, hash []byte) string {
	return string(rune('0'+version)) + ":" + string(hash)
}

func (f *fakeMFAStore) UseRecoveryCode(_ context.Context, a store.UseRecoveryCodeParams) (int64, error) {
	if f.recErr != nil {
		return 0, f.recErr
	}
	if used, ok := f.codes[codeKey(a.Version, a.CodeHash)]; ok && !used {
		f.codes[codeKey(a.Version, a.CodeHash)] = true
		return a.UserID, nil
	}
	return 0, pgx.ErrNoRows
}

type mfaFixture struct {
	mfa      *MFA
	store    *fakeMFAStore
	sessions *Sessions
	sstore   *fakeSessionStore
	audit    *fakeAudit
	clock    *fakeClock
	box      *secretbox.Box
	logs     *bytes.Buffer
	limiter  *RateLimiter
	cookie   *http.Cookie
}

const mfaUserID = 8 // "vic", a local user in fakeSessionStore

func newMFAFixture(t *testing.T) *mfaFixture {
	t.Helper()
	key := bytes.Repeat([]byte{7}, secretbox.KeySize)
	box, err := secretbox.New(key)
	if err != nil {
		t.Fatal(err)
	}
	recoveryKey, err := RecoveryKey(key)
	if err != nil {
		t.Fatal(err)
	}
	s, ss, clk := newTestSessions(t, true)
	f := &mfaFixture{
		store:    &fakeMFAStore{user: store.User{ID: mfaUserID, Source: SourceLocal, Username: "vic"}},
		sessions: s,
		sstore:   ss,
		audit:    &fakeAudit{},
		clock:    clk,
		box:      box,
		logs:     &bytes.Buffer{},
		limiter:  NewRateLimiter(30*time.Second, 5, clk.Now),
	}
	f.store.sessions = ss
	f.mfa = NewMFA(f.store, s, f.audit, func(*http.Request) netip.Addr { return netip.MustParseAddr("203.0.113.9") }, MFAOptions{
		Issuer:      "Scanopsis",
		Box:         box,
		Limiter:     f.limiter,
		Now:         clk.Now,
		Logger:      slog.New(slog.NewTextHandler(f.logs, nil)),
		RecoveryKey: recoveryKey,
	})
	f.cookie = start(t, s, mfaUserID, nil)
	return f
}

// do calls h with the session's principal in the context.
func (f *mfaFixture) do(t *testing.T, h http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := newRequest(http.MethodPost, "/", strings.NewReader(body))
	p, err := f.sessions.Authenticate(requestWith(f.cookie))
	if err != nil {
		t.Fatal(err)
	}
	r = r.WithContext(WithPrincipal(r.Context(), p))
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	// A promoted session comes with a new cookie: follow it, as a browser does.
	for _, c := range w.Result().Cookies() {
		if c.Name == f.cookie.Name {
			f.cookie = c
		}
	}
	return w
}

func (f *mfaFixture) enrol(t *testing.T) (secret string, w *httptest.ResponseRecorder) {
	t.Helper()
	w = f.do(t, f.mfa.StartEnrollmentHandler(), "")
	if w.Code != http.StatusOK {
		t.Fatalf("start: %d %s", w.Code, w.Body)
	}
	var resp struct {
		Secret string `json:"secret"`
		URI    string `json:"otpauth_uri"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	return resp.Secret, w
}

func (f *mfaFixture) codeAt(t *testing.T, secret string, at time.Time) string {
	t.Helper()
	c, err := totp.GenerateCode(secret, at)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func codeBody(c string) string { return fmt.Sprintf(`{"code":%q}`, c) }

// enrolled completes enrolment and returns the secret and recovery codes.
func (f *mfaFixture) enrolled(t *testing.T) (string, []string) {
	t.Helper()
	secret, _ := f.enrol(t)
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	var resp struct {
		Codes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	// A fresh sign-in: unverified session, as after the first factor.
	f.cookie = start(t, f.sessions, mfaUserID, nil)
	f.clock.Advance(time.Minute) // a later step than the one used to enrol
	return secret, resp.Codes
}

func errCode(t *testing.T, w *httptest.ResponseRecorder) string {
	t.Helper()
	var e struct {
		Error struct{ Code string }
	}
	if err := json.Unmarshal(w.Body.Bytes(), &e); err != nil {
		t.Fatalf("body %q: %v", w.Body, err)
	}
	return e.Error.Code
}

func (f *mfaFixture) verified(t *testing.T) bool {
	t.Helper()
	p, err := f.sessions.Authenticate(requestWith(f.cookie))
	if err != nil {
		t.Fatal(err)
	}
	return p.MFAVerified
}

func TestMFAStartEnrollment(t *testing.T) {
	f := newMFAFixture(t)
	secret, w := f.enrol(t)
	if w.Header().Get("Cache-Control") != "no-store" || w.Header().Get("Content-Type") != "application/json" {
		t.Fatalf("headers %v", w.Header())
	}
	var resp map[string]string
	_ = json.Unmarshal(w.Body.Bytes(), &resp)
	u, err := url.Parse(resp["otpauth_uri"])
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if u.Scheme != "otpauth" || u.Host != "totp" || u.Path != "/Scanopsis:local:vic" {
		t.Fatalf("uri %s", u)
	}
	if q.Get("secret") != secret || q.Get("issuer") != "Scanopsis" || q.Get("algorithm") != "SHA1" ||
		q.Get("digits") != "6" || q.Get("period") != "30" {
		t.Fatalf("query %v", q)
	}
	if len(secret) != 32 || strings.Contains(secret, "=") { // 20 bytes -> 32 base32 characters
		t.Fatalf("secret %q", secret)
	}

	// The secret round-trips: a code generated from it confirms.
	w = f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm %d %s", w.Code, w.Body)
	}
}

func TestMFASecretStoredEncryptedAndBound(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	enc := f.store.user.TotpSecretEnc
	if len(enc) == 0 || bytes.Contains(enc, []byte(secret)) {
		t.Fatal("secret stored in the clear")
	}
	got, err := f.box.Open(enc, "totp:8")
	if err != nil || string(got) != secret {
		t.Fatalf("open: %q, %v", got, err)
	}
	for _, purpose := range []string{"totp:9", "totp:", "setting:totp:8"} {
		if _, err := f.box.Open(enc, purpose); !errors.Is(err, secretbox.ErrDecrypt) {
			t.Fatalf("purpose %q: %v; want ErrDecrypt", purpose, err)
		}
	}
}

func TestMFAStartEnrollmentReplacesPendingAndRejectsEnrolled(t *testing.T) {
	f := newMFAFixture(t)
	s1, _ := f.enrol(t)
	s2, _ := f.enrol(t)
	if s1 == s2 {
		t.Fatal("pending secret not replaced")
	}
	// The old secret no longer confirms.
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, s1, f.clock.Now())))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("old secret: %d", w.Code)
	}
	f.enrolled(t)
	w = f.do(t, f.mfa.StartEnrollmentHandler(), "")
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_already_enrolled" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
}

func TestMFAConfirmErrors(t *testing.T) {
	f := newMFAFixture(t)
	h := f.mfa.ConfirmEnrollmentHandler()

	w := f.do(t, h, codeBody("123456"))
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_enrollment_not_started" {
		t.Fatalf("not started: %d %s", w.Code, w.Body)
	}

	secret, _ := f.enrol(t)
	good := f.codeAt(t, secret, f.clock.Now())
	wrong := "000000"
	if good == wrong {
		wrong = "111111"
	}
	w = f.do(t, h, codeBody(wrong))
	if w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
		t.Fatalf("wrong code: %d %s", w.Code, w.Body)
	}
	if f.store.user.TotpEnabledAt.Valid || len(f.store.codes) != 0 || f.verified(t) {
		t.Fatal("state changed by a wrong code")
	}

	for _, body := range []string{``, `{}`, `{"code":""}`, `{"code":1}`, `{"code":"1","x":2}`, `{"code":"1"}{}`, `[`,
		`{"code":"` + strings.Repeat("1", 65) + `"}`, `{"code":"` + strings.Repeat("1", 2000) + `"}`} {
		f.mfa.opts.Limiter = NewRateLimiter(30*time.Second, 5, f.clock.Now)
		w = f.do(t, h, body)
		if w.Code != http.StatusBadRequest || errCode(t, w) != "invalid_request" {
			t.Fatalf("body %.20q: %d %s", body, w.Code, w.Body)
		}
	}

	f.mfa.opts.Limiter = NewRateLimiter(30*time.Second, 5, f.clock.Now)
	w = f.do(t, h, codeBody(good))
	if w.Code != http.StatusOK {
		t.Fatalf("good code: %d %s", w.Code, w.Body)
	}
	w = f.do(t, h, codeBody(good))
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_already_enrolled" {
		t.Fatalf("already enrolled: %d %s", w.Code, w.Body)
	}
}

var recoveryFormat = regexp.MustCompile(`^[abcdefghjkmnpqrstuvwxyz23456789]{5}(-[abcdefghjkmnpqrstuvwxyz23456789]{5}){3}$`)

func TestMFAConfirmIssuesRecoveryCodes(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	// Old codes from a previous enrolment are replaced.
	f.store.codes = map[string]bool{"stale": false}
	now := f.clock.Now()
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, now)))
	if w.Code != http.StatusOK || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("%d %v", w.Code, w.Header())
	}
	var resp struct {
		Codes []string `json:"recovery_codes"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Codes) != 10 {
		t.Fatalf("%d codes", len(resp.Codes))
	}
	seen := map[string]bool{}
	for _, c := range resp.Codes {
		if !recoveryFormat.MatchString(c) {
			t.Fatalf("code %q has the wrong format", c)
		}
		if seen[c] {
			t.Fatalf("duplicate code %q", c)
		}
		seen[c] = true
		mac := recoveryMAC(f.mfa.opts.RecoveryKey, mfaUserID, strings.ReplaceAll(c, "-", ""))
		if used, ok := f.store.codes[codeKey(recoveryV2, mac)]; !ok || used {
			t.Fatalf("HMAC of %q not stored unused as version 2", c)
		}
	}
	if len(f.store.codes) != 10 {
		t.Fatalf("%d stored hashes; want exactly 10 (stale code deleted)", len(f.store.codes))
	}
	for k := range f.store.codes {
		if len(k) != len(codeKey(recoveryV2, nil))+sha256.Size {
			t.Fatalf("stored key of %d bytes is not a version and a 32-byte MAC", len(k))
		}
	}
	if !f.store.user.TotpEnabledAt.Valid || f.store.user.TotpLastStep != now.Unix()/30 {
		t.Fatalf("user %+v; want enabled with last_step %d", f.store.user, now.Unix()/30)
	}
	if !f.verified(t) {
		t.Fatal("session not marked verified")
	}
	want := []auditEvent{{"local:vic", audit.ActionMFAEnrolled, map[string]any{"method": "totp", "ip": "203.0.113.9"}}}
	if fmt.Sprint(f.audit.events) != fmt.Sprint(want) {
		t.Fatalf("audit %v; want %v", f.audit.events, want)
	}
}

func TestMFAVerifyTOTPSkew(t *testing.T) {
	cases := []struct {
		offset time.Duration
		ok     bool
	}{
		{0, true}, {-30 * time.Second, true}, {30 * time.Second, true},
		{-60 * time.Second, false}, {60 * time.Second, false}, {-90 * time.Second, false}, {5 * time.Minute, false},
	}
	for _, c := range cases {
		t.Run(c.offset.String(), func(t *testing.T) {
			f := newMFAFixture(t)
			secret, _ := f.enrolled(t)
			// Align to the middle of a step so that offsets are whole steps.
			f.clock.Advance(time.Duration(30-f.clock.Now().Unix()%30) * time.Second)
			f.clock.Advance(15 * time.Second)
			code := f.codeAt(t, secret, f.clock.Now().Add(c.offset))
			w := f.do(t, f.mfa.VerifyHandler(), codeBody(code))
			if c.ok {
				if w.Code != http.StatusNoContent || !f.verified(t) {
					t.Fatalf("%d %s", w.Code, w.Body)
				}
			} else if w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" || f.verified(t) {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
		})
	}
}

func TestMFAVerifyReplay(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	code := f.codeAt(t, secret, f.clock.Now())
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(code)); w.Code != http.StatusNoContent {
		t.Fatalf("first use: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(code)); w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
		t.Fatalf("replay: %d %s", w.Code, w.Body)
	}
	// An earlier step than the one used is also rejected, a later one works.
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now().Add(-30*time.Second)))); w.Code != http.StatusUnauthorized {
		t.Fatalf("older step: %d", w.Code)
	}
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now().Add(30*time.Second)))); w.Code != http.StatusNoContent {
		t.Fatalf("newer step: %d", w.Code)
	}
}

func TestMFAEnrolmentStepCannotBeReplayedAtVerify(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	code := f.codeAt(t, secret, f.clock.Now())
	if w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(code)); w.Code != http.StatusOK {
		t.Fatal(w.Code)
	}
	f.cookie = start(t, f.sessions, mfaUserID, nil)
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(code)); w.Code != http.StatusUnauthorized {
		t.Fatalf("code used to enrol accepted again: %d", w.Code)
	}
}

func TestMFAVerifyCodeSpacing(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	code := f.codeAt(t, secret, f.clock.Now())
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(code[:3]+" "+code[3:])); w.Code != http.StatusNoContent {
		t.Fatalf("spaced code: %d", w.Code)
	}
}

func TestMFARecoveryCode(t *testing.T) {
	f := newMFAFixture(t)
	_, codes := f.enrolled(t)
	h := f.mfa.VerifyHandler()

	// Case and hyphen do not matter.
	typed := strings.ToUpper(strings.ReplaceAll(codes[0], "-", ""))
	if w := f.do(t, h, codeBody(typed)); w.Code != http.StatusNoContent || !f.verified(t) {
		t.Fatalf("recovery code: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, h, codeBody(codes[0])); w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
		t.Fatalf("second use: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, h, codeBody(codes[1])); w.Code != http.StatusNoContent {
		t.Fatalf("another code: %d", w.Code)
	}
	// Wrong shapes and unknown codes fail without touching the store.
	for _, bad := range []string{"abcde-fghjk", "ABCDE", "0o1li-0o1li", "abcde-fghjk-mnpqr", "12345", "1234567"} {
		f.mfa.opts.Limiter = NewRateLimiter(30*time.Second, 5, f.clock.Now)
		if w := f.do(t, h, codeBody(bad)); w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
			t.Fatalf("%q: %d", bad, w.Code)
		}
	}
	last := f.audit.events[len(f.audit.events)-1]
	if last.Action != audit.ActionMFAFailed || last.Detail["method"] != "recovery_code" {
		t.Fatalf("last audit %+v", last)
	}
}

func TestMFAVerifyRequiresEnrolment(t *testing.T) {
	f := newMFAFixture(t)
	w := f.do(t, f.mfa.VerifyHandler(), codeBody("123456"))
	if w.Code != http.StatusConflict || errCode(t, w) != "mfa_enrollment_required" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	// A pending (unconfirmed) enrolment is not an enrolment.
	f.enrol(t)
	w = f.do(t, f.mfa.VerifyHandler(), codeBody("123456"))
	if w.Code != http.StatusConflict {
		t.Fatalf("pending: %d", w.Code)
	}
}

func TestMFARateLimit(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	h := f.mfa.VerifyHandler()
	for i := range 5 {
		if w := f.do(t, h, codeBody("000000")); w.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d: %d", i+1, w.Code)
		}
	}
	// Even a correct code is refused while limited.
	w := f.do(t, h, codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") != "30" || errCode(t, w) != "too_many_attempts" {
		t.Fatalf("%d retry-after %q %s", w.Code, w.Header().Get("Retry-After"), w.Body)
	}
	f.clock.Advance(30 * time.Second)
	if w := f.do(t, h, codeBody(f.codeAt(t, secret, f.clock.Now()))); w.Code != http.StatusNoContent {
		t.Fatalf("after waiting: %d %s", w.Code, w.Body)
	}
}

func TestMFARateLimitOnConfirmIsPerUser(t *testing.T) {
	f := newMFAFixture(t)
	f.enrol(t)
	h := f.mfa.ConfirmEnrollmentHandler()
	for range 5 {
		f.do(t, h, codeBody("000000"))
	}
	if w := f.do(t, h, codeBody("000000")); w.Code != http.StatusTooManyRequests {
		t.Fatalf("%d", w.Code)
	}
	if ok, _ := f.limiter.Allow("99"); !ok {
		t.Fatal("another user is limited too")
	}
}

func TestMFADecryptFailure(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	otherBox, _ := secretbox.New(bytes.Repeat([]byte{9}, secretbox.KeySize))
	f.mfa.opts.Box = otherBox // wrong SCANOPSIS_SECRET_KEY
	w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusInternalServerError || errCode(t, w) != "internal_error" {
		t.Fatalf("%d %s", w.Code, w.Body)
	}
	if !strings.Contains(f.logs.String(), "cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY") {
		t.Fatalf("log %q", f.logs)
	}
	if f.verified(t) {
		t.Fatal("verified despite failure")
	}

	// Same on confirm.
	f2 := newMFAFixture(t)
	secret2, _ := f2.enrol(t)
	f2.mfa.opts.Box = otherBox
	w = f2.do(t, f2.mfa.ConfirmEnrollmentHandler(), codeBody(f2.codeAt(t, secret2, f2.clock.Now())))
	if w.Code != http.StatusInternalServerError || !strings.Contains(f2.logs.String(), "check SCANOPSIS_SECRET_KEY") {
		t.Fatalf("confirm: %d %q", w.Code, f2.logs)
	}
}

func TestMFACorruptSecretIsInternalError(t *testing.T) {
	f := newMFAFixture(t)
	f.enrolled(t)
	// A valid ciphertext of something that is not base32.
	f.store.user.TotpSecretEnc, _ = f.box.Seal([]byte("not base32!"), "totp:8")
	w := f.do(t, f.mfa.VerifyHandler(), codeBody("123456"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("%d", w.Code)
	}
}

func TestMFAAuditAndLogsNeverContainCodesOrSecrets(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	enrolCode := f.codeAt(t, secret, f.clock.Now())
	f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody("000001"))
	_, codes := func() (string, []string) {
		w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(enrolCode))
		var r struct {
			C []string `json:"recovery_codes"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &r)
		return "", r.C
	}()
	f.cookie = start(t, f.sessions, mfaUserID, nil)
	f.clock.Advance(time.Minute)
	good := f.codeAt(t, secret, f.clock.Now())
	f.do(t, f.mfa.VerifyHandler(), codeBody("000002"))
	f.do(t, f.mfa.VerifyHandler(), codeBody("zzzzz-zzzzz"))
	f.do(t, f.mfa.VerifyHandler(), codeBody(good))
	f.do(t, f.mfa.VerifyHandler(), codeBody(codes[0]))

	var actions []string
	for _, e := range f.audit.events {
		actions = append(actions, e.Action)
		if e.Actor != "local:vic" {
			t.Fatalf("actor %q", e.Actor)
		}
	}
	wantActions := "mfa.failed mfa.enrolled mfa.failed mfa.failed mfa.verified mfa.verified"
	if strings.Join(actions, " ") != wantActions {
		t.Fatalf("actions %v; want %s", actions, wantActions)
	}
	if f.audit.events[4].Detail["method"] != "totp" || f.audit.events[5].Detail["method"] != "recovery_code" {
		t.Fatalf("methods %v", f.audit.events)
	}
	dump := fmt.Sprint(f.audit.events) + f.logs.String()
	secrets := append([]string{secret, enrolCode, good, "000001", "000002", "zzzzz-zzzzz", string(f.store.user.TotpSecretEnc)}, codes...)
	for _, s := range secrets {
		if strings.Contains(dump, s) || strings.Contains(dump, strings.ReplaceAll(s, "-", "")) && len(s) == 11 {
			t.Fatalf("audit or log output contains %q", s)
		}
	}
}

func TestMFANoPrincipalAndMethod(t *testing.T) {
	f := newMFAFixture(t)
	for _, h := range []http.Handler{f.mfa.StartEnrollmentHandler(), f.mfa.ConfirmEnrollmentHandler(), f.mfa.VerifyHandler()} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, newRequest(http.MethodPost, "/", strings.NewReader(codeBody("123456"))))
		if w.Code != http.StatusUnauthorized || errCode(t, w) != "unauthenticated" {
			t.Fatalf("%d %s", w.Code, w.Body)
		}
		w = httptest.NewRecorder()
		h.ServeHTTP(w, newRequest(http.MethodGet, "/", nil))
		if w.Code != http.StatusMethodNotAllowed || w.Header().Get("Allow") != "POST" {
			t.Fatalf("GET: %d", w.Code)
		}
	}
}

func TestMFAStoreErrors(t *testing.T) {
	boom := errors.New("boom")
	steps := map[string]struct {
		set  func(*fakeMFAStore)
		call func(*mfaFixture) http.Handler
		pre  func(*testing.T, *mfaFixture) string // returns body
	}{
		"get user": {func(s *fakeMFAStore) { s.getErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.VerifyHandler() },
			func(*testing.T, *mfaFixture) string { return codeBody("123456") }},
		"set pending": {func(s *fakeMFAStore) { s.setErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.StartEnrollmentHandler() },
			func(*testing.T, *mfaFixture) string { return "" }},
		"delete codes": {func(s *fakeMFAStore) { s.delErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.ConfirmEnrollmentHandler() },
			confirmBody},
		"insert code": {func(s *fakeMFAStore) { s.insErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.ConfirmEnrollmentHandler() },
			confirmBody},
		"enable": {func(s *fakeMFAStore) { s.enableEr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.ConfirmEnrollmentHandler() },
			confirmBody},
		"use step": {func(s *fakeMFAStore) { s.useErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.VerifyHandler() },
			verifyBody},
		"use recovery": {func(s *fakeMFAStore) { s.recErr = boom }, func(f *mfaFixture) http.Handler { return f.mfa.VerifyHandler() },
			func(t *testing.T, f *mfaFixture) string { f.enrolled(t); return codeBody("abcde-fghjk") }},
	}
	for name, c := range steps {
		t.Run(name, func(t *testing.T) {
			f := newMFAFixture(t)
			body := c.pre(t, f)
			c.set(f.store)
			w := f.do(t, c.call(f), body)
			if w.Code != http.StatusInternalServerError || errCode(t, w) != "internal_error" {
				t.Fatalf("%d %s", w.Code, w.Body)
			}
			if !strings.Contains(f.logs.String(), "boom") {
				t.Fatalf("error not logged: %q", f.logs)
			}
			if f.verified(t) {
				t.Fatal("session verified despite an error")
			}
		})
	}
}

func confirmBody(t *testing.T, f *mfaFixture) string {
	secret, _ := f.enrol(t)
	return codeBody(f.codeAt(t, secret, f.clock.Now()))
}

func verifyBody(t *testing.T, f *mfaFixture) string {
	secret, _ := f.enrolled(t)
	return codeBody(f.codeAt(t, secret, f.clock.Now()))
}

func TestMFAMarkVerifiedFailure(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	f.sstore.markErr = errors.New("boom")
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("confirm: %d", w.Code)
	}
	if len(f.audit.events) != 0 {
		t.Fatalf("audit recorded: %v", f.audit.events)
	}
}

func TestMFAVerifyMarkVerifiedFailure(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	f.sstore.markErr = errors.New("boom")
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now()))); w.Code != http.StatusInternalServerError {
		t.Fatalf("verify: %d", w.Code)
	}
	if len(f.audit.events) != 1 { // only the enrolment
		t.Fatalf("audit %v", f.audit.events)
	}
}

func TestMFADefaults(t *testing.T) {
	m := NewMFA(&fakeMFAStore{}, nil, &fakeAudit{}, nil, MFAOptions{})
	if m.opts.Now == nil || m.opts.Logger == nil {
		t.Fatal("defaults not set")
	}
}

func TestRecoveryCodesAreUniqueAndUniform(t *testing.T) {
	seen := map[string]bool{}
	for range 200 {
		codes, hashes, err := newRecoveryCodes(bytes.Repeat([]byte{1}, 32), 1)
		if err != nil || len(codes) != 10 || len(hashes) != 10 {
			t.Fatal(err, len(codes))
		}
		for _, c := range codes {
			if seen[c] {
				t.Fatalf("code %q generated twice in 2000 codes", c)
			}
			seen[c] = true
		}
	}
}

func TestMFAVerifyReplacesTheSessionToken(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	old := *f.cookie
	before, err := f.sessions.Authenticate(requestWith(&old))
	if err != nil {
		t.Fatal(err)
	}
	expires := f.sstore.sessions[string(before.SessionID)].ExpiresAt

	w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusNoContent {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	if f.cookie.Value == old.Value {
		t.Fatal("verify did not issue a new session cookie")
	}
	if _, err := f.sessions.Authenticate(requestWith(&old)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old cookie after verify: %v; want ErrNoSession", err)
	}
	after, err := f.sessions.Authenticate(requestWith(f.cookie))
	if err != nil || after.UserID != before.UserID || !after.MFAVerified {
		t.Fatalf("new cookie: %+v, %v", after, err)
	}
	if got := f.sstore.sessions[string(after.SessionID)].ExpiresAt; !got.Time.Equal(expires.Time) {
		t.Fatalf("expiry %v; want the original %v", got.Time, expires.Time)
	}
}

func TestMFAConfirmReplacesTheSessionToken(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrol(t)
	old := *f.cookie
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusOK {
		t.Fatalf("confirm: %d %s", w.Code, w.Body)
	}
	if _, err := f.sessions.Authenticate(requestWith(&old)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("old cookie after confirm: %v; want ErrNoSession", err)
	}
	if !f.verified(t) {
		t.Fatal("the new cookie is not a verified session")
	}
}

func TestMFAWrongCodeKeepsTheSessionToken(t *testing.T) {
	f := newMFAFixture(t)
	f.enrolled(t)
	old := *f.cookie
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody("000000")); w.Code != http.StatusUnauthorized {
		t.Fatalf("verify: %d", w.Code)
	}
	if f.cookie.Value != old.Value || f.verified(t) {
		t.Fatal("a wrong code changed the session")
	}
}

const lockedMessage = "Too many wrong codes. Ask an administrator to reset two-factor authentication."

func (f *mfaFixture) wrongCode() string { return codeBody("000000") }

func TestMFAVerifyCountsFailuresAndLocksAtTen(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	f.limiter = NewRateLimiter(time.Second, 1000, f.clock.Now)
	f.mfa.opts.Limiter = f.limiter
	otherSession := start(t, f.sessions, mfaUserID, nil)

	for i := 1; i < mfaMaxFailures; i++ {
		w := f.do(t, f.mfa.VerifyHandler(), f.wrongCode())
		if w.Code != http.StatusUnauthorized || errCode(t, w) != "invalid_code" {
			t.Fatalf("failure %d: %d %s", i, w.Code, w.Body)
		}
		if f.store.user.MfaFailedAttempts != int32(i) {
			t.Fatalf("counter %d after %d failures", f.store.user.MfaFailedAttempts, i)
		}
	}
	w := f.do(t, f.mfa.VerifyHandler(), f.wrongCode())
	if w.Code != http.StatusLocked || errCode(t, w) != "mfa_locked" {
		t.Fatalf("tenth failure: %d %s", w.Code, w.Body)
	}
	var e struct{ Error struct{ Message string } }
	_ = json.Unmarshal(w.Body.Bytes(), &e)
	if e.Error.Message != lockedMessage {
		t.Fatalf("message %q", e.Error.Message)
	}
	last := f.audit.events[len(f.audit.events)-1]
	if last.Action != audit.ActionMFALocked || last.Actor != "local:vic" {
		t.Fatalf("last audit event %+v; want mfa.locked", last)
	}
	// Every session of the user is gone.
	if len(f.sstore.sessions) != 0 {
		t.Fatalf("%d sessions left", len(f.sstore.sessions))
	}
	if _, err := f.sessions.Authenticate(requestWith(otherSession)); !errors.Is(err, ErrNoSession) {
		t.Fatalf("other session: %v", err)
	}

	// After a new first-factor sign-in even the right code is refused,
	// without being checked.
	f.cookie = start(t, f.sessions, mfaUserID, nil)
	events := len(f.audit.events)
	w = f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusLocked || errCode(t, w) != "mfa_locked" {
		t.Fatalf("right code while locked: %d %s", w.Code, w.Body)
	}
	if f.verified(t) || f.store.user.MfaFailedAttempts != mfaMaxFailures || len(f.audit.events) != events {
		t.Fatal("a locked account checked the code")
	}
	// Recovery codes are refused too.
	w = f.do(t, f.mfa.VerifyHandler(), codeBody("abcde-fghij"))
	if w.Code != http.StatusLocked {
		t.Fatalf("recovery code while locked: %d", w.Code)
	}
}

func TestMFAVerifyLockedWithoutSpendingTheLimiter(t *testing.T) {
	f := newMFAFixture(t)
	f.enrolled(t)
	f.store.user.MfaFailedAttempts = mfaMaxFailures
	f.cookie = start(t, f.sessions, mfaUserID, nil)
	for range 20 {
		if w := f.do(t, f.mfa.VerifyHandler(), f.wrongCode()); w.Code != http.StatusLocked {
			t.Fatalf("status %d; want 423 every time, not 429", w.Code)
		}
	}
}

func TestMFAVerifyRecoveryCodeFailuresCount(t *testing.T) {
	f := newMFAFixture(t)
	f.enrolled(t)
	f.do(t, f.mfa.VerifyHandler(), codeBody("abcde-fghij"))
	f.do(t, f.mfa.VerifyHandler(), codeBody("not a code at all"))
	if f.store.user.MfaFailedAttempts != 2 {
		t.Fatalf("counter %d; want 2", f.store.user.MfaFailedAttempts)
	}
}

func TestMFAVerifySuccessResetsTheCounter(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	for range 3 {
		f.do(t, f.mfa.VerifyHandler(), f.wrongCode())
	}
	if f.store.user.MfaFailedAttempts != 3 {
		t.Fatalf("counter %d; want 3", f.store.user.MfaFailedAttempts)
	}
	f.clock.Advance(time.Minute) // let the limiter refill
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now()))); w.Code != http.StatusNoContent {
		t.Fatalf("verify: %d %s", w.Code, w.Body)
	}
	if f.store.user.MfaFailedAttempts != 0 {
		t.Fatalf("counter %d after a success; want 0", f.store.user.MfaFailedAttempts)
	}
}

func TestMFAVerifyLockoutStoreErrors(t *testing.T) {
	t.Run("count", func(t *testing.T) {
		f := newMFAFixture(t)
		f.enrolled(t)
		f.store.failErr = errors.New("boom")
		if w := f.do(t, f.mfa.VerifyHandler(), f.wrongCode()); w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("sessions", func(t *testing.T) {
		f := newMFAFixture(t)
		f.enrolled(t)
		f.store.user.MfaFailedAttempts = mfaMaxFailures - 1
		f.store.delSessErr = errors.New("boom")
		if w := f.do(t, f.mfa.VerifyHandler(), f.wrongCode()); w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
	})
	t.Run("reset", func(t *testing.T) {
		f := newMFAFixture(t)
		secret, _ := f.enrolled(t)
		f.store.user.MfaFailedAttempts = 2
		f.store.resetErr = errors.New("boom")
		if w := f.do(t, f.mfa.VerifyHandler(), codeBody(f.codeAt(t, secret, f.clock.Now()))); w.Code != http.StatusInternalServerError {
			t.Fatalf("status %d", w.Code)
		}
		if f.verified(t) {
			t.Fatal("session verified although the reset failed")
		}
	})
}

func TestRecoveryCodeStrength(t *testing.T) {
	// 20 symbols from 31: at least 99 bits.
	if bits := float64(recoveryLen) * math.Log2(float64(len(recoveryAlphabet))); bits < 99 {
		t.Fatalf("recovery codes carry %.1f bits", bits)
	}
	if _, _, err := newRecoveryCodes(nil, 1); err == nil {
		t.Fatal("codes issued without a key")
	}
}

func TestRecoveryKeyDerivation(t *testing.T) {
	a, err := RecoveryKey(bytes.Repeat([]byte{7}, 32))
	if err != nil || len(a) != 32 {
		t.Fatalf("RecoveryKey = %x, %v", a, err)
	}
	b, _ := RecoveryKey(bytes.Repeat([]byte{8}, 32))
	if bytes.Equal(a, b) || bytes.Equal(a, bytes.Repeat([]byte{7}, 32)) {
		t.Fatal("the derived key does not depend on, or equals, the secret key")
	}
	if _, err := RecoveryKey(nil); err == nil {
		t.Fatal("an empty secret key was accepted")
	}
	// The stored form depends on the user and the key, not only the code.
	code := strings.Repeat("a", recoveryLen)
	if bytes.Equal(recoveryMAC(a, 1, code), recoveryMAC(a, 2, code)) || bytes.Equal(recoveryMAC(a, 1, code), recoveryMAC(b, 1, code)) {
		t.Fatal("the MAC ignores the user id or the key")
	}
}

func TestMFAStoredCodeIsBoundToTheUser(t *testing.T) {
	f := newMFAFixture(t)
	_, codes := f.enrolled(t)
	// Store another user's code: the same text does not unlock this user.
	f.store.codes = map[string]bool{codeKey(recoveryV2, recoveryMAC(f.mfa.opts.RecoveryKey, mfaUserID+1, strings.ReplaceAll(codes[0], "-", ""))): false}
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(codes[0])); w.Code != http.StatusUnauthorized {
		t.Fatalf("a code stored for another user: %d", w.Code)
	}
}

func TestMFALegacyRecoveryCodeStillWorksOnce(t *testing.T) {
	f := newMFAFixture(t)
	f.enrolled(t)
	// A code issued before version 2: ten symbols, stored as SHA-256.
	legacy := "abcde-fghjk"
	sum := sha256.Sum256([]byte("abcdefghjk"))
	f.store.codes[codeKey(recoveryV1, sum[:])] = false
	h := f.mfa.VerifyHandler()
	if w := f.do(t, h, codeBody(strings.ToUpper(legacy))); w.Code != http.StatusNoContent {
		t.Fatalf("legacy code: %d %s", w.Code, w.Body)
	}
	if w := f.do(t, h, codeBody(legacy)); w.Code != http.StatusUnauthorized {
		t.Fatalf("legacy code reused: %d", w.Code)
	}
	// A legacy hash is never matched as version 2, nor the other way round.
	f.store.codes[codeKey(recoveryV2, sum[:])] = false
	if w := f.do(t, h, codeBody("abcdefghjk")); w.Code != http.StatusUnauthorized {
		t.Fatalf("a version 2 row matched a legacy code: %d", w.Code)
	}
}

func TestMFAWithoutRecoveryKeyIssuesNoCodes(t *testing.T) {
	f := newMFAFixture(t)
	f.mfa.opts.RecoveryKey = nil
	secret, _ := f.enrol(t)
	w := f.do(t, f.mfa.ConfirmEnrollmentHandler(), codeBody(f.codeAt(t, secret, f.clock.Now())))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("confirm without a recovery key: %d", w.Code)
	}
	if _, _, ok := f.mfa.recoveryHash(mfaUserID, strings.Repeat("a", recoveryLen)); ok {
		t.Fatal("a version 2 code was looked up without a key")
	}
}

func TestCheckCodeReplay(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	ctx := context.Background()
	code := f.codeAt(t, secret, f.clock.Now())
	if err := f.mfa.CheckCode(ctx, mfaUserID, code); err != nil {
		t.Fatalf("first use: %v", err)
	}
	if err := f.mfa.CheckCode(ctx, mfaUserID, code); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("replay: %v; want ErrInvalidCode", err)
	}
	// A step the sign-in check already used is a replay here too.
	f.clock.Advance(time.Minute)
	signIn := f.codeAt(t, secret, f.clock.Now())
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(signIn)); w.Code != http.StatusNoContent {
		t.Fatalf("sign-in: %d %s", w.Code, w.Body)
	}
	if err := f.mfa.CheckCode(ctx, mfaUserID, signIn); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("code used at sign-in: %v; want ErrInvalidCode", err)
	}
	// A code with spaces, as typed, is accepted.
	f.clock.Advance(time.Minute)
	c := f.codeAt(t, secret, f.clock.Now())
	if err := f.mfa.CheckCode(ctx, mfaUserID, c[:3]+" "+c[3:]); err != nil {
		t.Fatalf("spaced code: %v", err)
	}
}

func TestCheckCodeLockout(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	f.mfa.opts.Limiter = NewRateLimiter(time.Second, 1000, f.clock.Now)
	ctx := context.Background()
	for i := 1; i < mfaMaxFailures; i++ {
		if err := f.mfa.CheckCode(ctx, mfaUserID, "000000"); !errors.Is(err, ErrInvalidCode) {
			t.Fatalf("failure %d: %v", i, err)
		}
		if f.store.user.MfaFailedAttempts != int32(i) {
			t.Fatalf("counter %d after %d failures", f.store.user.MfaFailedAttempts, i)
		}
	}
	if err := f.mfa.CheckCode(ctx, mfaUserID, "000000"); !errors.Is(err, ErrLocked) {
		t.Fatalf("tenth failure: %v; want ErrLocked", err)
	}
	if last := f.audit.events[len(f.audit.events)-1]; last.Action != audit.ActionMFALocked || last.Actor != "local:vic" {
		t.Fatalf("last audit event %+v; want mfa.locked", last)
	}
	if len(f.sstore.sessions) != 0 {
		t.Fatalf("%d sessions left after the lock", len(f.sstore.sessions))
	}
	// Locked: even the right code is refused, and nothing is counted.
	events := len(f.audit.events)
	if err := f.mfa.CheckCode(ctx, mfaUserID, f.codeAt(t, secret, f.clock.Now())); !errors.Is(err, ErrLocked) {
		t.Fatalf("right code while locked: %v", err)
	}
	if len(f.audit.events) != events || f.store.user.MfaFailedAttempts != mfaMaxFailures {
		t.Fatal("a locked account checked the code")
	}
}

func TestCheckCodeSuccessResetsTheCounter(t *testing.T) {
	f := newMFAFixture(t)
	secret, _ := f.enrolled(t)
	ctx := context.Background()
	_ = f.mfa.CheckCode(ctx, mfaUserID, "000000")
	_ = f.mfa.CheckCode(ctx, mfaUserID, "000000")
	if err := f.mfa.CheckCode(ctx, mfaUserID, f.codeAt(t, secret, f.clock.Now())); err != nil {
		t.Fatal(err)
	}
	if f.store.user.MfaFailedAttempts != 0 {
		t.Fatalf("counter %d after a right code", f.store.user.MfaFailedAttempts)
	}
}

func TestCheckCodeRejectsRecoveryCode(t *testing.T) {
	f := newMFAFixture(t)
	_, codes := f.enrolled(t)
	ctx := context.Background()
	if err := f.mfa.CheckCode(ctx, mfaUserID, codes[0]); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("recovery code: %v; want ErrInvalidCode", err)
	}
	if f.store.user.MfaFailedAttempts != 1 {
		t.Fatalf("counter %d; a recovery code counts as a failure", f.store.user.MfaFailedAttempts)
	}
	// It was not used up.
	if w := f.do(t, f.mfa.VerifyHandler(), codeBody(codes[0])); w.Code != http.StatusNoContent {
		t.Fatalf("recovery code at sign-in afterwards: %d %s", w.Code, w.Body)
	}
}

func TestCheckCodeLimiterAndNotEnrolled(t *testing.T) {
	f := newMFAFixture(t)
	ctx := context.Background()
	if err := f.mfa.CheckCode(ctx, mfaUserID, "123456"); !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("not enrolled: %v", err)
	}
	secret, _ := f.enrolled(t)
	_ = secret
	var err error
	for range 10 {
		if err = f.mfa.CheckCode(ctx, mfaUserID, "000000"); errors.Is(err, ErrTooManyAttempts) {
			break
		}
	}
	if !errors.Is(err, ErrTooManyAttempts) {
		t.Fatalf("last error %v; want ErrTooManyAttempts", err)
	}
}
