// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	tokenBytes = 32
	// touchInterval is how stale last_seen_at may get before a request
	// writes it again, so that requests do not each cause a write.
	touchInterval = time.Minute
)

// ErrNoSession is returned when a request carries no valid session.
var ErrNoSession = errors.New("no valid session")

// SessionStore is the persistence Sessions needs; *store.Queries implements it.
type SessionStore interface {
	CreateSession(ctx context.Context, arg store.CreateSessionParams) error
	GetSession(ctx context.Context, arg store.GetSessionParams) (store.GetSessionRow, error)
	TouchSession(ctx context.Context, arg store.TouchSessionParams) error
	DeleteSession(ctx context.Context, idHash []byte) error
	DeleteExpiredSessions(ctx context.Context, arg store.DeleteExpiredSessionsParams) (int64, error)
	MarkSessionMFAVerified(ctx context.Context, idHash []byte) error
}

// SessionOptions configures Sessions. A session ends after Idle without a
// request or Absolute after it started, whichever comes first.
type SessionOptions struct {
	Idle, Absolute time.Duration
	Secure         bool
	Now            func() time.Time // nil means time.Now
}

// Sessions manages server-side sessions identified by a random cookie. Only
// the SHA-256 of the token is stored.
type Sessions struct {
	// idle and absolute hold the current timeouts in nanoseconds; they can
	// change at run time through SetTimeouts.
	idle, absolute atomic.Int64

	store SessionStore
	opts  SessionOptions
}

// NewSessions returns a Sessions backed by s.
func NewSessions(s SessionStore, opts SessionOptions) *Sessions {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	out := &Sessions{store: s, opts: opts}
	out.SetTimeouts(opts.Idle, opts.Absolute)
	return out
}

// CookieName returns the session cookie's name. Secure deployments use the
// __Host- prefix, which makes browsers insist on Secure, Path=/ and no Domain.
func CookieName(secure bool) string {
	if secure {
		return "__Host-scanopsis_session"
	}
	return "scanopsis_session"
}

func ts(t time.Time) pgtype.Timestamptz { return pgtype.Timestamptz{Time: t, Valid: true} }

func hashToken(token []byte) []byte {
	h := sha256.Sum256(token)
	return h[:]
}

// tokenFrom returns the decoded token in the request's session cookie, or
// nil if the cookie is absent or malformed.
func (s *Sessions) tokenFrom(r *http.Request) []byte {
	c, err := r.Cookie(CookieName(s.opts.Secure))
	if err != nil {
		return nil
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(c.Value)
	if err != nil || len(token) != tokenBytes {
		return nil
	}
	return token
}

// cookie builds the session cookie. It has no Domain, so browsers keep it
// host-only.
func (s *Sessions) cookie(value string, expires time.Time, maxAge int) *http.Cookie {
	// Secure follows the deployment's TLS setting; plain HTTP is for local development only.
	return &http.Cookie{ //nolint:gosec
		Name:     CookieName(s.opts.Secure),
		Value:    value,
		Path:     "/",
		Expires:  expires,
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   s.opts.Secure,
		SameSite: http.SameSiteLaxMode,
	}
}

// SetTimeouts changes the idle and absolute timeouts for sessions started
// or checked from now on. It is safe to call while requests are served.
func (s *Sessions) SetTimeouts(idle, absolute time.Duration) {
	s.idle.Store(int64(idle))
	s.absolute.Store(int64(absolute))
}

// Start begins a session for userID, replacing any session the request
// carries, and sets the cookie on w.
func (s *Sessions) Start(w http.ResponseWriter, r *http.Request, userID int64) error {
	if old := s.tokenFrom(r); old != nil {
		if err := s.store.DeleteSession(r.Context(), hashToken(old)); err != nil {
			return fmt.Errorf("delete previous session: %w", err)
		}
	}
	token := make([]byte, tokenBytes)
	if _, err := rand.Read(token); err != nil {
		return fmt.Errorf("generate session token: %w", err)
	}
	expires := s.opts.Now().Add(time.Duration(s.absolute.Load()))
	err := s.store.CreateSession(r.Context(), store.CreateSessionParams{
		IDHash:    hashToken(token),
		UserID:    userID,
		ExpiresAt: ts(expires),
	})
	if err != nil {
		return fmt.Errorf("create session: %w", err)
	}
	http.SetCookie(w, s.cookie(base64.RawURLEncoding.EncodeToString(token), expires, 0))
	return nil
}

// Authenticate returns the principal for the request's session cookie. It
// returns ErrNoSession if there is no valid, unexpired, non-idle session.
func (s *Sessions) Authenticate(r *http.Request) (*Principal, error) {
	token := s.tokenFrom(r)
	if token == nil {
		return nil, ErrNoSession
	}
	now := s.opts.Now()
	id := hashToken(token)
	row, err := s.store.GetSession(r.Context(), store.GetSessionParams{
		IDHash:     id,
		Now:        ts(now),
		IdleCutoff: ts(now.Add(-time.Duration(s.idle.Load()))),
	})
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return nil, ErrNoSession
	}
	if err != nil {
		return nil, fmt.Errorf("look up session: %w", err)
	}
	if now.Sub(row.Session.LastSeenAt.Time) > touchInterval {
		// Best effort: failing to extend the idle timer must not fail the request.
		_ = s.store.TouchSession(r.Context(), store.TouchSessionParams{Now: ts(now), IDHash: id})
	}
	u := row.User
	return &Principal{
		UserID:   u.ID,
		Source:   u.Source,
		Username: u.Username,
		Name:     u.Name,
		Email:    u.Email,
		Role:     Role(u.Role),

		SessionID:          id,
		MFAEnrolled:        u.TotpEnabledAt.Valid,
		MFAVerified:        row.Session.MfaVerified,
		MustChangePassword: u.MustChangePassword,
	}, nil
}

// MarkMFAVerified records that the session identified by sessionID (a
// Principal's SessionID) has passed the second factor.
func (s *Sessions) MarkMFAVerified(ctx context.Context, sessionID []byte) error {
	if err := s.store.MarkSessionMFAVerified(ctx, sessionID); err != nil {
		return fmt.Errorf("mark session verified: %w", err)
	}
	return nil
}

// End deletes the request's session, if any, and clears the cookie.
func (s *Sessions) End(w http.ResponseWriter, r *http.Request) error {
	if token := s.tokenFrom(r); token != nil {
		if err := s.store.DeleteSession(r.Context(), hashToken(token)); err != nil {
			return fmt.Errorf("delete session: %w", err)
		}
	}
	http.SetCookie(w, s.cookie("", time.Unix(0, 0), -1))
	return nil
}

// RemoveExpired deletes sessions past their absolute or idle expiry.
func (s *Sessions) RemoveExpired(ctx context.Context) (int64, error) {
	now := s.opts.Now()
	return s.store.DeleteExpiredSessions(ctx, store.DeleteExpiredSessionsParams{
		Now:        ts(now),
		IdleCutoff: ts(now.Add(-time.Duration(s.idle.Load()))),
	})
}
