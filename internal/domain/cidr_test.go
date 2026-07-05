// Copyright (c) PRO-Robotech
// SPDX-License-Identifier: BUSL-1.1

package domain_test

import (
	"errors"
	"net/netip"
	"testing"

	"github.com/PRO-Robotech/kacho-vpc/internal/domain"
)

func mustPrefix(t *testing.T, s string) netip.Prefix {
	t.Helper()
	p, err := netip.ParsePrefix(s)
	if err != nil {
		t.Fatalf("bad prefix %q: %v", s, err)
	}
	return p
}

func TestUsableIPv4Count(t *testing.T) {
	cases := []struct {
		cidr string
		want int64
	}{
		{"10.0.0.0/24", 254},
		{"10.0.0.0/28", 14},
		{"10.0.0.0/31", 2},
		{"10.0.0.0/32", 1},
		{"10.0.0.0/30", 2},
		{"0.0.0.0/0", 0}, // hostBits>=31 → 0
		{"garbage", 0},
		{"fd00::/64", 0}, // not ipv4
		{"  10.0.0.0/24  ", 254},
	}
	for _, tc := range cases {
		if got := domain.UsableIPv4Count(tc.cidr); got != tc.want {
			t.Errorf("UsableIPv4Count(%q) = %d, want %d", tc.cidr, got, tc.want)
		}
	}
}

func TestUsableIPv4Sweep(t *testing.T) {
	// /30 has usable {.1,.2}; sweep excludes .0 network and .3 broadcast.
	got := domain.UsableIPv4Sweep(mustPrefix(t, "10.0.0.0/30"), 24)
	want := []string{"10.0.0.1", "10.0.0.2"}
	if len(got) != len(want) {
		t.Fatalf("/30 sweep = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("/30 sweep[%d] = %s, want %s", i, got[i], want[i])
		}
	}

	// /32 → single address (hostBits 0 branch: first=0,last=1).
	if s := domain.UsableIPv4Sweep(mustPrefix(t, "10.0.0.5/32"), 24); len(s) != 1 || s[0] != "10.0.0.5" {
		t.Fatalf("/32 sweep = %v, want [10.0.0.5]", s)
	}

	// /31 → both addresses (hostBits 1 branch: first=0,last=2).
	if s := domain.UsableIPv4Sweep(mustPrefix(t, "10.0.0.0/31"), 24); len(s) != 2 {
		t.Fatalf("/31 sweep = %v, want 2 entries", s)
	}

	// maxN caps the enumeration.
	if s := domain.UsableIPv4Sweep(mustPrefix(t, "10.0.0.0/24"), 5); len(s) != 5 {
		t.Fatalf("/24 sweep capped = %d entries, want 5", len(s))
	}

	// non-IPv4 → nil.
	if s := domain.UsableIPv4Sweep(mustPrefix(t, "fd00::/64"), 24); s != nil {
		t.Fatalf("v6 sweep = %v, want nil", s)
	}
}

func TestPickRandomIPv4_WithinPrefixExcludesNetworkBroadcast(t *testing.T) {
	p := mustPrefix(t, "10.0.0.0/24")
	net0 := p.Addr()                           // 10.0.0.0 network
	bcast := netip.MustParseAddr("10.0.0.255") // broadcast
	for i := 0; i < 500; i++ {
		s, err := domain.PickRandomIPv4(p)
		if err != nil {
			t.Fatalf("PickRandomIPv4 error: %v", err)
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("bad addr %q: %v", s, err)
		}
		if !p.Contains(a) {
			t.Fatalf("%s not in %s", s, p)
		}
		if a == net0 || a == bcast {
			t.Fatalf("picked network/broadcast %s", s)
		}
	}
}

func TestPickRandomIPv4_EdgeCases(t *testing.T) {
	// /32 → deterministic base.
	if s, err := domain.PickRandomIPv4(mustPrefix(t, "10.0.0.7/32")); err != nil || s != "10.0.0.7" {
		t.Fatalf("/32 = (%q,%v), want (10.0.0.7,nil)", s, err)
	}
	// /31 → one of the two addresses, network/broadcast not excluded.
	seen := map[string]bool{}
	for i := 0; i < 200; i++ {
		s, err := domain.PickRandomIPv4(mustPrefix(t, "10.0.0.0/31"))
		if err != nil {
			t.Fatalf("/31 error: %v", err)
		}
		if s != "10.0.0.0" && s != "10.0.0.1" {
			t.Fatalf("/31 picked %s, want .0 or .1", s)
		}
		seen[s] = true
	}
	if len(seen) != 2 {
		t.Fatalf("/31 over 200 draws saw %v, want both addresses", seen)
	}
	// non-IPv4 → ErrNotIPv4.
	if _, err := domain.PickRandomIPv4(mustPrefix(t, "fd00::/64")); !errors.Is(err, domain.ErrNotIPv4) {
		t.Fatalf("v6 err = %v, want ErrNotIPv4", err)
	}
}

func TestPickRandomIPv6_WithinPrefixSkipsAnycast(t *testing.T) {
	p := mustPrefix(t, "fd00::/64")
	anycast := p.Masked().Addr()
	for i := 0; i < 500; i++ {
		s, err := domain.PickRandomIPv6(p)
		if err != nil {
			t.Fatalf("PickRandomIPv6 error: %v", err)
		}
		a, err := netip.ParseAddr(s)
		if err != nil {
			t.Fatalf("bad addr %q: %v", s, err)
		}
		if !p.Contains(a) {
			t.Fatalf("%s not in %s", s, p)
		}
		if a == anycast {
			t.Fatalf("picked subnet-router anycast %s", s)
		}
	}
	// /128 → deterministic single addr.
	if s, err := domain.PickRandomIPv6(mustPrefix(t, "fd00::1/128")); err != nil || s != "fd00::1" {
		t.Fatalf("/128 = (%q,%v), want (fd00::1,nil)", s, err)
	}
}
