// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"unicode"
	"unicode/utf8"

	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/store"
)

// SetupStore is the persistence the first-run setup needs; *store.Queries
// implements it.
type SetupStore interface {
	CountAdmins(ctx context.Context) (int64, error)
	CreateLocalUser(ctx context.Context, arg store.CreateLocalUserParams) (store.User, error)
	TouchLastLogin(ctx context.Context, id int64) error
}

// Setup creates the first administrator, guarded by a one-time token that
// exists only in the memory of this process and in the server log.
type Setup struct {
	store    SetupStore
	sessions SessionStarter
	audit    AuditRecorder
	clientIP func(*http.Request) netip.Addr
	limiter  *RateLimiter
	log      *slog.Logger

	// mu serialises setup requests and guards the token state, so two
	// concurrent valid requests cannot create two administrators.
	mu        sync.Mutex
	tokenHash [sha256.Size]byte
	hasToken  bool
}

// NewSetup returns a Setup. Callers should use
// NewRateLimiter(12*time.Second, 5, nil) as limiter, which limits POSTs per
// client address (an IPv4 address or an IPv6 /48). Server-side failures are
// logged to log (nil discards them).
func NewSetup(s SetupStore, sessions SessionStarter, audit AuditRecorder, clientIP func(*http.Request) netip.Addr, limiter *RateLimiter, log *slog.Logger) *Setup {
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Setup{store: s, sessions: sessions, audit: audit, clientIP: clientIP, limiter: limiter, log: log}
}

// PrepareToken returns a new one-time token if no administrator exists, and
// "" otherwise. The caller logs it at start. Only its SHA-256 is kept, and
// only in memory; it stops working once setup completes. It fails if a token
// was already issued by this Setup and no administrator exists. When one
// does (the database was replaced by a restore), the old token is dropped.
func (s *Setup) PrepareToken(ctx context.Context) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.store.CountAdmins(ctx)
	if err != nil {
		return "", fmt.Errorf("count administrators: %w", err)
	}
	if n > 0 {
		s.hasToken = false
		s.tokenHash = [sha256.Size]byte{}
		return "", nil
	}
	if s.hasToken {
		return "", errors.New("a setup token was already issued")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("generate setup token: %w", err)
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.tokenHash = sha256.Sum256([]byte(token))
	s.hasToken = true
	return token, nil
}

var (
	// ErrSetupDone means an administrator exists, so there is no setup.
	ErrSetupDone = errors.New("setup is already complete")
	// ErrInvalidToken means the setup token is wrong.
	ErrInvalidToken = errors.New("the setup token is wrong")
)

// checkRateKey is the rate-limit key of CheckToken. The caller's address is
// not known there, so all checks share one bucket, which is stricter than
// the per-address limit of the setup form.
const checkRateKey = "setup-check"

// CheckToken checks token against the setup token without using it up. It
// returns ErrSetupDone once an administrator exists, ErrInvalidToken if the
// token is wrong or none was issued, and ErrTooManyAttempts when the setup
// rate limit is exceeded. It compares as the setup form does, in constant
// time, and uses the same limiter.
func (s *Setup) CheckToken(ctx context.Context, token string) error {
	if ok, _ := s.limiter.Allow(checkRateKey); !ok {
		return ErrTooManyAttempts
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.store.CountAdmins(ctx)
	if err != nil {
		return fmt.Errorf("count administrators: %w", err)
	}
	if n > 0 {
		return ErrSetupDone
	}
	sum := sha256.Sum256([]byte(token))
	if !s.hasToken || subtle.ConstantTimeCompare(sum[:], s.tokenHash[:]) != 1 {
		return ErrInvalidToken
	}
	return nil
}

// Handler serves GET and POST /api/v1/setup.
func (s *Setup) Handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			s.status(w, r)
		case http.MethodPost:
			s.complete(w, r)
		default:
			w.Header().Set("Allow", "GET, POST")
			writeError(w, http.StatusMethodNotAllowed, "method_not_allowed", "Use GET or POST.")
		}
	})
}

func (s *Setup) status(w http.ResponseWriter, r *http.Request) {
	n, err := s.store.CountAdmins(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "setup: cannot count administrators", "error", err)
		writeSetupInternalError(w)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(map[string]bool{"required": n == 0})
}

type setupRequest struct {
	Token    *string `json:"token"`
	Username *string `json:"username"`
	Password *string `json:"password"`
}

