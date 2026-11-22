// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgtype"

	"github.com/siyamsarker/scanopsis/internal/audit"
	"github.com/siyamsarker/scanopsis/internal/auth"
	"github.com/siyamsarker/scanopsis/internal/ingest"
	"github.com/siyamsarker/scanopsis/internal/store"
)

const adminUsage = `Usage: scanopsis admin <command>

Administration commands that run on the server, against the database. They
read only SCANOPSIS_DATABASE_URL (or SCANOPSIS_DATABASE_URL_FILE) from the
environment, and the server must have been started once so the schema exists.

Commands:
  reset-password <username>
      Set a new temporary password for a local account and sign it out
      everywhere. The temporary password is printed once; the user must
      choose a new one at the next sign-in.

  reset-2fa <local:username|gitlab:username>
      Remove the two-factor enrolment and recovery codes of an account and
      sign it out everywhere. The user enrols again at the next sign-in.

  export-report <scan-id>
      Write the original Trivy report of a scan to standard output as JSON,
      for example to check it with "trivy convert". The scan ID is the number
      in the scan's address or in the API. The report is cleared by data
      retention 30 days after the scan unless the scan is current; the
      command then fails. In the container:
      docker compose exec scanopsis /scanopsis admin export-report 42 > report.json

Exit status: 0 on success, 1 on failure, 2 on a usage error.
`

// tempPasswordAlphabet has no look-alike characters (0/O, 1/l/I).
const (
	tempPasswordAlphabet = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKMNPQRSTUVWXYZ23456789"
	tempPasswordLength   = 20
	userPageSize         = 500
	actorCLI             = "cli"
	actionMFAReset       = "mfa.reset"
)

// adminBackend is what the commands need from the database; runAdmin builds
// it from a connection pool and tests use fakes.
type adminBackend struct {
	localUser func(ctx context.Context, username string) (store.User, error)
	listUsers func(ctx context.Context, afterID int64, limit int32) ([]store.User, error)
	// setPassword stores the hash with must_change_password set and ends all
	// sessions of the user, in one transaction.
	setPassword func(ctx context.Context, id int64, hash string) error
	resetMFA    func(ctx context.Context, id int64) error
	// rawReport returns the stored, gzip-compressed report of a scan; empty
	// when it was cleared, pgx.ErrNoRows when the scan does not exist.
	rawReport func(ctx context.Context, id int64) ([]byte, error)
	audit     auth.AuditRecorder
}

// runAdmin runs "scanopsis admin ..." and returns the process exit status.
func runAdmin(ctx context.Context, args []string, stdout, stderr io.Writer, getenv func(string) string) int {
	if len(args) == 0 {
		_, _ = io.WriteString(stderr, adminUsage)
		return 2
	}
	switch args[0] {
	case "-h", "-help", "--help", "help":
		_, _ = io.WriteString(stdout, adminUsage)
		return 0
	}

	// Check the arguments before touching the database.
	var err error
	var exec func(context.Context, *adminBackend, io.Writer) error
	switch cmd, rest := args[0], args[1:]; cmd {
	case "reset-password":
		if len(rest) != 1 || rest[0] == "" || strings.HasPrefix(rest[0], "-") {
			return usageError(stderr, "reset-password needs exactly one argument: the username of a local account.")
		}
		exec = func(ctx context.Context, b *adminBackend, out io.Writer) error {
			return b.resetPassword(ctx, rest[0], out)
		}
	case "reset-2fa":
		if len(rest) != 1 {
			return usageError(stderr, "reset-2fa needs exactly one argument: local:<username> or gitlab:<username>.")
		}
		source, username, ok := parseAccount(rest[0])
		if !ok {
			return usageError(stderr, "the account must be written local:<username> or gitlab:<username>.")
		}
		exec = func(ctx context.Context, b *adminBackend, out io.Writer) error {
			return b.reset2FA(ctx, source, username, out)
		}
	case "export-report":
		var id int64
		if len(rest) == 1 {
			id, err = strconv.ParseInt(rest[0], 10, 64)
		}
		if len(rest) != 1 || err != nil || id <= 0 {
			return usageError(stderr, "export-report needs exactly one argument: the ID of a scan, a positive number.")
		}
		exec = func(ctx context.Context, b *adminBackend, out io.Writer) error {
			return b.exportReport(ctx, id, out)
		}
	default:
		return usageError(stderr, fmt.Sprintf("unknown command %q.", args[0]))
	}

	url, err := databaseURL(getenv)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scanopsis admin: %v\n", err)
		return 1
	}
	openCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	pool, err := store.Open(openCtx, url)
	if err != nil {
		_, _ = fmt.Fprintf(stderr, "scanopsis admin: %v\n", err)
		return 1
	}
	defer pool.Close()
	q := store.New(pool)
	b := &adminBackend{
		localUser: q.GetLocalUserByUsername,
		listUsers: func(ctx context.Context, afterID int64, limit int32) ([]store.User, error) {
			return q.ListUsers(ctx, store.ListUsersParams{AfterID: afterID, MaxRows: limit})
		},
		setPassword: func(ctx context.Context, id int64, hash string) error {
			return pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
				tq := store.New(tx)
				err := tq.SetPassword(ctx, store.SetPasswordParams{
					ID: id, PasswordHash: pgtype.Text{String: hash, Valid: true}, MustChange: true,
				})
				if err != nil {
					return err
				}
				if err := tq.ResetMFAFailures(ctx, id); err != nil {
					return err
				}
				return tq.DeleteUserSessions(ctx, id)
			})
		},
		resetMFA:  func(ctx context.Context, id int64) error { return store.ResetMFA(ctx, pool, id) },
		rawReport: q.ScanRawReport,
		audit:     audit.New(q, slog.New(slog.NewTextHandler(stderr, nil))),
	}
	if err := exec(ctx, b, stdout); err != nil {
		_, _ = fmt.Fprintf(stderr, "scanopsis admin: %s\n", describeAdminError(err))
		return 1
	}
	return 0
}

