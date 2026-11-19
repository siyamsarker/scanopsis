// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/pquerna/otp"
	"github.com/pquerna/otp/totp"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/secretbox"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	maxMFABody = 1 << 10
	// maxMFACodeLen bounds the code field; the longest valid input is a
	// recovery code with a hyphen (11 characters) plus some spacing.
	maxMFACodeLen = 64

	totpPeriod     = 30
	totpSecretSize = 20
	totpSkew       = 1

	recoveryCodeCount = 10
	recoveryHalf      = 5
	// recoveryAlphabet has no characters that are easily confused (0/o, 1/l/i).
	recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"
)

// MFAStore is the persistence MFA needs; *store.Queries implements it.
type MFAStore interface {
	GetUser(ctx context.Context, id int64) (store.User, error)
	SetPendingTOTP(ctx context.Context, arg store.SetPendingTOTPParams) error
	EnableTOTP(ctx context.Context, arg store.EnableTOTPParams) error
	UseTOTPStep(ctx context.Context, arg store.UseTOTPStepParams) (int64, error)
	DeleteRecoveryCodes(ctx context.Context, userID int64) error
	InsertRecoveryCode(ctx context.Context, arg store.InsertRecoveryCodeParams) error
	UseRecoveryCode(ctx context.Context, arg store.UseRecoveryCodeParams) (int64, error)
}

// MFAOptions configures MFA.
type MFAOptions struct {
	// Issuer is the application name shown by authenticator apps.
	Issuer string
	// Box encrypts TOTP secrets, with the purpose "totp:<userID>".
	Box *secretbox.Box
	// Limiter limits attempts per user for confirm and verify; callers use
	// NewRateLimiter(30*time.Second, 5, nil).
	Limiter *RateLimiter
	Now     func() time.Time // nil means time.Now
	Logger  *slog.Logger     // nil means slog.Default()
}

// MFA serves the two-factor endpoints: TOTP enrolment, its confirmation and
// the second step of sign-in. The handlers expect the router to have put the
// principal of a first-factor session in the request context.
type MFA struct {
	store    MFAStore
	sessions *Sessions
	audit    AuditRecorder
	clientIP func(*http.Request) netip.Addr
	opts     MFAOptions
}

// NewMFA returns an MFA.
func NewMFA(s MFAStore, sessions *Sessions, audit AuditRecorder, clientIP func(*http.Request) netip.Addr, opts MFAOptions) *MFA {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	return &MFA{store: s, sessions: sessions, audit: audit, clientIP: clientIP, opts: opts}
}

func totpPurpose(userID int64) string { return "totp:" + strconv.FormatInt(userID, 10) }

var totpValidate = totp.ValidateOpts{Period: totpPeriod, Digits: otp.DigitsSix, Algorithm: otp.AlgorithmSHA1}

// StartEnrollmentHandler handles POST /api/v1/auth/mfa/enrollment.
func (m *MFA) StartEnrollmentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, user, ok := m.begin(w, r)
		if !ok {
			return
		}
		if user.TotpEnabledAt.Valid {
			writeError(w, http.StatusConflict, "mfa_already_enrolled", "Two-factor authentication is already set up.")
			return
		}
		key, err := totp.Generate(totp.GenerateOpts{
			Issuer:      m.opts.Issuer,
			AccountName: p.Source + ":" + p.Username,
			Period:      totpPeriod,
			SecretSize:  totpSecretSize,
			Digits:      otp.DigitsSix,
			Algorithm:   otp.AlgorithmSHA1,
		})
		if err != nil {
			m.fail(w, r, "cannot generate TOTP secret", err)
			return
		}
		sealed, err := m.opts.Box.Seal([]byte(key.Secret()), totpPurpose(user.ID))
		if err != nil {
			m.fail(w, r, "cannot encrypt TOTP secret", err)
			return
		}
		if err := m.store.SetPendingTOTP(r.Context(), store.SetPendingTOTPParams{SecretEnc: sealed, ID: user.ID}); err != nil {
			m.fail(w, r, "cannot store TOTP secret", err)
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"secret": key.Secret(), "otpauth_uri": key.URL()})
	})
}

