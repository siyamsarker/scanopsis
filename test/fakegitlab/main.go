// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

// Command fakegitlab runs a fake GitLab OpenID Connect provider for
// end-to-end tests. It must never be exposed outside a test environment.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"time"

	"github.com/siyamsarker/scanopsis/internal/testutil/fakegitlab"
)

type userJSON struct {
	ID       int64    `json:"ID"`
	Username string   `json:"Username"`
	Name     string   `json:"Name"`
	Email    string   `json:"Email"`
	Groups   []string `json:"Groups"`
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func main() {
	if err := run(); err != nil {
		slog.Error("fakegitlab failed", "error", err)
		os.Exit(1)
	}
}

func run() error {
	log := slog.New(slog.NewTextHandler(os.Stderr, nil))
	listen := flag.String("listen", envOr("FAKEGITLAB_LISTEN", ":9000"), "address to listen on")
	issuer := flag.String("issuer", envOr("FAKEGITLAB_ISSUER", "http://fake-gitlab:9000"), "public issuer URL")
	clientID := flag.String("client-id", os.Getenv("FAKEGITLAB_CLIENT_ID"), "OAuth client ID")
	clientSecret := flag.String("client-secret", os.Getenv("FAKEGITLAB_CLIENT_SECRET"), "OAuth client secret")
	redirectURI := flag.String("redirect-uri", os.Getenv("FAKEGITLAB_REDIRECT_URI"), "OAuth redirect URI")
	usersFile := flag.String("users", os.Getenv("FAKEGITLAB_USERS"), "JSON file with an array of users")
	flag.Parse()

	if *clientID == "" || *clientSecret == "" || *redirectURI == "" {
		return fmt.Errorf("-client-id, -client-secret and -redirect-uri are required")
	}
	srv, err := fakegitlab.New(*issuer)
	if err != nil {
		return err
	}
	srv.AddClient(*clientID, *clientSecret, *redirectURI)

	if *usersFile != "" {
		b, err := os.ReadFile(*usersFile) //nolint:gosec // path chosen by the operator running the fake
		if err != nil {
			return err
		}
		var users []userJSON
		if err := json.Unmarshal(b, &users); err != nil {
			return fmt.Errorf("parse %s: %w", *usersFile, err)
		}
		for _, u := range users {
			srv.AddUser(fakegitlab.User(u))
		}
		log.Info("loaded users", "count", len(users))
	}

	httpSrv := &http.Server{
		Addr:              *listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	log.Info("fake GitLab listening", "addr", *listen, "issuer", *issuer)
	return httpSrv.ListenAndServe()
}