func (s *Setup) complete(w http.ResponseWriter, r *http.Request) {
	ip := s.clientIP(r)
	if ok, wait := s.limiter.Allow(addrKey(ip)); !ok {
		tooManyAttempts(w, wait)
		return
	}
	var req setupRequest
	if !decodeBody(w, r, &req, "Send a JSON object with only \"token\", \"username\" and \"password\" (at most 8 KiB).") {
		return
	}
	if req.Token == nil || req.Username == nil || req.Password == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "\"token\", \"username\" and \"password\" are required.")
		return
	}
	token, username, password := *req.Token, *req.Username, *req.Password

	s.mu.Lock()
	defer s.mu.Unlock()
	n, err := s.store.CountAdmins(r.Context())
	if err != nil {
		s.log.ErrorContext(r.Context(), "setup: cannot count administrators", "error", err)
		writeSetupInternalError(w)
		return
	}
	if n > 0 {
		s.hasToken = false
		writeError(w, http.StatusConflict, "setup_complete", "Setup is already complete. Sign in instead.")
		return
	}
	sum := sha256.Sum256([]byte(token))
	if !s.hasToken || subtle.ConstantTimeCompare(sum[:], s.tokenHash[:]) != 1 {
		writeError(w, http.StatusForbidden, "invalid_setup_token", "The setup token is wrong. Copy it from the server log written at start.")
		return
	}
	if msg := checkUsername(username); msg != "" {
		writeError(w, http.StatusBadRequest, "invalid_request", msg)
		return
	}
	if err := CheckPassword(password, username); err != nil {
		writeError(w, http.StatusBadRequest, "weak_password", err.Error())
		return
	}
	hash, err := HashPassword(r.Context(), password)
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			writeError(w, http.StatusServiceUnavailable, "busy", "The server is busy. Try again in a moment.")
			return
		}
		s.log.ErrorContext(r.Context(), "setup: cannot hash password", "error", err)
		writeSetupInternalError(w)
		return
	}
	user, err := s.store.CreateLocalUser(r.Context(), store.CreateLocalUserParams{
		Username:     username,
		Role:         store.RoleAdmin,
		PasswordHash: pgtype.Text{String: hash, Valid: true},
	})
	if err != nil {
		s.log.ErrorContext(r.Context(), "setup: cannot create administrator", "error", err)
		writeSetupInternalError(w)
		return
	}
	// The token is spent even if the session cannot be started: the account
	// exists, and the user can sign in.
	s.hasToken = false
	s.tokenHash = [sha256.Size]byte{}
	s.audit.Record(r.Context(), "local:"+username, audit.ActionSetupCompleted,
		map[string]any{"username": username, "ip": ip.String()})
	if err := s.sessions.Start(w, r, user.ID); err != nil {
		s.log.ErrorContext(r.Context(), "setup: cannot start session", "error", err)
		writeSetupInternalError(w)
		return
	}
	// Setup signs the administrator in, so it is their first sign-in. A
	// failure here only leaves "never signed in" on the users page.
	if err := s.store.TouchLastLogin(r.Context(), user.ID); err != nil {
		s.log.ErrorContext(r.Context(), "setup: cannot record sign-in time", "error", err)
	}
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(http.StatusNoContent)
}

// checkUsername returns a message for a username that is not 1 to 200 bytes
// of valid text without control characters, or "".
func checkUsername(username string) string {
	if username == "" || len(username) > maxUsernameBytes {
		return "The username must be 1 to 200 bytes long."
	}
	if !utf8.ValidString(username) || strings.ContainsFunc(username, unicode.IsControl) {
		return "The username must not contain control characters."
	}
	return ""
}

// decodeBody reads one JSON object of at most 8 KiB from the request into v,
// rejecting unknown fields and trailing data. It writes a 400 itself on
// failure.
func decodeBody(w http.ResponseWriter, r *http.Request, v any, shape string) bool {
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, maxLoginBody))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_request", shape)
		return false
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		writeError(w, http.StatusBadRequest, "invalid_request", "Send a single JSON object and nothing after it.")
		return false
	}
	return true
}

func writeSetupInternalError(w http.ResponseWriter) {
	writeError(w, http.StatusInternalServerError, "internal_error",
		"Setup failed because of a server problem. Try again, and check the server logs if it persists.")
}
