// Copyright 2026 Siyam Sarker
// SPDX-License-Identifier: Apache-2.0

// Package clientip determines the IP address of the client that sent a
// request, honouring X-Forwarded-For only from trusted reverse proxies.
package clientip

import (
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// Resolver resolves client addresses. The zero value trusts no proxy.
type Resolver struct {
	trusted []netip.Prefix
}

// New returns a Resolver that trusts X-Forwarded-For entries added by the
// given proxy ranges.
func New(trusted []netip.Prefix) Resolver { return Resolver{trusted: trusted} }

// IP returns the client address. If the direct peer is a trusted proxy, it
// walks X-Forwarded-For from right to left and returns the first address
// that is not a trusted proxy; entries further left are client-controlled
// and ignored. It returns the zero Addr only if RemoteAddr is unparsable.
func (res Resolver) IP(r *http.Request) netip.Addr {
	peer := parseAddr(r.RemoteAddr)
	if !peer.IsValid() || !res.isTrusted(peer) {
		return peer
	}
	var hops []string
	for _, h := range r.Header.Values("X-Forwarded-For") {
		hops = append(hops, strings.Split(h, ",")...)
	}
	client := peer
	for i := len(hops) - 1; i >= 0; i-- {
		addr := parseAddr(strings.TrimSpace(hops[i]))
		if !addr.IsValid() {
			break
		}
		client = addr
		if !res.isTrusted(addr) {
			break
		}
	}
	return client
}

func (res Resolver) isTrusted(a netip.Addr) bool {
	for _, p := range res.trusted {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

func parseAddr(s string) netip.Addr {
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Addr{}
	}
	return a.Unmap()
}
