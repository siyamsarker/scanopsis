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
	limiter  *RateLimiter
	log      *slog.Logger
}

// NewLocalLogin returns the handler for POST /api/v1/auth/local/login.
// Callers should use NewRateLimiter(12*time.Second, 10, nil) as limiter: ten
// attempts per client address, then five per minute.
// Server-side failures are logged to log (nil discards them).
func NewLocalLogin(users LocalUserStore, sessions SessionStarter, audit AuditRecorder, clientIP func(*http.Request) netip.Addr, limiter *RateLimiter, log *slog.Logger) http.Handler {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &localLogin{users: users, sessions: sessions, audit: audit, clientIP: clientIP, limiter: limiter, log: log}
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
	if ok, wait := h.limiter.Allow(ip.String()); !ok {
		secs := max(1, int(math.Ceil(wait.Seconds())))
		w.Header().Set("Retry-After", strconv.Itoa(secs))
		writeError(w, http.StatusTooManyRequests, "too_many_attempts",
			"Too many sign-in attempts. Wait "+strconv.Itoa(secs)+" seconds and try again.")
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
	match, verr := verifyPassword(hash, password)
	if !found || !user.PasswordHash.Valid || verr != nil || !match {
		h.audit.Record(r.Context(), username, audit.ActionLoginFailed, h.detail(username, ip))
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
	h.audit.Record(r.Context(), username, audit.ActionLoginSucceeded, h.detail(username, ip))
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
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