func usageError(stderr io.Writer, msg string) int {
	_, _ = fmt.Fprintf(stderr, "scanopsis admin: %s\nRun \"scanopsis admin -h\" for help.\n", msg)
	return 2
}

// parseAccount splits "local:name" or "gitlab:name".
func parseAccount(s string) (source, username string, ok bool) {
	source, username, ok = strings.Cut(s, ":")
	if !ok || username == "" || (source != auth.SourceLocal && source != auth.SourceGitLab) {
		return "", "", false
	}
	return source, username, true
}

// databaseURL reads the connection string the way the server does: from
// SCANOPSIS_DATABASE_URL, or from the file named by SCANOPSIS_DATABASE_URL_FILE.
func databaseURL(getenv func(string) string) (string, error) {
	const name = "SCANOPSIS_DATABASE_URL"
	v, path := getenv(name), strings.TrimSpace(getenv(name+"_FILE"))
	switch {
	case v != "" && path != "":
		return "", fmt.Errorf("%s and %s_FILE are both set; set only one", name, name)
	case path != "":
		b, err := os.ReadFile(path) //nolint:gosec // G304: the operator names this file, as for the server.
		if err != nil {
			return "", fmt.Errorf("cannot read %s_FILE: %w", name, err)
		}
		v = strings.TrimRight(string(b), "\r\n")
	}
	if v == "" {
		return "", fmt.Errorf("%s (or %s_FILE) is not set", name, name)
	}
	return v, nil
}

// describeAdminError turns a database error into a message for the operator.
func describeAdminError(err error) string {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) && pgErr.Code == "42P01" { // undefined_table
		return "the database has no schema yet: run the server once first so it can create it."
	}
	return err.Error()
}

func (b *adminBackend) resetPassword(ctx context.Context, username string, stdout io.Writer) error {
	user, err := b.localUser(ctx, username)
	if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
		return fmt.Errorf("there is no local account named %q", username)
	}
	if err != nil {
		return fmt.Errorf("look up the account: %w", err)
	}
	temp, err := newTempPassword()
	if err != nil {
		return err
	}
	hash, err := auth.HashPassword(ctx, temp)
	if err != nil {
		return fmt.Errorf("hash the password: %w", err)
	}
	if err := b.setPassword(ctx, user.ID, hash); err != nil {
		return fmt.Errorf("save the password: %w", err)
	}
	b.audit.Record(ctx, actorCLI, audit.ActionPasswordReset, map[string]any{"username": user.Username})
	_, _ = fmt.Fprintf(stdout, "The temporary password for the local account %q is:\n\n%s\n\n"+
		"It is shown only now and is not stored anywhere. The account was signed out everywhere, "+
		"and the temporary password must be changed at the next sign-in.\n", user.Username, temp)
	return nil
}

