// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

// Package clientip resolves the address a request actually came from, behind
// whatever reverse proxies sit in front of the application.
//
// The TCP peer is always the last hop, which is the proxy, not the client.
// The client address lives in X-Forwarded-For instead, appended to by every
// proxy the request passed through, and a request that never touched a proxy
// can set that header to anything it likes. Trusting it blindly lets a client
// impersonate another address for an access log or a rate limit; trusting the
// TCP peer alone attributes every request to the proxy. This package resolves
// the address between those two mistakes: the header counts only once the
// peer is a proxy this application was told to trust.
package clientip

import (
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"strings"
)

// maxProxyHops bounds how many X-Forwarded-For entries Resolve inspects, so a
// request with a forged header carrying thousands of entries cannot make
// resolution expensive.
const maxProxyHops = 32

// forwardedForHeader is the header a trusted proxy appends the client address
// to.
const forwardedForHeader = "X-Forwarded-For"

// Resolver resolves the client address of a request, trusting
// X-Forwarded-For only from the proxies it was built with.
type Resolver struct {
	trusted []netip.Prefix
}

// NewResolver returns a Resolver that trusts X-Forwarded-For from a TCP peer
// inside one of the given prefixes. With no prefixes, Resolve always returns
// the TCP peer: the secure default is to trust nothing, not to trust
// everything.
func NewResolver(trusted []netip.Prefix) *Resolver {
	return &Resolver{trusted: trusted}
}

// Resolve returns the client address of req: the TCP peer, unless the peer is
// a trusted proxy. In that case it walks X-Forwarded-For from the right,
// where each trusted proxy on the path appended the address it received the
// request from, and stops at the first entry that is not itself a trusted
// proxy; that entry is the client. Entries to the left of it came from
// whoever made the original request and may be forged, so they never decide
// the result. The walk never looks past maxProxyHops entries.
//
// Resolve falls back to the TCP peer whenever the header is missing, empty,
// or holds an entry it cannot parse as an address.
func (r *Resolver) Resolve(req *http.Request) netip.Addr {
	peer, ok := parseHostAddr(req.RemoteAddr)
	if !ok || !r.trusts(peer) {
		return peer
	}

	var entries []string
	for _, line := range req.Header.Values(forwardedForHeader) {
		entries = append(entries, strings.Split(line, ",")...)
	}

	client := peer
	for i, hops := len(entries)-1, 0; i >= 0 && hops < maxProxyHops; i, hops = i-1, hops+1 {
		addr, ok := parseHostAddr(entries[i])
		if !ok {
			break
		}
		client = addr
		if !r.trusts(addr) {
			break
		}
	}
	return client
}

// trusts reports whether addr falls inside one of the trusted prefixes.
func (r *Resolver) trusts(addr netip.Addr) bool {
	for _, prefix := range r.trusted {
		if prefix.Contains(addr) {
			return true
		}
	}
	return false
}

// parseHostAddr parses one address as it appears in RemoteAddr or in an
// X-Forwarded-For entry: optionally bracketed, with an optional port, and
// with surrounding whitespace. The result is normalized to drop a zone and
// to unwrap an IPv4 address mapped into IPv6, so the same client address
// compares equal regardless of which form a proxy chose to write.
func parseHostAddr(s string) (netip.Addr, bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return netip.Addr{}, false
	}

	if addrPort, err := netip.ParseAddrPort(s); err == nil {
		return normalize(addrPort.Addr()), true
	}
	if host, _, err := net.SplitHostPort(s); err == nil {
		s = host
	}

	addr, err := netip.ParseAddr(strings.Trim(s, "[]"))
	if err != nil {
		return netip.Addr{}, false
	}
	return normalize(addr), true
}

// normalize drops a zone and unwraps an IPv4 address mapped into IPv6.
func normalize(addr netip.Addr) netip.Addr {
	return addr.Unmap().WithZone("")
}

// ParseCIDRs parses raw, a comma separated list of CIDR prefixes, into the
// slice Resolver expects as its trusted proxies. An entry without a slash is
// a bare address, parsed as that one host. Surrounding whitespace around an
// entry and empty entries are ignored, so a trailing comma or extra spacing
// in a configured environment variable does not turn into an error.
//
// ParseCIDRs is what an application parses the proxy list from, typically
// read from the environment once at startup.
func ParseCIDRs(raw string) ([]netip.Prefix, error) {
	var prefixes []netip.Prefix
	for _, entry := range strings.Split(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		if strings.Contains(entry, "/") {
			prefix, err := netip.ParsePrefix(entry)
			if err != nil {
				return nil, fmt.Errorf("clientip: trusted proxy %q: %w", entry, err)
			}
			prefixes = append(prefixes, prefix.Masked())
			continue
		}

		addr, err := netip.ParseAddr(entry)
		if err != nil {
			return nil, fmt.Errorf("clientip: trusted proxy %q: %w", entry, err)
		}
		addr = normalize(addr)
		prefixes = append(prefixes, netip.PrefixFrom(addr, addr.BitLen()))
	}
	return prefixes, nil
}

// Key returns a stable, normalized string key for addr, for grouping by
// client in a rate limiter or an access log. An IPv4 address keys on its
// plain string. An IPv6 address keys on its /64: a single client is commonly
// handed a whole /64 by its network and may rotate the address inside it per
// request, and grouping by /64 keeps such a client under one key instead of
// resetting state on every rotation. An invalid address, which Resolve never
// returns but a caller could still construct, keys on a fixed placeholder
// rather than panicking.
func Key(addr netip.Addr) string {
	addr = addr.Unmap()
	if !addr.IsValid() {
		return "invalid"
	}
	if addr.Is6() {
		prefix, err := addr.Prefix(64)
		if err != nil {
			return "invalid"
		}
		return prefix.String()
	}
	return addr.String()
}
