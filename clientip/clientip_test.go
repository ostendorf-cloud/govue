// Copyright 2026 Simon Ostendorf
// SPDX-License-Identifier: Apache-2.0

package clientip_test

import (
	"net/http/httptest"
	"net/netip"
	"strconv"
	"strings"
	"testing"

	"github.com/ostendorf-cloud/govue/clientip"
)

func TestResolverResolve(t *testing.T) {
	t.Parallel()

	trusted, err := clientip.ParseCIDRs("10.0.0.0/8, 192.168.1.10, fd00::/8")
	if err != nil {
		t.Fatalf("ParseCIDRs: %v", err)
	}

	for _, tc := range []struct {
		name         string
		trustedProxy bool
		peer         string
		forwardedFor []string
		want         string
	}{
		{
			name:         "no proxies configured ignores the header",
			trustedProxy: false,
			peer:         "203.0.113.5:1234",
			forwardedFor: []string{"198.51.100.1"},
			want:         "203.0.113.5",
		},
		{
			name:         "untrusted peer ignores the header",
			trustedProxy: true,
			peer:         "203.0.113.5:1234",
			forwardedFor: []string{"198.51.100.1"},
			want:         "203.0.113.5",
		},
		{
			name:         "trusted peer without a header",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: nil,
			want:         "10.0.0.2",
		},
		{
			name:         "trusted peer with one hop",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"198.51.100.1"},
			want:         "198.51.100.1",
		},
		{
			name:         "forged entries left of the client are ignored",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"1.1.1.1, 8.8.8.8, 198.51.100.1"},
			want:         "198.51.100.1",
		},
		{
			name:         "a forged trusted address left of the client is ignored",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"10.9.9.9, 198.51.100.1"},
			want:         "198.51.100.1",
		},
		{
			name:         "a chain of several trusted proxies",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"6.6.6.6, 198.51.100.1, 10.0.0.7, 10.0.0.8"},
			want:         "198.51.100.1",
		},
		{
			name:         "a single host prefix is trusted",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"198.51.100.1, 192.168.1.10"},
			want:         "198.51.100.1",
		},
		{
			name:         "a host next to the single host prefix is not",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"198.51.100.1, 192.168.1.11"},
			want:         "192.168.1.11",
		},
		{
			name:         "several header lines are joined",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"6.6.6.6", "198.51.100.1, 10.0.0.7"},
			want:         "198.51.100.1",
		},
		{
			name:         "all entries trusted gives the leftmost one",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"10.0.0.5, 10.0.0.7"},
			want:         "10.0.0.5",
		},
		{
			name:         "a garbage entry stops the walk at the last good hop",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"198.51.100.1, garbage, 10.0.0.7"},
			want:         "10.0.0.7",
		},
		{
			name:         "garbage in the only entry falls back to the peer",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"1.2.3.4, unknown"},
			want:         "10.0.0.2",
		},
		{
			name:         "an empty header value falls back to the peer",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{""},
			want:         "10.0.0.2",
		},
		{
			name:         "an entry carrying a port",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"198.51.100.1:5555"},
			want:         "198.51.100.1",
		},
		{
			name:         "an ipv6 client",
			trustedProxy: true,
			peer:         "[fd00::2]:1234",
			forwardedFor: []string{"2001:db8::1"},
			want:         "2001:db8::1",
		},
		{
			name:         "a bracketed ipv6 entry carrying a port",
			trustedProxy: true,
			peer:         "[fd00::2]:1234",
			forwardedFor: []string{"[2001:db8::1]:443"},
			want:         "2001:db8::1",
		},
		{
			name:         "an ipv4 mapped peer",
			trustedProxy: true,
			peer:         "[::ffff:10.0.0.2]:1234",
			forwardedFor: []string{"198.51.100.1"},
			want:         "198.51.100.1",
		},
		{
			name:         "an ipv4 mapped client",
			trustedProxy: true,
			peer:         "10.0.0.2:1234",
			forwardedFor: []string{"::ffff:198.51.100.1"},
			want:         "198.51.100.1",
		},
		{
			name:         "a zone on the peer is dropped",
			trustedProxy: false,
			peer:         "[fe80::1%eth0]:1234",
			forwardedFor: nil,
			want:         "fe80::1",
		},
		{
			name:         "a peer without a port",
			trustedProxy: false,
			peer:         "203.0.113.5",
			forwardedFor: nil,
			want:         "203.0.113.5",
		},
		{
			name:         "an unparsable peer",
			trustedProxy: false,
			peer:         "not-an-address",
			forwardedFor: nil,
			want:         "invalid IP",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			resolver := clientip.NewResolver(nil)
			if tc.trustedProxy {
				resolver = clientip.NewResolver(trusted)
			}

			req := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
			req.RemoteAddr = tc.peer
			for _, v := range tc.forwardedFor {
				req.Header.Add("X-Forwarded-For", v)
			}

			if got := resolver.Resolve(req).String(); got != tc.want {
				t.Errorf("Resolve() = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestResolverResolveHopCap(t *testing.T) {
	t.Parallel()

	resolver := clientip.NewResolver([]netip.Prefix{netip.MustParsePrefix("10.0.0.0/8")})

	// The client sits behind 40 trusted proxies, each a distinct address
	// under the trusted prefix. The walk gives up after 32 hops, so it
	// never reaches the client and reports the 32nd proxy instead.
	entries := []string{"198.51.100.1"}
	for i := 40; i > 0; i-- {
		entries = append(entries, "10.0.0."+strconv.Itoa(i))
	}

	req := httptest.NewRequestWithContext(t.Context(), "GET", "/", nil)
	req.RemoteAddr = "10.0.0.200:4711"
	req.Header.Set("X-Forwarded-For", strings.Join(entries, ", "))

	const want = "10.0.0.32"
	if got := resolver.Resolve(req).String(); got != want {
		t.Errorf("Resolve() = %s, want the 32nd hop %s", got, want)
	}
}

func TestParseCIDRs(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		raw     string
		want    []string
		wantErr bool
	}{
		{name: "empty string", raw: "", want: nil},
		{name: "only separators and spacing", raw: " , ,", want: nil},
		{name: "a prefix", raw: "10.0.0.0/8", want: []string{"10.0.0.0/8"}},
		{name: "a prefix gets masked", raw: "10.1.2.3/8", want: []string{"10.0.0.0/8"}},
		{name: "a bare address becomes a host prefix", raw: "192.168.1.10", want: []string{"192.168.1.10/32"}},
		{name: "a bare ipv6 address", raw: "fd00::1", want: []string{"fd00::1/128"}},
		{name: "an ipv4 mapped address is unmapped", raw: "::ffff:10.0.0.1", want: []string{"10.0.0.1/32"}},
		{
			name: "a mixed list with spacing",
			raw:  " 10.0.0.0/8 , 192.168.1.10 , fd00::/8 ",
			want: []string{"10.0.0.0/8", "192.168.1.10/32", "fd00::/8"},
		},
		{name: "an invalid prefix length", raw: "10.0.0.0/33", wantErr: true},
		{name: "a hostname", raw: "proxy.internal", wantErr: true},
		{name: "a prefix carrying a port", raw: "10.0.0.1:80/8", wantErr: true},
		{name: "garbage", raw: "???", wantErr: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			got, err := clientip.ParseCIDRs(tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseCIDRs(%q) = %v, want an error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseCIDRs(%q) returned an unexpected error: %v", tc.raw, err)
			}

			if len(got) != len(tc.want) {
				t.Fatalf("ParseCIDRs(%q) = %v, want %v", tc.raw, got, tc.want)
			}
			for i, prefix := range got {
				if prefix.String() != tc.want[i] {
					t.Errorf("prefix %d = %s, want %s", i, prefix, tc.want[i])
				}
			}
		})
	}
}

