// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package ingest

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

const openWarning = "ingest accepts uploads from every project of the GitLab instance; set ingest.allowed_namespaces to restrict it"

type plainSettings struct{ snap settings.Snapshot }

func (p plainSettings) Current() settings.Snapshot { return p.snap }

func newOpenIngest(src SettingsSource) (*Ingest, *bytes.Buffer) {
	logs := &bytes.Buffer{}
	return New(nil, src, &recorder{}, Options{Logger: slog.New(slog.NewTextHandler(logs, nil))}), logs
}

func TestIngestWarnsWhenOpenToEveryProject(t *testing.T) {
	cfg := &fakeSettings{snap: settings.Snapshot{GitLabURL: "https://gitlab.example.com"}}
	_, logs := newOpenIngest(cfg)
	count := func() int { return strings.Count(logs.String(), openWarning) }
	if count() != 1 || !strings.Contains(logs.String(), "level=WARN") {
		t.Fatalf("start-up: %s", logs)
	}
	// Unrelated changes do not repeat it.
	cfg.change(func(s *settings.Snapshot) { s.IngestProtectedRefsOnly = true })
	cfg.change(func(s *settings.Snapshot) { s.GitLabURL = "https://gitlab2.example.com" })
	if count() != 1 {
		t.Errorf("%d warnings after unrelated changes", count())
	}
	// Restricting is silent; opening again warns again, once.
	cfg.change(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = []string{"platform"} })
	if count() != 1 {
		t.Errorf("%d warnings after restricting", count())
	}
	cfg.change(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = nil })
	cfg.change(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = nil })
	if count() != 2 {
		t.Errorf("%d warnings after opening again", count())
	}
}

func TestIngestDoesNotWarnWhenRestrictedOrUnconfigured(t *testing.T) {
	cfg := &fakeSettings{snap: settings.Snapshot{GitLabURL: "https://gitlab.example.com", IngestAllowedNamespaces: []string{"platform"}}}
	_, logs := newOpenIngest(cfg)
	if strings.Contains(logs.String(), openWarning) {
		t.Errorf("warning with namespaces set: %s", logs)
	}
	cfg.change(func(s *settings.Snapshot) { s.GitLabURL = ""; s.IngestAllowedNamespaces = nil })
	if strings.Contains(logs.String(), openWarning) {
		t.Errorf("warning with GitLab not configured: %s", logs)
	}
}

// A settings source without OnChange is checked when an upload arrives.
func TestIngestWarnsOnceWithoutChangeNotifications(t *testing.T) {
	in, logs := newOpenIngest(plainSettings{settings.Snapshot{GitLabURL: "https://gitlab.example.com"}})
	for range 3 {
		in.checkOpen(in.cfg.Current())
	}
	if n := strings.Count(logs.String(), openWarning); n != 1 {
		t.Errorf("%d warnings: %s", n, logs)
	}
}

func TestIngestUploadsDoNotRepeatTheOpenWarning(t *testing.T) {
	e := newHEnv(t, nil)
	e.cfg.change(func(s *settings.Snapshot) { s.IngestAllowedNamespaces = nil })
	for range 2 {
		e.do(req{body: []byte(`{"SchemaVersion":2,"ArtifactName":"a"}`)})
	}
	if n := strings.Count(e.logs.String(), openWarning); n != 0 {
		t.Errorf("%d warnings for unchanged settings: %s", n, e.logs)
	}
}
