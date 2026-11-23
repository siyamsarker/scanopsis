// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

package clientip

import (
	"net/http/httptest"
	"net/netip"
	"testing"
)

func TestIP(t *testing.T) {
	trusted := New([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8"), netip.MustParsePrefix("fd00::/8")})
	tests := []struct {
		name   string
		res    Resolver
		remote string
		xff    []string
		want   string
	}{
		{"no proxy trusted ignores header", Resolver{}, "203.0.113.5:1234", []string{"198.51.100.1"}, "203.0.113.5"},
		{"untrusted peer ignores header", trusted, "203.0.113.5:1234", []string{"198.51.100.1"}, "203.0.113.5"},
		{"trusted peer without header", trusted, "10.0.0.2:1234", nil, "10.0.0.2"},
		{"trusted peer uses header", trusted, "10.0.0.2:1234", []string{"198.51.100.1"}, "198.51.100.1"},
		{"spoofed left entries ignored", trusted, "10.0.0.2:1234", []string{"1.1.1.1, 198.51.100.1"}, "198.51.100.1"},
		{"chain of trusted proxies", trusted, "10.0.0.2:1234", []string{"198.51.100.1, 10.0.0.9"}, "198.51.100.1"},
		{"multiple header lines", trusted, "10.0.0.2:1234", []string{"1.1.1.1", "198.51.100.1, 10.0.0.9"}, "198.51.100.1"},
		{"all hops trusted", trusted, "10.0.0.2:1234", []string{"10.0.0.7, 10.0.0.9"}, "10.0.0.7"},
		{"garbage stops the walk", trusted, "10.0.0.2:1234", []string{"198.51.100.1, not-an-ip"}, "10.0.0.2"},
		{"ipv6 and mapped ipv4", trusted, "[fd00::1]:443", []string{"::ffff:198.51.100.7"}, "198.51.100.7"},
		{"unparsable remote addr", trusted, "bogus", nil, "invalid IP"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			r.RemoteAddr = tt.remote
			for _, v := range tt.xff {
				r.Header.Add("X-Forwarded-For", v)
			}
			if got := tt.res.IP(r).String(); got != tt.want {
				t.Fatalf("IP() = %s, want %s", got, tt.want)
			}
		})
	}
}