func TestKey(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name string
		in   netip.Addr
		want string
	}{
		{name: "an ipv4 address keys on itself", in: netip.MustParseAddr("203.0.113.5"), want: "203.0.113.5"},
		{
			name: "an ipv6 address keys on its /64",
			in:   netip.MustParseAddr("2001:db8:1:2:3:4:5:6"),
			want: "2001:db8:1:2::/64",
		},
		{
			name: "a rotated address inside the same /64 keys the same",
			in:   netip.MustParseAddr("2001:db8:1:2:ffff::9999"),
			want: "2001:db8:1:2::/64",
		},
		{
			name: "a different /64 keys differently",
			in:   netip.MustParseAddr("2001:db8:1:3::1"),
			want: "2001:db8:1:3::/64",
		},
		{name: "an invalid address does not panic", in: netip.Addr{}, want: "invalid"},
		{
			name: "an ipv4 mapped address keys on its unmapped form",
			in:   netip.MustParseAddr("::ffff:198.51.100.7"),
			want: "198.51.100.7",
		},
		{
			name: "a different ipv4 mapped address keys distinctly",
			in:   netip.MustParseAddr("::ffff:203.0.113.9"),
			want: "203.0.113.9",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			if got := clientip.Key(tc.in); got != tc.want {
				t.Errorf("Key(%s) = %s, want %s", tc.in, got, tc.want)
			}
		})
	}
}

func TestKeyGroupsOneClientsWholeSlash64(t *testing.T) {
	t.Parallel()

	a := netip.MustParseAddr("2001:db8:1:2:aaaa::1")
	b := netip.MustParseAddr("2001:db8:1:2:bbbb::9")
	if clientip.Key(a) != clientip.Key(b) {
		t.Errorf("Key(%s) = %s, Key(%s) = %s, want the same key inside one /64", a, clientip.Key(a), b, clientip.Key(b))
	}
}