func (b *adminBackend) reset2FA(ctx context.Context, source, username string, stdout io.Writer) error {
	user, err := b.findUser(ctx, source, username)
	if err != nil {
		return err
	}
	if err := b.resetMFA(ctx, user.ID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return fmt.Errorf("the account %s:%s no longer exists", source, username)
		}
		return fmt.Errorf("reset two-factor authentication: %w", err)
	}
	b.audit.Record(ctx, actorCLI, actionMFAReset, map[string]any{"username": user.Username, "source": user.Source})
	_, _ = fmt.Fprintf(stdout, "Two-factor authentication was reset for %s:%s. The account was signed out everywhere "+
		"and enrols again at the next sign-in.\n", source, user.Username)
	return nil
}

// exportReport writes the decompressed original report of a scan to stdout.
// Nothing is written unless the report can be read.
func (b *adminBackend) exportReport(ctx context.Context, id int64, stdout io.Writer) error {
	raw, err := b.rawReport(ctx, id)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("there is no scan with ID %d", id)
	}
	if err != nil {
		return fmt.Errorf("read the report: %w", err)
	}
	if len(raw) == 0 {
		return fmt.Errorf("the original report of scan %d was cleared by data retention, 30 days after the scan; "+
			"only the reports of scans that are current are kept", id)
	}
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	if err != nil {
		return fmt.Errorf("the stored report of scan %d is not readable: %w", id, err)
	}
	// Decompress fully before writing, so a damaged report leaves stdout empty.
	// A report was limited to this size when it was uploaded.
	var out bytes.Buffer
	n, err := io.Copy(&out, io.LimitReader(zr, ingest.DefaultMaxReportBytes+1))
	if err != nil {
		return fmt.Errorf("the stored report of scan %d is not readable: %w", id, err)
	}
	if n > ingest.DefaultMaxReportBytes {
		return fmt.Errorf("the stored report of scan %d is larger than the upload limit of %d bytes", id, ingest.DefaultMaxReportBytes)
	}
	if _, err := out.WriteTo(stdout); err != nil {
		return fmt.Errorf("write the report: %w", err)
	}
	// Only the ID is recorded, never the content.
	b.audit.Record(ctx, actorCLI, audit.ActionReportExported, map[string]any{"scan_id": id})
	return nil
}

func (b *adminBackend) findUser(ctx context.Context, source, username string) (store.User, error) {
	notFound := fmt.Errorf("there is no %s account named %q", source, username)
	if source == auth.SourceLocal {
		u, err := b.localUser(ctx, username)
		if errors.Is(err, pgx.ErrNoRows) || errors.Is(err, store.ErrNotFound) {
			return store.User{}, notFound
		}
		if err != nil {
			return store.User{}, fmt.Errorf("look up the account: %w", err)
		}
		return u, nil
	}
	// There is no query by GitLab username; the user table is small and this
	// is an operator command, so page through it.
	var after int64
	for {
		page, err := b.listUsers(ctx, after, userPageSize)
		if err != nil {
			return store.User{}, fmt.Errorf("look up the account: %w", err)
		}
		for _, u := range page {
			if u.Source == source && u.Username == username {
				return u, nil
			}
		}
		if len(page) < userPageSize {
			return store.User{}, notFound
		}
		after = page[len(page)-1].ID
	}
}

// newTempPassword returns tempPasswordLength characters from
// tempPasswordAlphabet, drawn without modulo bias.
func newTempPassword() (string, error) {
	const limit = 256 - 256%len(tempPasswordAlphabet) // bytes at or above this are rejected
	out := make([]byte, 0, tempPasswordLength)
	buf := make([]byte, 2*tempPasswordLength)
	for len(out) < tempPasswordLength {
		if _, err := rand.Read(buf); err != nil {
			return "", fmt.Errorf("generate the password: %w", err)
		}
		for _, c := range buf {
			if int(c) < limit && len(out) < tempPasswordLength {
				out = append(out, tempPasswordAlphabet[int(c)%len(tempPasswordAlphabet)])
			}
		}
	}
	return string(out), nil
}
