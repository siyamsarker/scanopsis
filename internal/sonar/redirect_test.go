// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package sonar

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/siyamsarker/scanopsis/internal/settings"
)

func TestRedirectsToOtherHostsAreRefused(t *testing.T) {
	var leaked int
	other := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "" {
			leaked++
		}
	}))
	defer other.Close()
	same := ""
	redir := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/server/version":
			http.Redirect(w, r, other.URL+"/api/server/version", http.StatusFound)
		case "/api/measures/component":
			http.Redirect(w, r, same+"/ok", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, same+"/loop", http.StatusFound)
		default:
			_, _ = w.Write([]byte("26.9"))
		}
	}))
	defer redir.Close()
	same = redir.URL
	c := &client{http: sameHostClient(&http.Client{}), base: redir.URL, token: testToken}
	ctx := context.Background()

	if _, err := c.serverVersion(ctx); err == nil || strings.Contains(err.Error(), testToken) {
		t.Errorf("cross-host redirect: %v", err)
	}
	if leaked != 0 {
		t.Errorf("the token was sent to another host %d times", leaked)
	}
	if _, err := c.get(ctx, "api/measures/component", nil); err != nil {
		t.Errorf("same-host redirect: %v", err)
	}
	if _, err := c.get(ctx, "loop", nil); err == nil {
		t.Error("a redirect loop must stop")
	}
	custom := sameHostClient(&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("custom") }})
	if _, err := (&client{http: custom, base: redir.URL}).get(ctx, "api/measures/component", nil); err == nil {
		t.Error("custom CheckRedirect ignored")
	}
}

func TestPlainHTTPWarnsOncePerConfiguration(t *testing.T) {
	var buf bytes.Buffer
	s := New(nil, &fixedSettings{}, Options{Logger: slog.New(slog.NewTextHandler(&buf, nil))})
	plain := settings.Snapshot{SonarURL: "http://sonarqube.internal:9000", SonarToken: "t0k3n"}
	s.client(plain)
	s.client(plain)
	if n := strings.Count(buf.String(), "the SonarQube token is sent without TLS"); n != 1 {
		t.Errorf("%d warnings for an unchanged URL\n%s", n, buf.String())
	}
	s.client(settings.Snapshot{SonarURL: "http://other.internal:9000", SonarToken: "t0k3n"})
	if n := strings.Count(buf.String(), "without TLS"); n != 2 {
		t.Errorf("%d warnings after the URL changed", n)
	}
	buf.Reset()
	s.client(settings.Snapshot{SonarURL: "https://sonarqube.example.com", SonarToken: "t0k3n"})
	if buf.Len() != 0 {
		t.Errorf("https must not warn: %s", buf.String())
	}
	s.client(plain)
	if n := strings.Count(buf.String(), "without TLS"); n != 1 {
		t.Errorf("%d warnings after switching back to http", n)
	}
	if strings.Contains(buf.String(), "t0k3n") {
		t.Error("the log must not contain the token")
	}
}