// ConfirmEnrollmentHandler handles POST /api/v1/auth/mfa/enrollment/confirm.
func (m *MFA) ConfirmEnrollmentHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, user, ok := m.begin(w, r)
		if !ok {
			return
		}
		if user.TotpEnabledAt.Valid {
			writeError(w, http.StatusConflict, "mfa_already_enrolled", "Two-factor authentication is already set up.")
			return
		}
		if len(user.TotpSecretEnc) == 0 {
			writeError(w, http.StatusConflict, "mfa_enrollment_not_started", "Start the enrolment first.")
			return
		}
		if !m.allow(w, user.ID) {
			return
		}
		code, ok := decodeMFACode(w, r)
		if !ok {
			return
		}
		step, matched, err := m.matchTOTP(user, code)
		if err != nil {
			m.decryptFailed(w, r, err)
			return
		}
		if !matched {
			m.rejected(w, r, p, "totp")
			return
		}
		codes, hashes, err := newRecoveryCodes()
		if err != nil {
			m.fail(w, r, "cannot generate recovery codes", err)
			return
		}
		// Codes first: if enabling fails the user stays pending and can retry.
		ctx := r.Context()
		if err := m.store.DeleteRecoveryCodes(ctx, user.ID); err != nil {
			m.fail(w, r, "cannot replace recovery codes", err)
			return
		}
		for _, h := range hashes {
			if err := m.store.InsertRecoveryCode(ctx, store.InsertRecoveryCodeParams{UserID: user.ID, CodeHash: h}); err != nil {
				m.fail(w, r, "cannot store recovery codes", err)
				return
			}
		}
		if err := m.store.EnableTOTP(ctx, store.EnableTOTPParams{LastStep: step, ID: user.ID}); err != nil {
			m.fail(w, r, "cannot enable two-factor authentication", err)
			return
		}
		if err := m.sessions.PromoteMFAVerified(ctx, w, p.SessionID); err != nil {
			m.fail(w, r, "cannot mark session verified", err)
			return
		}
		m.audit.Record(ctx, actorOf(p), audit.ActionMFAEnrolled, m.detail(r, "totp"))
		writeJSON(w, http.StatusOK, map[string]any{"recovery_codes": codes})
	})
}

// VerifyHandler handles POST /api/v1/auth/mfa/verify.
func (m *MFA) VerifyHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, user, ok := m.begin(w, r)
		if !ok {
			return
		}
		if !user.TotpEnabledAt.Valid {
			writeError(w, http.StatusConflict, "mfa_enrollment_required", "Set up two-factor authentication first.")
			return
		}
		if !m.allow(w, user.ID) {
			return
		}
		code, ok := decodeMFACode(w, r)
		if !ok {
			return
		}
		ctx := r.Context()
		method := "recovery_code"
		accepted := false
		if isTOTPCode(code) {
			method = "totp"
			step, matched, err := m.matchTOTP(user, code)
			if err != nil {
				m.decryptFailed(w, r, err)
				return
			}
			if matched {
				// Only a step newer than the last accepted one is a row; an
				// older or equal one is a replay.
				used, err := queryRow(m.store.UseTOTPStep(ctx, store.UseTOTPStepParams{Step: step, ID: user.ID}))
				if err != nil {
					m.fail(w, r, "cannot record TOTP step", err)
					return
				}
				accepted = used
			}
		} else if hash, valid := recoveryHash(code); valid {
			used, err := queryRow(m.store.UseRecoveryCode(ctx, store.UseRecoveryCodeParams{UserID: user.ID, CodeHash: hash}))
			if err != nil {
				m.fail(w, r, "cannot use recovery code", err)
				return
			}
			accepted = used
		}
		if !accepted {
			m.rejected(w, r, p, method)
			return
		}
		if err := m.sessions.PromoteMFAVerified(ctx, w, p.SessionID); err != nil {
			m.fail(w, r, "cannot mark session verified", err)
			return
		}
		m.audit.Record(ctx, actorOf(p), audit.ActionMFAVerified, m.detail(r, method))
		w.Header().Set("Cache-Control", "no-store")
		w.WriteHeader(http.StatusNoContent)
	})
}

// begin checks the method and the principal and loads the user's current
// state. It writes the response itself when it returns false.
func (m *MFA) begin(w http.ResponseWriter, r *http.Request) (*Principal, store.User, bool) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST.")
		return nil, store.User{}, false
	}
	p, ok := PrincipalFrom(r.Context())
	if !ok {
		writeError(w, http.StatusUnauthorized, "unauthenticated", "Sign in first.")
		return nil, store.User{}, false
	}
	user, err := m.store.GetUser(r.Context(), p.UserID)
	if err != nil {
		m.fail(w, r, "cannot load user", err)
		return nil, store.User{}, false
	}
	return p, user, true
}

func (m *MFA) allow(w http.ResponseWriter, userID int64) bool {
	ok, wait := m.opts.Limiter.Allow(strconv.FormatInt(userID, 10))
	if !ok {
		tooManyAttempts(w, wait)
	}
	return ok
}

func (m *MFA) fail(w http.ResponseWriter, r *http.Request, msg string, err error) {
	m.opts.Logger.ErrorContext(r.Context(), "two-factor: "+msg, "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error",
		"Two-factor authentication failed because of a server problem. Check the server logs.")
}

