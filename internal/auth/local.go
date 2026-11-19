// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const (
	maxLoginBody     = 8 << 10
	maxUsernameBytes = 200
	maxPasswordBytes = 1024
)

// dummyHash is verified when the user does not exist, so that unknown and
// known users cost the same time. No password matches it.
const dummyHash = "$argon2id$v=19$m=65536,t=3,p=4$c2Nhbm9wc2lzLWR1bW15IQ$AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8"

// verifyPassword is a variable so tests can observe that it runs.
var verifyPassword = VerifyPassword

// LocalUserStore is the persistence the local login needs; *store.Queries
// implements it.
type LocalUserStore interface {
	GetLocalUserByUsername(ctx context.Context, username string) (store.User, error)
	TouchLastLogin(ctx context.Context, id int64) error
}

type localLogin struct {
	users    LocalUserStore
	sessions SessionStarter
	audit    AuditRecorder
	clientIP func(*http.Request) netip.Addr
	byAddr   *RateLimiter
	byUser   *RateLimiter
	log      *slog.Logger
}

// NewLocalLogin returns the handler for POST /api/v1/auth/local/login.
// Callers should use NewRateLimiter(12*time.Second, 10, nil) for both
// limiters: byAddr limits every attempt per client network (an IPv4 address or
// an IPv6 /64) and byUser the failed password checks per username (as given),
// each to ten attempts and then five per minute. A correct password is never
// refused because of the byUser limit.
// Server-side failures are logged to log (nil discards them).
func NewLocalLogin(users LocalUserStore, sessions SessionStarter, audit AuditRecorder, clientIP func(*http.Request) netip.Addr, byAddr, byUser *RateLimiter, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &localLogin{users: users, sessions: sessions, audit: audit, clientIP: clientIP, byAddr: byAddr, byUser: byUser, log: log}
}

type loginRequest struct {
	Username *string `json:"username"`
	Password *string `json:"password"`
}

func (h *localLogin) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.Header().Set("Allow", http.MethodPost)
		writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use POST to sign in.")
		return
	}
	ip := h.clientIP(r)
	// Limit before any parsing or hashing, so floods cost almost nothing.
	okAddr, waitAddr := h.byAddr.Allow(addrKey(ip))
	if !okAddr {
		tooManyAttempts(w, waitAddr)
		return
	}

	username, password, ok := decodeLogin(w, r)
	if !ok {
		return
	}
	user, err := h.users.GetLocalUserByUsername(r.Context(), username)
	found := err == nil
	if err != nil && !errors.Is(err, pgx.ErrNoRows) && !errors.Is(err, store.ErrNotFound) {
		h.log.ErrorContext(r.Context(), "local sign-in: cannot look up user", "error", err)
		writeInternalError(w)
		return
	}
	// Always verify against some hash so the response time does not reveal
	// whether the user exists.
	hash := dummyHash
	if found && user.PasswordHash.Valid {
		hash = user.PasswordHash.String
	}
	match, verr := verifyPassword(r.Context(), hash, password)
	if errors.Is(verr, context.Canceled) || errors.Is(verr, context.DeadlineExceeded) {
		// The client left while waiting for a hashing slot: not a failed sign-in.
		writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
		return
	}
	if !found || !user.PasswordHash.Valid || verr != nil || !match {
		h.audit.Record(r.Context(), "local:"+username, audit.ActionLoginFailed, h.detail(username, ip))
		// Only failed verifications are charged to the account, so that
		// attempts from elsewhere can never lock the real user out: a
		// correct password is not refused. The key is the exact name used
		// for the lookup.
		if ok, wait := h.byUser.Allow(username); !ok {
			tooManyAttempts(w, max(waitAddr, wait))
			return
		}
		writeError(w, http.StatusUnauthorized, "invalid_credentials", "Wrong username or password.")
		return
	}

	if err := h.users.TouchLastLogin(r.Context(), user.ID); err != nil {
		h.log.ErrorContext(r.Context(), "local sign-in: cannot record sign-in time", "error", err)
		writeInternalError(w)
		return
	}
	if err := h.sessions.Start(w, r, user.ID); err != nil {
		h.log.ErrorContext(r.Context(), "local sign-in: cannot start session", "error", err)
		writeInternalError(w)
		return
	}
	h.audit.Record(r.Context(), "local:"+username, audit.ActionLoginSucceeded, h.detail(username, ip))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// retryAfterSeconds rounds wait up to whole seconds, at least one.
func retryAfterSeconds(wait time.Duration) int {
	return max(1, int(math.Ceil(wait.Seconds())))
}

func tooManyAttempts(w http.ResponseWriter, wait time.Duration) {
	secs := retryAfterSeconds(wait)
	w.Header().Set("Retry-After", strconv.Itoa(secs))
	writeError(w, http.StatusTooManyRequests, "too_many_attempts",
		"Too many sign-in attempts. Wait "+strconv.Itoa(secs)+" seconds and try again.")
}

func (h *localLogin) detail(username string, ip netip.Addr) map[string]any {
	return map[string]any{"method": SourceLocal, "username": username, "ip": ip.String()}
}

// decodeLogin reads and validates the request body, writing a 400 itself on
// failure.
func decodeLogin(w http.ResponseWriter, r *http.Request) (username, password string, ok bool) {
	bad := func(msg string) (string, string, bool) {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return "", "", false
	}
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	dec.DisallowUnknownFields()
	var req loginRequest
	if err := dec.Decode(&req); err != nil {
		return bad("Send a JSON object with only \"username\" and \"password\" (at most 8 KiB).")
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return bad("Send a single JSON object and nothing after it.")
	}
	if req.Username == nil || req.Password == nil || *req.Username == "" || *req.Password == "" {
		return bad("Both \"username\" and \"password\" are required.")
	}
	if len(*req.Username) > maxUsernameBytes || strings.ContainsRune(*req.Username, 0) {
		return bad("The username is too long or contains invalid characters.")
	}
	if len(*req.Password) > maxPasswordBytes {
		return bad("The password is too long.")
	}
	return *req.Username, *req.Password, true
}

func writeInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error",
		"Sign-in failed because of a server problem. Try again, and check the server logs if it persists.")
}
