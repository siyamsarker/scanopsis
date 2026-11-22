// Copyright 2026 The Scanopsis Authors
// SPDX-License-Identifier: Apache-2.0

package store

import "testing"

func TestPoolConfigDefaults(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name        string
		url         string
		wantMax     int32
		wantTimeout string // "" means the parameter must be absent
		wantOptions string
	}{
		{"url without parameters", "postgres://u:p@h:5432/db?sslmode=disable", DefaultMaxConns, "30000", ""},
		{"postgresql scheme", "postgresql://u@h/db", DefaultMaxConns, "30000", ""},
		{"key value form", "host=h user=u dbname=db", DefaultMaxConns, "30000", ""},
		{"pool size from the url", "postgres://u@h/db?pool_max_conns=3", 3, "30000", ""},
		{"pool size from key value form", "host=h dbname=db pool_max_conns=40", 40, "30000", ""},
		{"statement timeout from the url", "postgres://u@h/db?statement_timeout=120000", DefaultMaxConns, "120000", ""},
		{"statement timeout disabled", "postgres://u@h/db?statement_timeout=0", DefaultMaxConns, "0", ""},
		{"statement timeout in options", "postgres://u@h/db?options=-c%20statement_timeout%3D5000", DefaultMaxConns, "", "-c statement_timeout=5000"},
		{"both", "postgres://u@h/db?pool_max_conns=7&statement_timeout=1000", 7, "1000", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cfg, err := poolConfig(tt.url)
			if err != nil {
				t.Fatal(err)
			}
			if cfg.MaxConns != tt.wantMax {
				t.Errorf("MaxConns = %d, want %d", cfg.MaxConns, tt.wantMax)
			}
			params := cfg.ConnConfig.RuntimeParams
			got, set := params["statement_timeout"]
			if tt.wantTimeout == "" && set || tt.wantTimeout != "" && got != tt.wantTimeout {
				t.Errorf("statement_timeout = %q (set %v), want %q", got, set, tt.wantTimeout)
			}
			if params["options"] != tt.wantOptions {
				t.Errorf("options = %q, want %q", params["options"], tt.wantOptions)
			}
		})
	}
}

func TestPoolConfigRejectsBadURL(t *testing.T) {
	t.Parallel()
	if _, err := poolConfig("postgres://u@h/db?pool_max_conns=many"); err == nil {
		t.Error("a non-numeric pool_max_conns was accepted")
	}
}