func (m *MFA) decryptFailed(w http.ResponseWriter, r *http.Request, err error) {
	m.fail(w, r, "cannot decrypt TOTP secret; check SCANOPSIS_SECRET_KEY", err)
}

func (m *MFA) rejected(w http.ResponseWriter, r *http.Request, p *Principal, method string) {
	m.audit.Record(r.Context(), actorOf(p), audit.ActionMFAFailed, m.detail(r, method))
	writeError(w, http.StatusUnauthorized, "invalid_code", "That code is wrong or has already been used.")
}

func (m *MFA) detail(r *http.Request, method string) map[string]any {
	return map[string]any{"method": method, "ip": m.clientIP(r).String()}
}

func actorOf(p *Principal) string { return p.Source + ":" + p.Username }

// queryRow turns the result of a RETURNING query into "a row was returned".
func queryRow(_ int64, err error) (bool, error) {
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// matchTOTP decrypts the user's secret and returns the time step whose code
// equals code, allowing totpSkew steps either side of now. All candidate
// steps are always compared, in constant time. err is set only if the stored
// secret is unusable.
func (m *MFA) matchTOTP(user store.User, code string) (step int64, matched bool, err error) {
	secret, err := m.opts.Box.Open(user.TotpSecretEnc, totpPurpose(user.ID))
	if err != nil {
		return 0, false, err
	}
	cur := m.opts.Now().Unix() / totpPeriod
	for s := cur - totpSkew; s <= cur+totpSkew; s++ {
		if s < 0 {
			continue
		}
		want, err := totp.GenerateCodeCustom(string(secret), time.Unix(s*totpPeriod, 0), totpValidate)
		if err != nil {
			return 0, false, err
		}
		if subtle.ConstantTimeCompare([]byte(want), []byte(code)) == 1 {
			step, matched = s, true
		}
	}
	return step, matched, nil
}

// isTOTPCode reports whether code is six ASCII digits.
func isTOTPCode(code string) bool {
	if len(code) != 6 {
		return false
	}
	for i := range len(code) {
		if code[i] < '0' || code[i] > '9' {
			return false
		}
	}
	return true
}

// normaliseCode lowercases code and drops spaces and hyphens, so that
// "123 456" and "ABCDE-FGHJK" are accepted as typed from an app or a print-out.
func normaliseCode(code string) string {
	return strings.Map(func(r rune) rune {
		if r == ' ' || r == '-' {
			return -1
		}
		return r
	}, strings.ToLower(code))
}

// recoveryHash returns the SHA-256 of a normalised recovery code, and false
// if code cannot be one.
func recoveryHash(code string) ([]byte, bool) {
	if len(code) != 2*recoveryHalf {
		return nil, false
	}
	for i := range len(code) {
		if strings.IndexByte(recoveryAlphabet, code[i]) < 0 {
			return nil, false
		}
	}
	h := sha256.Sum256([]byte(code))
	return h[:], true
}

// newRecoveryCodes returns recoveryCodeCount distinct codes formatted
// xxxxx-xxxxx and the SHA-256 of each normalised code.
func newRecoveryCodes() (codes []string, hashes [][]byte, err error) {
	// Bytes at or above limit are rejected so that the choice stays uniform.
	limit := byte(256 / len(recoveryAlphabet) * len(recoveryAlphabet))
	seen := make(map[string]bool, recoveryCodeCount)
	for len(codes) < recoveryCodeCount {
		raw := make([]byte, 0, 2*recoveryHalf)
		for len(raw) < 2*recoveryHalf {
			var b [16]byte
			if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
				return nil, nil, err
			}
			for _, c := range b {
				if c < limit && len(raw) < 2*recoveryHalf {
					raw = append(raw, recoveryAlphabet[int(c)%len(recoveryAlphabet)])
				}
			}
		}
		plain := string(raw)
		if seen[plain] {
			continue
		}
		seen[plain] = true
		h := sha256.Sum256(raw)
		codes = append(codes, plain[:recoveryHalf]+"-"+plain[recoveryHalf:])
		hashes = append(hashes, h[:])
	}
	return codes, hashes, nil
}

// decodeMFACode reads {"code": "..."} and returns the normalised code,
// writing a 400 itself on failure.
func decodeMFACode(w http.ResponseWriter, r *http.Request) (string, bool) {
	bad := func() (string, bool) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Send a JSON object with only \"code\" (at most 1 KiB).")
		return "", false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxMFABody))
	dec.DisallowUnknownFields()
	var req struct {
		Code *string `json:"code"`
	}
	if err := dec.Decode(&req); err != nil {
		return bad()
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad()
	}
	if req.Code == nil || *req.Code == "" || len(*req.Code) > maxMFACodeLen {
		return bad()
	}
	return normaliseCode(*req.Code), true
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}
